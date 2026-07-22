//go:build windows

package hrtime

import (
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// On Windows, Go's monotonic clock is coarse (see the package comment), so the
// performance counter is read directly. QueryPerformanceCounter is backed by
// the invariant TSC where available and by the HPET otherwise; in both cases
// QueryPerformanceFrequency reports its rate, which is fixed for the lifetime
// of the system.

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	procQPC  = kernel32.NewProc("QueryPerformanceCounter")
	procQPF  = kernel32.NewProc("QueryPerformanceFrequency")

	initOnce sync.Once
	// tickScale converts one counter tick to nanoseconds.
	tickScale float64
	// usable is false if the counter is unavailable, in which case this
	// package falls back to the standard library.
	usable bool
	fbBase time.Time
)

func initClock() {
	initOnce.Do(func() {
		fbBase = time.Now()
		var freq int64
		r, _, _ := procQPF.Call(uintptr(unsafe.Pointer(&freq)))
		if r == 0 || freq <= 0 {
			return
		}
		tickScale = 1e9 / float64(freq)
		usable = true
	})
}

func now() Time {
	initClock()
	if !usable {
		return Time{ticks: int64(time.Since(fbBase))}
	}
	var counter int64
	procQPC.Call(uintptr(unsafe.Pointer(&counter)))
	return Time{ticks: counter}
}

func sub(t, earlier Time) time.Duration {
	d := t.ticks - earlier.ticks
	return time.Duration(float64(d) * scale())
}

// scale returns nanoseconds per counter tick, initialising the clock first.
// Reading the package variable directly is a bug: it is zero until the first
// Now() call, so a caller that samples the scale before taking any reading
// converts every duration to zero.
func scale() float64 {
	initClock()
	if !usable {
		return 1 // the fallback already counts in nanoseconds
	}
	return tickScale
}
