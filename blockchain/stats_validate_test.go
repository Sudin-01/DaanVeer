package blockchain

// Validation of the measurement statistics against an independent
// implementation.
//
// Every performance claim in the paper is filtered through quantile() and
// mannWhitney() in stats_test.go, both written by hand. A silent error in
// either would not fail any test -- it would just produce a plausible wrong
// number, which is precisely the failure mode Section 9 of the paper is about.
// So they are checked against scipy, whose values are frozen into
// stats_ref_test.go by scratchpad/gen_stats_ref.py.

import (
	"math"
	"math/rand"
	"testing"
)

func TestStats_QuantileMatchesNumPy(t *testing.T) {
	const tol = 1e-9
	for _, ref := range statsRefs {
		d := newDist(ref.a)
		for _, c := range []struct {
			name string
			got  float64
			want float64
		}{
			{"p25", d.p25, ref.q25},
			{"p50", d.p50, ref.q50},
			{"p95", d.p95, ref.q95},
		} {
			if math.Abs(c.got-c.want) > tol {
				t.Errorf("%s/%s: got %.10f, numpy %.10f (diff %.2e)",
					ref.name, c.name, c.got, c.want, math.Abs(c.got-c.want))
			}
		}
	}
}

func TestStats_MannWhitneyMatchesSciPy(t *testing.T) {
	for _, ref := range statsRefs {
		u, p := mannWhitney(ref.a, ref.b)

		if math.Abs(u-ref.u) > 1e-9 {
			t.Errorf("%s: U = %.4f, scipy %.4f", ref.name, u, ref.u)
		}
		// p-values are compared relatively, with a tolerance loose enough to
		// absorb the difference between two normal-tail implementations. At
		// p ~ 1e-7 the two agree to four significant figures but not to
		// machine precision, because math.Erfc and scipy's ndtr lose
		// different amounts in the far tail. A genuine algorithmic error --
		// a missing tie correction, a wrong variance, ranks off by one --
		// moves a p-value by orders of magnitude, not by parts per thousand,
		// so this tolerance still catches everything worth catching.
		if rel := math.Abs(p-ref.p) / math.Max(ref.p, 1e-12); rel > 1e-3 {
			t.Errorf("%s: p = %.10g, scipy %.10g (relative diff %.2e)",
				ref.name, p, ref.p, rel)
		}
		t.Logf("%-12s U=%8.1f  %s  (scipy p=%.4g)",
			ref.name, u, significance(p), ref.p)
	}
}

// The tie correction matters: without it, a heavily tied sample gets an
// understated variance and therefore an overstated significance. This asserts
// the correction is actually applied rather than silently skipped.
func TestStats_TieCorrectionIsApplied(t *testing.T) {
	var tied statsRef
	for _, ref := range statsRefs {
		if ref.name == "tied" {
			tied = ref
		}
	}
	if tied.name == "" {
		t.Skip("no tied reference case")
	}

	_, withTies := mannWhitney(tied.a, tied.b)

	// Perturb each value by a distinct negligible amount, breaking every tie
	// without meaningfully moving any observation.
	perturb := func(xs []float64, offset int) []float64 {
		out := make([]float64, len(xs))
		for i, v := range xs {
			out[i] = v + float64(offset+i)*1e-9
		}
		return out
	}
	_, withoutTies := mannWhitney(perturb(tied.a, 0), perturb(tied.b, 1000))

	if math.Abs(withTies-withoutTies) < 1e-6 {
		t.Errorf("tie correction appears not to be applied: p is %.6g either way",
			withTies)
	}
	t.Logf("tied data: p=%.6g with correction, %.6g without", withTies, withoutTies)
}

// A confidence interval is only meaningful if it has the coverage it claims.
// This draws many samples from a known distribution and counts how often the
// interval contains the true median. Nominal coverage is 95%; the percentile
// bootstrap is approximate, so the test accepts a band around it rather than
// the exact figure.
func TestStats_BootstrapCICoverage(t *testing.T) {
	if testing.Short() {
		t.Skip("coverage simulation is slow")
	}
	const (
		trials     = 400
		sampleSize = repetitionsDefault
		trueMedian = 100.0
	)
	rng := rand.New(rand.NewSource(7))
	covered := 0
	for i := 0; i < trials; i++ {
		xs := make([]float64, sampleSize)
		for j := range xs {
			// Log-normal: right-skewed, like the latency data, and with a
			// median of exactly trueMedian.
			xs[j] = trueMedian * math.Exp(rng.NormFloat64()*0.3)
		}
		d := newDist(xs)
		if d.ciLo <= trueMedian && trueMedian <= d.ciHi {
			covered++
		}
	}
	rate := 100 * float64(covered) / trials
	t.Logf("bootstrap CI covered the true median in %.1f%% of %d trials (nominal 95%%)",
		rate, trials)
	if rate < 85 || rate > 99.5 {
		t.Errorf("coverage %.1f%% is too far from the nominal 95%%", rate)
	}
}

// Cliff's delta has known values at the extremes; check the boundaries and the
// symmetric case rather than trusting the loop.
func TestStats_CliffsDelta(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{4, 5, 6}
	if got := cliffsDelta(a, b); got != -1 {
		t.Errorf("fully dominated: delta = %v, want -1", got)
	}
	if got := cliffsDelta(b, a); got != 1 {
		t.Errorf("fully dominating: delta = %v, want 1", got)
	}
	if got := cliffsDelta(a, a); got != 0 {
		t.Errorf("identical samples: delta = %v, want 0", got)
	}
	if got := cliffsMagnitude(0.05); got != "negligible" {
		t.Errorf("magnitude(0.05) = %q", got)
	}
	if got := cliffsMagnitude(0.9); got != "large" {
		t.Errorf("magnitude(0.9) = %q", got)
	}
}

// measureSink is package-level so the compiler cannot prove the writes below
// are dead and delete the loop being timed.
var measureSink uint64

// measure must not report a sub-microsecond operation as zero. This is the
// bug that made the balance index appear to cost 0.000 ms.
func TestStats_MeasureResolvesFastOperations(t *testing.T) {
	d := autoMeasure(repetitionsMicro, func() { measureSink = measureSink*6364136223846793005 + 1 })
	if d.p50 <= 0 {
		t.Fatalf("timed a non-empty operation at %.6f ms/op; batching failed", d.p50)
	}
	if measureSink == 0 {
		t.Fatal("operation was optimised away")
	}
	t.Logf("single multiply-add: %s", d)
}

// autoMeasure must return the same answer whatever batch size the calibration
// picks. If the reported cost depended on the batch size, the batching would
// be contributing to the measurement rather than just resolving it.
func TestStats_MeasureIsBatchIndependent(t *testing.T) {
	work := func() { measureSink = measureSink*6364136223846793005 + 1 }

	var results []dist
	for _, batch := range []int{1000, 10000, 100000} {
		d := measure(repetitionsMicro, batch, work)
		results = append(results, d)
		t.Logf("batch %6d: %.6f ms/op (IQR %.6f)", batch, d.p50, d.iqr())
	}

	lo, hi := results[0].p50, results[0].p50
	for _, d := range results {
		lo = math.Min(lo, d.p50)
		hi = math.Max(hi, d.p50)
	}
	if lo > 0 && hi/lo > 1.5 {
		t.Errorf("per-op cost varies %.2fx across batch sizes (%.6f to %.6f ms); "+
			"batching is affecting the measurement", hi/lo, lo, hi)
	}
}
