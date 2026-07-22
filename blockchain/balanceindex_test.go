package blockchain

// The balance index must agree with the full-chain scan it replaces, under
// every path that changes the chain: extension, reorganisation, and rebuild.
//
// Run: go test ./blockchain/ -run TestIndex -v

import (
	"testing"

	"github.com/Sudin-01/DaanVeer/wallet"
)

// assertAgrees checks the index against the reference scan implementation.
func assertAgrees(t *testing.T, chain *BlockChain, wallets map[string]*wallet.Wallet) {
	t.Helper()
	for name, w := range wallets {
		indexed, err := chain.IndexedBalance(w.Address)
		if err != nil {
			t.Fatalf("%s: indexed balance: %v", name, err)
		}
		scanned, err := chain.ScanWalletBalance(w.Address)
		if err != nil {
			t.Fatalf("%s: scanned balance: %v", name, err)
		}
		if indexed != scanned {
			t.Fatalf("%s: index says %d, chain scan says %d", name, indexed, scanned)
		}
	}
}

func TestIndexMatchesScanOnExtension(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	donor := newTestWallet(t)
	fundWallet(t, chain, donor, 10000)

	tracked := map[string]*wallet.Wallet{"donor": donor}

	for round := 0; round < 6; round++ {
		charity := newTestWallet(t)
		tracked[charity.Address] = charity

		blk := CreateBlock()
		blk.Height = chain.GetHeight() + 1
		blk.PreviousHash = chain.LastHash
		if err := blk.AddTxToBlock([]Transactions{mkTx(t, donor, charity, 100)}); err != nil {
			t.Fatalf("round %d: add tx: %v", round, err)
		}
		if err := ProofOfAuthority(blk, validator); err != nil {
			t.Fatalf("round %d: sign: %v", round, err)
		}
		blk.BlockHash = blk.Hash()

		if _, err := chain.AcceptBlock(blk); err != nil {
			t.Fatalf("round %d: accept: %v", round, err)
		}
		assertAgrees(t, chain, tracked)
	}

	if balance, _ := chain.IndexedBalance(donor.Address); balance != 10000-600 {
		t.Fatalf("donor balance = %d, want %d", balance, 10000-600)
	}
	t.Log("index agreed with the chain scan after every extension")
}

// A reorganisation must revert the effect of disconnected blocks.
func TestIndexSurvivesReorg(t *testing.T) {
	chain := newTestChain(t)
	validator, rival := authorizeRivals(t)

	donor := newTestWallet(t)
	fundWallet(t, chain, donor, 10000)
	onlyOnBranchA := newTestWallet(t)

	base := tipOf(t, chain)
	tracked := map[string]*wallet.Wallet{
		"donor":         donor,
		"onlyOnBranchA": onlyOnBranchA,
	}

	// Branch A pays onlyOnBranchA.
	blockA := buildBlock(t, base, validator, []Transactions{mkTx(t, donor, onlyOnBranchA, 500)})
	if _, err := chain.AcceptBlock(blockA); err != nil {
		t.Fatalf("accept A: %v", err)
	}
	if balance, _ := chain.IndexedBalance(onlyOnBranchA.Address); balance != 500 {
		t.Fatalf("balance on branch A = %d, want 500", balance)
	}
	assertAgrees(t, chain, tracked)

	// Branch B is longer, omits that payment, and comes from the rival.
	blockB1 := buildBlock(t, base, rival, nil)
	blockB2 := buildBlock(t, blockB1, rival, nil)

	if _, err := chain.AcceptBlock(blockB1); err != nil {
		t.Fatalf("accept B1: %v", err)
	}
	status, err := chain.AcceptBlock(blockB2)
	if err != nil {
		t.Fatalf("accept B2: %v", err)
	}
	if status != StatusReorg {
		t.Fatalf("status = %s, want reorg", status)
	}

	// The payment is no longer in the canonical chain, so it must not be in
	// the index either.
	if balance, _ := chain.IndexedBalance(onlyOnBranchA.Address); balance != 0 {
		t.Fatalf("balance after reorg = %d, want 0 (payment was disconnected)", balance)
	}
	assertAgrees(t, chain, tracked)
	t.Log("index reverted the disconnected payment and agrees with the chain scan")
}

func TestIndexRebuildMatchesScan(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	donor := newTestWallet(t)
	fundWallet(t, chain, donor, 10000)
	tracked := map[string]*wallet.Wallet{"donor": donor}

	for round := 0; round < 5; round++ {
		charity := newTestWallet(t)
		tracked[charity.Address] = charity
		blk := buildBlock(t, tipOf(t, chain), validator, []Transactions{mkTx(t, donor, charity, 250)})
		if _, err := chain.AcceptBlock(blk); err != nil {
			t.Fatalf("accept: %v", err)
		}
	}

	before := map[string]uint64{}
	for name, w := range tracked {
		before[name], _ = chain.IndexedBalance(w.Address)
	}

	if err := chain.RebuildIndex(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	for name, w := range tracked {
		after, _ := chain.IndexedBalance(w.Address)
		if after != before[name] {
			t.Fatalf("%s: %d before rebuild, %d after", name, before[name], after)
		}
	}
	assertAgrees(t, chain, tracked)
	t.Log("rebuilding the index from genesis reproduced it exactly")
}

// V11: identical donations sent within the same second must remain distinct.
//
// The transaction identifier was derived from sender, recipient, value and a
// second-resolution timestamp, so a donor sending the same amount to the same
// recipient twice in one second produced one identifier, and the second
// donation was silently rejected as a mempool duplicate. Throughput was capped
// independently of consensus. Found by the E2 load generator, which recorded
// 25,055 rejections against 120 accepted submissions.
func TestV11_IdenticalDonationsInSameSecondAreDistinct(t *testing.T) {
	chain := newTestChain(t)
	donor, charity := newTestWallet(t), newTestWallet(t)
	fundWallet(t, chain, donor, 1000)

	const attempts = 50
	seen := map[string]bool{}
	for i := 0; i < attempts; i++ {
		tx, err := NewTransaction(donor, charity.Address, 1, chain)
		if err != nil {
			t.Fatalf("donation %d of %d rejected: %v", i+1, attempts, err)
		}
		id := string(tx.TxID)
		if seen[id] {
			t.Fatalf("donation %d produced a duplicate transaction id", i+1)
		}
		seen[id] = true
	}

	if chain.Mempool.Len() != attempts {
		t.Fatalf("mempool holds %d transactions, want %d", chain.Mempool.Len(), attempts)
	}
	t.Logf("%d identical donations in the same second produced %d distinct ids", attempts, len(seen))
}
