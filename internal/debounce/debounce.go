package debounce

import (
	"sync"
	"time"
)

// Debouncer is an abstraction that allows debouncing a callback so that it may
// only be applied
type Debouncer struct {
	Frequency time.Duration

	mut sync.Mutex
	now time.Time
	fun func()
}

func (d *Debouncer) Run(f func()) {
	d.mut.Lock()
	defer d.mut.Unlock()

	now := time.Now()

	then := d.now
	d.now = now

	offset := now.Sub(then)

	if offset > d.Frequency {
		go f()
		return
	}

	started := d.fun != nil
	d.fun = f

	if started {
		return
	}

	time.AfterFunc(offset, func() {
		d.mut.Lock()
		fn := d.fun
		d.fun = nil
		d.mut.Unlock()

		fn()
	})
}
