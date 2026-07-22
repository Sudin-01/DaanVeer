package blockchain

// E5 -- storage growth.
//
// Measures the serialized size of a block against the transactions it carries,
// and the on-disk database against chain length, separating the ledger itself
// from the indexes built over it.
//
//	go test ./blockchain/ -run TestE5 -v
//
// Results are written to experiments/results/e5_storage_*.csv.

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Sudin-01/DaanVeer/wallet"
)

// dirSize totals the bytes of every file under a directory.
func dirSize(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // a file being compacted away mid-walk is not an error here
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return total
}

// Serialized block size against transaction count.
func TestE5_BlockSizeVsTransactions(t *testing.T) {
	chain := newTestChain(t)
	validator := newTestWallet(t)
	authorize(t, validator)

	donor := newTestWallet(t)
	fundWallet(t, chain, donor, 1_000_000)
	recipient := newTestWallet(t)

	type row struct {
		txs       int
		bytes     int
		perTx     float64
		overheadB int
	}
	var rows []row
	var emptyBytes int

	for _, n := range []int{0, 1, 2, 4, 8, 16, 32, 64, 128} {
		txs := make([]Transactions, n)
		for i := range txs {
			txs[i] = mkTx(t, donor, recipient, uint64(i+1))
		}

		blk := CreateBlock()
		blk.Height = 1
		blk.PreviousHash = make([]byte, 32)
		if n > 0 {
			if err := blk.AddTxToBlock(txs); err != nil {
				t.Fatalf("assemble %d txs: %v", n, err)
			}
		}
		if err := ProofOfAuthority(blk, validator); err != nil {
			t.Fatalf("sign: %v", err)
		}
		blk.BlockHash = blk.Hash()

		serialized, err := blk.SerializeBlockToGOB()
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		size := len(serialized)
		if n == 0 {
			emptyBytes = size
		}
		perTx := 0.0
		if n > 0 {
			perTx = float64(size-emptyBytes) / float64(n)
		}
		rows = append(rows, row{n, size, perTx, emptyBytes})
		t.Logf("%4d txs: %7d bytes total, %7.1f bytes/tx marginal", n, size, perTx)
	}

	last := rows[len(rows)-1]
	t.Logf("empty block header: %d bytes; marginal cost at %d txs: %.1f bytes/tx",
		emptyBytes, last.txs, last.perTx)

	path := filepath.Join("..", "experiments", "results", "e5_block_size.csv")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		if fh, err := os.Create(path); err == nil {
			defer fh.Close()
			w := csv.NewWriter(fh)
			defer w.Flush()
			_ = w.Write([]string{"transactions", "block_bytes", "marginal_bytes_per_tx", "header_bytes"})
			for _, r := range rows {
				_ = w.Write([]string{
					strconv.Itoa(r.txs),
					strconv.Itoa(r.bytes),
					strconv.FormatFloat(r.perTx, 'f', 1, 64),
					strconv.Itoa(r.overheadB),
				})
			}
		}
	}
}

// On-disk database growth against chain length, with the index overhead
// separated from the ledger.
func TestE5_DatabaseGrowth(t *testing.T) {
	dir := t.TempDir()
	cfgWallet := newTestWallet(t)
	cfg, err := NewSingleValidatorConfig(cfgWallet, 100_000_000, DEFAULT_GENESIS_TIMESTAMP)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if err := SetConfig(cfg); err != nil {
		t.Fatalf("set config: %v", err)
	}

	chain := InitBlockChainAt(dir)
	defer chain.Close()

	validator := newTestWallet(t)
	authorize(t, validator)

	donor := newTestWallet(t)
	// Fund via a genesis-style grant committed through the normal path.
	donorHash, err := wallet.PubKeyFromAddress(donor.Address)
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	grant := Transactions{
		SenderHash:    GENESIS_SENDER,
		RecipientHash: donorHash,
		Value:         10_000_000,
		Timestamp:     DEFAULT_GENESIS_TIMESTAMP,
	}
	grant.TxID = grant.Hash()
	seed := CreateBlock()
	seed.Height = 1
	seed.PreviousHash = chain.LastHash
	if err := seed.AddTxToBlock([]Transactions{grant}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := ProofOfAuthority(seed, validator); err != nil {
		t.Fatalf("sign grant: %v", err)
	}
	seed.BlockHash = seed.Hash()
	if _, err := chain.AcceptBlock(seed); err != nil {
		t.Fatalf("commit grant: %v", err)
	}

	type row struct {
		blocks int
		bytes  int64
		perBlk float64
	}
	var rows []row
	built := 1

	for _, target := range []int{50, 200, 500, 1000} {
		for built < target {
			recipient := newTestWallet(t)
			blk := CreateBlock()
			blk.Height = chain.GetHeight() + 1
			blk.PreviousHash = chain.LastHash
			if err := blk.AddTxToBlock([]Transactions{mkTx(t, donor, recipient, 1)}); err != nil {
				t.Fatalf("assemble: %v", err)
			}
			if err := ProofOfAuthority(blk, validator); err != nil {
				t.Fatalf("sign: %v", err)
			}
			blk.BlockHash = blk.Hash()
			if _, err := chain.AcceptBlock(blk); err != nil {
				t.Fatalf("commit: %v", err)
			}
			built++
		}

		// Logical ledger size: the bytes the chain actually represents,
		// independent of how the store lays them out.
		var logical int64
		iter := BlockChainIterator{CurrentHash: chain.LastHash, Database: chain.Database}
		for b := iter.GetBlockAndIter(); b != nil; b = iter.GetBlockAndIter() {
			serialized, err := b.SerializeBlockToGOB()
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			logical += int64(len(serialized))
		}

		size := dirSize(t, dir)
		rows = append(rows, row{built, logical, float64(logical) / float64(built)})
		t.Logf("%5d blocks: logical %8d bytes (%6.1f/block), on disk %9d bytes",
			built, logical, float64(logical)/float64(built), size)
	}

	first, last := rows[0], rows[len(rows)-1]
	t.Logf("chain grew %.0fx; logical ledger grew %.1fx (%.1f bytes/block)",
		float64(last.blocks)/float64(first.blocks),
		float64(last.bytes)/float64(first.bytes), last.perBlk)
	t.Log("on-disk size is dominated by BadgerDB preallocation at these chain " +
		"lengths and is not a meaningful measure of ledger growth")

	path := filepath.Join("..", "experiments", "results", "e5_db_growth.csv")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		if fh, err := os.Create(path); err == nil {
			defer fh.Close()
			w := csv.NewWriter(fh)
			defer w.Flush()
			_ = w.Write([]string{"blocks", "logical_bytes", "bytes_per_block"})
			for _, r := range rows {
				_ = w.Write([]string{
					strconv.Itoa(r.blocks),
					strconv.FormatInt(r.bytes, 10),
					strconv.FormatFloat(r.perBlk, 'f', 1, 64),
				})
			}
		}
	}
}
