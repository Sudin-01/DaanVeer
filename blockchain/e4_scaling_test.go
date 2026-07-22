package blockchain

// E4 -- balance query cost against chain length.
//
// Balances were recomputed by replaying the whole chain on every query, and
// NewTransaction runs that query before admitting each donation, so the cost of
// accepting a donation grew linearly with the ledger's history. This measures
// the scan against the index that replaces it.
//
//	go test ./blockchain/ -run TestE4 -v
//	go test ./blockchain/ -run TestE4 -v -e4-max=10000    (longer chains)
//
// Results are written to experiments/results/e4_balance_scaling.csv.

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/Sudin-01/DaanVeer/wallet"
)

var e4Max = flag.Int("e4-max", 2000, "longest chain to measure in E4")

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

// medianPerOp times `batch` calls together and returns the median per-call
// duration across `samples` batches.
//
// Batching matters for the index path: a single lookup is faster than the
// resolution of a naive time.Since, which truncates to whole microseconds and
// reports a genuine sub-microsecond result as zero.
func medianPerOp(samples, batch int, fn func()) time.Duration {
	observations := make([]time.Duration, samples)
	for i := range observations {
		start := time.Now()
		for j := 0; j < batch; j++ {
			fn()
		}
		observations[i] = time.Since(start) / time.Duration(batch)
	}
	sort.Slice(observations, func(i, j int) bool { return observations[i] < observations[j] })
	return observations[len(observations)/2]
}

// milliseconds converts with nanosecond precision retained.
func milliseconds(d time.Duration) float64 {
	return float64(d.Nanoseconds()) / 1e6
}

func TestE4_BalanceQueryScaling(t *testing.T) {
	lengths := []int{10, 100, 500, 1000}
	for _, extra := range []int{2000, 5000, 10000} {
		if *e4Max >= extra {
			lengths = append(lengths, extra)
		}
	}

	var rows []e4Row

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

		samples := 21
		if built > 2000 {
			samples = 5 // the scan gets expensive
		}
		scanTime := medianPerOp(samples, 1, func() { _, _ = chain.ScanWalletBalance(donor.Address) })
		indexTime := medianPerOp(21, 1000, func() { _, _ = chain.IndexedBalance(donor.Address) })

		scanMS := milliseconds(scanTime)
		indexMS := milliseconds(indexTime)
		speedup := 0.0
		if indexMS > 0 {
			speedup = scanMS / indexMS
		}
		rows = append(rows, e4Row{built, scanMS, indexMS, speedup})
		t.Logf("%6d blocks: scan %9.3f ms   index %8.5f ms   speedup %8.0fx",
			built, scanMS, indexMS, speedup)
	}

	// The scan must grow with chain length; the index must not.
	first, last := rows[0], rows[len(rows)-1]
	growth := last.scanMS / first.scanMS
	lengthRatio := float64(last.blocks) / float64(first.blocks)
	t.Logf("chain grew %.0fx; scan cost grew %.1fx; index cost %.5f -> %.5f ms",
		lengthRatio, growth, first.indexMS, last.indexMS)

	if growth < lengthRatio/4 {
		t.Errorf("scan cost grew only %.1fx over a %.0fx longer chain; expected roughly linear",
			growth, lengthRatio)
	}
	if last.indexMS > first.indexMS*10+1 {
		t.Errorf("index cost grew from %.3f to %.3f ms; expected roughly constant",
			first.indexMS, last.indexMS)
	}

	writeE4CSV(t, rows)
}

// e4Row is one measured chain length.
type e4Row struct {
	blocks          int
	scanMS, indexMS float64
	speedup         float64
}

func writeE4CSV(t *testing.T, rows []e4Row) {
	t.Helper()
	path := filepath.Join("..", "experiments", "results", "e4_balance_scaling.csv")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Logf("could not create results directory: %v", err)
		return
	}
	fh, err := os.Create(path)
	if err != nil {
		t.Logf("could not write results: %v", err)
		return
	}
	defer fh.Close()

	w := csv.NewWriter(fh)
	defer w.Flush()
	_ = w.Write([]string{"blocks", "scan_ms", "index_ms", "speedup"})
	for _, r := range rows {
		_ = w.Write([]string{
			strconv.Itoa(r.blocks),
			strconv.FormatFloat(r.scanMS, 'f', 4, 64),
			strconv.FormatFloat(r.indexMS, 'f', 4, 64),
			strconv.FormatFloat(r.speedup, 'f', 1, 64),
		})
	}
	fmt.Printf("wrote %s\n", path)
}
