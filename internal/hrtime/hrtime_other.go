//go:build !windows

package hrtime

import "time"

// Elsewhere the standard library's monotonic clock is already fine-grained
// (nanosecond-resolution on Linux and macOS), so it is used unchanged.
// Resolution() will report whatever the platform actually delivers, so an
// experiment run on another machine still states its own measurement floor
// rather than inheriting this one.

var base = time.Now()

// scale is 1 because ticks are already nanoseconds here.
func scale() float64 { return 1 }

func now() Time { return Time{ticks: int64(time.Since(base))} }

func sub(t, earlier Time) time.Duration {
	return time.Duration(t.ticks - earlier.ticks)
}
