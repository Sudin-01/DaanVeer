package blockchain

// E11 -- cost of a fund-tracing query.
//
// The claim being tested is that tracing a campaign costs time proportional to
// the number of accounts holding that campaign, not to the length of the chain.
// A donation platform's ledger grows indefinitely; a trace query that grew with
// it would become unusable exactly when the audit history got interesting.
//
//	go test ./blockchain/ -run TestE11 -v
//
// Results are written to experiments/results/e11_tracing.csv.

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Sudin-01/DaanVeer/wallet"
)

type e11Row struct {
	blocks, holders int
	traceMS         float64
	total           uint64
}

// Trace cost against chain length, with the number of holders held constant.
func TestE11_TraceCostVsChainLength(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	donor := newTestWallet(t)
	fundWallet(t, chain, donor, 2_000_000)

	// A fixed set of campaign holders, established once.
	const holders = 20
	recipients := make([]*wallet.Wallet, holders)
	for i := range recipients {
		recipients[i] = newTestWallet(t)
		if _, err := NewCampaignTransaction(donor, recipients[i].Address, 100, "relief", chain); err != nil {
			t.Fatalf("seed donation %d: %v", i, err)
		}
	}
	mineMempool(t, chain, validator)

	relief := CampaignID("relief")
	var rows []e11Row
	unrelated := newTestWallet(t)

	built := 1
	for _, target := range []int{10, 100, 500, 1000, 2000} {
		// Grow the chain with traffic unrelated to this campaign.
		for built < target {
			if _, err := NewTransaction(donor, unrelated.Address, 1, chain); err != nil {
				t.Fatalf("filler donation: %v", err)
			}
			mineMempool(t, chain, validator)
			built++
		}

		var total uint64
		elapsed := medianPerOp(21, 100, func() {
			_, tot, err := chain.TraceCampaign(relief)
			if err != nil {
				t.Fatalf("trace: %v", err)
			}
			total = tot
		})

		if total != holders*100 {
			t.Fatalf("campaign total = %d, want %d", total, holders*100)
		}
		rows = append(rows, e11Row{built, holders, milliseconds(elapsed), total})
		t.Logf("%5d blocks, %2d holders: trace %.4f ms (total %d)",
			built, holders, milliseconds(elapsed), total)
	}

	first, last := rows[0], rows[len(rows)-1]
	t.Logf("chain grew %.0fx; trace cost %.4f -> %.4f ms",
		float64(last.blocks)/float64(first.blocks), first.traceMS, last.traceMS)

	if last.traceMS > first.traceMS*5+0.5 {
		t.Errorf("trace cost grew from %.4f to %.4f ms with chain length; expected roughly flat",
			first.traceMS, last.traceMS)
	}
	writeE11CSV(t, "chain_length", rows)
}

// Trace cost against the number of holders, with the chain length held constant.
func TestE11_TraceCostVsHolders(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	donor := newTestWallet(t)
	fundWallet(t, chain, donor, 2_000_000)

	relief := CampaignID("relief")
	var rows []e11Row
	holders := 0

	for _, target := range []int{10, 50, 100, 250, 500} {
		for holders < target {
			recipient := newTestWallet(t)
			if _, err := NewCampaignTransaction(donor, recipient.Address, 100, "relief", chain); err != nil {
				t.Fatalf("donation %d: %v", holders, err)
			}
			holders++
		}
		mineMempool(t, chain, validator)

		var total uint64
		elapsed := medianPerOp(21, 100, func() {
			_, tot, err := chain.TraceCampaign(relief)
			if err != nil {
				t.Fatalf("trace: %v", err)
			}
			total = tot
		})

		rows = append(rows, e11Row{int(chain.GetHeight()), holders, milliseconds(elapsed), total})
		t.Logf("%3d holders: trace %.4f ms (total %d, chain height %d)",
			holders, milliseconds(elapsed), total, chain.GetHeight())
	}

	first, last := rows[0], rows[len(rows)-1]
	t.Logf("holders grew %.0fx; trace cost grew %.1fx",
		float64(last.holders)/float64(first.holders), last.traceMS/first.traceMS)
	writeE11CSV(t, "holders", rows)
}

func writeE11CSV(t *testing.T, sweep string, rows []e11Row) {
	t.Helper()
	path := filepath.Join("..", "experiments", "results", "e11_tracing_"+sweep+".csv")
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
	_ = w.Write([]string{"blocks", "holders", "trace_ms", "attributed_total"})
	for _, r := range rows {
		_ = w.Write([]string{
			strconv.Itoa(r.blocks),
			strconv.Itoa(r.holders),
			strconv.FormatFloat(r.traceMS, 'f', 5, 64),
			strconv.FormatUint(r.total, 10),
		})
	}
}
