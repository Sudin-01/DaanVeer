package blockchain

// Shared measurement and inference helpers for the experiment harnesses.
//
// Every timing figure the paper reports comes through here, so that the
// reporting is uniform: a point estimate is always a median over repeated
// runs, always carries a dispersion measure, and any claim that one
// configuration is faster than another is backed by a hypothesis test rather
// than by two numbers that happen to differ.
//
// Three choices are worth stating because they shape every number downstream.
//
// Median, not mean. Latency distributions here are right-skewed -- a garbage
// collection or a scheduler preemption inflates one sample by an order of
// magnitude -- and the mean tracks those tails rather than the typical case.
// The mean is recorded too, and a large mean/median gap is itself a signal.
//
// Bootstrap confidence intervals. The sampling distribution of a median has no
// convenient closed form and the underlying data are not normal, so the
// interval is resampled rather than derived. The seed is fixed, so a given
// data set always yields the same interval.
//
// Mann-Whitney rather than a t-test. Comparisons are of location between two
// small, non-normal, unequal-variance samples. Mann-Whitney assumes neither
// normality nor equal variance, which is the honest choice here even though it
// is the less powerful one.

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/Sudin-01/DaanVeer/internal/hrtime"
)

// repetitionsDefault is the number of independent observations per measured
// point. Ten is the floor for reporting an interval at all; microbenchmarks
// use more because each observation is cheap.
const (
	repetitionsDefault = 10
	repetitionsMicro   = 31
	bootstrapResamples = 2000
)

// dist is the full summary of one measured point. All times are milliseconds.
type dist struct {
	raw                     []float64 // sorted ascending
	n                       int
	min, p25, p50, p75, p95 float64
	p99, max                float64
	mean, sd                float64
	ciLo, ciHi              float64 // 95% CI of the median
}

func (d dist) iqr() float64 { return d.p75 - d.p25 }

// rsd is the relative standard deviation, a scale-free measure of how noisy a
// point is. Above roughly 10% the point should not carry a strong claim.
func (d dist) rsd() float64 {
	if d.mean == 0 {
		return 0
	}
	return 100 * d.sd / d.mean
}

func (d dist) String() string {
	return fmt.Sprintf("p50 %.4f [%.4f, %.4f] IQR %.4f p95 %.4f p99 %.4f (n=%d, RSD %.1f%%)",
		d.p50, d.ciLo, d.ciHi, d.iqr(), d.p95, d.p99, d.n, d.rsd())
}

// quantile uses linear interpolation between order statistics (the "type 7"
// definition, which is what NumPy and R default to).
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (pos-float64(lo))*(sorted[hi]-sorted[lo])
}

// newDist summarises raw observations. The input is copied, not aliased.
func newDist(raw []float64) dist {
	s := append([]float64(nil), raw...)
	sort.Float64s(s)

	d := dist{raw: s, n: len(s)}
	if d.n == 0 {
		return d
	}
	d.min, d.max = s[0], s[len(s)-1]
	d.p25 = quantile(s, 0.25)
	d.p50 = quantile(s, 0.50)
	d.p75 = quantile(s, 0.75)
	d.p95 = quantile(s, 0.95)
	d.p99 = quantile(s, 0.99)

	for _, v := range s {
		d.mean += v
	}
	d.mean /= float64(d.n)
	if d.n > 1 {
		var ss float64
		for _, v := range s {
			ss += (v - d.mean) * (v - d.mean)
		}
		d.sd = math.Sqrt(ss / float64(d.n-1))
	}
	d.ciLo, d.ciHi = bootstrapMedianCI(s)
	return d
}

// bootstrapMedianCI returns a percentile bootstrap 95% interval for the median.
func bootstrapMedianCI(sorted []float64) (float64, float64) {
	if len(sorted) < 3 {
		return sorted[0], sorted[len(sorted)-1]
	}
	rng := rand.New(rand.NewSource(0x5EED)) // fixed: same data, same interval
	medians := make([]float64, bootstrapResamples)
	resample := make([]float64, len(sorted))
	for i := range medians {
		for j := range resample {
			resample[j] = sorted[rng.Intn(len(sorted))]
		}
		sort.Float64s(resample)
		medians[i] = quantile(resample, 0.5)
	}
	sort.Float64s(medians)
	return quantile(medians, 0.025), quantile(medians, 0.975)
}

// measure times fn and returns its distribution in milliseconds per call.
//
// Timing goes through internal/hrtime rather than time.Now. On this platform
// time.Now advances only about once per millisecond -- of 200,000 back-to-back
// calls, two return a new value -- so a millisecond-scale duration measured
// with the standard library is quantised to a handful of levels, and a
// microsecond-scale one is indistinguishable from zero. hrtime reads the
// performance counter directly and resolves 100 ns, four orders of magnitude
// finer. See internal/hrtime and Section 9 of the paper.
//
// Each observation still times `batch` consecutive calls and divides, for two
// reasons that survive the clock fix: a single clock read costs about 140 ns,
// which is not negligible against an operation costing a microsecond, and
// averaging over a batch gives each observation a stable value whose spread
// across repetitions is what the statistics then characterise.
//
// A discarded warm-up batch precedes the measurement so that first-call costs
// (page faults, cache population, lazily built indexes) are not attributed to
// the operation.
func measure(reps, batch int, fn func()) dist {
	for j := 0; j < batch; j++ { // warm-up, discarded
		fn()
	}
	out := make([]float64, reps)
	for i := range out {
		start := hrtime.Now()
		for j := 0; j < batch; j++ {
			fn()
		}
		out[i] = float64(hrtime.Since(start).Nanoseconds()) / 1e6 / float64(batch)
	}
	return newDist(out)
}

// batchFor chooses a batch size so that one observation spans at least
// minSpan, given a rough prior estimate of the per-call cost. Sizing the batch
// to the clock rather than guessing a constant is what keeps quantisation
// error below a fraction of a percent regardless of how fast the operation is.
func batchFor(estimate, minSpan time.Duration) int {
	if estimate <= 0 {
		return 1000
	}
	n := int(minSpan / estimate)
	if n < 1 {
		n = 1
	}
	if n > 200000 {
		n = 200000
	}
	return n
}

// calibrate estimates the per-call cost of fn cheaply, so batchFor can size
// the real measurement. It doubles the call count until the elapsed time is
// comfortably above the clock's resolution.
func calibrate(fn func()) time.Duration {
	for n := 1; n <= 1<<22; n *= 2 {
		start := hrtime.Now()
		for i := 0; i < n; i++ {
			fn()
		}
		if d := hrtime.Since(start); d > 200*time.Microsecond {
			return d / time.Duration(n)
		}
	}
	return 0
}

// autoMeasure calibrates, sizes the batch so each observation spans at least
// 20 ms, and then measures. Use it in preference to measure() with a
// hand-picked batch size.
func autoMeasure(reps int, fn func()) dist {
	return measure(reps, batchFor(calibrate(fn), 20*time.Millisecond), fn)
}

// mannWhitney tests H0: the two samples come from distributions with the same
// location, against a two-sided alternative.
//
// It returns U and the two-sided p-value from the normal approximation with a
// tie correction. The approximation is adequate once both groups have about
// eight or more observations, which every comparison in this paper satisfies;
// below that the p-value should not be trusted and the function says so by
// returning a p-value of 1.
func mannWhitney(a, b []float64) (u, p float64) {
	na, nb := len(a), len(b)
	if na < 8 || nb < 8 {
		return 0, 1
	}

	type obs struct {
		v     float64
		group int
	}
	all := make([]obs, 0, na+nb)
	for _, v := range a {
		all = append(all, obs{v, 0})
	}
	for _, v := range b {
		all = append(all, obs{v, 1})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v < all[j].v })

	// Midranks, so tied observations share the average of the ranks they span.
	ranks := make([]float64, len(all))
	var tieTerm float64
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j].v == all[i].v {
			j++
		}
		mid := float64(i+j+1) / 2 // ranks are 1-based
		for k := i; k < j; k++ {
			ranks[k] = mid
		}
		if t := float64(j - i); t > 1 {
			tieTerm += t*t*t - t
		}
		i = j
	}

	var rankSumA float64
	for i, o := range all {
		if o.group == 0 {
			rankSumA += ranks[i]
		}
	}

	nA, nB := float64(na), float64(nb)
	uA := rankSumA - nA*(nA+1)/2
	uB := nA*nB - uA
	u = math.Min(uA, uB)

	mean := nA * nB / 2
	n := nA + nB
	variance := (nA * nB / 12) * ((n + 1) - tieTerm/(n*(n-1)))
	if variance <= 0 {
		return u, 1
	}
	// Continuity correction.
	z := (math.Abs(uA-mean) - 0.5) / math.Sqrt(variance)
	if z < 0 {
		z = 0
	}
	return u, 2 * (1 - normalCDF(z))
}

// normalCDF is the standard normal cumulative distribution function.
func normalCDF(z float64) float64 {
	return 0.5 * math.Erfc(-z/math.Sqrt2)
}

// significance renders a p-value the way the paper's tables do.
func significance(p float64) string {
	switch {
	case p < 0.001:
		return "p<0.001"
	case p < 0.01:
		return fmt.Sprintf("p=%.3f", p)
	default:
		return fmt.Sprintf("p=%.2f", p)
	}
}

// cliffsDelta is a non-parametric effect size on [-1, 1]: the probability that
// a random observation from a exceeds one from b, minus the reverse. It is
// reported alongside p-values because with enough repetitions a difference of
// no practical consequence still reaches significance.
func cliffsDelta(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var gt, lt int
	for _, x := range a {
		for _, y := range b {
			switch {
			case x > y:
				gt++
			case x < y:
				lt++
			}
		}
	}
	return float64(gt-lt) / float64(len(a)*len(b))
}

// cliffsMagnitude applies the conventional thresholds.
func cliffsMagnitude(d float64) string {
	switch a := math.Abs(d); {
	case a < 0.147:
		return "negligible"
	case a < 0.33:
		return "small"
	case a < 0.474:
		return "medium"
	default:
		return "large"
	}
}

// ---------------------------------------------------------------------------
// CSV output
// ---------------------------------------------------------------------------

// distColumns is the header fragment written for every measured distribution.
func distColumns(prefix string) []string {
	return []string{
		prefix + "_n", prefix + "_p50", prefix + "_ci_lo", prefix + "_ci_hi",
		prefix + "_p25", prefix + "_p75", prefix + "_p95", prefix + "_p99",
		prefix + "_mean", prefix + "_sd", prefix + "_min", prefix + "_max",
	}
}

// distValues matches distColumns.
func distValues(d dist) []string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'g', 6, 64) }
	return []string{
		strconv.Itoa(d.n), f(d.p50), f(d.ciLo), f(d.ciHi),
		f(d.p25), f(d.p75), f(d.p95), f(d.p99),
		f(d.mean), f(d.sd), f(d.min), f(d.max),
	}
}

// resultsPath resolves a filename inside experiments/results.
func resultsPath(name string) string {
	return filepath.Join("..", "experiments", "results", name)
}

// measurementRun reports whether this process was started to take a
// measurement, rather than to check correctness as part of the whole suite.
//
// The distinction is not pedantic. A timing test that runs after fifty other
// tests inherits their heap, their garbage collector state, and their
// BadgerDB page cache, and it produces different numbers as a result: the
// fund-tracing plateau measures 1.07x in a dedicated process and 1.67x at the
// end of a full suite run. Neither number is wrong about the machine, but only
// the first is a measurement of the system under test.
//
// A run counts as a measurement run when -test.run names a specific experiment
// rather than matching everything. Results are only written to disk on such a
// run, so that `go test ./...` can never silently overwrite a published data
// file with a contaminated one -- which has already happened once in this
// project's history, replacing a 10,000-block sweep with a 2,000-block one.
func measurementRun() bool {
	f := flag.Lookup("test.run")
	if f == nil {
		return false
	}
	pattern := f.Value.String()
	return pattern != "" && pattern != "." && pattern != ".*"
}

// requireMeasurementRun logs why a result is being withheld, and returns
// whether the caller should enforce publication-grade assertions.
func requireMeasurementRun(t *testing.T) bool {
	t.Helper()
	if measurementRun() {
		return true
	}
	t.Logf("NOT a dedicated measurement run: results will not be written and " +
		"tolerances are relaxed. Timings taken alongside the rest of the suite " +
		"are contaminated by its heap and cache state. To produce figures for " +
		"the paper, run this experiment alone, e.g. " +
		"`go test ./blockchain/ -run TestE11 -v`.")
	return false
}

// writeCSV writes a header and rows, reporting failure through the test log
// rather than failing: a measurement that succeeded should not be recorded as
// a test failure because the disk was full.
func writeCSV(t *testing.T, name string, header []string, rows [][]string) {
	t.Helper()
	if !measurementRun() {
		t.Logf("skipping write of %s: not a dedicated measurement run", name)
		return
	}
	path := resultsPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Logf("could not create results directory: %v", err)
		return
	}
	fh, err := os.Create(path)
	if err != nil {
		t.Logf("could not write %s: %v", name, err)
		return
	}
	defer fh.Close()

	w := csv.NewWriter(fh)
	defer w.Flush()
	_ = w.Write(header)
	for _, r := range rows {
		_ = w.Write(r)
	}
	t.Logf("wrote %s", path)
}

// writeRawSamples records every individual observation, so that the summary
// statistics in the paper can be recomputed from source and a reader can apply
// a different test if they prefer one.
func writeRawSamples(t *testing.T, name string, keyCols []string, keyed map[string]dist) {
	t.Helper()
	header := append(append([]string(nil), keyCols...), "replicate", "value_ms")
	var rows [][]string

	keys := make([]string, 0, len(keyed))
	for k := range keyed {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		for i, v := range keyed[k].raw {
			rows = append(rows, []string{k, strconv.Itoa(i + 1),
				strconv.FormatFloat(v, 'g', 6, 64)})
		}
	}
	writeCSV(t, name, header, rows)
}
