package blockchain

// E4 -- balance query cost against chain length.
//
// Balances were recomputed by replaying the whole chain on every query, and
// NewTransaction runs that query before admitting each donation, so the cost of
// accepting a donation grew linearly with the ledger's history. This measures
// the scan against the index that replaces it.
//
//	go test ./blockchain/ -run TestE4 -v
//	go test ./blockchain/ -run TestE4 -v -e4-max=50000 -timeout 60m
//
// Every point is repeated and reported as a median with a bootstrap interval;
// the scan-versus-index comparison at each length is tested with Mann-Whitney
// rather than asserted from the point estimates. Timing goes through
// internal/hrtime, because the standard library clock on this platform cannot
// resolve the indexed lookup at all -- see E12.
//
// Results are written to experiments/results/e4_balance_scaling.csv, with
// every individual observation in e4_balance_samples.csv.

import (
	"flag"
	"strconv"
	"testing"

	"github.com/Sudin-01/DaanVeer/wallet"
)

var (
	e4Max  = flag.Int("e4-max", 2000, "longest chain to measure in E4")
	e4Reps = flag.Int("e4-reps", 0, "observations per point (0 selects a default)")
)

// buildChain appends n blocks, each carrying one donation from donor.
func buildChain(t *testing.T, chain *BlockChain, validator, donor *wallet.Wallet, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		recipient := newTestWallet(t)
		blk := CreateBlock()
		blk.Height = chain.GetHeight() + 1
		blk.PreviousHash = chain.LastHash
		if err := blk.AddTxToBlock([]Transactions{mkTx(t, donor, recipient, 1)}); err != nil {
			t.Fatalf("block %d: add tx: %v", i, err)
		}
		if err := ProofOfAuthority(blk, validator); err != nil {
			t.Fatalf("block %d: sign: %v", i, err)
		}
		blk.BlockHash = blk.Hash()
		if _, err := chain.AcceptBlock(blk); err != nil {
			t.Fatalf("block %d: accept: %v", i, err)
		}
	}
}

// e4Row is one measured chain length.
type e4Row struct {
	blocks  int
	scan    dist
	index   dist
	speedup float64
	p       float64
	delta   float64
}

func TestE4_BalanceQueryScaling(t *testing.T) {
	lengths := []int{10, 100, 500, 1000}
	for _, extra := range []int{2000, 5000, 10000, 20000, 50000, 100000} {
		if *e4Max >= extra {
			lengths = append(lengths, extra)
		}
	}

	var rows []e4Row
	samples := map[string]dist{}

	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	donor := newTestWallet(t)
	fundWallet(t, chain, donor, uint64(*e4Max)+1000)

	built := 0
	for _, target := range lengths {
		buildChain(t, chain, validator, donor, target-built)
		built = target

		// Correctness first: a performance comparison between two functions
		// that disagree is meaningless.
		scanned, err := chain.ScanWalletBalance(donor.Address)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		indexed, err := chain.IndexedBalance(donor.Address)
		if err != nil {
			t.Fatalf("index: %v", err)
		}
		if scanned != indexed {
			t.Fatalf("at %d blocks: scan=%d index=%d", built, scanned, indexed)
		}

		// The scan gets expensive on long chains, so it gets fewer
		// observations -- but never so few that an interval is meaningless.
		reps := *e4Reps
		if reps == 0 {
			switch {
			case built > 20000:
				reps = 10
			case built > 5000:
				reps = 15
			default:
				reps = repetitionsDefault * 2
			}
		}

		// The scan is milliseconds and needs no batching; the indexed lookup
		// is microseconds and is batched automatically.
		scanD := measure(reps, 1, func() { _, _ = chain.ScanWalletBalance(donor.Address) })
		indexD := autoMeasure(repetitionsMicro, func() { _, _ = chain.IndexedBalance(donor.Address) })

		speedup := 0.0
		if indexD.p50 > 0 {
			speedup = scanD.p50 / indexD.p50
		}
		_, p := mannWhitney(scanD.raw, indexD.raw)
		delta := cliffsDelta(scanD.raw, indexD.raw)

		rows = append(rows, e4Row{built, scanD, indexD, speedup, p, delta})
		samples[strconv.Itoa(built)+"|scan"] = scanD
		samples[strconv.Itoa(built)+"|index"] = indexD

		t.Logf("%6d blocks:", built)
		t.Logf("    scan  %s", scanD)
		t.Logf("    index %s", indexD)
		t.Logf("    speedup %.0fx, %s, Cliff's delta %.2f (%s)",
			speedup, significance(p), delta, cliffsMagnitude(delta))
	}

	// The scan must grow with chain length; the index must not.
	first, last := rows[0], rows[len(rows)-1]
	growth := last.scan.p50 / first.scan.p50
	lengthRatio := float64(last.blocks) / float64(first.blocks)
	t.Logf("chain grew %.0fx; scan cost grew %.1fx; index cost %.5f -> %.5f ms",
		lengthRatio, growth, first.index.p50, last.index.p50)

	if growth < lengthRatio/4 {
		t.Errorf("scan cost grew only %.1fx over a %.0fx longer chain; expected roughly linear",
			growth, lengthRatio)
	}
	if last.index.p50 > first.index.p50*10+1 {
		t.Errorf("index cost grew from %.5f to %.5f ms; expected roughly constant",
			first.index.p50, last.index.p50)
	}

	// The scan being slower than the index is the claim; test it rather than
	// reading it off the medians.
	for _, r := range rows {
		if r.p > 0.05 {
			t.Errorf("at %d blocks the scan/index difference is not significant (%s)",
				r.blocks, significance(r.p))
		}
	}

	writeE4CSV(t, rows)
	writeRawSamples(t, "e4_balance_samples.csv", []string{"blocks_and_method"}, samples)
}

func writeE4CSV(t *testing.T, rows []e4Row) {
	t.Helper()
	header := append([]string{"blocks"}, distColumns("scan_ms")...)
	header = append(header, distColumns("index_ms")...)
	header = append(header, "speedup", "mannwhitney_p", "cliffs_delta")

	var out [][]string
	for _, r := range rows {
		rec := append([]string{strconv.Itoa(r.blocks)}, distValues(r.scan)...)
		rec = append(rec, distValues(r.index)...)
		rec = append(rec,
			strconv.FormatFloat(r.speedup, 'f', 1, 64),
			strconv.FormatFloat(r.p, 'g', 4, 64),
			strconv.FormatFloat(r.delta, 'f', 4, 64))
		out = append(out, rec)
	}
	writeCSV(t, "e4_balance_scaling.csv", header, out)
}
