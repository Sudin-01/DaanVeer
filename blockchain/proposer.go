package blockchain

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/Sudin-01/DaanVeer/wallet"
)

// Proposer scheduling and attestation quorum.
//
// The v1 chain had neither: a single hardcoded address could sign any block at
// any height, alone. That is authority, but it is not a consensus protocol --
// there is no notion of whose turn it is, and no agreement among validators
// before a block becomes canonical.
//
// This adds:
//   - a deterministic round-robin proposer schedule, so exactly one validator
//     is entitled to propose at each height; and
//   - an attestation quorum, so a block is canonical only once enough distinct
//     authorised validators have signed it.

// Attestation is one validator's signature over a block hash.
type Attestation struct {
	ValidatorAddress []byte
	Signature        string
}

// SortedValidatorAddresses returns the validator set in deterministic order.
//
// Ordering must not depend on map iteration, which Go randomises: every node
// has to derive the same schedule from the same configuration.
func SortedValidatorAddresses() []string {
	validatorMu.RLock()
	addresses := make([]string, 0, len(Validators))
	for address := range Validators {
		addresses = append(addresses, address)
	}
	validatorMu.RUnlock()

	sort.Strings(addresses)
	return addresses
}

// ProposerFor returns the validator entitled to propose the block at the given
// height, by round-robin over the sorted validator set.
func ProposerFor(height uint64) (string, error) {
	addresses := SortedValidatorAddresses()
	if len(addresses) == 0 {
		return "", errors.New("validator set is empty")
	}
	return addresses[height%uint64(len(addresses))], nil
}

// QuorumSize returns the number of distinct validator signatures a block needs.
//
// Defaults to ceil(2N/3), the standard Byzantine-fault-tolerant threshold,
// tolerating f faulty validators out of N = 3f+1. A configuration may set an
// explicit quorum, which is what the experiments sweep.
func QuorumSize() int {
	n := ValidatorCount()
	if n == 0 {
		return 0
	}
	if cfg, err := ActiveConfig(); err == nil && cfg.Quorum > 0 {
		if cfg.Quorum > n {
			return n
		}
		return cfg.Quorum
	}
	return (2*n + 2) / 3 // ceil(2n/3)
}

// IsProposer reports whether the address may propose at the given height.
func IsProposer(address string, height uint64) bool {
	expected, err := ProposerFor(height)
	return err == nil && expected == address
}

// Attest adds a validator's signature to a block.
//
// Attestations cover the block hash, which is fixed before any attestation is
// produced, so collecting them cannot change the block's identity.
func (blk *Block) Attest(w *wallet.Wallet) error {
	if w == nil || w.PrivateKey == nil {
		return errors.New("cannot attest without a wallet")
	}
	if _, authorized := LookupValidator(w.Address); !authorized {
		return errors.New("not an authorized validator")
	}
	for _, existing := range blk.Attestations {
		if string(existing.ValidatorAddress) == w.Address {
			return nil // already attested
		}
	}

	hash := blk.Hash()
	r, s, err := ecdsa.Sign(rand.Reader, w.PrivateKey, hash)
	if err != nil {
		return err
	}
	blk.Attestations = append(blk.Attestations, Attestation{
		ValidatorAddress: []byte(w.Address),
		Signature:        hex.EncodeToString(append(wallet.PadTo32(r), wallet.PadTo32(s)...)),
	})
	return nil
}

// verifyAttestationSignature checks one attestation against a block hash.
func verifyAttestationSignature(validator Validator, attestation Attestation, hash []byte) bool {
	raw, err := hex.DecodeString(attestation.Signature)
	if err != nil || len(raw) != 64 {
		return false
	}
	r := new(big.Int).SetBytes(raw[:32])
	s := new(big.Int).SetBytes(raw[32:])
	return ecdsa.Verify(validator.PublicKey, hash, r, s)
}

// CountAttestations returns the number of distinct, valid attestations from
// authorised validators, counting the proposer's own signature.
//
// This counts signatures without reference to eviction, because a Block has no
// access to chain state. Quorum decisions use the chain-aware
// activeAttestations, which additionally excludes validators caught
// equivocating.
func (blk *Block) CountAttestations() int {
	hash := blk.Hash()
	counted := map[string]bool{}

	// The proposer's signature counts once, and only for the proposer.
	if blk.VerifyProof() {
		counted[string(blk.ValidatorAddress)] = true
	}

	for _, attestation := range blk.Attestations {
		address := string(attestation.ValidatorAddress)
		if counted[address] {
			continue
		}
		validator, authorized := LookupValidator(address)
		if !authorized {
			continue
		}
		if verifyAttestationSignature(validator, attestation, hash) {
			counted[address] = true
		}
	}
	return len(counted)
}

// VerifyQuorum checks that a block carries enough valid attestations.
func (blk *Block) VerifyQuorum() error {
	required := QuorumSize()
	if required == 0 {
		return errors.New("validator set is empty")
	}
	if got := blk.CountAttestations(); got < required {
		return fmt.Errorf("insufficient attestations: %d of %d required", got, required)
	}
	return nil
}

// ValidateProposal checks everything about a block except its quorum.
//
// A proposal is by definition not yet attested, so quorum cannot be required
// when deciding whether to attest to it. Every other property -- hash
// integrity, an authorised proposer, the schedule, and transaction validity --
// must hold before a validator puts its signature on the block.
func (blk *Block) ValidateProposal() error {
	if !blk.VerifyBlockHash() {
		return errors.New("block hash does not match its contents")
	}
	if !blk.VerifyProof() {
		return errors.New("proposal is not signed by an authorized validator")
	}
	if cfg, err := ActiveConfig(); err == nil && cfg.RequireSchedule {
		if err := blk.VerifySchedule(); err != nil {
			return err
		}
	}
	return blk.VerifyTransactions()
}

// AddAttestation merges an attestation from a peer, returning whether it was
// counted. Invalid, duplicate and unauthorised attestations are discarded.
func (blk *Block) AddAttestation(attestation Attestation) bool {
	before := blk.CountAttestations()
	for _, existing := range blk.Attestations {
		if string(existing.ValidatorAddress) == string(attestation.ValidatorAddress) {
			return false
		}
	}
	blk.Attestations = append(blk.Attestations, attestation)

	if blk.CountAttestations() > before {
		return true
	}
	// Did not count: drop it again so invalid attestations cannot accumulate.
	blk.Attestations = blk.Attestations[:len(blk.Attestations)-1]
	return false
}

// VerifySchedule checks that the block was proposed by the validator whose turn
// it was at that height.
func (blk *Block) VerifySchedule() error {
	expected, err := ProposerFor(blk.Height)
	if err != nil {
		return err
	}
	if got := string(blk.ValidatorAddress); got != expected {
		return fmt.Errorf("block at height %d proposed by %s, expected %s", blk.Height, got, expected)
	}
	return nil
}
