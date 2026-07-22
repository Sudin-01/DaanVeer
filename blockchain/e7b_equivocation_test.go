package blockchain

// E7b -- safety under equivocation.
//
// E7 measures the liveness boundary under validators that withhold
// attestations. That is a *crash* fault: the validator is merely absent. This
// file covers the canonical *Byzantine* fault, in which a validator actively
// signs two conflicting blocks at the same height in an attempt to have two
// histories accepted.
//
// The distinction bounds what the paper may claim. Without these tests, "E7
// measures Byzantine fault tolerance" would be an overstatement of a
// crash-fault result.
//
// Run: go test ./blockchain/ -run TestE7b -v

import (
	"bytes"
	"testing"

	"github.com/Sudin-01/DaanVeer/wallet"
)

// equivocate builds two different blocks at the same height, both correctly
// signed by the same validator. Only a Byzantine validator can produce this
// pair, and the pair is self-contained evidence of it.
func equivocate(t *testing.T, parent *Block, culprit *wallet.Wallet,
	donor, victimA, victimB *wallet.Wallet) (*Block, *Block) {
	t.Helper()
	first := buildBlock(t, parent, culprit, []Transactions{mkTx(t, donor, victimA, 100)})
	second := buildBlock(t, parent, culprit, []Transactions{mkTx(t, donor, victimB, 100)})
	if bytes.Equal(first.BlockHash, second.BlockHash) {
		t.Fatal("setup failed: the two blocks are identical, so this is not equivocation")
	}
	return first, second
}

// A validator signing two conflicting blocks at one height must be detected,
// and the evidence retained.
func TestE7b_EquivocationIsDetected(t *testing.T) {
	chain := newTestChain(t)
	culprit, _ := authorizeRivals(t)

	donor, victimA, victimB := newTestWallet(t), newTestWallet(t), newTestWallet(t)
	fundWallet(t, chain, donor, 10000)

	first, second := equivocate(t, tipOf(t, chain), culprit, donor, victimA, victimB)

	if _, err := chain.AcceptBlock(first); err != nil {
		t.Fatalf("the first block is well-formed and should be accepted: %v", err)
	}
	if chain.IsEvicted(culprit.Address) {
		t.Fatal("validator evicted after a single proposal")
	}

	_, err := chain.AcceptBlock(second)
	if err == nil {
		t.Fatal("the conflicting block was accepted; equivocation undetected")
	}

	evidence, ok := err.(*EquivocationEvidence)
	if !ok {
		t.Fatalf("rejected, but not as equivocation: %v", err)
	}
	if evidence.ValidatorAddress != culprit.Address || evidence.Height != first.Height {
		t.Fatalf("evidence names %s at height %d, want %s at %d",
			evidence.ValidatorAddress, evidence.Height, culprit.Address, first.Height)
	}
	if !chain.IsEvicted(culprit.Address) {
		t.Fatal("equivocator was not evicted")
	}

	stored := chain.EvictionEvidence(culprit.Address)
	if len(stored) != 2 || bytes.Equal(stored[0], stored[1]) {
		t.Fatal("stored evidence does not contain two distinct block hashes")
	}
	t.Logf("detected: %s", chain.DescribeEviction(culprit.Address))
}

// Once evicted, an equivocator's signatures must stop counting toward quorum.
// This is the point of eviction: its signatures remain cryptographically valid,
// so nothing else would stop them.
func TestE7b_EvictedValidatorCannotHelpFinalise(t *testing.T) {
	chain := newTestChain(t)
	// Four validators, quorum three.
	validators := validatorSet(t, 4, 3, false)
	culprit := validators[0]
	honest := validators[1:]

	donor, victimA, victimB := newTestWallet(t), newTestWallet(t), newTestWallet(t)
	fundWallet(t, chain, donor, 10000)

	// The culprit equivocates and is evicted.
	first, second := equivocate(t, tipOf(t, chain), culprit, donor, victimA, victimB)
	_, _ = chain.AcceptBlock(first)
	if _, err := chain.AcceptBlock(second); err == nil {
		t.Fatal("equivocation undetected")
	}
	if !chain.IsEvicted(culprit.Address) {
		t.Fatal("equivocator not evicted")
	}

	// An honest proposer now builds a block. Two honest attestations plus the
	// evicted validator's would reach three by raw count, but must not by the
	// count that matters.
	blk := buildBlock(t, tipOf(t, chain), honest[0], nil)
	if err := blk.Attest(honest[1]); err != nil {
		t.Fatalf("attest: %v", err)
	}
	if err := blk.Attest(culprit); err != nil {
		t.Fatalf("attest: %v", err)
	}

	if raw := blk.CountAttestations(); raw != 3 {
		t.Fatalf("raw signature count = %d, want 3 (two honest plus the equivocator)", raw)
	}
	if active := chain.activeAttestations(blk); active != 2 {
		t.Fatalf("effective count = %d, want 2 (the equivocator must not count)", active)
	}
	if _, err := chain.AcceptBlock(blk); err == nil {
		t.Fatal("block finalised with an evicted validator making up the quorum")
	} else {
		t.Logf("correctly refused: %v", err)
	}

	// With a third honest attestation the same block commits.
	if err := blk.Attest(honest[2]); err != nil {
		t.Fatalf("attest: %v", err)
	}
	blk.BlockHash = blk.Hash()
	if _, err := chain.AcceptBlock(blk); err != nil {
		t.Fatalf("block with three honest attestations was refused: %v", err)
	}
	t.Log("quorum reached only once three honest validators had signed")
}

// A validator that signs a block containing an invalid transaction must have it
// rejected, however well-formed the block itself is.
func TestE7b_InvalidBlockFromValidatorRejected(t *testing.T) {
	chain := newTestChain(t)
	validator, _ := authorizeRivals(t)

	donor, recipient := newTestWallet(t), newTestWallet(t)
	fundWallet(t, chain, donor, 10000)

	tampered := mkTx(t, donor, recipient, 1)
	tampered.Value = 999_999 // signature no longer covers the value

	blk := CreateBlock()
	blk.Height = chain.GetHeight() + 1
	blk.PreviousHash = chain.LastHash
	blk.Txs = []Transactions{tampered} // bypass the builder's own check
	blk.TxMerkleTree = NewMerkleTree(blk.Txs)
	if err := ProofOfAuthority(blk, validator); err != nil {
		t.Fatalf("sign: %v", err)
	}
	blk.BlockHash = blk.Hash()

	if _, err := chain.AcceptBlock(blk); err == nil {
		t.Fatal("a block containing an invalid transaction was committed")
	} else {
		t.Logf("rejected: %v", err)
	}
	if chain.GetHeight() != 1 {
		t.Fatalf("height advanced to %d despite rejection", chain.GetHeight())
	}
}

// Safety sweep: an equivocating minority must never finalise a block, whatever
// the validator count.
func TestE7b_SafetyUnderEquivocatingMinority(t *testing.T) {
	type result struct {
		n, byzantine, quorum, honest int
		finalised                    bool
	}
	var results []result

	for _, n := range []int{4, 5, 7} {
		for byzantine := 1; byzantine <= (n-1)/3+1; byzantine++ {
			chain := newTestChain(t)
			validators := validatorSet(t, n, 0, false)
			quorum := QuorumSize()

			donor, victimA, victimB := newTestWallet(t), newTestWallet(t), newTestWallet(t)
			fundWallet(t, chain, donor, 100000)

			// The first `byzantine` validators each equivocate and are evicted.
			for i := 0; i < byzantine; i++ {
				first, second := equivocate(t, tipOf(t, chain), validators[i], donor, victimA, victimB)
				_, _ = chain.AcceptBlock(first)
				_, _ = chain.AcceptBlock(second)
				if !chain.IsEvicted(validators[i].Address) {
					t.Fatalf("N=%d: validator %d equivocated without being evicted", n, i)
				}
			}

			// Every remaining honest validator attests to an honest block.
			honest := validators[byzantine:]
			blk := buildBlock(t, tipOf(t, chain), honest[0], nil)
			for _, v := range honest[1:] {
				_ = blk.Attest(v)
			}
			// The evicted validators also attest, attempting to make up quorum.
			for i := 0; i < byzantine; i++ {
				_ = blk.Attest(validators[i])
			}
			blk.BlockHash = blk.Hash()

			_, err := chain.AcceptBlock(blk)
			finalised := err == nil
			results = append(results, result{n, byzantine, quorum, len(honest), finalised})

			// Safety: finalisation implies enough *honest* validators signed.
			if finalised && len(honest) < quorum {
				t.Fatalf("SAFETY VIOLATION: N=%d, %d Byzantine, only %d honest, quorum %d, yet committed",
					n, byzantine, len(honest), quorum)
			}
		}
	}

	t.Logf("%-4s%-12s%-8s%-8s%s", "N", "byzantine", "quorum", "honest", "finalised")
	for _, r := range results {
		t.Logf("%-4d%-12d%-8d%-8d%v", r.n, r.byzantine, r.quorum, r.honest, r.finalised)
	}
	t.Log("no block was finalised without a quorum of non-evicted validators")
}
