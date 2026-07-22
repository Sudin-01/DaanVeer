package blockchain

// E1 attack suite for the DaanVeer chain.
//
// Each test mounts a concrete attack that succeeded against v1 and asserts it
// now fails. Defect IDs and the v1 measurements are in docs/RESEARCH_PLAN.md.
//
// Run: go test ./blockchain/ -run TestE1 -v
//      git stash && go test ./blockchain/ -run TestE1 -v   # to see v1 fail

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/Sudin-01/DaanVeer/wallet"
	"github.com/dgraph-io/badger/v4"
)

// ---------- helpers ----------

func newTestWallet(t *testing.T) *wallet.Wallet {
	t.Helper()
	w := &wallet.Wallet{}
	if err := w.GenerateKeyPair(); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	w.Address = wallet.GenerateAddress(w.PublicKey)
	return w
}

// newTestChain builds an isolated chain on a temp BadgerDB so tests never
// touch ./db. It installs a throwaway genesis configuration; callers that care
// about the validator set override it afterwards with authorize().
func newTestChain(t *testing.T) *BlockChain {
	t.Helper()

	genesisWallet := newTestWallet(t)
	cfg, err := NewSingleValidatorConfig(genesisWallet, DEFAULT_GENESIS_AMOUNT, DEFAULT_GENESIS_TIMESTAMP)
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	if err := SetConfig(cfg); err != nil {
		t.Fatalf("set config: %v", err)
	}

	opts := BadgerOptions(t.TempDir())
	opts.Logger = nil
	db, errOpen := badger.Open(opts)
	err = errOpen
	if err != nil {
		t.Fatalf("badger open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	genesis := CreateGenesisBlock()
	ser, err := genesis.SerializeBlockToGOB()
	if err != nil {
		t.Fatalf("serialize genesis: %v", err)
	}
	err = db.Update(func(txn *badger.Txn) error {
		if err := txn.Set(genesis.BlockHash, ser); err != nil {
			return err
		}
		return txn.Set([]byte(LAST_BLOCK_HASH), genesis.BlockHash)
	})
	if err != nil {
		t.Fatalf("seed genesis: %v", err)
	}
	return &BlockChain{Database: db, LastHash: genesis.BlockHash, Mempool: NewMempool()}
}

// writeBlockRaw commits a block bypassing AddBlock's checks. Fixture setup only.
func writeBlockRaw(t *testing.T, chain *BlockChain, blk *Block) {
	t.Helper()
	ser, err := blk.SerializeBlockToGOB()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	err = chain.Database.Update(func(txn *badger.Txn) error {
		if err := txn.Set(blk.BlockHash, ser); err != nil {
			return err
		}
		return txn.Set([]byte(LAST_BLOCK_HASH), blk.BlockHash)
	})
	if err != nil {
		t.Fatalf("write block: %v", err)
	}
	chain.LastHash = blk.BlockHash

	// This helper stands in for a commit, so the balance index must move with
	// the chain -- otherwise balances read as zero.
	if err := chain.connectToIndex(blk); err != nil {
		t.Fatalf("index block: %v", err)
	}
}

// authorize installs wallets as the validator set for the duration of a test.
func authorize(t *testing.T, wallets ...*wallet.Wallet) {
	t.Helper()
	keys := make([]*ecdsa.PublicKey, 0, len(wallets))
	for _, w := range wallets {
		keys = append(keys, w.PublicKey)
	}
	SetValidators(keys)
	t.Cleanup(func() { SetValidators(nil) })
}

// authorizeRivals installs two validators with a quorum of one, so each can
// commit a block alone.
//
// Fork tests need two distinct proposers. A single validator signing two
// different blocks at the same height is not a fork -- it is equivocation, and
// the chain now rejects it (see equivocation.go). Modelling a fork with one
// validator was therefore both unrealistic and, once detection existed,
// self-contradictory.
func authorizeRivals(t *testing.T) (*wallet.Wallet, *wallet.Wallet) {
	t.Helper()
	first, second := newTestWallet(t), newTestWallet(t)

	cfg, err := ActiveConfig()
	if err != nil {
		t.Fatalf("active config: %v", err)
	}
	next := *cfg
	next.Quorum = 1
	next.RequireSchedule = false
	next.Validators = nil
	for _, w := range []*wallet.Wallet{first, second} {
		if err := next.AddValidator(w.PublicKey); err != nil {
			t.Fatalf("add validator: %v", err)
		}
	}
	if err := SetConfig(&next); err != nil {
		t.Fatalf("set config: %v", err)
	}
	t.Cleanup(func() { SetValidators(nil) })
	return first, second
}

// signBlockAs is the attacker's forging routine: exactly what ProofOfAuthority
// does, but for an arbitrary wallet.
func signBlockAs(t *testing.T, blk *Block, w *wallet.Wallet) {
	t.Helper()
	blk.ValidatorAddress = []byte(w.Address)
	h := blk.Hash()
	r, s, err := ecdsa.Sign(rand.Reader, w.PrivateKey, h)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	blk.Signature = hex.EncodeToString(append(wallet.PadTo32(r), wallet.PadTo32(s)...))
	blk.BlockHash = blk.Hash()
}

func mkTx(t *testing.T, from, to *wallet.Wallet, amount uint64) Transactions {
	t.Helper()
	fromHash, err := wallet.PubKeyFromAddress(from.Address)
	if err != nil {
		t.Fatalf("from addr: %v", err)
	}
	toHash, err := wallet.PubKeyFromAddress(to.Address)
	if err != nil {
		t.Fatalf("to addr: %v", err)
	}
	tx := Transactions{
		SenderHash:    fromHash,
		RecipientHash: toHash,
		Value:         amount,
		Timestamp:     uint64(time.Now().UnixNano()),
	}
	if err := tx.SignTransaction(from); err != nil {
		t.Fatalf("sign tx: %v", err)
	}
	return tx
}

// fundWallet seeds a wallet with a genesis-style grant via a raw block.
func fundWallet(t *testing.T, chain *BlockChain, w *wallet.Wallet, amount uint64) {
	t.Helper()
	hash, err := wallet.PubKeyFromAddress(w.Address)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	grant := Transactions{
		SenderHash:    GENESIS_SENDER,
		RecipientHash: hash,
		Value:         amount,
		Timestamp:     uint64(time.Now().UnixNano()),
	}
	grant.TxID = grant.Hash()

	blk := CreateBlock()
	blk.Height = 1
	blk.PreviousHash = chain.LastHash
	if err := blk.AddTxToBlock([]Transactions{grant}); err != nil {
		t.Fatalf("fund: %v", err)
	}
	blk.BlockHash = blk.Hash()
	writeBlockRaw(t, chain, blk)
}

// ---------- V1 / V7: integrity of the signed payload ----------

// V1: the block hash must commit to the transactions it carries.
func TestE1_V1_BlockHashCommitsToTransactions(t *testing.T) {
	alice, bob, mallory := newTestWallet(t), newTestWallet(t), newTestWallet(t)

	blk := CreateBlock()
	blk.Height = 1
	blk.PreviousHash = []byte("previous")

	if err := blk.AddTxToBlock([]Transactions{mkTx(t, alice, bob, 100)}); err != nil {
		t.Fatalf("add honest txs: %v", err)
	}
	honestHash := blk.Hash()

	if err := blk.AddTxToBlock([]Transactions{mkTx(t, alice, mallory, 1000)}); err != nil {
		t.Fatalf("add forged txs: %v", err)
	}
	forgedHash := blk.Hash()

	if bytes.Equal(honestHash, forgedHash) {
		t.Fatalf("V1 REGRESSION: transaction set swapped without changing block hash %x", honestHash)
	}
	t.Logf("V1 fixed: swapping transactions changes the block hash (%x -> %x)", honestHash[:8], forgedHash[:8])
}

// V1b: a validator signature must not survive replacement of the payload.
func TestE1_V1b_SignatureDoesNotSurviveTransactionSwap(t *testing.T) {
	validator := newTestWallet(t)
	authorize(t, validator)
	alice, bob, mallory := newTestWallet(t), newTestWallet(t), newTestWallet(t)

	blk := CreateBlock()
	blk.Height = 1
	blk.PreviousHash = []byte("previous")
	if err := blk.AddTxToBlock([]Transactions{mkTx(t, alice, bob, 100)}); err != nil {
		t.Fatalf("add txs: %v", err)
	}
	signBlockAs(t, blk, validator)

	if !blk.VerifyProof() || !blk.VerifyBlockHash() {
		t.Fatalf("setup failed: honestly signed block did not verify")
	}

	if err := blk.AddTxToBlock([]Transactions{mkTx(t, alice, mallory, 1000)}); err != nil {
		t.Fatalf("swap txs: %v", err)
	}

	if blk.VerifyBlockHash() {
		t.Fatalf("V1b REGRESSION: block hash still valid after transaction swap")
	}
	t.Log("V1b fixed: substituting transactions after signing invalidates the block")
}

// V7: the transaction signature must commit to the recipient.
func TestE1_V7_TxHashCommitsToRecipient(t *testing.T) {
	alice, bob, mallory := newTestWallet(t), newTestWallet(t), newTestWallet(t)

	tx := mkTx(t, alice, bob, 100)
	original := tx.Hash()

	mallH, err := wallet.PubKeyFromAddress(mallory.Address)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	redirected := tx
	redirected.RecipientHash = mallH

	if bytes.Equal(original, redirected.Hash()) {
		t.Fatalf("V7 REGRESSION: recipient changed without changing the tx hash %x", original)
	}
	if err := redirected.Verify(); err == nil {
		t.Fatalf("V7 REGRESSION: redirected transaction still verifies")
	}
	t.Logf("V7 fixed: redirecting a signed donation invalidates it (%v)", redirected.Verify())
}

// ---------- V2: transaction verification is enforced ----------

func TestE1_V2_TamperedTransactionRejected(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)
	alice, mallory := newTestWallet(t), newTestWallet(t)

	tx := mkTx(t, alice, mallory, 1)
	tx.Value = 1_000_000 // tamper after signing

	if err := tx.Verify(); err == nil {
		t.Fatalf("setup failed: tampered tx verified")
	}

	blk := CreateBlock()
	blk.Height = 1
	blk.PreviousHash = chain.LastHash

	// The block builder must refuse it outright.
	if err := blk.AddTxToBlock([]Transactions{tx}); err == nil {
		t.Fatalf("V2 REGRESSION: AddTxToBlock accepted a tampered transaction")
	} else {
		t.Logf("AddTxToBlock rejected it: %v", err)
	}

	// And if an attacker bypasses the builder by writing the field directly,
	// AddBlock must still reject the block.
	blk.Txs = []Transactions{tx}
	blk.TxMerkleTree = NewMerkleTree(blk.Txs)
	signBlockAs(t, blk, validator)

	if err := chain.AddBlock(blk); err == nil {
		t.Fatalf("V2 REGRESSION: AddBlock committed a block with an invalid transaction")
	} else {
		t.Logf("V2 fixed: AddBlock rejected it: %v", err)
	}
}

// V2b: a transaction may not be signed by a key other than the one its
// SenderHash commits to.
func TestE1_V2b_ForeignKeyCannotSignForSender(t *testing.T) {
	alice, mallory, bob := newTestWallet(t), newTestWallet(t), newTestWallet(t)

	aliceHash, err := wallet.PubKeyFromAddress(alice.Address)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	bobHash, err := wallet.PubKeyFromAddress(bob.Address)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}

	// Mallory drafts a transaction that spends from Alice, and signs it herself.
	forged := Transactions{
		SenderHash:    aliceHash,
		RecipientHash: bobHash,
		Value:         500,
		Timestamp:     uint64(time.Now().UnixNano()),
	}
	if err := forged.SignTransaction(mallory); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if err := forged.Verify(); err == nil {
		t.Fatalf("V2b REGRESSION: transaction spending Alice's funds signed by Mallory verified")
	} else {
		t.Logf("V2b fixed: %v", err)
	}
}

// ---------- V3: authorisation ----------

func TestE1_V3_FailedMineDoesNotSelfRegister(t *testing.T) {
	chain := newTestChain(t)
	honest := newTestWallet(t)
	authorize(t, honest)

	attacker := newTestWallet(t)

	blk := CreateBlock()
	if err := blk.MineBlock(chain, attacker); err == nil {
		t.Fatalf("V3 REGRESSION: unauthorized mining succeeded")
	} else {
		t.Logf("mining rejected: %v", err)
	}

	if _, listed := LookupValidator(attacker.Address); listed {
		t.Fatalf("V3 REGRESSION: rejected miner was added to the validator set")
	}
	t.Log("V3 fixed: rejected miner is not a validator")
}

func TestE1_V3b_UnauthorizedNodeCannotCommitBlock(t *testing.T) {
	chain := newTestChain(t)
	honest := newTestWallet(t)
	authorize(t, honest)
	attacker := newTestWallet(t)

	_ = CreateBlock().MineBlock(chain, attacker)

	// Hand-craft and self-sign a block minting funds to the attacker.
	blk := CreateBlock()
	blk.Height = 1
	blk.PreviousHash = chain.LastHash
	attackerHash, err := wallet.PubKeyFromAddress(attacker.Address)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	mint := Transactions{
		SenderHash:    GENESIS_SENDER,
		RecipientHash: attackerHash,
		Value:         999_999,
		Timestamp:     uint64(time.Now().UnixNano()),
	}
	mint.TxID = mint.Hash()
	if err := blk.AddTxToBlock([]Transactions{mint}); err != nil {
		t.Fatalf("add tx: %v", err)
	}
	signBlockAs(t, blk, attacker)

	if err := chain.AddBlock(blk); err == nil {
		bal, _ := chain.GetWalletBalance(attacker.Address)
		t.Fatalf("V3b REGRESSION: unauthorized node committed a block and minted %d units", bal)
	} else {
		t.Logf("V3b fixed: %v", err)
	}
}

// ---------- V4: signature encoding ----------

func TestE1_V4_SignatureEncodingIsFixedWidth(t *testing.T) {
	w := newTestWallet(t)

	const trials = 20000
	failures, shortSigs := 0, 0

	for i := 0; i < trials; i++ {
		msg := sha256.Sum256([]byte(fmt.Sprintf("donation-%d", i)))
		r, s, err := ecdsa.Sign(rand.Reader, w.PrivateKey, msg[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		if len(r.Bytes()) != 32 || len(s.Bytes()) != 32 {
			shortSigs++
		}

		sig := append(wallet.PadTo32(r), wallet.PadTo32(s)...)
		if len(sig) != 64 {
			t.Fatalf("encoded signature is %d bytes, want 64", len(sig))
		}
		r2 := new(big.Int).SetBytes(sig[:32])
		s2 := new(big.Int).SetBytes(sig[32:])

		if !ecdsa.Verify(w.PublicKey, msg[:], r2, s2) {
			failures++
		}
	}

	t.Logf("short r or s encountered: %d/%d (%.3f%%)", shortSigs, trials, float64(shortSigs)/float64(trials)*100)
	if failures > 0 {
		t.Fatalf("V4 REGRESSION: %d/%d round-trip failures", failures, trials)
	}
	t.Logf("V4 fixed: 0/%d failures despite %d short components", trials, shortSigs)
}

// V4b: public key serialization is likewise a bijection.
func TestE1_V4b_PublicKeyRoundTrip(t *testing.T) {
	for i := 0; i < 2000; i++ {
		w := newTestWallet(t)
		raw, err := wallet.PublicKeyToBytes(w.PublicKey)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if len(raw) != 64 {
			t.Fatalf("encoded public key is %d bytes, want 64", len(raw))
		}
		back, err := wallet.BytesToPublicKey(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if back.X.Cmp(w.PublicKey.X) != 0 || back.Y.Cmp(w.PublicKey.Y) != 0 {
			t.Fatalf("V4b REGRESSION: public key did not survive round-trip")
		}
	}
	t.Log("V4b fixed: 2000/2000 public keys round-tripped exactly")
}

// ---------- V5: mempool double-spend ----------

func TestE1_V5_MempoolDoubleSpendRejected(t *testing.T) {
	chain := newTestChain(t)
	donor, charityA, charityB := newTestWallet(t), newTestWallet(t), newTestWallet(t)
	fundWallet(t, chain, donor, 1000)

	if bal, err := chain.GetWalletBalance(donor.Address); err != nil || bal != 1000 {
		t.Fatalf("setup failed: donor balance %d (err %v), want 1000", bal, err)
	}

	if _, err := NewTransaction(donor, charityA.Address, 1000, chain); err != nil {
		t.Fatalf("first spend rejected: %v", err)
	}
	t.Log("first spend of 1000 accepted")

	if _, err := NewTransaction(donor, charityB.Address, 1000, chain); err == nil {
		t.Fatalf("V5 REGRESSION: donor spent the same 1000 units twice")
	} else {
		t.Logf("V5 fixed: second spend rejected: %v", err)
	}

	if spendable, err := chain.SpendableBalance(donor.Address); err != nil || spendable != 0 {
		t.Fatalf("spendable balance is %d (err %v), want 0", spendable, err)
	}
}

// ---------- V6: address derivation ----------

func TestE1_V6_AddressUsesRIPEMD160(t *testing.T) {
	w := newTestWallet(t)
	got := wallet.PublicKeyHashRipeMD160(w.PublicKey)

	if len(got) != 20 {
		t.Fatalf("V6 REGRESSION: public key hash is %d bytes, want 20 (RIPEMD-160)", len(got))
	}

	pubBytes, err := wallet.PublicKeyToBytes(w.PublicKey)
	if err != nil {
		t.Fatalf("pubkey bytes: %v", err)
	}
	sha := sha256.Sum256(pubBytes)
	if bytes.Equal(got, sha[:]) {
		t.Fatalf("V6 REGRESSION: returned the SHA-256 digest instead of RIPEMD-160")
	}
	t.Logf("V6 fixed: 20-byte RIPEMD-160 digest as documented")
}

// ---------- V8: transaction enumeration ----------

// V8: every transaction in a block must be recoverable. Previously the block
// stored only its Merkle tree, whose Nodes slice holds the root alone, so all
// but the first transaction vanished from balance computation.
func TestE1_V8_AllTransactionsRetained(t *testing.T) {
	chain := newTestChain(t)
	donor := newTestWallet(t)
	fundWallet(t, chain, donor, 10000)

	recipients := make([]*wallet.Wallet, 5)
	txs := make([]Transactions, 5)
	for i := range recipients {
		recipients[i] = newTestWallet(t)
		txs[i] = mkTx(t, donor, recipients[i], 100)
	}

	blk := CreateBlock()
	blk.Height = 2
	blk.PreviousHash = chain.LastHash
	if err := blk.AddTxToBlock(txs); err != nil {
		t.Fatalf("add txs: %v", err)
	}
	blk.BlockHash = blk.Hash()
	writeBlockRaw(t, chain, blk)

	if n := len(blk.Transactions()); n != 5 {
		t.Fatalf("V8 REGRESSION: block retained %d of 5 transactions", n)
	}

	for i, r := range recipients {
		bal, err := chain.GetWalletBalance(r.Address)
		if err != nil {
			t.Fatalf("balance: %v", err)
		}
		if bal != 100 {
			t.Fatalf("V8 REGRESSION: recipient %d has balance %d, want 100", i, bal)
		}
	}

	donorBal, err := chain.GetWalletBalance(donor.Address)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if donorBal != 10000-500 {
		t.Fatalf("V8 REGRESSION: donor balance %d, want %d", donorBal, 10000-500)
	}
	t.Logf("V8 fixed: all 5 transactions retained; donor debited %d, each recipient credited 100", 500)
}
