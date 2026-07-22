package hrtime

import (
	"testing"
	"time"
)

// The whole point of this package is a finer tick than the standard library's
// on this platform. If that is not true, the package is pointless and the
// experiments should say so.
func TestResolutionBeatsStdlib(t *testing.T) {
	mine := Profile()
	std := ProfileStdlib()

	t.Log(mine)
	t.Log(std)

	if mine.Advanced == 0 {
		t.Fatal("clock never advanced across 200,000 reads")
	}
	if mine.Smallest <= 0 {
		t.Fatal("clock reported a non-positive tick")
	}
	if mine.Smallest > std.Smallest {
		t.Errorf("hrtime tick %v is coarser than time.Now's %v; the package is not earning its keep",
			mine.Smallest, std.Smallest)
	}
	t.Logf("resolution improvement: %.0fx", float64(std.Smallest)/float64(mine.Smallest))
}

// A clock that resolves finely but measures wrongly is worse than a coarse
// one. This checks hrtime against a known interval, using time.Sleep, which is
// independent of the counter being tested.
func TestAgreesWithWallClockOverLongIntervals(t *testing.T) {
	for _, d := range []time.Duration{20 * time.Millisecond, 100 * time.Millisecond} {
		start := Now()
		wallStart := time.Now()
		time.Sleep(d)
		got := Since(start)
		wall := time.Since(wallStart)

		// Sleep overshoots, so both clocks should agree with each other far
		// more closely than either agrees with the requested duration.
		diff := got - wall
		if diff < 0 {
			diff = -diff
		}
		tolerance := wall / 100 // 1%
		if tolerance < time.Millisecond {
			tolerance = time.Millisecond
		}
		if diff > tolerance {
			t.Errorf("sleep(%v): hrtime %v, time.Now %v, disagree by %v (tolerance %v)",
				d, got, wall, diff, tolerance)
		} else {
			t.Logf("sleep(%v): hrtime %v, time.Now %v, agree within %v", d, got, wall, diff)
		}
	}
}

func TestMonotonic(t *testing.T) {
	prev := Now()
	for i := 0; i < 100000; i++ {
		cur := Now()
		if cur.Sub(prev) < 0 {
			t.Fatalf("clock went backwards at read %d", i)
		}
		prev = cur
	}
}

// Sub-microsecond durations must be distinguishable from zero. This is the
// property time.Now() lacks here and the reason the balance-index measurement
// originally reported 0.000 ms.
func TestResolvesSubMicrosecond(t *testing.T) {
	nonzero := 0
	const trials = 1000
	for i := 0; i < trials; i++ {
		start := Now()
		_ = make([]byte, 64) // small, but far from free
		if Since(start) > 0 {
			nonzero++
		}
	}
	if nonzero < trials/2 {
		t.Errorf("only %d of %d sub-microsecond intervals were nonzero", nonzero, trials)
	}
	t.Logf("%d of %d sub-microsecond intervals resolved as nonzero", nonzero, trials)
}
