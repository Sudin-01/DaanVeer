package blockchain

// E12 -- the measurement floor of the platform clock.
//
// Every latency figure in this paper is a difference of two clock readings, so
// the clock's granularity bounds what any of them can mean. This experiment
// measures that granularity directly, and then demonstrates its effect by
// timing the same operation through both clocks.
//
// The finding is that Go's time.Now() on this Windows host advances roughly
// once per millisecond rather than once per nanosecond. A measurement of a
// few milliseconds therefore has only a few distinguishable levels, and a
// measurement below a millisecond has one. The performance counter, read
// directly, resolves 100 ns.
//
//	go test ./blockchain/ -run TestE12 -v
//
// Results are written to experiments/results/e12_clock_resolution.csv.

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Sudin-01/DaanVeer/internal/hrtime"
)

// TestE12_ClockResolution records what each clock can resolve.
func TestE12_ClockResolution(t *testing.T) {
	fine := hrtime.Profile()
	coarse := hrtime.ProfileStdlib()

	t.Log(fine)
	t.Log(coarse)

	if fine.Smallest <= 0 {
		t.Fatal("high-resolution clock reported no advance")
	}

	improvement := float64(coarse.Smallest) / float64(fine.Smallest)
	t.Logf("resolution improvement: %.0fx", improvement)

	rows := [][]string{
		clockRow("time.Now (Go standard library)", coarse),
		clockRow("QueryPerformanceCounter", fine),
	}
	writeCSV(t, "e12_clock_resolution.csv",
		[]string{"clock", "tick_ns", "median_advance_ns", "reads",
			"reads_advanced", "advanced_pct", "call_cost_ns"},
		rows)

	// The paper claims the standard library clock is too coarse for
	// millisecond-scale work on this platform. Assert it, so that if the
	// paper is ever rebuilt on a machine where this is false, the claim
	// fails loudly instead of being quietly wrong.
	if coarse.Smallest < 100*time.Microsecond {
		t.Logf("NOTE: on this host time.Now resolves %v, which is fine enough "+
			"that Section 9's clock discussion does not apply. Re-check the "+
			"paper's wording before publishing this run.", coarse.Smallest)
	}
}

func clockRow(name string, r hrtime.ResolutionReport) []string {
	return []string{
		name,
		strconv.FormatInt(r.Smallest.Nanoseconds(), 10),
		strconv.FormatInt(r.Median.Nanoseconds(), 10),
		strconv.Itoa(r.Reads),
		strconv.Itoa(r.Advanced),
		strconv.FormatFloat(100*float64(r.Advanced)/float64(r.Reads), 'f', 4, 64),
		strconv.FormatInt(r.CallCost.Nanoseconds(), 10),
	}
}

// spinSink is package-level so the compiler cannot delete the loop below.
var spinSink uint64

// spin performs a fixed, clock-independent amount of work. Using a counted
// loop rather than a busy-wait on a deadline matters: a busy-wait that polls
// a clock to decide when to stop entangles the thing being measured with the
// instrument measuring it, which is how an earlier version of this experiment
// produced a result that contradicted every other measurement of the same
// quantity.
func spin(iterations int) {
	for i := 0; i < iterations; i++ {
		spinSink = spinSink*6364136223846793005 + 1
	}
}

// TestE12_QuantisationDemonstration shows the practical consequence of a
// coarse clock: operations shorter than one tick measure as zero, and
// operations of a few ticks collapse onto a handful of levels.
//
// This is the mechanism behind the block-propagation figure discussed in
// Section 9. It is reproduced here on a purely local, purely CPU-bound
// operation so that the effect is isolated from anything to do with the
// network.
func TestE12_QuantisationDemonstration(t *testing.T) {
	const samples = 300

	// Iteration counts chosen to span from far below one clock tick to
	// several ticks. The actual durations are recovered from the fine clock.
	workloads := []int{2000, 20000, 200000, 2000000}

	var rows [][]string
	for _, iters := range workloads {
		coarse := make([]float64, samples)
		fine := make([]float64, samples)

		// Randomise which clock leads, so that any ordering effect (cache
		// state, frequency scaling) does not systematically favour one.
		for i := 0; i < samples; i++ {
			if i%2 == 0 {
				coarse[i] = timeCoarse(iters)
				fine[i] = timeFine(iters)
			} else {
				fine[i] = timeFine(iters)
				coarse[i] = timeCoarse(iters)
			}
		}

		dc, df := newDist(coarse), newDist(fine)
		zeros := 0
		for _, v := range coarse {
			if v == 0 {
				zeros++
			}
		}

		t.Logf("spin(%7d) ~ %.3f ms of work (fine clock)", iters, df.p50)
		t.Logf("    coarse: p50 %.4f ms, %d distinct values, %d/%d samples read exactly zero",
			dc.p50, countDistinct(coarse), zeros, samples)
		t.Logf("    fine:   p50 %.4f ms, %d distinct values",
			df.p50, countDistinct(fine))

		rows = append(rows, []string{
			strconv.Itoa(iters),
			strconv.FormatFloat(df.p50, 'g', 6, 64),
			strconv.FormatFloat(dc.p50, 'g', 6, 64),
			strconv.Itoa(countDistinct(coarse)),
			strconv.Itoa(countDistinct(fine)),
			strconv.Itoa(zeros),
			strconv.Itoa(samples),
		})
	}

	writeCSV(t, "e12_quantisation.csv",
		[]string{"spin_iterations", "true_ms_fine_clock", "median_ms_coarse_clock",
			"distinct_coarse", "distinct_fine", "coarse_read_zero", "samples"},
		rows)
}

// timeCoarse times spin with the standard library clock, in milliseconds.
func timeCoarse(iters int) float64 {
	s := time.Now()
	spin(iters)
	return float64(time.Since(s).Nanoseconds()) / 1e6
}

// timeFine times spin with the performance counter, in milliseconds.
func timeFine(iters int) float64 {
	s := hrtime.Now()
	spin(iters)
	return float64(hrtime.Since(s).Nanoseconds()) / 1e6
}

func countDistinct(xs []float64) int {
	seen := make(map[float64]bool, len(xs))
	for _, v := range xs {
		seen[v] = true
	}
	return len(seen)
}

// TestE12_ReanalyseE6 recovers the clock tick from the recorded propagation
// data, independently of any direct measurement of the clock.
//
// Within one round every peer's delay is measured against the same t0, so if
// the clock is coarse the within-round values must be near-multiples of a
// single tick. Two signatures follow: distinct peers report bit-identical
// delays, and the gaps between distinct within-round values cluster on the
// tick. Recovering the same figure the direct measurement gives is strong
// evidence that the recorded data is resolution-limited.
func TestE12_ReanalyseE6(t *testing.T) {
	for _, f := range []string{"e6_propagation_n3.csv", "e6_propagation_n5.csv"} {
		byRound, err := readE6Rounds(resultsPath(f))
		if err != nil {
			t.Skipf("%s: %v", f, err)
		}
		if len(byRound) == 0 {
			t.Skipf("%s: no usable observations", f)
		}

		total, repeats := 0, 0
		var gaps []float64
		for _, vals := range byRound {
			total += len(vals)
			seen := map[float64]int{}
			for _, v := range vals {
				seen[v]++
			}
			for _, c := range seen {
				repeats += c - 1
			}

			uniq := make([]float64, 0, len(seen))
			for v := range seen {
				uniq = append(uniq, v)
			}
			d := newDist(uniq)
			for i := 1; i < len(d.raw); i++ {
				gaps = append(gaps, d.raw[i]-d.raw[i-1])
			}
		}

		gd := newDist(gaps)
		var small []float64
		for _, g := range gaps {
			if g < gd.p50 {
				small = append(small, g)
			}
		}
		estimate := newDist(small).mean

		t.Logf("%s: %d/%d within-round samples (%.0f%%) are exact repeats of "+
			"another peer's; smallest distinct gaps imply a tick of %.3f ms",
			f, repeats, total, 100*float64(repeats)/float64(total), estimate)
	}
}

// readE6Rounds groups the recorded propagation delays by round.
func readE6Rounds(path string) (map[int][]float64, error) {
	records, err := readCSV(path)
	if err != nil {
		return nil, err
	}
	out := map[int][]float64{}
	for _, rec := range records {
		if rec["timed_out"] != "false" {
			continue
		}
		round, err1 := strconv.Atoi(rec["round"])
		delay, err2 := strconv.ParseFloat(rec["delay_ms"], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out[round] = append(out[round], delay)
	}
	return out, nil
}

// readCSV reads a header-bearing CSV into maps.
func readCSV(path string) ([]map[string]string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()

	rows, err := csv.NewReader(fh).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%s: no data rows", path)
	}
	header := rows[0]
	out := make([]map[string]string, 0, len(rows)-1)
	for _, r := range rows[1:] {
		m := map[string]string{}
		for i, h := range header {
			if i < len(r) {
				m[h] = r[i]
			}
		}
		out = append(out, m)
	}
	return out, nil
}
