//go:build darwin || linux

package memory_test

import (
	"time"

	"golang.org/x/sys/unix"
)

// Process CPU time distinguishes algorithm work from descheduling on a shared
// developer machine. It includes user/system time and GC, but not time blocked
// on disk. This metric complements (and never replaces) ns/op wall time.
func processCPUTime() time.Duration {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	return time.Duration(usage.Utime.Sec+usage.Stime.Sec)*time.Second +
		time.Duration(usage.Utime.Usec+usage.Stime.Usec)*time.Microsecond
}
