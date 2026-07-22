package blockchain

// E9 -- cryptographic and serialization microbenchmarks.
//
// These establish the per-operation floor the rest of the system sits on: if a
// signature verification costs X, no amount of consensus tuning makes a block
// carrying n transactions verify faster than n*X.
//
//	go test ./blockchain/ -run '^$' -bench . -benchmem
//
// Report ns/op alongside the throughput figures so a reader can see which
// costs are cryptographic and which are ours.

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/Sudin-01/DaanVeer/wallet"
)

func benchWallet(b *testing.B) *wallet.Wallet {
	b.Helper()
	w := &wallet.Wallet{}
	if err := w.GenerateKeyPair(); err != nil {
		b.Fatalf("keygen: %v", err)
	}
	w.Address = wallet.GenerateAddress(w.PublicKey)
	return w
}

func benchTx(b *testing.B, from, to *wallet.Wallet, amount uint64) Transactions {
	b.Helper()
	fromHash, _ := wallet.PubKeyFromAddress(from.Address)
	toHash, _ := wallet.PubKeyFromAddress(to.Address)
	nonce := make([]byte, NONCE_LENGTH)
	_, _ = rand.Read(nonce)

	tx := Transactions{
		SenderHash:    fromHash,
		RecipientHash: toHash,
		Nonce:         nonce,
		Value:         amount,
		Timestamp:     1700000000,
	}
	if err := tx.SignTransaction(from); err != nil {
		b.Fatalf("sign: %v", err)
	}
	return tx
}

func BenchmarkSHA256_32B(b *testing.B) {
	data := make([]byte, 32)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sha256.Sum256(data)
	}
}

func BenchmarkSHA256_1KB(b *testing.B) {
	data := make([]byte, 1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sha256.Sum256(data)
	}
}

func BenchmarkECDSASign(b *testing.B) {
	w := benchWallet(b)
	digest := sha256.Sum256([]byte("donation"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := ecdsa.Sign(rand.Reader, w.PrivateKey, digest[:]); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkECDSAVerify(b *testing.B) {
	w := benchWallet(b)
	digest := sha256.Sum256([]byte("donation"))
	r, s, err := ecdsa.Sign(rand.Reader, w.PrivateKey, digest[:])
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !ecdsa.Verify(w.PublicKey, digest[:], r, s) {
			b.Fatal("verification failed")
		}
	}
}

// Address derivation: SHA-256 then RIPEMD-160 then Base58Check.
func BenchmarkAddressDerivation(b *testing.B) {
	w := benchWallet(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wallet.GenerateAddress(w.PublicKey)
	}
}

// Full transaction verification: key decode, hash-binding check, signature.
func BenchmarkTransactionVerify(b *testing.B) {
	from, to := benchWallet(b), benchWallet(b)
	tx := benchTx(b, from, to, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := tx.Verify(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTransactionHash(b *testing.B) {
	from, to := benchWallet(b), benchWallet(b)
	tx := benchTx(b, from, to, 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tx.Hash()
	}
}

// Merkle construction against transaction count.
func BenchmarkMerkleTree(b *testing.B) {
	from, to := benchWallet(b), benchWallet(b)
	for _, n := range []int{1, 4, 16, 64, 256, 1024} {
		txs := make([]Transactions, n)
		for i := range txs {
			txs[i] = benchTx(b, from, to, uint64(i+1))
		}
		b.Run(fmt.Sprintf("txs=%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = NewMerkleTree(txs)
			}
		})
	}
}

// Block hashing against transaction count. The Merkle root is precomputed, so
// this isolates the header preimage.
func BenchmarkBlockHash(b *testing.B) {
	from, to := benchWallet(b), benchWallet(b)
	for _, n := range []int{1, 16, 256} {
		txs := make([]Transactions, n)
		for i := range txs {
			txs[i] = benchTx(b, from, to, uint64(i+1))
		}
		blk := CreateBlock()
		blk.Height = 1
		blk.PreviousHash = make([]byte, 32)
		blk.Txs = txs
		blk.TxMerkleTree = NewMerkleTree(txs)

		b.Run(fmt.Sprintf("txs=%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = blk.Hash()
			}
		})
	}
}

// Whole-block verification: every transaction plus the Merkle root recompute.
// This is what a validator pays per received block.
func BenchmarkBlockVerifyTransactions(b *testing.B) {
	from, to := benchWallet(b), benchWallet(b)
	for _, n := range []int{1, 16, 128} {
		txs := make([]Transactions, n)
		for i := range txs {
			txs[i] = benchTx(b, from, to, uint64(i+1))
		}
		blk := CreateBlock()
		blk.Height = 1
		blk.Txs = txs
		blk.TxMerkleTree = NewMerkleTree(txs)

		b.Run(fmt.Sprintf("txs=%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := blk.VerifyTransactions(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkGobSerializeBlock(b *testing.B) {
	from, to := benchWallet(b), benchWallet(b)
	txs := make([]Transactions, 16)
	for i := range txs {
		txs[i] = benchTx(b, from, to, uint64(i+1))
	}
	blk := CreateBlock()
	blk.Height = 1
	blk.Txs = txs
	blk.TxMerkleTree = NewMerkleTree(txs)
	blk.BlockHash = blk.Hash()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := blk.SerializeBlockToGOB(); err != nil {
			b.Fatal(err)
		}
	}
}
