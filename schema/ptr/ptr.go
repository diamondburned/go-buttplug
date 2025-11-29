// Package ptr contains helper types and functions for working with optional
// types as pointers.
package ptr

// Optional indicates that a type may be omitted if null using `omitzero`.
type Optional[T any] *T

// To creates a new Optional value wrapping the given value.
func To[T any](v T) Optional[T] {
	return &v
}

// ValueOrZero returns the value or the zero value if not set.
func ValueOrZero[T any](o Optional[T]) T {
	if o == nil {
		var zero T
		return zero
	}
	return *o
}

// ValueOrDefault returns the value or the given default if not set.
func ValueOrDefault[T any](o Optional[T], def T) T {
	if o == nil {
		return def
	}
	return *o
}
