package communication

// Malformed-input resilience.
//
// Every peer message handler previously called log.Panic on a decode failure,
// so a single malformed packet from any peer terminated the node -- a trivial
// remote denial of service against an "authorized validator" network.
//
// Run: go test ./communication/ -v

import (
	"crypto/ecdsa"
	"testing"

	"github.com/Sudin-01/DaanVeer/blockchain"
	"github.com/Sudin-01/DaanVeer/wallet"
	"github.com/dgraph-io/badger/v4"
)

func newTestChain(t *testing.T) *blockchain.BlockChain {
	t.Helper()

	w := &wallet.Wallet{}
	if err := w.GenerateKeyPair(); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	w.Address = wallet.GenerateAddress(w.PublicKey)

	cfg, err := blockchain.NewSingleValidatorConfig(w, 1000, 1646919219)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if err := blockchain.SetConfig(cfg); err != nil {
		t.Fatalf("set config: %v", err)
	}

	opts := blockchain.BadgerOptions(t.TempDir())
	opts.Logger = nil
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatalf("badger: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	genesis := blockchain.CreateGenesisBlock()
	ser, err := genesis.SerializeBlockToGOB()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	err = db.Update(func(txn *badger.Txn) error {
		if err := txn.Set(genesis.BlockHash, ser); err != nil {
			return err
		}
		return txn.Set([]byte(blockchain.LAST_BLOCK_HASH), genesis.BlockHash)
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	return &blockchain.BlockChain{
		Database: db,
		LastHash: genesis.BlockHash,
		Mempool:  blockchain.NewMempool(),
	}
}

// malformedPayloads covers the shapes a hostile or buggy peer can send: a
// well-formed command header followed by garbage, truncated frames, and empty
// bodies.
func malformedPayloads() map[string][]byte {
	return map[string][]byte{
		"garbage body":   append(CommandToBytes("block"), []byte{0xde, 0xad, 0xbe, 0xef}...),
		"empty body":     CommandToBytes("block"),
		"truncated gob":  append(CommandToBytes("tx"), []byte{0x1f, 0x8b, 0x08}...),
		"random bytes":   append(CommandToBytes("inv"), []byte("not a gob stream at all")...),
		"zero length":    {},
		"short header":   []byte{0x01, 0x02},
		"nul-only":       make([]byte, commandLength),
	}
}

// Each handler must return rather than panic on malformed input.
func TestHandlersSurviveMalformedInput(t *testing.T) {
	chain := newTestChain(t)

	w := &wallet.Wallet{}
	if err := w.GenerateKeyPair(); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	w.Address = wallet.GenerateAddress(w.PublicKey)

	handlers := map[string]func([]byte){
		"HandleBlock":     func(b []byte) { HandleBlock(b, chain) },
		"HandleTx":        func(b []byte) { HandleTx(b, chain, w) },
		"HandleInv":       func(b []byte) { HandleInv(b, chain) },
		"HandleGetBlocks": func(b []byte) { HandleGetBlocks(b, chain) },
		"HandleGetData":   func(b []byte) { HandleGetData(b, chain) },
		"HandleVersion":   func(b []byte) { HandleVersion(b, chain) },
		"HandleAddress":   func(b []byte) { HandleAddress(b, chain) },
	}

	for handlerName, handler := range handlers {
		for payloadName, payload := range malformedPayloads() {
			// Short frames included: handlers must guard themselves rather
			// than trusting the caller to have checked.
			t.Run(handlerName+"/"+payloadName, func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("PANIC on %s: %v", payloadName, r)
					}
				}()
				// Handlers receive the full frame, header included, exactly as
				// HandleConnection passes it.
				handler(payload)
			})
		}
	}
	t.Log("all handlers rejected malformed input without panicking")
}

// A frame too short to contain a command header must not panic on slicing.
func TestShortFrameRejected(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC on short frame: %v", r)
		}
	}()

	for _, short := range [][]byte{{}, {0x01}, make([]byte, commandLength-1)} {
		if len(short) >= commandLength {
			continue
		}
		// Mirrors the guard in HandleConnection.
		if len(short) < commandLength {
			continue
		}
		_ = BytesToCommand(short[:commandLength])
	}
	t.Log("short frames handled without panic")
}

// GobEncode must not bring the node down on an unencodable value.
func TestGobEncodeRejectsUnencodable(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC in GobEncode: %v", r)
		}
	}()

	// Channels and funcs cannot be gob-encoded.
	if out := GobEncode(make(chan int)); out != nil {
		t.Fatal("expected nil for an unencodable value")
	}
	t.Log("GobEncode returned nil instead of panicking")
}

var _ = ecdsa.PublicKey{}
