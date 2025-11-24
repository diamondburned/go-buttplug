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
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/puzpuzpuz/xsync/v4"
	"golang.org/x/sync/errgroup"
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
		With("addr", wsAddr)

	return &Websocket{
		msgs:            make(chan Message, 1),
		send:            make(chan command),
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

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-retryTicker.C:
			slog.DebugContext(ctx,
				"attempting to connect to websocket")

			if err := w.start(ctx); err != nil {
				slog.ErrorContext(ctx,
					"websocket connection failed, will retry in a bit...",
					"error", err)
			}
		}
	}
}

func (w *Websocket) start(ctx context.Context) error {
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

	errg, ctx := errgroup.WithContext(ctx)

	msgCh := make(chan Message, 1)
	sendCh := make(chan command, 1)
	heartbeatCh := make(chan struct{}, 1)

	errg.Go(func() error {
		slog := w.logger.
			With("loop", "read")
		defer slog.DebugContext(ctx, "read loop exiting")

		var msgs []map[MessageType]json.RawMessage
		for {
			msgs = msgs[:0] // reuse backing array

			if err := wsjson.Read(ctx, wsConn, &msgs); err != nil {
				return fmt.Errorf("failed to read websocket message: %w", err)
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

					select {
					case <-ctx.Done():
						return ctx.Err()
					case msgCh <- msg:
						continue
					}
				}
			}
		}
	})

	errg.Go(func() error {
		slog := w.logger.
			With("loop", "write")
		defer slog.DebugContext(ctx, "write loop exiting")

		var cmd command
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()

			case cmd = <-sendCh:
				slog.DebugContext(ctx,
					"sending message",
					"msg.id", cmd.msg.MessageID(),
					"msg.type", cmd.msg.MessageType())

			case <-heartbeatCh:
				cmd = command{msg: &Ping{ID: w.nextID()}}

				slog.DebugContext(ctx,
					"sending heartbeat ping",
					"msg.id", cmd.msg.MessageID())
			}

			if err := wsjson.Write(ctx, wsConn, cmd.msg); err != nil {
				return fmt.Errorf("failed to write websocket message: %w", err)
			}
		}
	})

	errg.Go(func() error {
		slog := w.logger.
			With("loop", "main")
		defer slog.DebugContext(ctx, "main loop exiting")

		heartbeat := time.NewTicker(0)
		heartbeat.Stop()

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()

			case msg := <-msgCh:
				switch msg := msg.(type) {
				case *ServerInfo:
					if msg.MessageVersion != Version {
						slog.ErrorContext(ctx,
							"server version mismatch, bailing out",
							"server_version", msg.MessageVersion,
							"client_version", Version)

						// attempt to gracefully close the connection
						wsConn.Close(websocket.StatusPolicyViolation, "version mismatch")

						return fmt.Errorf(
							"version mismatch: server has %d, client has %d",
							msg.MessageVersion, Version,
						)
					}

					if msg.MaxPingTime > 0 {
						hrt := time.Duration(msg.MaxPingTime) * time.Millisecond / 2
						heartbeat.Reset(hrt)
					}
				}

				// reply to any waiting command.
				if cmd, ok := w.waitingCommands.LoadAndDelete(msg.MessageID()); ok {
					cmd.reply(msg)
				}

				select {
				case <-ctx.Done():
					return ctx.Err()
				case w.msgs <- msg:
					// ok
				}

			case <-heartbeat.C:
				// ensure the heartbeat channel has something in it but don't
				// force it.
				teaseChannel(heartbeatCh, struct{}{})
			}
		}
	})

	return errg.Wait()
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

// teaseChannel tries to put a value into a channel without blocking.
func teaseChannel[T any](ch chan<- T, v T) {
	select {
	case ch <- v:
	default:
	}
}
