// Package hrtime provides a monotonic clock with a documented resolution, and
// a way to measure that resolution at run time.
//
// It exists because Go's time.Now() is not uniformly fine-grained across
// platforms. On the Windows 11 machine used for this paper's measurements,
// time.Now() advances in steps of roughly 600 microseconds: of 200,000
// back-to-back calls, two returned a value different from the previous one.
// Any duration measured with time.Since is therefore an integer multiple of
// that step, and a millisecond-scale measurement carries a quantisation error
// of tens of percent.
//
// That is not a hypothetical concern for this work. The block-propagation
// experiment originally reported a median of 2.3 ms, which is about four
// clock steps; 55% of the within-round samples were bit-identical to another
// peer's sample in the same round, which a continuous clock essentially never
// produces.
//
// On Windows this package reads QueryPerformanceCounter directly, which is
// backed by the HPET or the invariant TSC and typically resolves to well under
// a microsecond. On other platforms time.Now() is already fine-grained and is
// used unchanged.
//
// Resolution() reports the measured granularity so that experiments can state
// their own measurement floor rather than assuming one.
package hrtime

import (
	"fmt"
	"sort"
	"time"
)

// Now returns a monotonic reading. The zero point is arbitrary and stable
// only within a process, so readings are meaningful only as differences.
func Now() Time { return now() }

// Time is a monotonic instant from this package's clock.
type Time struct{ ticks int64 }

// Sub returns the duration from earlier to t.
func (t Time) Sub(earlier Time) time.Duration { return sub(t, earlier) }

// Since returns the duration elapsed since t.
func Since(t Time) time.Duration { return Now().Sub(t) }

// Resolution measures the smallest nonzero interval this clock can report, by
// reading it repeatedly and taking the smallest observed advance.
//
// The returned value is the measurement floor: no duration smaller than this
// can be distinguished from zero, and any measured duration is a multiple of
// it. Experiments should report it alongside their results.
func Resolution() time.Duration {
	const reads = 200000
	samples := make([]Time, reads)
	for i := range samples {
		samples[i] = Now()
	}
	smallest := time.Duration(1<<63 - 1)
	advances := 0
	for i := 1; i < reads; i++ {
		if d := samples[i].Sub(samples[i-1]); d > 0 {
			advances++
			if d < smallest {
				smallest = d
			}
		}
	}
	if advances == 0 {
		return 0
	}
	return smallest
}

// ResolutionReport describes a clock's granularity in enough detail to put in
// a methodology section.
type ResolutionReport struct {
	Name     string
	Smallest time.Duration // smallest nonzero advance observed
	Median   time.Duration // median nonzero advance
	Advanced int           // reads that differed from the previous read
	Reads    int
	CallCost time.Duration // cost of one clock read
}

func (r ResolutionReport) String() string {
	pct := 100 * float64(r.Advanced) / float64(r.Reads)
	return fmt.Sprintf(
		"%s: tick %v (median advance %v); %d/%d reads advanced (%.2f%%); %v per call",
		r.Name, r.Smallest.Round(time.Nanosecond), r.Median.Round(time.Nanosecond),
		r.Advanced, r.Reads, pct, r.CallCost.Round(time.Nanosecond))
}

// Profile characterises this package's clock.
func Profile() ResolutionReport {
	return profile("hrtime", func() int64 { return Now().ticks }, scale())
}

// ProfileStdlib characterises time.Now(), for comparison.
func ProfileStdlib() ResolutionReport {
	base := time.Now()
	return profile("time.Now", func() int64 { return int64(time.Since(base)) }, 1)
}

// profile reads a counter repeatedly and summarises how it advances. scale
// converts one counter tick to nanoseconds.
func profile(name string, read func() int64, scale float64) ResolutionReport {
	const reads = 200000
	raw := make([]int64, reads)
	for i := range raw {
		raw[i] = read()
	}

	var advances []time.Duration
	for i := 1; i < reads; i++ {
		if d := raw[i] - raw[i-1]; d > 0 {
			advances = append(advances, time.Duration(float64(d)*scale))
		}
	}

	rep := ResolutionReport{Name: name, Reads: reads, Advanced: len(advances)}
	if len(advances) > 0 {
		sort.Slice(advances, func(i, j int) bool { return advances[i] < advances[j] })
		rep.Smallest = advances[0]
		rep.Median = advances[len(advances)/2]
	}

	const costReads = 1000000
	start := time.Now()
	for i := 0; i < costReads; i++ {
		_ = read()
	}
	rep.CallCost = time.Since(start) / costReads
	return rep
}
