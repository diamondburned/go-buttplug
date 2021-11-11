package errorbox

import "sync/atomic"

// Box is a mutex-guarded error box.
type Box struct {
	v atomic.Value
}

type value struct{ error }

func (b *Box) Set(err error) {
	b.v.Store(value{err})
}

func (b *Box) Get() error {
	v, ok := b.v.Load().(value)
	if ok {
		return v.error
	}
	return nil
}
