package debounce

import (
	"sort"
	"testing"
	"time"
)

func TestDebounce(t *testing.T) {
	d := Debouncer{
		Frequency: 500 * time.Millisecond,
	}

	ch := make(chan int)

	go func() {
		for i := 0; i < 5; i++ {
			i := i
			d.Run(func() { ch <- i })
		}
	}()

	after := time.After(time.Second)
	var values []int

loop:
	for {
		select {
		case v := <-ch:
			values = append(values, v)
		case <-after:
			break loop
		}
	}

	if len(values) != 2 {
		t.Fatalf("did not get 2 values: %#v", values)
	}

	sort.Ints(values)

	for i, v := range []int{0, 4} {
		if v != values[i] {
			t.Fatalf("(values[%d] = %d) = %d", i, values[i], v)
		}
	}
}
