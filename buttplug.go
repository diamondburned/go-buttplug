// Package buttplug provides Go wrappers around the Intiface API, which is a
// wrapper around the buttplug.io specifications.
//
// Most people should use package intiface instead. This package only supplies
// the messages and the Websocket implementation, but intiface allows those to
// automatically interact with the Intiface server.
package buttplug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/puzpuzpuz/xsync/v4"
)

//go:generate go run ./internal/cmd/genschema

// IsServerEvent returns true if ID is 0, indicating that it's an event from the
// server or an internal event.
func (id ID) IsServerEvent() bool { return id == 0 }

// WebsocketReset is an empty message that is sent from the websocket loop to
// indicate that the connection has been reset and that internal state should be
// cleared.
type WebsocketReset struct{}

func (*WebsocketReset) MessageID() ID            { return 0 }
func (*WebsocketReset) MessageType() MessageType { return MessageType("WebsocketReset") }
func (*WebsocketReset) SetMessageID(ID)          {}

const (
	// WebsocketDialTimeout is the maximum duration each dial.
	WebsocketDialTimeout = 10 * time.Second
	// WebsocketDialDelay is the delay between dials.
	WebsocketDialDelay = time.Second
)

// Version is the buttplug.io schema version.
const Version = 2

// WebsocketBackoff is the default backoff policy for reconnecting to a Buttplug
// server over websocket.
var WebsocketBackoff backoff.BackOff = &backoff.ExponentialBackOff{
	InitialInterval:     200 * time.Millisecond,
	RandomizationFactor: 0.5,
	Multiplier:          1.5,
	MaxInterval:         2 * time.Second,
}

// NewRequestServerInfo creates a new RequestServerInfo with the current client
// information.
func NewRequestServerInfo() *RequestServerInfo {
	v := new(int)
	*v = Version
	return &RequestServerInfo{
		ClientName:     "go-buttplug",
		MessageVersion: v,
	}
}

type command struct {
	msg   Message
	reply func(Message)
}

// Websocket describes a websocket connection to the Buttplug server.
type Websocket struct {
	id   atomic.Uint32
	msgs chan Message
	send chan command
	beat chan time.Time
	// track which commands are waiting for replies.
	waitingCommands *xsync.Map[ID, command]

	logger *slog.Logger
	addr   string
}

// NewWebsocket creates a new Buttplug Websocket client instance and optionally
// a [slog.Logger] for internal logging.
func NewWebsocket(wsAddr string, logger *slog.Logger) *Websocket {
	if logger == nil {
		logger = slog.Default()
	}

	logger = logger.
		WithGroup("buttplug").
		With("ws.addr", wsAddr)

	return &Websocket{
		msgs:            make(chan Message, 1),
		send:            make(chan command, 1),
		beat:            make(chan time.Time, 1),
		waitingCommands: xsync.NewMap[ID, command](),

		logger: logger,
		addr:   wsAddr,
	}
}

// Messages returns a channel that receives all messages from the websocket
// connection. You must call [Websocket.Start] before any messages are received.
func (w *Websocket) Messages() <-chan Message {
	return w.msgs
}

// Start starts the websocket connection persistently and blocks until the given
// context is cancelled. It transparently reconnects on connection or loop
// failure with a backoff defined by [WebsocketBackoff].
func (w *Websocket) Start(ctx context.Context) error {
	retryTicker := backoff.NewTicker(WebsocketBackoff)
	defer retryTicker.Stop()

	for attempt := 0; ctx.Err() == nil; attempt++ {
		w.id.Store(0)

		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-retryTicker.C:
			slog := w.logger.With("ws.attempt", attempt)
			slog.DebugContext(ctx,
				"attempting to connect to websocket")

			if err := w.start(ctx, slog); err != nil && !errors.Is(err, context.Canceled) {
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
	select {
	case <-ctx.Done():
		return ctx.Err()
	case w.msgs <- &WebsocketReset{}:
	}

	msgCh := make(chan Message, 1)

	// Begin a new lifetime just for the websocket read and write loops, since
	// these being cancelled immediately ends the connection.
	wsCtx, wsCancel := context.WithCancel(context.Background())
	defer wsCancel()

	var wsGroup sync.WaitGroup
	defer wsGroup.Wait()

	wsGroup.Go(func() {
		defer wsCancel()

		slog := slog.
			With("loop", "read")
		defer slog.DebugContext(ctx, "read loop exiting")

		var msgs []map[MessageType]json.RawMessage
		for {
			msgs = msgs[:0] // reuse backing array

			if err := wsjson.Read(wsCtx, wsConn, &msgs); err != nil {
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

			for _, msg := range msgs {
				for msgType, msgJSON := range msg {
					fn, ok := knownMessages[msgType]
					if !ok {
						slog.WarnContext(ctx,
							"ignoring unknown message with type",
							"type", msgType,
							"data", string(msgJSON))
						continue
					}

					msg := fn()
					if err := json.Unmarshal(msgJSON, msg); err != nil {
						slog.ErrorContext(ctx,
							"failed to unmarshal message",
							"type", msgType,
							"data", string(msgJSON),
							"error", err)
						continue
					}

					slog.DebugContext(ctx,
						"successfully read and decoded websocket message from server",
						"msg.id", msg.MessageID(),
						"msg.type", msg.MessageType())

					select {
					case <-wsCtx.Done():
						return
					case msgCh <- msg:
						continue
					}
				}
			}
		}
	})

	wsGroup.Go(func() {
		defer wsCancel()

		slog := slog.
			With("loop", "write")
		defer slog.DebugContext(ctx, "write loop exiting")

		var cmd command
		for {
			select {
			case <-wsCtx.Done():
				return

			case cmd = <-w.send:
				slog.DebugContext(ctx,
					"sending message",
					"msg.id", cmd.msg.MessageID(),
					"msg.type", cmd.msg.MessageType())

			case t := <-w.beat:
				cmd = command{msg: &Ping{ID: w.nextID()}}

				slog.DebugContext(ctx,
					"sending heartbeat ping",
					"msg.id", cmd.msg.MessageID(),
					"beat_time", t)
			}

			slog.DebugContext(ctx,
				"writing websocket message to server",
				"msg.id", cmd.msg.MessageID(),
				"msg.type", cmd.msg.MessageType())

			if err := wsjson.Write(wsCtx, wsConn, cmd.msg); err != nil {
				slog.ErrorContext(ctx,
					"failed to write websocket message",
					"msg.id", cmd.msg.MessageID(),
					"msg.type", cmd.msg.MessageType(),
					"err", err)
				return
			}
		}
	})

	var heartbeat <-chan time.Time
	var loopError error
mainLoop:
	for {
		select {
		case <-ctx.Done():
			break mainLoop

		case msg := <-msgCh:
			switch msg := msg.(type) {
			case *ServerInfo:
				if msg.MessageVersion != Version {
					slog.ErrorContext(ctx,
						"server version mismatch, bailing out",
						"server_version", msg.MessageVersion,
						"client_version", Version)

					loopError = errors.New("buttplug version mismatch between client and server")
					break mainLoop
				}

				if msg.MaxPingTime > 0 {
					hrt := time.Duration(msg.MaxPingTime) * time.Millisecond / 2
					heartbeat = time.Tick(hrt)
				}

			case *Log:
				slog.InfoContext(ctx,
					"received Log message from server",
					"level", msg.LogLevel,
					"message", msg.LogMessage)

			case *Error:
				slog.ErrorContext(ctx,
					"received Error message",
					"id", msg.MessageID(),
					"code", msg.ErrorCode,
					"error", msg.ErrorMessage)
			}

			// reply to any waiting command.
			if cmd, ok := w.waitingCommands.LoadAndDelete(msg.MessageID()); ok {
				cmd.reply(msg)
			}

			select {
			case <-ctx.Done():
				break mainLoop
			case w.msgs <- msg:
				// ok
			}

		case t := <-heartbeat:
			// ensure the heartbeat channel has something in it but don't
			// force it.
			teaseChannel(w.beat, t)
		}
	}

	// make sure we send out a StopDeviceCmd and close the websocket
	// gracefully if we can.
	stopCtx, cancel := context.WithTimeout(wsCtx, 5*time.Second)
	defer cancel()

	stopEvent := &StopAllDevices{ID: w.nextID()}
	slog.DebugContext(stopCtx,
		"sending StopAllDevices command during estop",
		"msg.id", stopEvent.MessageID())

	if err := wsjson.Write(stopCtx, wsConn, stopEvent); err != nil {
		slog.WarnContext(stopCtx,
			"failed to send StopAllDevices command during estop, sorry for the pleasure~",
			"err", err)
	}

	slog.DebugContext(stopCtx,
		"closing websocket gracefully during estop")

	if loopError == nil {
		err = wsConn.Close(websocket.StatusNormalClosure, "client is stopping")
	} else {
		err = wsConn.Close(websocket.StatusGoingAway, "client is stopping due to error")
	}

	if err != nil {
		slog.ErrorContext(ctx,
			"failed to close websocket gracefully during estop",
			"err", err)
		return fmt.Errorf("failed to close websocket: %w", err)
	}

	return ctx.Err()
}

func (w *Websocket) nextID() ID {
	return ID(w.id.Add(1))
}

// Send queues the given messages to be sent in the main [Websocket.Start] loop.
// An error is only returned if context is cancelled.
//
// It is safe to call this method concurrently.
func (w *Websocket) Send(ctx context.Context, msg Message) error {
	return w.sendWithReply(ctx, msg, nil)
}

// SendCommand sends a message and waits for a reply. An error is only returned
// if context is cancelled.
//
// It is safe to call this method concurrently.
func (w *Websocket) SendCommand(ctx context.Context, msg Message) (Message, error) {
	msg.SetMessageID(w.nextID())

	reply := make(chan Message, 1)
	if err := w.sendWithReply(ctx, msg, func(m Message) { reply <- m }); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-reply:
		return r, nil
	}
}

func (w *Websocket) sendWithReply(ctx context.Context, msg Message, reply func(Message)) error {
	msg.SetMessageID(w.nextID())

	cmd := command{msg: msg, reply: reply}
	w.waitingCommands.Store(msg.MessageID(), cmd)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case w.send <- cmd:
		return nil
	}
}

// SignalHeartbeat signals the websocket to send a heartbeat ping as soon as
// possible.
func (w *Websocket) SignalHeartbeat() {
	teaseChannel(w.beat, time.Now())
}

// teaseChannel tries to put a value into a channel without blocking.
func teaseChannel[T any](ch chan<- T, v T) {
	select {
	case ch <- v:
	default:
	}
}
