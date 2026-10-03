package topk

import (
	"math/bits"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func TestSelectionMatchesFullSort(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	for _, n := range []int{0, 1, 10, 100, 1000} {
		for _, k := range []int{1, 3, 5, 20, 1001} {
			for _, ties := range []bool{false, true} {
				values := rng.Perm(n)
				if ties {
					for i := range values {
						values[i] %= 7
					}
				}
				s := New(k, func(a, b int) bool { return a > b })
				for _, value := range values {
					s.Add(value)
				}
				want := append([]int(nil), values...)
				sort.Sort(sort.Reverse(sort.IntSlice(want)))
				want = want[:min(k, n)]
				if got := s.Sorted(); !reflect.DeepEqual(got, want) {
					t.Fatalf("n=%d k=%d ties=%v: got %v, want %v", n, k, ties, got, want)
				}
			}
		}
	}
}

func TestComparisonBudget(t *testing.T) {
	const k = 5
	for _, n := range []int{1, 10, 100, 1000, 10000} {
		comparisons := 0
		s := New(k, func(a, b int) bool { comparisons++; return a > b })
		// Ascending input forces replacement of the root on every full-heap Add.
		for i := 0; i < n; i++ {
			s.Add(i)
		}
		s.Sorted()
		budget := n*(2*bits.Len(uint(k))+1) + k*k
		if comparisons > budget {
			t.Fatalf("N=%d: %d comparisons exceed O(N log K) budget %d", n, comparisons, budget)
		}
		t.Logf("N=%d K=%d comparisons=%d", n, k, comparisons)
	}
}

func TestTiePolicyAndSmallestSelection(t *testing.T) {
	type item struct{ score, id int }
	better := func(a, b item) bool {
		if a.score != b.score {
			return a.score < b.score
		}
		return a.id < b.id
	}
	s := New(2, better)
	for _, v := range []item{{3, 0}, {1, 2}, {2, 3}, {1, 1}, {1, 0}} {
		s.Add(v)
	}
	if got, want := s.Sorted(), []item{{1, 0}, {1, 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
