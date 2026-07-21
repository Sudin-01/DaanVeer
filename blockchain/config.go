package blockchain

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/Sudin-01/DaanVeer/wallet"
)

// CHAIN_CONFIG is the default path of the chain configuration.
const CHAIN_CONFIG = "config/chain.json"

// ChainConfig defines the genesis state and the authorised validator set.
//
// These were previously compile-time constants: a genesis address, a genesis
// amount, and a single hardcoded validator address and public key. That made
// the chain impossible to re-initialise, impossible to run with more than one
// validator, and impossible to reproduce from a clean checkout.
type ChainConfig struct {
	GenesisAddress   string            `json:"genesis_address"`
	GenesisAmount    uint64            `json:"genesis_amount"`
	GenesisTimestamp uint64            `json:"genesis_timestamp"`
	Validators       []ValidatorConfig `json:"validators"`

	// Quorum is the number of distinct validator signatures a block requires.
	// Zero selects the default, ceil(2N/3). Experiments sweep this to measure
	// the safety/liveness tradeoff (E3, E7).
	Quorum int `json:"quorum,omitempty"`

	// RequireSchedule enforces the round-robin proposer schedule. Disabling it
	// lets any authorised validator propose at any height, which is the v1
	// behaviour and is retained for comparison measurements.
	RequireSchedule bool `json:"require_schedule"`
}

// ValidatorConfig lists a validator by public key only. The address is derived
// from the key, so a configuration cannot claim an address it lacks the key for.
type ValidatorConfig struct {
	PublicKey string `json:"public_key"`
	Label     string `json:"label,omitempty"`
}

var (
	configMu    sync.RWMutex
	chainConfig *ChainConfig
)

// Validate checks that a configuration is internally consistent.
func (c *ChainConfig) Validate() error {
	if c == nil {
		return errors.New("nil chain config")
	}
	if c.GenesisAddress == "" {
		return errors.New("genesis_address is required")
	}
	if _, err := wallet.PubKeyFromAddress(c.GenesisAddress); err != nil {
		return fmt.Errorf("genesis_address is not a valid address: %v", err)
	}
	if c.GenesisAmount == 0 {
		return errors.New("genesis_amount must be greater than zero")
	}
	if len(c.Validators) == 0 {
		return errors.New("at least one validator is required")
	}
	for i, v := range c.Validators {
		if _, err := v.parseKey(); err != nil {
			return fmt.Errorf("validator %d: %v", i, err)
		}
	}
	return nil
}

func (v ValidatorConfig) parseKey() (*ecdsa.PublicKey, error) {
	raw, err := hex.DecodeString(v.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("invalid public key hex: %v", err)
	}
	return wallet.BytesToPublicKey(raw)
}

// ActiveConfig returns the loaded chain configuration.
func ActiveConfig() (*ChainConfig, error) {
	configMu.RLock()
	defer configMu.RUnlock()
	if chainConfig == nil {
		return nil, fmt.Errorf("no chain configuration loaded; run with -init to create %s", CHAIN_CONFIG)
	}
	return chainConfig, nil
}

// SetConfig installs a configuration and applies its validator set.
func SetConfig(c *ChainConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	keys := make([]*ecdsa.PublicKey, 0, len(c.Validators))
	for _, v := range c.Validators {
		key, err := v.parseKey()
		if err != nil {
			return err
		}
		keys = append(keys, key)
	}

	configMu.Lock()
	chainConfig = c
	configMu.Unlock()

	SetValidators(keys)
	return nil
}

// LoadChainConfig reads and applies a configuration file.
func LoadChainConfig(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var c ChainConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("parsing %s: %v", path, err)
	}
	if err := SetConfig(&c); err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	return nil
}

// SaveChainConfig writes a configuration to disk.
func SaveChainConfig(path string, c *ChainConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}

// AddValidator appends a validator, ignoring one that is already present.
func (c *ChainConfig) AddValidator(pubKey *ecdsa.PublicKey) error {
	raw, err := wallet.PublicKeyToBytes(pubKey)
	if err != nil {
		return err
	}
	encoded := hex.EncodeToString(raw)
	for _, existing := range c.Validators {
		if existing.PublicKey == encoded {
			return nil
		}
	}
	c.Validators = append(c.Validators, ValidatorConfig{
		PublicKey: encoded,
		Label:     wallet.GenerateAddress(pubKey),
	})
	return nil
}

// NewSingleValidatorConfig builds a bootstrap configuration in which one wallet
// is both the genesis recipient and the sole authorised validator.
func NewSingleValidatorConfig(w *wallet.Wallet, amount uint64, timestamp uint64) (*ChainConfig, error) {
	pubKeyBytes, err := wallet.PublicKeyToBytes(w.PublicKey)
	if err != nil {
		return nil, err
	}
	return &ChainConfig{
		GenesisAddress:   w.Address,
		GenesisAmount:    amount,
		GenesisTimestamp: timestamp,
		Validators: []ValidatorConfig{
			{PublicKey: hex.EncodeToString(pubKeyBytes), Label: "bootstrap"},
		},
	}, nil
}
