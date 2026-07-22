package blockchain

// Fork-choice tests. The v1 chain had no fork resolution at all: any block that
// did not extend the tip caused the receiving node to call os.Exit(69).
//
// These also form the basis of E7 (Byzantine fault tolerance) -- see
// docs/RESEARCH_PLAN.md.
//
// Run: go test ./blockchain/ -run TestFork -v

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Sudin-01/DaanVeer/wallet"
)

// buildBlock assembles and signs a block on top of an explicit parent, which
// is what makes competing branches constructible.
func buildBlock(t *testing.T, parent *Block, validator *wallet.Wallet, txs []Transactions) *Block {
	t.Helper()
	blk := CreateBlock()
	blk.PreviousHash = parent.BlockHash
	blk.Height = parent.Height + 1
	if len(txs) > 0 {
		if err := blk.AddTxToBlock(txs); err != nil {
			t.Fatalf("add txs: %v", err)
		}
	}
	if err := ProofOfAuthority(blk, validator); err != nil {
		t.Fatalf("sign block: %v", err)
	}
	blk.BlockHash = blk.Hash()
	return blk
}

func tipOf(t *testing.T, chain *BlockChain) *Block {
	t.Helper()
	blk, err := chain.BlockByHash(chain.LastHash)
	if err != nil {
		t.Fatalf("read tip: %v", err)
	}
	return blk
}

func TestForkChoice_ExtendsTip(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	genesis := tipOf(t, chain)
	blk := buildBlock(t, genesis, validator, nil)

	status, err := chain.AcceptBlock(blk)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if status != StatusExtended {
		t.Fatalf("status = %s, want extended", status)
	}
	if !bytes.Equal(chain.LastHash, blk.BlockHash) {
		t.Fatal("tip did not advance")
	}
	t.Logf("tip advanced to height %d", chain.GetHeight())
}

func TestForkChoice_DuplicateIgnored(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	blk := buildBlock(t, tipOf(t, chain), validator, nil)
	if _, err := chain.AcceptBlock(blk); err != nil {
		t.Fatalf("first accept: %v", err)
	}

	status, err := chain.AcceptBlock(blk)
	if err != nil {
		t.Fatalf("second accept: %v", err)
	}
	if status != StatusDuplicate {
		t.Fatalf("status = %s, want duplicate", status)
	}
}

// A competing block at the same height must be stored without moving the tip.
func TestForkChoice_SideBranchDoesNotMoveTip(t *testing.T) {
	chain := newTestChain(t)
	validator, rival := authorizeRivals(t)

	genesis := tipOf(t, chain)

	blockA := buildBlock(t, genesis, validator, nil)
	if _, err := chain.AcceptBlock(blockA); err != nil {
		t.Fatalf("accept A: %v", err)
	}

	// A competing block at the same height, from a different proposer.
	blockB := buildBlock(t, genesis, rival, nil)
	if bytes.Equal(blockA.BlockHash, blockB.BlockHash) {
		t.Skip("competing blocks collided; timestamp resolution too coarse")
	}

	status, err := chain.AcceptBlock(blockB)
	if err != nil {
		t.Fatalf("accept B: %v", err)
	}
	if status != StatusSideBranch {
		t.Fatalf("status = %s, want side-branch", status)
	}
	if !bytes.Equal(chain.LastHash, blockA.BlockHash) {
		t.Fatal("tip moved to the shorter branch")
	}
	if !chain.HasBlock(blockB.BlockHash) {
		t.Fatal("side-branch block was not stored")
	}
	t.Log("competing block stored; tip unchanged")
}

// The chain must switch to a strictly longer branch.
func TestForkChoice_ReorgToLongerBranch(t *testing.T) {
	chain := newTestChain(t)
	validator, rival := authorizeRivals(t)

	genesis := tipOf(t, chain)

	// Branch A: one block, becomes the tip.
	blockA1 := buildBlock(t, genesis, validator, nil)
	if _, err := chain.AcceptBlock(blockA1); err != nil {
		t.Fatalf("accept A1: %v", err)
	}

	// Branch B: two blocks from genesis, built offline by the rival proposer.
	blockB1 := buildBlock(t, genesis, rival, nil)
	blockB2 := buildBlock(t, blockB1, rival, nil)

	if status, err := chain.AcceptBlock(blockB1); err != nil {
		t.Fatalf("accept B1: %v", err)
	} else if status != StatusSideBranch {
		t.Fatalf("B1 status = %s, want side-branch", status)
	}

	status, err := chain.AcceptBlock(blockB2)
	if err != nil {
		t.Fatalf("accept B2: %v", err)
	}
	if status != StatusReorg {
		t.Fatalf("status = %s, want reorg", status)
	}
	if !bytes.Equal(chain.LastHash, blockB2.BlockHash) {
		t.Fatal("tip did not switch to the longer branch")
	}
	if h := chain.GetHeight(); h != 2 {
		t.Fatalf("height = %d, want 2", h)
	}
	t.Log("reorganised onto the longer branch; height 2")
}

// A block whose parent is unknown must be reported as an orphan, not crash the
// node and not be silently accepted.
func TestForkChoice_OrphanReported(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	phantom := CreateBlock()
	phantom.Height = 5
	phantom.BlockHash = []byte("this-parent-does-not-exist------")

	orphan := buildBlock(t, phantom, validator, nil)

	status, err := chain.AcceptBlock(orphan)
	if !errors.Is(err, ErrOrphanBlock) {
		t.Fatalf("err = %v, want ErrOrphanBlock", err)
	}
	if status != StatusOrphan {
		t.Fatalf("status = %s, want orphan", status)
	}
	if chain.HasBlock(orphan.BlockHash) {
		t.Fatal("orphan block was stored")
	}
	t.Log("orphan reported without being stored")
}

// A block signed by a non-validator must be rejected even when it would
// otherwise extend the tip.
func TestForkChoice_RejectsUnauthorizedBlock(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)
	attacker := newTestWallet(t)

	genesis := tipOf(t, chain)

	blk := CreateBlock()
	blk.PreviousHash = genesis.BlockHash
	blk.Height = 1
	signBlockAs(t, blk, attacker) // self-signed, not in the validator set

	status, err := chain.AcceptBlock(blk)
	if err == nil {
		t.Fatal("unauthorized block was accepted")
	}
	if status != StatusOrphan {
		t.Fatalf("status = %s, want orphan (rejected)", status)
	}
	if !bytes.Equal(chain.LastHash, genesis.BlockHash) {
		t.Fatal("tip moved despite rejection")
	}
	t.Logf("rejected: %v", err)
}

// A block claiming a height inconsistent with its parent must be rejected.
func TestForkChoice_RejectsHeightMismatch(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	genesis := tipOf(t, chain)

	blk := CreateBlock()
	blk.PreviousHash = genesis.BlockHash
	blk.Height = 99 // should be 1
	if err := ProofOfAuthority(blk, validator); err != nil {
		t.Fatalf("sign: %v", err)
	}
	blk.BlockHash = blk.Hash()

	if _, err := chain.AcceptBlock(blk); err == nil {
		t.Fatal("block with inconsistent height was accepted")
	} else {
		t.Logf("rejected: %v", err)
	}
}

// Transactions in disconnected blocks must return to the mempool so they are
// not lost when the chain reorganises.
func TestForkChoice_ReorgRestoresTransactions(t *testing.T) {
	chain := newTestChain(t)
	validator, rival := authorizeRivals(t)

	donor, charity := newTestWallet(t), newTestWallet(t)
	fundWallet(t, chain, donor, 1000)

	genesis := tipOf(t, chain)
	tx := mkTx(t, donor, charity, 100)

	// Branch A carries the donation.
	blockA1 := buildBlock(t, genesis, validator, []Transactions{tx})
	if _, err := chain.AcceptBlock(blockA1); err != nil {
		t.Fatalf("accept A1: %v", err)
	}
	if chain.Mempool.Len() != 0 {
		t.Fatalf("mempool should be empty after mining, has %d", chain.Mempool.Len())
	}

	// Branch B is longer, does not carry it, and comes from the rival.
	blockB1 := buildBlock(t, genesis, rival, nil)
	blockB2 := buildBlock(t, blockB1, rival, nil)

	if _, err := chain.AcceptBlock(blockB1); err != nil {
		t.Fatalf("accept B1: %v", err)
	}
	if status, err := chain.AcceptBlock(blockB2); err != nil {
		t.Fatalf("accept B2: %v", err)
	} else if status != StatusReorg {
		t.Fatalf("status = %s, want reorg", status)
	}

	if chain.Mempool.Len() != 1 {
		t.Fatalf("mempool has %d transactions, want 1 restored donation", chain.Mempool.Len())
	}
	if _, found := chain.Mempool.Get(tx.TxID); !found {
		t.Fatal("the disconnected donation was not restored to the mempool")
	}
	t.Log("donation from the disconnected block returned to the mempool")
}
