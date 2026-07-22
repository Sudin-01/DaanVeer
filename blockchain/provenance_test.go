package blockchain

// C3 -- earmarked donations and fund tracing.
//
// Run: go test ./blockchain/ -run TestC3 -v

import (
	"bytes"
	"testing"

	"github.com/Sudin-01/DaanVeer/wallet"
)

// mine packages the mempool into a block and commits it.
func mineMempool(t *testing.T, chain *BlockChain, validator *wallet.Wallet) {
	t.Helper()
	pending := chain.Mempool.All()
	if len(pending) == 0 {
		t.Fatal("nothing pending to mine")
	}
	blk := CreateBlock()
	blk.Height = chain.GetHeight() + 1
	blk.PreviousHash = chain.LastHash
	if err := blk.AddTxToBlock(pending); err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if err := ProofOfAuthority(blk, validator); err != nil {
		t.Fatalf("sign: %v", err)
	}
	blk.BlockHash = blk.Hash()
	if _, err := chain.AcceptBlock(blk); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func holdingFor(holdings []CampaignHolding, w *wallet.Wallet, t *testing.T) uint64 {
	t.Helper()
	hash, err := wallet.PubKeyFromAddress(w.Address)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	for _, h := range holdings {
		if bytes.Equal(h.PubKeyHash, hash) {
			return h.Amount
		}
	}
	return 0
}

// An earmarked donation must be traceable to where the funds now sit, including
// after the charity forwards them onward.
func TestC3_EarmarkedDonationIsTraceable(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	donor := newTestWallet(t)
	charity := newTestWallet(t)
	supplier := newTestWallet(t)
	fundWallet(t, chain, donor, 10000)

	// A donor gives 1000 earmarked for earthquake relief.
	if _, err := NewCampaignTransaction(donor, charity.Address, 1000, "earthquake-relief", chain); err != nil {
		t.Fatalf("donation: %v", err)
	}
	mineMempool(t, chain, validator)

	relief := CampaignID("earthquake-relief")
	holdings, total, err := chain.TraceCampaign(relief)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if total != 1000 {
		t.Fatalf("campaign total = %d, want 1000", total)
	}
	if got := holdingFor(holdings, charity, t); got != 1000 {
		t.Fatalf("charity holds %d of the campaign, want 1000", got)
	}
	t.Logf("after donation: campaign total %d, held by the charity", total)

	// The charity forwards 400 to a supplier. The funds remain attributed to
	// the campaign, now at a new location.
	if _, err := NewTransaction(charity, supplier.Address, 400, chain); err != nil {
		t.Fatalf("onward transfer: %v", err)
	}
	mineMempool(t, chain, validator)

	holdings, total, err = chain.TraceCampaign(relief)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if total != 1000 {
		t.Fatalf("campaign total = %d after forwarding, want 1000 (funds move, they do not vanish)", total)
	}
	if got := holdingFor(holdings, charity, t); got != 600 {
		t.Fatalf("charity holds %d, want 600", got)
	}
	if got := holdingFor(holdings, supplier, t); got != 400 {
		t.Fatalf("supplier holds %d, want 400", got)
	}
	t.Logf("after onward transfer: charity 600, supplier 400, campaign total still %d", total)
}

// A charity holding two campaigns forwards funds; provenance must be split in
// proportion rather than attributed to one campaign.
func TestC3_MixedFundsAreAttributedProportionally(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	donorA, donorB := newTestWallet(t), newTestWallet(t)
	charity, supplier := newTestWallet(t), newTestWallet(t)
	fundWallet(t, chain, donorA, 10000)
	fundWallet(t, chain, donorB, 10000)

	// 750 for relief, 250 for schools: a 3:1 split.
	if _, err := NewCampaignTransaction(donorA, charity.Address, 750, "relief", chain); err != nil {
		t.Fatalf("donation A: %v", err)
	}
	if _, err := NewCampaignTransaction(donorB, charity.Address, 250, "schools", chain); err != nil {
		t.Fatalf("donation B: %v", err)
	}
	mineMempool(t, chain, validator)

	// The charity spends 400 without declaring a campaign.
	if _, err := NewTransaction(charity, supplier.Address, 400, chain); err != nil {
		t.Fatalf("onward: %v", err)
	}
	mineMempool(t, chain, validator)

	reliefHoldings, reliefTotal, err := chain.TraceCampaign(CampaignID("relief"))
	if err != nil {
		t.Fatalf("trace relief: %v", err)
	}
	schoolHoldings, schoolTotal, err := chain.TraceCampaign(CampaignID("schools"))
	if err != nil {
		t.Fatalf("trace schools: %v", err)
	}

	// Totals are conserved.
	if reliefTotal != 750 || schoolTotal != 250 {
		t.Fatalf("totals not conserved: relief %d (want 750), schools %d (want 250)", reliefTotal, schoolTotal)
	}

	// The 400 should split 300/100 on a 3:1 composition.
	reliefAtSupplier := holdingFor(reliefHoldings, supplier, t)
	schoolAtSupplier := holdingFor(schoolHoldings, supplier, t)
	if reliefAtSupplier+schoolAtSupplier != 400 {
		t.Fatalf("supplier holds %d attributed, want 400", reliefAtSupplier+schoolAtSupplier)
	}
	if reliefAtSupplier != 300 || schoolAtSupplier != 100 {
		t.Fatalf("proportional split = relief %d / schools %d, want 300 / 100",
			reliefAtSupplier, schoolAtSupplier)
	}
	t.Logf("charity held 750 relief + 250 schools; a 400 payment split %d / %d",
		reliefAtSupplier, schoolAtSupplier)
}

// An account's balance must decompose into its campaign attributions.
func TestC3_AccountProvenanceMatchesBalance(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	donor, charity := newTestWallet(t), newTestWallet(t)
	fundWallet(t, chain, donor, 10000)

	for _, campaign := range []string{"relief", "schools", "clinics"} {
		if _, err := NewCampaignTransaction(donor, charity.Address, 300, campaign, chain); err != nil {
			t.Fatalf("donation to %s: %v", campaign, err)
		}
	}
	mineMempool(t, chain, validator)

	provenance, err := chain.AccountProvenance(charity.Address)
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	balance, err := chain.GetWalletBalance(charity.Address)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}

	var attributed uint64
	for _, amount := range provenance {
		attributed += amount
	}
	if attributed != balance {
		t.Fatalf("attributed %d but balance is %d", attributed, balance)
	}
	if len(provenance) != 3 {
		t.Fatalf("charity shows %d campaigns, want 3", len(provenance))
	}
	t.Logf("charity balance %d decomposes across %d campaigns", balance, len(provenance))
}

// Provenance must survive a reorganisation: a donation in a disconnected block
// must stop being attributed.
func TestC3_ProvenanceRevertedOnReorg(t *testing.T) {
	chain := newTestChain(t)
	validator, rival := authorizeRivals(t)

	donor, charity := newTestWallet(t), newTestWallet(t)
	fundWallet(t, chain, donor, 10000)
	base := tipOf(t, chain)

	donation, err := NewCampaignTransaction(donor, charity.Address, 500, "relief", chain)
	if err != nil {
		t.Fatalf("donation: %v", err)
	}
	blockA := buildBlock(t, base, validator, []Transactions{*donation})
	if _, err := chain.AcceptBlock(blockA); err != nil {
		t.Fatalf("accept A: %v", err)
	}
	if _, total, _ := chain.TraceCampaign(CampaignID("relief")); total != 500 {
		t.Fatalf("campaign total = %d on branch A, want 500", total)
	}

	// A longer branch without the donation, from the rival proposer.
	blockB1 := buildBlock(t, base, rival, nil)
	blockB2 := buildBlock(t, blockB1, rival, nil)
	if _, err := chain.AcceptBlock(blockB1); err != nil {
		t.Fatalf("accept B1: %v", err)
	}
	if status, err := chain.AcceptBlock(blockB2); err != nil {
		t.Fatalf("accept B2: %v", err)
	} else if status != StatusReorg {
		t.Fatalf("status = %s, want reorg", status)
	}

	_, total, err := chain.TraceCampaign(CampaignID("relief"))
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if total != 0 {
		t.Fatalf("campaign total = %d after the donation was disconnected, want 0", total)
	}
	t.Log("provenance reverted with the disconnected donation")
}

// A rebuild must reproduce provenance exactly, not double-count it.
func TestC3_ProvenanceSurvivesRebuild(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	donor, charity, supplier := newTestWallet(t), newTestWallet(t), newTestWallet(t)
	fundWallet(t, chain, donor, 10000)

	if _, err := NewCampaignTransaction(donor, charity.Address, 900, "relief", chain); err != nil {
		t.Fatalf("donation: %v", err)
	}
	mineMempool(t, chain, validator)
	if _, err := NewTransaction(charity, supplier.Address, 300, chain); err != nil {
		t.Fatalf("onward: %v", err)
	}
	mineMempool(t, chain, validator)

	beforeHoldings, beforeTotal, err := chain.TraceCampaign(CampaignID("relief"))
	if err != nil {
		t.Fatalf("trace: %v", err)
	}

	if err := chain.RebuildIndex(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	afterHoldings, afterTotal, err := chain.TraceCampaign(CampaignID("relief"))
	if err != nil {
		t.Fatalf("trace after rebuild: %v", err)
	}
	if beforeTotal != afterTotal {
		t.Fatalf("campaign total %d before rebuild, %d after", beforeTotal, afterTotal)
	}
	if len(beforeHoldings) != len(afterHoldings) {
		t.Fatalf("%d holders before rebuild, %d after", len(beforeHoldings), len(afterHoldings))
	}
	t.Logf("rebuild reproduced %d attributed across %d holders", afterTotal, len(afterHoldings))
}
