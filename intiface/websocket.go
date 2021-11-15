package intiface

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/diamondburned/go-buttplug"
	"github.com/diamondburned/go-buttplug/internal/errorbox"
	"github.com/pkg/errors"
)

// Websocket wraps around a Buttplug Websocket to simultaneously manage the CLI
// in the background.
type Websocket struct {
	*buttplug.Websocket
	ev chan buttplug.Message

	arg0 string
	argv []string
	port int

	fatalErr errorbox.Box
	started  uint32
}

// NewWebsocket creates a new websocket instance. If port is 0, then the default
// port is 20000.
func NewWebsocket(port int, arg0 string, argv ...string) *Websocket {
	ws := buttplug.NewWebsocket()
	ws.DialTimeout = time.Second
	ws.DialDelay = 250 * time.Millisecond

	return &Websocket{
		Websocket: ws,
		ev:        make(chan buttplug.Message),
		arg0:      arg0,
		argv:      argv,
		port:      port,
	}
}

// Open starts the CLI. It does nothing if called more than once.
func (w *Websocket) Open(ctx context.Context) <-chan buttplug.Message {
	if atomic.CompareAndSwapUint32(&w.started, 0, 1) {
		ctx, cancel := context.WithCancel(ctx)
		adapter := wsAdapter{
			ws:     w,
			ctx:    ctx,
			cancel: cancel,
		}

		StartCLI(ctx, w.port, adapter, w.arg0, w.argv...)
	}

	return w.ev
}

// LastError returns the last error.
func (w *Websocket) LastError() error {
	// We only store fatal errors, so our error is more important.
	if err := w.fatalErr.Get(); err != nil {
		return err
	}
	return w.Websocket.LastError()
}

type wsAdapter struct {
	ws     *Websocket
	ctx    context.Context    // parent
	cancel context.CancelFunc // parent
}

func (a wsAdapter) Log(err error) {
	msg := &buttplug.InternalError{Err: errors.Wrap(err, "intiface CLI error")}
	a.ws.fatalErr.Set(msg.Err)
	a.ws.ev <- msg
}

func (a wsAdapter) Close() {
	if a.cancel != nil {
		a.cancel()
	}
	close(a.ws.ev)
}

func (a wsAdapter) OnConnect(address string) bool {
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()

	var tries int

loop:
	for ev := range a.ws.Websocket.Open(a.ctx, address) {
		switch ev := ev.(type) {
		case *buttplug.InternalError:
			var wsErr *buttplug.DialError
			if errors.As(ev.Err, &wsErr) {
				if tries++; tries > 4 {
					return false
				}
			}
		}

		select {
		case a.ws.ev <- ev:
			continue
		case <-ctx.Done():
			break loop
		}
	}

	// Context expired so loop broke. Return.
	return true
}
