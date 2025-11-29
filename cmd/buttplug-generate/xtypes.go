package main

func ptrEqual[T comparable](a, b *T) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return *a == *b
}

func ptr[T any](v T) *T {
	return &v
}

func isAllFunc[T any](slice []T, fn func(T) bool) bool {
	for _, v := range slice {
		if !fn(v) {
			return false
		}
	}
	return true
}
