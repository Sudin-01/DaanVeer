package blockchain

// C1 -- proposer schedule and attestation quorum.
//
// Also the basis of E7 (Byzantine fault tolerance): TestE7_ByzantineTolerance
// sweeps validator count against faulty count and records where safety and
// liveness hold.
//
// Run: go test ./blockchain/ -run 'TestC1|TestE7' -v

import (
	"crypto/ecdsa"
	"fmt"
	"testing"

	"github.com/Sudin-01/DaanVeer/wallet"
)

// validatorSet installs n validators, returning them in schedule order so a
// test can address "the validator whose turn it is" directly.
func validatorSet(t *testing.T, n int, quorum int, requireSchedule bool) []*wallet.Wallet {
	t.Helper()

	byAddress := map[string]*wallet.Wallet{}
	keys := make([]*ecdsa.PublicKey, 0, n)
	validatorCfgs := make([]ValidatorConfig, 0, n)

	for i := 0; i < n; i++ {
		w := newTestWallet(t)
		byAddress[w.Address] = w
		keys = append(keys, w.PublicKey)

		raw, err := wallet.PublicKeyToBytes(w.PublicKey)
		if err != nil {
			t.Fatalf("encode key: %v", err)
		}
		validatorCfgs = append(validatorCfgs, ValidatorConfig{PublicKey: hexEncode(raw)})
	}

	genesis := newTestWallet(t)
	cfg := &ChainConfig{
		GenesisAddress:   genesis.Address,
		GenesisAmount:    DEFAULT_GENESIS_AMOUNT,
		GenesisTimestamp: DEFAULT_GENESIS_TIMESTAMP,
		Validators:       validatorCfgs,
		Quorum:           quorum,
		RequireSchedule:  requireSchedule,
	}
	if err := SetConfig(cfg); err != nil {
		t.Fatalf("set config: %v", err)
	}
	SetValidators(keys)
	t.Cleanup(func() { SetValidators(nil) })

	ordered := make([]*wallet.Wallet, 0, n)
	for _, address := range SortedValidatorAddresses() {
		ordered = append(ordered, byAddress[address])
	}
	return ordered
}

func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}

// proposeAt builds a block at the given height, signed by the scheduled proposer.
func proposeAt(t *testing.T, height uint64, parentHash []byte, validators []*wallet.Wallet) *Block {
	t.Helper()
	proposer := validators[height%uint64(len(validators))]

	blk := CreateBlock()
	blk.Height = height
	blk.PreviousHash = parentHash
	if err := ProofOfAuthority(blk, proposer); err != nil {
		t.Fatalf("propose at height %d: %v", height, err)
	}
	blk.BlockHash = blk.Hash()
	return blk
}

// ---------- proposer schedule ----------

func TestC1_ProposerRotatesRoundRobin(t *testing.T) {
	validators := validatorSet(t, 4, 1, true)

	seen := make([]string, 0, 8)
	for height := uint64(0); height < 8; height++ {
		proposer, err := ProposerFor(height)
		if err != nil {
			t.Fatalf("height %d: %v", height, err)
		}
		seen = append(seen, proposer)
	}

	// Each of the 4 validators proposes once per 4 heights, in a stable cycle.
	for i := 0; i < 4; i++ {
		if seen[i] != validators[i].Address {
			t.Fatalf("height %d proposer = %s, want %s", i, seen[i], validators[i].Address)
		}
		if seen[i] != seen[i+4] {
			t.Fatalf("schedule not periodic: height %d = %s, height %d = %s", i, seen[i], i+4, seen[i+4])
		}
	}
	t.Log("proposer rotates round-robin with period equal to the validator count")
}

// The schedule must not depend on Go's randomised map iteration: every node
// derives it independently and they must agree.
func TestC1_ScheduleIsDeterministic(t *testing.T) {
	validatorSet(t, 5, 1, true)

	first := SortedValidatorAddresses()
	for attempt := 0; attempt < 50; attempt++ {
		again := SortedValidatorAddresses()
		for i := range first {
			if first[i] != again[i] {
				t.Fatalf("validator order changed between calls at index %d", i)
			}
		}
	}
	t.Logf("validator order stable across 50 derivations (%d validators)", len(first))
}

func TestC1_OutOfTurnProposalRejected(t *testing.T) {
	chain := newTestChain(t)
	validators := validatorSet(t, 3, 1, true)

	genesis := tipOf(t, chain)

	// Height 1's proposer is validators[1]; have validators[2] try instead.
	wrong := validators[2]
	blk := CreateBlock()
	blk.Height = 1
	blk.PreviousHash = genesis.BlockHash

	if err := ProofOfAuthority(blk, wrong); err == nil {
		t.Fatal("an out-of-turn validator was allowed to sign")
	} else {
		t.Logf("production blocked: %v", err)
	}

	// Even if it forges the block directly, validation must reject it.
	signBlockAs(t, blk, wrong)
	if _, err := chain.AcceptBlock(blk); err == nil {
		t.Fatal("out-of-turn block was accepted")
	} else {
		t.Logf("validation blocked: %v", err)
	}
}

func TestC1_ScheduledProposerAccepted(t *testing.T) {
	chain := newTestChain(t)
	validators := validatorSet(t, 3, 1, true)

	blk := proposeAt(t, 1, tipOf(t, chain).BlockHash, validators)
	status, err := chain.AcceptBlock(blk)
	if err != nil {
		t.Fatalf("scheduled proposer rejected: %v", err)
	}
	if status != StatusExtended {
		t.Fatalf("status = %s, want extended", status)
	}
	t.Log("the scheduled proposer's block was accepted")
}

// ---------- quorum ----------

func TestC1_QuorumDefaultsToTwoThirds(t *testing.T) {
	for _, tc := range []struct{ n, want int }{
		{1, 1}, {2, 2}, {3, 2}, {4, 3}, {5, 4}, {7, 5}, {10, 7},
	} {
		validatorSet(t, tc.n, 0, false)
		if got := QuorumSize(); got != tc.want {
			t.Fatalf("N=%d: quorum = %d, want ceil(2N/3) = %d", tc.n, got, tc.want)
		}
	}
	t.Log("quorum defaults to ceil(2N/3)")
}

func TestC1_BlockBelowQuorumRejected(t *testing.T) {
	chain := newTestChain(t)
	validators := validatorSet(t, 4, 3, true) // needs 3 signatures

	blk := proposeAt(t, 1, tipOf(t, chain).BlockHash, validators)

	// Only the proposer has signed: 1 of 3.
	if got := blk.CountAttestations(); got != 1 {
		t.Fatalf("attestations = %d, want 1", got)
	}
	if _, err := chain.AcceptBlock(blk); err == nil {
		t.Fatal("block below quorum was accepted")
	} else {
		t.Logf("rejected: %v", err)
	}

	// One more signature: still short at 2 of 3.
	if err := blk.Attest(validators[2]); err != nil {
		t.Fatalf("attest: %v", err)
	}
	if _, err := chain.AcceptBlock(blk); err == nil {
		t.Fatal("block with 2 of 3 attestations was accepted")
	}

	// Third signature reaches quorum.
	if err := blk.Attest(validators[3]); err != nil {
		t.Fatalf("attest: %v", err)
	}
	if got := blk.CountAttestations(); got != 3 {
		t.Fatalf("attestations = %d, want 3", got)
	}
	if _, err := chain.AcceptBlock(blk); err != nil {
		t.Fatalf("block at quorum was rejected: %v", err)
	}
	t.Log("block accepted only once the third of three required signatures arrived")
}

func TestC1_DuplicateAttestationsDoNotCount(t *testing.T) {
	chain := newTestChain(t)
	validators := validatorSet(t, 4, 3, true)

	blk := proposeAt(t, 1, tipOf(t, chain).BlockHash, validators)

	// The same validator signing repeatedly must not manufacture a quorum,
	// and neither must the proposer attesting to its own block.
	for i := 0; i < 5; i++ {
		_ = blk.Attest(validators[2])
	}
	_ = blk.Attest(validators[1]) // the proposer at height 1

	if got := blk.CountAttestations(); got != 2 {
		t.Fatalf("attestations = %d, want 2 distinct", got)
	}
	if _, err := chain.AcceptBlock(blk); err == nil {
		t.Fatal("duplicate attestations were counted toward quorum")
	}
	t.Log("repeated and self attestations counted once")
}

func TestC1_UnauthorizedAttestationsDoNotCount(t *testing.T) {
	chain := newTestChain(t)
	validators := validatorSet(t, 4, 3, true)
	outsider := newTestWallet(t)

	blk := proposeAt(t, 1, tipOf(t, chain).BlockHash, validators)

	// An outsider cannot attest through the API...
	if err := blk.Attest(outsider); err == nil {
		t.Fatal("a non-validator was allowed to attest")
	}
	// ...nor by appending an attestation directly.
	blk.Attestations = append(blk.Attestations, Attestation{
		ValidatorAddress: []byte(outsider.Address),
		Signature:        blk.Signature,
	})

	if got := blk.CountAttestations(); got != 1 {
		t.Fatalf("attestations = %d, want 1", got)
	}
	if _, err := chain.AcceptBlock(blk); err == nil {
		t.Fatal("an outsider's attestation counted toward quorum")
	}
	t.Log("attestations from outside the validator set are ignored")
}

// Attestations must not be able to change a block's identity.
func TestC1_AttestationsDoNotAlterBlockHash(t *testing.T) {
	validators := validatorSet(t, 4, 3, true)

	blk := proposeAt(t, 1, []byte("parent"), validators)
	before := blk.Hash()

	for _, v := range validators {
		_ = blk.Attest(v)
	}
	after := blk.Hash()

	if string(before) != string(after) {
		t.Fatalf("block hash changed after attestation: %x -> %x", before, after)
	}
	if !blk.VerifyBlockHash() {
		t.Fatal("block hash invalid after attestation")
	}
	t.Log("gathering attestations leaves the block hash unchanged")
}

// ---------- E7: Byzantine fault tolerance ----------

// TestE7_ByzantineTolerance sweeps validator count against the number of
// validators that refuse to attest, recording where liveness is retained.
//
// Safety is enforced structurally -- a block below quorum is never committed --
// so this measures the liveness boundary: the largest f for which N-f honest
// validators can still reach quorum.
func TestE7_ByzantineTolerance(t *testing.T) {
	type result struct {
		n, faulty, quorum, honest int
		live                      bool
	}
	var results []result

	for _, n := range []int{1, 3, 4, 5, 7} {
		for faulty := 0; faulty < n; faulty++ {
			chain := newTestChain(t)
			validators := validatorSet(t, n, 0, false) // default quorum
			quorum := QuorumSize()

			// The last `faulty` validators are Byzantine and withhold their
			// attestations. validators[0] is always honest and proposes: a
			// faulty proposer produces no block at all, which is a separate
			// case from a block that fails to gather quorum.
			proposer := validators[0]
			blk := CreateBlock()
			blk.Height = 1
			blk.PreviousHash = tipOf(t, chain).BlockHash
			if err := ProofOfAuthority(blk, proposer); err != nil {
				t.Fatalf("N=%d f=%d: propose: %v", n, faulty, err)
			}
			blk.BlockHash = blk.Hash()

			// The proposer's own signature already counts, so honest
			// signatures total n-faulty.
			honest := 1
			for i := 1; i < n-faulty; i++ {
				if err := blk.Attest(validators[i]); err == nil {
					honest++
				}
			}

			_, err := chain.AcceptBlock(blk)
			live := err == nil

			results = append(results, result{n, faulty, quorum, honest, live})

			// Safety check: acceptance must imply the quorum was genuinely met.
			if live && blk.CountAttestations() < quorum {
				t.Fatalf("SAFETY VIOLATION: N=%d f=%d committed with %d of %d attestations",
					n, faulty, blk.CountAttestations(), quorum)
			}
			if !live && blk.CountAttestations() >= quorum {
				t.Fatalf("LIVENESS BUG: N=%d f=%d had quorum but was rejected: %v", n, faulty, err)
			}
		}
	}

	t.Logf("%-4s%-8s%-8s%-8s%s", "N", "faulty", "quorum", "honest", "live")
	for _, r := range results {
		t.Logf("%-4d%-8d%-8d%-8d%v", r.n, r.faulty, r.quorum, r.honest, r.live)
	}

	// The tolerated fault count should follow N - ceil(2N/3).
	for _, r := range results {
		expected := r.honest >= r.quorum
		if r.live != expected {
			t.Fatalf("N=%d f=%d: live=%v, expected %v (honest=%d quorum=%d)",
				r.n, r.faulty, r.live, expected, r.honest, r.quorum)
		}
	}
	fmt.Print("")
}
