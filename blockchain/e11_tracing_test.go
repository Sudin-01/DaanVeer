package blockchain

// E11 -- cost of a fund-tracing query.
//
// The claim being tested is that tracing a campaign costs time proportional to
// the number of accounts holding that campaign, not to the length of the chain.
// A donation platform's ledger grows indefinitely; a trace query that grew with
// it would become unusable exactly when the audit history got interesting.
//
//	go test ./blockchain/ -run TestE11 -v
//	go test ./blockchain/ -run TestE11 -v -e11-holders=2000 -timeout 60m
//
// Both sweeps report medians with bootstrap intervals over repeated
// observations. The chain-length sweep additionally tests the flatness claim
// with Mann-Whitney between the shortest and longest chain, because "roughly
// flat" asserted from two point estimates is not a result.
//
// Results are written to experiments/results/e11_tracing_*.csv.

import (
	"flag"
	"fmt"
	"strconv"
	"testing"

	"github.com/Sudin-01/DaanVeer/wallet"
)

var (
	e11MaxHolders = flag.Int("e11-holders", 500, "largest holder count to measure in E11")
	e11MaxBlocks  = flag.Int("e11-blocks", 2000, "longest chain to measure in E11")
)

type e11Row struct {
	blocks, holders int
	trace           dist
	total           uint64
}

// e11Lengths returns the sweep points up to a ceiling.
func e11Lengths(candidates []int, ceiling int) []int {
	var out []int
	for _, c := range candidates {
		if c <= ceiling {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = []int{candidates[0]}
	}
	return out
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
	samples := map[string]dist{}
	unrelated := newTestWallet(t)

	built := 1
	for _, target := range e11Lengths([]int{10, 100, 500, 1000, 2000, 5000, 10000}, *e11MaxBlocks) {
		// Grow the chain with traffic unrelated to this campaign.
		for built < target {
			if _, err := NewTransaction(donor, unrelated.Address, 1, chain); err != nil {
				t.Fatalf("filler donation: %v", err)
			}
			mineMempool(t, chain, validator)
			built++
		}

		var total uint64
		d := autoMeasure(repetitionsMicro, func() {
			_, tot, err := chain.TraceCampaign(relief)
			if err != nil {
				t.Fatalf("trace: %v", err)
			}
			total = tot
		})

		if total != holders*100 {
			t.Fatalf("campaign total = %d, want %d", total, holders*100)
		}
		rows = append(rows, e11Row{built, holders, d, total})
		samples[fmt.Sprintf("%d", built)] = d
		t.Logf("%5d blocks, %2d holders: %s", built, holders, d)
	}

	first, last := rows[0], rows[len(rows)-1]
	t.Logf("chain grew %.0fx; trace cost %.4f -> %.4f ms (%.2fx)",
		float64(last.blocks)/float64(first.blocks), first.trace.p50, last.trace.p50,
		last.trace.p50/first.trace.p50)

	// The cost is NOT independent of chain length over the whole sweep, and an
	// earlier version of this test obscured that by only checking a loose
	// ratio. Between 10 and 500 blocks the cost rises about fourfold; beyond
	// that it is flat. The rise is a property of the store rather than of the
	// tracing algorithm -- a larger database spreads the campaign index across
	// more levels until the working set stops growing -- but the paper must
	// state the plateau, not claim independence it does not have.
	//
	// So the assertion is made where the claim actually holds: from the
	// plateau onwards.
	_, pFull := mannWhitney(first.trace.raw, last.trace.raw)
	t.Logf("shortest vs longest chain: %s, Cliff's delta %.2f -- the cost is NOT "+
		"flat across the full range", significance(pFull),
		cliffsDelta(first.trace.raw, last.trace.raw))

	const plateauFrom = 500
	var plateau []e11Row
	for _, r := range rows {
		if r.blocks >= plateauFrom {
			plateau = append(plateau, r)
		}
	}
	if len(plateau) < 2 {
		t.Logf("sweep too short to test the plateau (need points at >= %d blocks)", plateauFrom)
	} else {
		lo, hi := plateau[0], plateau[len(plateau)-1]
		ratio := hi.trace.p50 / lo.trace.p50
		_, p := mannWhitney(lo.trace.raw, hi.trace.raw)
		delta := cliffsDelta(lo.trace.raw, hi.trace.raw)
		t.Logf("plateau (%d -> %d blocks, %.0fx): cost ratio %.2fx, %s, "+
			"Cliff's delta %.2f (%s)",
			lo.blocks, hi.blocks, float64(hi.blocks)/float64(lo.blocks),
			ratio, significance(p), delta, cliffsMagnitude(delta))

		// The tolerance depends on whether this is a measurement run. In a
		// dedicated process the ratio is consistently near 1.05; run at the
		// end of the full suite it drifts to about 1.7 on the same code,
		// because the store's cache is in a different state. Enforcing the
		// tight bound everywhere would make `go test ./...` fail for reasons
		// that have nothing to do with correctness.
		tolerance := 1.5
		if !requireMeasurementRun(t) {
			tolerance = 2.5
		}
		if ratio > tolerance {
			t.Errorf("beyond %d blocks the trace cost still grew %.2fx (%.4f -> %.4f ms); "+
				"the plateau claim in Section 7 does not hold",
				plateauFrom, ratio, lo.trace.p50, hi.trace.p50)
		}
	}
	writeE11CSV(t, "chain_length", rows)
	writeRawSamples(t, "e11_tracing_chain_length_samples.csv", []string{"blocks"}, samples)
}

// Trace cost against the number of holders, with the chain length held constant.
func TestE11_TraceCostVsHolders(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	donor := newTestWallet(t)
	fundWallet(t, chain, donor, 20_000_000)

	relief := CampaignID("relief")
	var rows []e11Row
	samples := map[string]dist{}
	holders := 0

	for _, target := range e11Lengths([]int{10, 50, 100, 250, 500, 1000, 2000}, *e11MaxHolders) {
		for holders < target {
			recipient := newTestWallet(t)
			if _, err := NewCampaignTransaction(donor, recipient.Address, 100, "relief", chain); err != nil {
				t.Fatalf("donation %d: %v", holders, err)
			}
			holders++
		}
		mineMempool(t, chain, validator)

		var total uint64
		d := autoMeasure(repetitionsMicro, func() {
			_, tot, err := chain.TraceCampaign(relief)
			if err != nil {
				t.Fatalf("trace: %v", err)
			}
			total = tot
		})

		rows = append(rows, e11Row{int(chain.GetHeight()), holders, d, total})
		samples[fmt.Sprintf("%d", holders)] = d
		t.Logf("%4d holders (chain height %d): %s", holders, chain.GetHeight(), d)
	}

	first, last := rows[0], rows[len(rows)-1]
	holderRatio := float64(last.holders) / float64(first.holders)
	costRatio := last.trace.p50 / first.trace.p50
	t.Logf("holders grew %.0fx; trace cost grew %.1fx", holderRatio, costRatio)

	// Cost proportional to holders is the claim. Allow a wide band -- constant
	// overheads dominate at the small end, so the measured exponent is below
	// one even when the underlying behaviour is linear -- but require that the
	// cost does grow, and does not grow faster than the holder count.
	if costRatio > holderRatio*2 {
		t.Errorf("trace cost grew %.1fx for a %.0fx increase in holders; expected at most linear",
			costRatio, holderRatio)
	}
	if costRatio < 1 {
		t.Errorf("trace cost fell (%.2fx) as holders grew %.0fx; the measurement is suspect",
			costRatio, holderRatio)
	}

	writeE11CSV(t, "holders", rows)
	writeRawSamples(t, "e11_tracing_holders_samples.csv", []string{"holders"}, samples)
}

func writeE11CSV(t *testing.T, sweep string, rows []e11Row) {
	t.Helper()
	header := append([]string{"blocks", "holders"}, distColumns("trace_ms")...)
	header = append(header, "attributed_total")

	var out [][]string
	for _, r := range rows {
		rec := append([]string{strconv.Itoa(r.blocks), strconv.Itoa(r.holders)},
			distValues(r.trace)...)
		rec = append(rec, strconv.FormatUint(r.total, 10))
		out = append(out, rec)
	}
	writeCSV(t, "e11_tracing_"+sweep+".csv", header, out)
}
