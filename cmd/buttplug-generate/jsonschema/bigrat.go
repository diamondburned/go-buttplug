package jsonschema

import (
	"math"
	"math/big"
)

// deal with this dumbass library that for some reason uses big.Rat for numbers
// instead of something sane like [json.Number]??
func bigratToNum[T int | float64](b *big.Rat) *T {
	if b == nil {
		return nil
	}
	f64, _ := b.Float64()
	v := new(T)
	switch anyV := any(v).(type) {
	case *int:
		*anyV = int(math.Round(f64))
	case *float64:
		*anyV = f64
	}
	return v
}
