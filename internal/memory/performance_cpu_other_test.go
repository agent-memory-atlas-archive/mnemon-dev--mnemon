//go:build !darwin && !linux

package memory_test

import "time"

// The portable benchmarks still report wall time and allocations elsewhere.
func processCPUTime() time.Duration { return 0 }
