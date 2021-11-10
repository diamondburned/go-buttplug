// Package buttplug provides Go wrappers around the Intiface API, which is a
// wrapper around the buttplug.io specifications.
package buttplug

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
)

//go:generate go run ./internal/cmd/genschema

// IsServerEvent returns true if ID is 0, indicating that it's an event from the
// server or an internal event.
func (id ID) IsServerEvent() bool { return id == 0 }

// UnknownEventError is an error that's sent on an unknown event.
type UnknownEventError struct {
	Type MessageType
	Data json.RawMessage
}

// Error returns the formatted error; it implements error.
func (err UnknownEventError) Error() string {
	return fmt.Sprintf("unknown event type %s", err.Type)
}

// Additional local-only event types.
const (
	InternalErrorMessage MessageType = "__buttplug.InternalError"
)

// InternalError is a pseudo-message that is sent over the event channel on
// background errors.
type InternalError struct {
	// ID is 0 if the error is a background event loop error, or non-0 if it's
	// an event associated with a command.
	ID ID
	// Err is the error.
	Err error
	// Fatal is true if a reconnection is needed.
	Fatal bool
}

func newInternalError(err error, wrap string, fatal bool) *InternalError {
	if wrap != "" {
		err = errors.Wrap(err, wrap)
	}
	return &InternalError{Err: err, Fatal: fatal}
}

// MessageID returns 0.
func (e *InternalError) MessageID() ID { return 0 }

// MessageType implements Message.
func (e *InternalError) MessageType() MessageType {
	return InternalErrorMessage
}

type errorBox struct {
	mut sync.Mutex
	err error
}

func (b *errorBox) Set(err error) {
	b.mut.Lock()
	b.err = err
	b.mut.Unlock()
}

func (b *errorBox) Get() error {
	b.mut.Lock()
	err := b.err
	b.mut.Unlock()
	return err
}

type idGenerator uint32

// Next returns the next generated ID. It is not thread-safe.
func (id *idGenerator) Next() ID {
	next := *id
	*id++
	return ID(next)
}

type command struct {
	Message
	Reply chan Message
}

type overrideMessageID struct {
	Message
	id ID
}

func (o overrideMessageID) MessageID() ID { return o.id }

type contextBox struct {
	context.Context
}

// Websocket describes a websocket connection to the Buttplug server.
type Websocket struct {
	url string
	err errorBox

	ev  chan Message
	cmd chan []command

	ctx atomic.Value

	// make pointers in case of 32-bit atomic alignment.
	ids     *idGenerator
	started *uint32
}

// NewWebsocket creates a new Buttplug Websocket client instance.
func NewWebsocket(url string) *Websocket {
	return &Websocket{
		url: url,
		ev:  make(chan Message),
		cmd: make(chan []command),

		ids:     new(idGenerator),
		started: new(uint32),
	}
}

// LastError returns the last error in the websocket. It's recommended to call
// this method once the Websocket channel is closed to ensure that it has
// gracefully exited.
func (w *Websocket) LastError() error {
	return w.err.Get()
}

// Open opens the websocket connection by continuously dialing it and ensuring
// its best that the connection stays alive.
//
// If the given context is cancelled OR if the websocket stumbles upon an
// unrecoverable error, then the channel is closed with the last event being of
// type InternalError. A more convenient way of error checking can be done by
// calling the LastError method.
func (w *Websocket) Open(ctx context.Context) <-chan Message {
	if atomic.CompareAndSwapUint32(w.started, 0, 1) {
		go w.spin(ctx)
	}
	return w.ev
}

func (w *Websocket) spin(ctx context.Context) {
	defer close(w.ev)

	// Save the context so we can timeout our sends.
	w.ctx.Store(contextBox{ctx})

	pending := map[ID]command{}
	var ids idGenerator

	reconnect := make(chan struct{}, 1)
	reconnect <- struct{}{}

	// Create our own event channel to directly receive events before passing it
	// off to the user. This channel is never closed.
	ev := make(chan Message, 1)

	var conn *websocket.Conn

	closeConn := func() {
		if conn != nil {
			conn.Close()
		}
	}
	// Always close the connection when we exit.
	defer closeConn()

	for {
		select {
		case <-ctx.Done():
			return

		case cmds := <-w.cmd:
			msgs := make([]overrideMessageID, len(cmds))
			for i, msg := range msgs {
				msgs[i] = overrideMessageID{
					Message: msg,
					id:      ids.Next(),
				}
				// Store the pending message if the caller expects a reply.
				if cmds[i].Reply != nil {
					pending[msgs[i].id] = cmds[i]
				}
			}

			// TODO: figure out something better.
			go func(conn *websocket.Conn) {
				err := conn.WriteJSON(msgs)
				if err == nil {
					return
				}

				// Log the error.
				ev <- &InternalError{
					ID:  0, // system error
					Err: errors.Wrap(err, "failed to send to WS"),
				}

				// Fire the same error to all waiting commands.
				for i, msg := range msgs {
					if cmds[i].Reply == nil {
						// Ignore since asynchronous.
						continue
					}

					errorEv := &InternalError{
						ID:  msg.id,
						Err: err,
					}

					select {
					case <-ctx.Done():
						return
					case ev <- errorEv:
					}
				}
			}(conn)

		case ev := <-ev:
			// Check if we have any pending commands.
			cmd, ok := pending[ev.MessageID()]
			if ok {
				delete(pending, ev.MessageID())
				select {
				case cmd.Reply <- ev:
					// ok
				case <-ctx.Done():
					return
				}
			}

			select {
			case w.ev <- ev:
				continue
			case <-ctx.Done():
				return
			}

		case <-reconnect:
			// Close the old websocket if we haven't already. We can safely
			// ignore the error here.
			closeConn()

			for {
				c, _, err := websocket.DefaultDialer.DialContext(ctx, w.url, nil)
				if err == nil {
					conn = c
					break
				}

				sendErr(ctx, w.ev, err, "cannot dial WS", false)

				// Wait for a while before retrying.
				// TODO: implement exponential backoff.
				select {
				case <-time.After(time.Second):
					continue
				case <-ctx.Done():
					return
				}
			}

			// If conn is nil for some reason, then bail. In the future, this
			// might come in handy if we implement
			if conn == nil {
				return
			}

			// Start the read loop.
			go func(conn *websocket.Conn) {
				readWebsocket(ctx, conn, ev)

				// Queue a reconnect once the read loop fails, unless the
				// context has failed, then we bail.
				select {
				case reconnect <- struct{}{}:
				case <-ctx.Done():
				}
			}(conn)
		}
	}
}

func readWebsocket(ctx context.Context, ws *websocket.Conn, ev chan<- Message) {
	for {
		// Do a context check so we can call continue and bail instead of doing
		// if-checks on sendErr.
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, r, err := ws.NextReader()
		if err != nil {
			// We cannot read, so bail.
			return
		}

		var raw map[MessageType]json.RawMessage

		if err := json.NewDecoder(r).Decode(&raw); err != nil {
			sendErr(ctx, ev, err, "cannot decode JSON packet", false)
			continue
		}

		for t, raw := range raw {
			fn, ok := knownMessages[t]
			if !ok {
				err := UnknownEventError{t, raw}
				sendErr(ctx, ev, err, "", false)
				continue
			}

			msg := fn()
			if err := json.Unmarshal(raw, msg); err != nil {
				err = fmt.Errorf("cannot unmarshal event %s: %w", t, err)
				sendErr(ctx, ev, err, "", false)
				continue
			}

			select {
			case ev <- msg:
				// ok
			case <-ctx.Done():
				return
			}
		}
	}
}

func sendErr(ctx context.Context, ev chan<- Message, err error, wrap string, fatal bool) bool {
	event := newInternalError(err, wrap, fatal)

	select {
	case <-ctx.Done():
		return false
	case ev <- event:
		return true
	}
}

// Send sends the given messages instance over the websocket asynchronously. If
// the user needs a synchronous sending API, they should use the Command method.
func (w *Websocket) Send(ctx context.Context, msgs ...Message) {
	cmds := make([]command, len(msgs))
	for i, msg := range msgs {
		cmds[i] = command{Message: msg}
	}

	wsCtx := w.ctx.Load().(contextBox)
	select {
	case <-ctx.Done():
	case <-wsCtx.Done():
	case w.cmd <- cmds:
	}
}

// Command sends a message over the websocket and waits for a reply. If the
// caller calls this method after the websocket is closed, the function will
// block forever, since a websocket cannot be started back up. The returned
// message is never nil, but it may be of type InternalError, which the function
// will unbox into the return type.
func (w *Websocket) Command(ctx context.Context, msg Message) (Message, error) {
	cmd := command{
		Message: msg,
		Reply:   make(chan Message),
	}

	wsCtx := w.ctx.Load().(contextBox)

	select {
	case <-ctx.Done():
		return &InternalError{Err: ctx.Err()}, ctx.Err()
	case <-wsCtx.Done():
		return &InternalError{Err: wsCtx.Err()}, wsCtx.Err()
	case w.cmd <- []command{cmd}:
		// ok
	}

	select {
	case <-ctx.Done():
		return &InternalError{Err: ctx.Err()}, ctx.Err()
	case <-wsCtx.Done():
		return &InternalError{Err: wsCtx.Err()}, wsCtx.Err()
	case reply := <-cmd.Reply:
		if err, ok := reply.(*InternalError); ok {
			return reply, err.Err
		}
		return reply, nil
	}
}

// CommandCh is a channel variant of Command. The returned channel is never
// closed and will be sent into once.
func (w *Websocket) CommandCh(ctx context.Context, msg Message) <-chan Message {
	cmd := command{
		Message: msg,
		Reply:   make(chan Message),
	}

	wsCtx := w.ctx.Load().(contextBox)
	go func() {
		select {
		case <-ctx.Done():
			cmd.Reply <- &InternalError{Err: ctx.Err()}
		case <-wsCtx.Done():
			cmd.Reply <- &InternalError{Err: wsCtx.Err()}
		case w.cmd <- []command{cmd}:
			// ok
		}
	}()

	return cmd.Reply
}
