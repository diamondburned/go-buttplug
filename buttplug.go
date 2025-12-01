// Package buttplug provides Go wrappers around the Intiface API, which is a
// wrapper around the buttplug.io specifications.
//
// Most people should use package intiface instead. This package only supplies
// the messages and the Websocket implementation, but intiface allows those to
// automatically interact with the Intiface server.
package buttplug

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/coder/websocket"
	buttplugschema "libdb.so/go-buttplug/schema/v3"
)

// MessageVersion is the current Buttplug message version this library
// implements. See https://docs.buttplug.io/docs/spec/changelog.
const MessageVersion = 3

// WebsocketResetMessage is an empty message that is sent from the websocket
// loop to indicate that the connection has been reset and that internal state
// should be cleared.
type WebsocketResetMessage struct {
	buttplugschema.InternalMessage
}

const (
	// WebsocketDialTimeout is the maximum duration each dial.
	WebsocketDialTimeout = 10 * time.Second
	// WebsocketDialDelay is the delay between dials.
	WebsocketDialDelay = time.Second
)

// WebsocketBackoff is the default backoff policy for reconnecting to a Buttplug
// server over websocket.
var WebsocketBackoff backoff.BackOff = &backoff.ExponentialBackOff{
	InitialInterval:     200 * time.Millisecond,
	RandomizationFactor: 0.5,
	Multiplier:          1.5,
	MaxInterval:         2 * time.Second,
}

// Websocket describes a websocket connection to the Buttplug server.
type Websocket struct {
	send  chan buttplugschema.ClientMessage
	msgCh atomic.Pointer[messageChannel]

	logger     *slog.Logger
	id         atomic.Int64
	addr       string
	serverName string
}

type messageChannel struct {
	ch   chan<- buttplugschema.Message
	ctx  context.Context
	done bool
	next atomic.Pointer[messageChannel]
}

// NewWebsocket creates a new Buttplug Websocket client instance and optionally
// a [slog.Logger] for internal logging.
func NewWebsocket(wsAddr string, logger *slog.Logger) *Websocket {
	return NewWebsocketWithServerName(wsAddr, "go-buttplug", logger)
}

// NewWebsocketWithServerName creates a new Buttplug Websocket client instance
// with a custom client name and optionally a [slog.Logger] for internal
// logging.
func NewWebsocketWithServerName(wsAddr, serverName string, logger *slog.Logger) *Websocket {
	if logger == nil {
		logger = slog.Default()
	}

	logger = logger.
		With("module", "buttplug")

	return &Websocket{
		send:       make(chan buttplugschema.ClientMessage, 1), // buffered for initial dispatch
		logger:     logger,
		addr:       wsAddr,
		serverName: serverName,
	}
}

// messageChannels returns an iterator over all message channels.
func (w *Websocket) messageChannels() iter.Seq[*messageChannel] {
	return eachMessageChannel(w.msgCh.Load())
}

func eachMessageChannel(mc *messageChannel) iter.Seq[*messageChannel] {
	return func(yield func(*messageChannel) bool) {
		for mc != nil && yield(mc) {
			mc = mc.next.Load()
		}
	}
}

// dispatchMessage sends the given message to all registered message channels.
func (w *Websocket) dispatchMessage(ctx context.Context, msg buttplugschema.Message) error {
	for mc := range w.messageChannels() {
		if mc.done {
			continue
		}

		slog.DebugContext(ctx,
			"dispatching message to channel",
			"ch_ptr", mc.ch,
			"msg", msg)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-mc.ctx.Done():
			// dispatchMessage is only sent within the main loop, so no
			// synchronization is necessary here.
			if !mc.done {
				close(mc.ch)
				mc.done = true
			}
		case mc.ch <- msg:
		}
	}

	return nil
}

// MessageChannel returns a new channel that will receive all incoming messages
// coming from the Buttplug server. For convenience, this channel is closed when
// [Websocket.Start] exits or when the given context is cancelled, and no
// messages will be sent to the channel after that.
//
// It is safe to call this method concurrently.
//
// Note that if this method is called after [Websocket.Start], messages may be
// missed. Therefore, this method should only be called before starting the
// websocket.
func (w *Websocket) MessageChannel(ctx context.Context) (<-chan buttplugschema.Message, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	ch := make(chan buttplugschema.Message, 1)
	racy := true
	for racy {
		var last *messageChannel
		for mc := range w.messageChannels() {
			last = mc
		}
		if last == nil {
			racy = !w.msgCh.CompareAndSwap(nil, &messageChannel{ch: ch, ctx: ctx})
		} else {
			racy = !last.next.CompareAndSwap(nil, &messageChannel{ch: ch, ctx: ctx})
		}
	}
	return ch, cancel
}

// Start starts the websocket connection persistently and blocks until the given
// context is cancelled. It transparently reconnects on connection or loop
// failure with a backoff defined by [WebsocketBackoff].
func (w *Websocket) Start(ctx context.Context) error {
	// Ensure all message channels are closed when we exit, and that the
	// channels are no longer reachable after closing.
	defer func() {
		oldCh := w.msgCh.Swap(nil)
		for mc := range eachMessageChannel(oldCh) {
			if !mc.done {
				close(mc.ch)
			}
		}
	}()

	retryTicker := backoff.NewTicker(WebsocketBackoff)
	defer retryTicker.Stop()

	for attempt := 0; ctx.Err() == nil; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-retryTicker.C:
			slog := w.logger
			slog.DebugContext(ctx,
				"attempting to connect to websocket",
				"attempt", attempt,
				"address", w.addr)

			if err := w.start(ctx, slog); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx,
					"websocket connection failed, will retry in a bit...",
					"error", err)
			}
		}
	}

	return ctx.Err()
}

func (w *Websocket) start(ctx context.Context, slog *slog.Logger) error {
	wsConn, _, err := websocket.Dial(ctx, w.addr, nil)
	if err != nil {
		return fmt.Errorf("failed to dial websocket: %w", err)
	}
	defer wsConn.CloseNow()

	// deliver our first message.
	if err := w.dispatchMessage(ctx, &WebsocketResetMessage{}); err != nil {
		return err
	}

	msgCh := make(chan buttplugschema.Message)
	beatCh := make(chan time.Time, 1)
	sendCh := make(chan buttplugschema.ClientMessage, 1) // buffered for initial message

	var wsGroup sync.WaitGroup
	defer wsGroup.Wait()

	// Begin a new lifetime just for the websocket read and write loops, since
	// these being cancelled immediately ends the connection.
	wsCtx, wsCancel := context.WithCancelCause(context.Background())
	defer wsCancel(nil)

	wsGroup.Go(func() {
		defer wsCancel(nil)

		slog := slog.
			With("loop", "read")
		defer slog.DebugContext(ctx, "read loop exiting")

		for {
			_, r, err := wsConn.Reader(wsCtx)
			if err != nil {
				var closeErr websocket.CloseError
				if !errors.As(err, &closeErr) {
					slog.ErrorContext(ctx,
						"failed to read websocket message",
						"err", err)
				} else {
					slog.DebugContext(ctx,
						"websocket closed by server while reading",
						"code", closeErr.Code,
						"reason", closeErr.Reason)
				}
				return
			}

			var payload buttplugschema.Payload
			if err := json.UnmarshalRead(r, &payload); err != nil {
				slog.ErrorContext(ctx,
					"failed to unmarshal incoming websocket message payload, ignoring",
					"err", err)
				continue
			}

			for _, msg := range payload {
				select {
				case <-wsCtx.Done():
					return
				case msgCh <- msg:
					continue
				}
			}
		}
	})

	wsGroup.Go(func() {
		defer wsCancel(nil)

		slog := slog.
			With("loop", "write")
		defer slog.DebugContext(ctx, "write loop exiting")

		var msg buttplugschema.ClientMessage
		var buf bytes.Buffer
		var ok bool

	writeLoop:
		for {
			select {
			case <-wsCtx.Done():
				return

			case t := <-beatCh:
				msg = &buttplugschema.PingMessage{ID: w.nextID()}

				slog.DebugContext(ctx,
					"sending heartbeat ping",
					"beat_time", t)

			case msg, ok = <-sendCh:
				if !ok {
					slog.DebugContext(ctx,
						"send channel closed, exiting write loop")
					break writeLoop
				}

				slog.DebugContext(ctx,
					"writing websocket payload to server",
					"msg", msg)
			}

			buf.Reset()
			if err := json.MarshalWrite(&buf, buttplugschema.Payload{msg}); err != nil {
				slog.ErrorContext(ctx,
					"failed to marshal websocket message, ignoring",
					"msg", msg,
					"err", err)
				continue
			}

			if err := wsConn.Write(wsCtx, websocket.MessageText, buf.Bytes()); err != nil {
				slog.ErrorContext(ctx,
					"failed to write websocket message, exiting",
					"err", err)
				break writeLoop
			}
		}

		if err := wsConn.Close(websocket.StatusNormalClosure, "write loop exiting"); err != nil {
			slog.ErrorContext(ctx,
				"failed to close websocket connection gracefully",
				"err", err)
			wsCancel(err)
		}
	})

	// Send the initial [RequestServerInfo] message so that we receive a
	// [ServerInfo] back.
	handshakeMsg := &buttplugschema.RequestServerInfoMessage{
		ID:             w.nextID(),
		ClientName:     w.serverName,
		MessageVersion: MessageVersion,
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case sendCh <- handshakeMsg:
	}

	slog.DebugContext(ctx,
		"beginning main websocket loop")

	var heartbeat <-chan time.Time
	var upstreamSendCh <-chan buttplugschema.ClientMessage
mainLoop:
	for {
		select {
		case <-ctx.Done():
			break mainLoop

		case msg := <-msgCh:
			slog.DebugContext(ctx,
				"received message",
				"msg", msg)

			switch msg := msg.(type) {
			case *buttplugschema.ServerInfoMessage:
				if msg.MaxPingTime > 0 {
					hrt := (time.Duration(msg.MaxPingTime) * time.Millisecond) / 2
					heartbeat = time.Tick(hrt)
				}

				// Server is ready to receive messages now. Set this channel so
				// that the main loop starts sending messages sent from
				// [Websocket.Send].
				upstreamSendCh = w.send

			case *buttplugschema.ErrorMessage:
				slog.ErrorContext(ctx,
					"received Error message",
					"msg", msg,
					"code", msg.ErrorCode,
					"error", msg.ErrorMessage)
			}

			if err := w.dispatchMessage(ctx, msg); err != nil {
				break mainLoop
			}

		case msg := <-upstreamSendCh:
			select {
			case <-ctx.Done():
				break mainLoop
			case sendCh <- msg:
			}

		case t := <-heartbeat:
			// ensure the heartbeat channel has something in it but don't
			// force it.
			teaseChannel(beatCh, t)
		}
	}

	// make sure we send out a StopDeviceCmd and close the websocket
	// gracefully if we can.
	stopCtx, cancel := context.WithTimeout(wsCtx, 5*time.Second)
	defer cancel()

	slog.DebugContext(stopCtx,
		"sending StopAllDevices command during estop")

	select {
	case <-stopCtx.Done():
		slog.WarnContext(stopCtx,
			"stop context done before sending StopAllDevices command",
			"error", stopCtx.Err())
	case sendCh <- &buttplugschema.StopAllDevicesMessage{ID: w.nextID()}:
		close(sendCh)
	}

	return ctx.Err()
}

func (w *Websocket) nextID() buttplugschema.ClientID {
	return buttplugschema.ClientID(w.id.Add(1))
}

// Send queues the given messages to be sent in the main [Websocket.Start] loop.
// An error is only returned if context is cancelled.
//
// It is safe to call this method concurrently.
func (w *Websocket) Send(ctx context.Context, msg buttplugschema.ClientMessage) (buttplugschema.ClientID, error) {
	id := w.nextID()
	select {
	case <-ctx.Done():
		return id, ctx.Err()
	case w.send <- msg.WithID(id):
		return id, nil
	}
}

// SendCommand sends a single Buttplug command and waits for the response.
// This is a convenience method around [Websocket.Send] and listening to the
// message channels.
func (w *Websocket) SendCommand(ctx context.Context, cmd buttplugschema.ClientMessage) (buttplugschema.Message, error) {
	msgs, cancel := w.MessageChannel(ctx)
	defer cancel()

	slog.DebugContext(ctx,
		"message channel created for SendCommand, now sending command",
		"ch_ptr", msgs,
		"cmd", cmd)

	sentID, err := w.Send(ctx, cmd)
	if err != nil {
		return nil, err
	}

	slog.DebugContext(ctx,
		"command sent, now waiting for response",
		"ch_ptr", msgs,
		"cmd.id", sentID,
		"cmd", cmd)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case msg, ok := <-msgs:
			if !ok {
				return nil, fmt.Errorf("message channel closed before response received")
			}

			switch msg := msg.(type) {
			case buttplugschema.ClientMessage:
				if sentID == msg.ClientID() {
					return msg, nil
				}
			}
		}
	}
}

// teaseChannel tries to put a value into a channel without blocking.
func teaseChannel[T any](ch chan<- T, v T) {
	select {
	case ch <- v:
	default:
	}
}
