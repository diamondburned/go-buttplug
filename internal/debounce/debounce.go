package debounce

import (
	"context"
	"sync"
	"time"
)

// Debouncer is an abstraction that allows debouncing a callback so that it may
// only be applied after a certain duration. Debouncer and its methods are safe
// to be used concurrently.
type Debouncer struct {
	Frequency time.Duration

	mut sync.Mutex
	now time.Time
	fun func(context.Context)
}

func (d *Debouncer) Run(ctx context.Context, f func(context.Context)) {
	d.mut.Lock()
	defer d.mut.Unlock()

	now := time.Now()

	then := d.now
	d.now = now

	offset := now.Sub(then)

	if offset > d.Frequency {
		f(ctx)
		return
	}

	started := d.fun != nil
	d.fun = f

	if started {
		return
	}

	t := time.NewTimer(offset)
	go func() {
		defer t.Stop()

		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.mut.Lock()
			d.fun(ctx)
			d.fun = nil
			d.mut.Unlock()
		}
	}()
}
