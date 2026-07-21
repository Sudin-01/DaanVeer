package blockchain

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/Sudin-01/DaanVeer/wallet"
)

type Validator struct {
	PublicKey  *ecdsa.PublicKey
	Address    string
	authorized bool
}

// Authorized reports whether this validator may sign blocks.
func (v Validator) Authorized() bool { return v.authorized }

var (
	validatorMu sync.RWMutex
	// Validators is the authorised validator set, keyed by address.
	//
	// It is populated only from configuration. Nothing on the mining path may
	// write to it: a node previously inserted itself here before its
	// authorisation was checked, so a rejected miner still became a validator.
	Validators = map[string]Validator{}
)

// SetValidators replaces the validator set. Called by SetConfig; also used
// directly by tests.
func SetValidators(pubKeys []*ecdsa.PublicKey) {
	next := make(map[string]Validator, len(pubKeys))
	for _, pk := range pubKeys {
		address := wallet.GenerateAddress(pk)
		next[address] = Validator{PublicKey: pk, Address: address, authorized: true}
	}
	validatorMu.Lock()
	defer validatorMu.Unlock()
	Validators = next
}

// LookupValidator returns the validator registered at the given address.
func LookupValidator(address string) (Validator, bool) {
	validatorMu.RLock()
	defer validatorMu.RUnlock()
	v, ok := Validators[address]
	return v, ok
}

// ValidatorCount returns the size of the authorised validator set.
func ValidatorCount() int {
	validatorMu.RLock()
	defer validatorMu.RUnlock()
	return len(Validators)
}

// VerifyProof checks that the block was signed by an authorised validator over
// this block's own hash.
func (blk *Block) VerifyProof() bool {
	validator, exists := LookupValidator(string(blk.ValidatorAddress))
	if !exists || !validator.authorized {
		fmt.Println("Block rejected: validator is not authorized.")
		return false
	}

	signatureBytes, err := hex.DecodeString(blk.Signature)
	if err != nil {
		fmt.Println("Block rejected: invalid signature encoding:", err)
		return false
	}
	if len(signatureBytes) != 64 {
		fmt.Printf("Block rejected: malformed signature (%d bytes, want 64)\n", len(signatureBytes))
		return false
	}

	// Fixed-width halves. Splitting at len/2 over a variable-width encoding
	// misaligns whenever r or s has a stripped leading zero byte.
	r := new(big.Int).SetBytes(signatureBytes[:32])
	s := new(big.Int).SetBytes(signatureBytes[32:])

	if !ecdsa.Verify(validator.PublicKey, blk.Hash(), r, s) {
		fmt.Println("Block rejected: signature verification failed.")
		return false
	}
	return true
}

// ProofOfAuthority signs a block with an authorised validator's private key.
func ProofOfAuthority(blk *Block, validatorWallet *wallet.Wallet) error {
	if validatorWallet == nil || validatorWallet.PrivateKey == nil {
		return errors.New("cannot sign without a validator wallet")
	}
	validatorAddr := validatorWallet.Address

	// Authorisation is decided solely by the configured set. There is no
	// hardcoded address, and no self-registration.
	if _, exists := LookupValidator(validatorAddr); !exists {
		return errors.New("you are not authorized to mine the block")
	}

	// Enforce the proposer schedule at production time as well as validation
	// time, so a validator does not build a block it cannot get accepted.
	if cfg, err := ActiveConfig(); err == nil && cfg.RequireSchedule {
		if !IsProposer(validatorAddr, blk.Height) {
			expected, _ := ProposerFor(blk.Height)
			return fmt.Errorf("not this node's turn to propose at height %d (expected %s)", blk.Height, expected)
		}
	}

	blk.ValidatorAddress = []byte(validatorAddr)
	blockHash := blk.Hash()

	r, s, err := ecdsa.Sign(rand.Reader, validatorWallet.PrivateKey, blockHash)
	if err != nil {
		return err
	}
	blk.Signature = hex.EncodeToString(append(wallet.PadTo32(r), wallet.PadTo32(s)...))
	return nil
}
