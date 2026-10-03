// Package topk retains a bounded set of best candidates without sorting or
// storing all matches. Callers own relevance and tie-breaking policy.
package topk

import "sort"

// Selector is a worst-first heap. It belongs to one synchronous selection.
type Selector[T any] struct {
	items  []T
	limit  int
	better func(T, T) bool
}

// New requires a positive limit and a strict ordering. better must be pure and
// nonblocking; equal candidates do not displace an already retained candidate.
func New[T any](limit int, better func(T, T) bool) *Selector[T] {
	if limit <= 0 {
		panic("topk: limit must be positive")
	}
	return &Selector[T]{limit: limit, better: better}
}

// Add takes O(log K) time and keeps at most K values. Typed heap operations avoid
// boxing each candidate into container/heap's any-shaped Push/Pop interface.
func (s *Selector[T]) Add(value T) {
	if len(s.items) < s.limit {
		s.items = append(s.items, value)
		for i := len(s.items) - 1; i > 0; {
			parent := (i - 1) / 2
			if !s.better(s.items[parent], s.items[i]) {
				break
			}
			s.items[parent], s.items[i] = s.items[i], s.items[parent]
			i = parent
		}
		return
	}
	if !s.better(value, s.items[0]) {
		return
	}
	s.items[0] = value
	for i := 0; ; {
		child := 2*i + 1
		if child >= len(s.items) {
			break
		}
		if child+1 < len(s.items) && s.better(s.items[child], s.items[child+1]) {
			child++
		}
		if !s.better(s.items[i], s.items[child]) {
			break
		}
		s.items[i], s.items[child] = s.items[child], s.items[i]
		i = child
	}
}

// Sorted consumes the selector and returns its owned slice, best first.
// Do not call Add after Sorted.
func (s *Selector[T]) Sorted() []T {
	sort.Slice(s.items, func(i, j int) bool { return s.better(s.items[i], s.items[j]) })
	return s.items
}
