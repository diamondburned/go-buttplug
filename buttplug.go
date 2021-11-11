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
	"sync"
	"sync/atomic"
	"time"

	"github.com/diamondburned/go-buttplug/internal/errorbox"
	"github.com/diamondburned/go-buttplug/internal/lazytime"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
)

//go:generate go run ./internal/cmd/genschema

// IsServerEvent returns true if ID is 0, indicating that it's an event from the
// server or an internal event.
func (id ID) IsServerEvent() bool { return id == 0 }

type idGenerator uint32

// Next returns the next generated ID. It is not thread-safe.
func (id *idGenerator) Next() ID {
	return ID(atomic.AddUint32((*uint32)(id), 1))
}

// Version is the buttplug.io schema version.
const Version = 2

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

// Broadcaster is used for creating multiple event loops on the same Buttplug
// server.
type Broadcaster struct {
	dst  map[chan<- Message]struct{}
	mut  sync.Mutex
	void bool
}

// NewBroadcaster creates a new broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		dst: make(map[chan<- Message]struct{}),
	}
}

// Start starts the broadcaster.
func (b *Broadcaster) Start(src <-chan Message) {
	b.mut.Lock()
	if b.void {
		panic("Start called on voided Broadcaster")
	}
	b.mut.Unlock()

	go func() {
		for op := range src {
			b.mut.Lock()

			for ch := range b.dst {
				ch <- op
			}

			b.mut.Unlock()
		}

		b.mut.Lock()
		b.void = true

		for ch := range b.dst {
			close(ch)
		}

		b.mut.Unlock()
	}()
}

// Subscribe subscribes the given channel
func (b *Broadcaster) Subscribe(ch chan<- Message) {
	b.mut.Lock()
	if b.void {
		panic("Subscribe called on voided Broadcaster")
	}
	b.dst[ch] = struct{}{}
	b.mut.Unlock()
}

// Listen returns a newly subscribed Op channel.
func (b *Broadcaster) Listen() <-chan Message {
	ch := make(chan Message, 1)
	b.Subscribe(ch)
	return ch
}

type command struct {
	msg   Message
	reply chan Message
}

// Websocket describes a websocket connection to the Buttplug server.
type Websocket struct {
	err errorbox.Box
	cmd chan []command

	ctxMu  sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc

	id idGenerator

	// DialTimeout is the maximum duration each dial.
	DialTimeout time.Duration
	// DialDelay is the delay between dials.
	DialDelay time.Duration
}

// NewWebsocket creates a new Buttplug Websocket client instance.
func NewWebsocket() *Websocket {
	return &Websocket{
		cmd:         make(chan []command),
		DialTimeout: 10 * time.Second,
		DialDelay:   time.Second,
	}
}

// LastError returns the last error in the websocket. It's recommended to call
// this method once the Websocket channel is closed to ensure that it has
// gracefully exited.
func (w *Websocket) LastError() error {
	return w.err.Get()
}

// Open opens the websocket connection by continuously dialing it and ensuring
// its best that the connection stays alive. If the Websocket is already opened,
// then it'll be reconnected.
//
// If the given context is cancelled OR if the websocket stumbles upon an
// unrecoverable error, then the channel is closed with the last event being of
// type InternalError. A more convenient way of error checking can be done by
// calling the LastError method.
func (w *Websocket) Open(ctx context.Context, url string) <-chan Message {
	w.ctxMu.Lock()
	defer w.ctxMu.Unlock()

	if w.ctx != nil {
		select {
		case <-w.ctx.Done():
			// dead
		default:
			// still alive, kill
			w.cancel()
		}
	}

	w.ctx, w.cancel = context.WithCancel(ctx)
	ev := make(chan Message)
	go w.spin(ctx, ev, url)

	return ev
}

func (w *Websocket) spin(ctx context.Context, ev chan<- Message, url string) {
	defer close(ev)

	pending := map[ID]command{}

	// Create our own event channel to directly receive events before passing it
	// off to the user. This channel is never closed.
	wsMsg := make(chan Message, 1)

	var wsCmd chan []command

	reconnect := make(chan struct{}, 1)

	queueReconnect := func() {
		select {
		case reconnect <- struct{}{}:
			// ok
		default:
			// channel is already filled, we're good
		}
	}
	queueReconnect()

	s := loopState{
		w:         w,
		ctx:       ctx,
		events:    ev,
		pending:   pending,
		reconnect: queueReconnect,
	}

	closeConn := func() {
		if s.conn != nil {
			s.conn.Close()
		}
	}
	// Always close the connection when we exit.
	defer closeConn()

	var heartbeat lazytime.Ticker
	var heartrate time.Duration
	var heartPing [2]time.Time // [sent, received]
	var heartReply <-chan Message

	ensureAlive := func() (alive bool) {
		if heartPing[0].Add(heartrate).Before(heartPing[1]) {
			// Missed a beat, reconnect.
			w.sendErr(ctx, ev, errors.New("server missed a heartbeat"), "", false)
			queueReconnect()
			return false
		}
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return

		case <-heartReply:
			heartPing[1] = time.Now()
			ensureAlive()

		case <-heartbeat.C:
			if ensureAlive() {
				s.sendCommand(command{msg: &Ping{ID: w.id.Next()}})
				heartPing[0] = time.Now()
			}

		case cmds := <-wsCmd:
			s.sendCommand(cmds...)

		case msg := <-wsMsg:
			// Clear the error.
			w.err.Set(nil)

			switch msg := msg.(type) {
			case *ServerInfo:
				// Verify version.
				if msg.MessageVersion != Version {
					err := VersionMismatchError{msg.MessageVersion}
					w.sendErr(ctx, ev, err, "", true)
					return
				}
				// Update the heartbeat duration. Half the duration to be extra
				// careful.
				if msg.MaxPingTime > 0 {
					hrt := time.Duration(msg.MaxPingTime) * time.Millisecond / 2
					heartbeat.Reset(hrt)
				}
			}

			// Check if we have any pending commands.
			cmd, ok := pending[msg.MessageID()]
			if ok {
				delete(pending, msg.MessageID())
				select {
				case cmd.reply <- msg:
					// ok
				case <-ctx.Done():
					return
				}
			}

			select {
			case ev <- msg:
				continue
			case <-ctx.Done():
				return
			}

		case <-reconnect:
			// Close the old websocket if we haven't already. We can safely
			// ignore the error here.
			closeConn()
			// Disable sending.
			wsCmd = nil
			heartbeat.Stop()

			for {
				connCtx, cancel := context.WithTimeout(ctx, w.DialTimeout)
				c, _, err := websocket.DefaultDialer.DialContext(connCtx, url, nil)
				cancel()

				if err == nil {
					s.conn = c
					break
				}

				err = &DialError{err}
				w.sendErr(ctx, ev, err, "", false)

				// Wait for a while before retrying.
				// TODO: implement exponential backoff.
				select {
				case <-time.After(w.DialDelay):
					continue
				case <-ctx.Done():
					return
				}
			}

			// If conn is nil for some reason, then bail. In the future, this
			// might come in handy if we implement
			if s.conn == nil {
				return
			}

			// Start the read loop.
			go func(conn *websocket.Conn) {
				readWebsocket(ctx, conn, wsMsg)
				queueReconnect()
			}(s.conn)

			// Send the handshake asynchronously.
			handshake := NewRequestServerInfo()
			handshake.ID = w.id.Next()
			s.sendCommand(command{msg: handshake})

			// Restore the command channel.
			wsCmd = w.cmd
		}
	}
}

type loopState struct {
	w         *Websocket
	conn      *websocket.Conn
	ctx       context.Context
	events    chan<- Message
	pending   map[ID]command
	reconnect func()
}

func (s *loopState) sendErr(ctx context.Context, err error, wrap string, fatal bool) {
	s.w.sendErr(ctx, s.events, err, "failed to marshal message for sending", false)
}

func (s *loopState) sendCommand(cmds ...command) {
	msgs := make([]Messages, len(cmds))
	for i, cmd := range cmds {
		msgs[i] = Messages{cmd.msg.MessageType(): cmd.msg}
		// Store the pending message if the caller expects a reply.
		if cmd.reply != nil {
			s.pending[cmd.msg.MessageID()] = cmds[i]
		}
	}

	// TODO: figure out something better.
	go func(conn *websocket.Conn) {
		err := conn.WriteJSON(msgs)
		if err == nil {
			return
		}

		// Log the error.
		s.events <- &InternalError{
			ID:  0, // system error
			Err: errors.Wrap(err, "failed to send to WS"),
		}

		// Queue a reconnect immediately.
		s.reconnect()

		// Fire the same error to all waiting commands.
		for i, cmd := range cmds {
			if cmds[i].reply == nil {
				// Ignore since asynchronous.
				continue
			}

			errorEv := &InternalError{
				ID:  cmd.msg.MessageID(),
				Err: err,
			}

			select {
			case <-s.ctx.Done():
				return
			case s.events <- errorEv:
			}
		}
	}(s.conn)
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
			return
		}

		var messages []map[MessageType]json.RawMessage

		if err := json.NewDecoder(r).Decode(&messages); err != nil {
			sendErr(ctx, ev, err, "cannot decode JSON packet", false)
			continue
		}

		for _, msg := range messages {
			for t, raw := range msg {
				fn, ok := knownMessages[t]
				if !ok {
					err := &UnknownEventError{t, raw}
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
}

func (w *Websocket) sendErr(
	ctx context.Context, ev chan<- Message, err error, wrap string, fatal bool) bool {

	w.err.Set(err)
	return sendErr(ctx, ev, err, wrap, fatal)
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
		msg.SetMessageID(w.id.Next())
		cmds[i] = command{msg: msg}
	}

	select {
	case <-ctx.Done():
	case w.cmd <- cmds:
	}
}

// Command sends a message over the websocket and waits for a reply. If the
// caller calls this method after the websocket is closed, the function will
// block forever, since a websocket cannot be started back up. The returned
// message is never nil, but it may be of type InternalError or Error, which the
// function will unbox into the return error type.
func (w *Websocket) Command(ctx context.Context, msg Message) (Message, error) {
	msg.SetMessageID(w.id.Next())
	cmd := command{
		msg:   msg,
		reply: make(chan Message, 1),
	}

	select {
	case <-ctx.Done():
		err := errors.Wrap(ctx.Err(), "timed out sending")
		return &InternalError{Err: err}, err
	case w.cmd <- []command{cmd}:
		// ok
	}

	select {
	case <-ctx.Done():
		err := errors.Wrap(ctx.Err(), "timed out waiting")
		return &InternalError{Err: err}, err
	case reply := <-cmd.reply:
		switch reply := reply.(type) {
		case *InternalError:
			return reply, reply.Err
		case *Error:
			return reply, reply
		default:
			return reply, nil
		}
	}
}

// CommandCh is a channel variant of Command. The returned channel is never
// closed and will be sent into once.
func (w *Websocket) CommandCh(ctx context.Context, msg Message) <-chan Message {
	msg.SetMessageID(w.id.Next())
	cmd := command{
		msg:   msg,
		reply: make(chan Message, 1),
	}

	go func() {
		select {
		case <-ctx.Done():
			err := errors.Wrap(ctx.Err(), "timed out sending")
			cmd.reply <- &InternalError{Err: err}
		case w.cmd <- []command{cmd}:
			// ok
		}
	}()

	return cmd.reply
}
