package main

import (
	"iter"
)

func all[T comparable](iter iter.Seq[T], v T) bool {
	return allFunc(iter, func(t T) bool { return t == v })
}

func allFunc[T any](iter iter.Seq[T], fn func(T) bool) bool {
	for v := range iter {
		if !fn(v) {
			return false
		}
	}
	return true
}

func generate(count int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range count {
			if !yield(i) {
				return
			}
		}
	}
}
