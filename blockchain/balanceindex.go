package blockchain

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/Sudin-01/DaanVeer/wallet"
	"github.com/dgraph-io/badger/v4"
)

// Account balance index.
//
// Balances were computed by walking the entire chain and replaying every
// transaction, on every query -- and NewTransaction queries the sender's
// balance before admitting a donation. Cost per query is therefore linear in
// chain length, so throughput degrades as the ledger grows.
//
// The index maintains a balance per account, updated as blocks are connected
// and disconnected, making a query a single key lookup.

// INDEX_HEIGHT_KEY records the tip the index currently reflects, so a stale or
// missing index can be detected and rebuilt.
const INDEX_HEIGHT_KEY = "balance_index_tip"

func balanceKey(pubKeyHash []byte) []byte {
	key := make([]byte, 0, len(BALANCE_PREFIX)+len(pubKeyHash))
	key = append(key, []byte(BALANCE_PREFIX)...)
	return append(key, pubKeyHash...)
}

func readBalance(txn *badger.Txn, key []byte) (uint64, error) {
	item, err := txn.Get(key)
	if errors.Is(err, badger.ErrKeyNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var balance uint64
	err = item.Value(func(val []byte) error {
		if len(val) != 8 {
			return fmt.Errorf("corrupt balance entry: %d bytes", len(val))
		}
		balance = binary.BigEndian.Uint64(val)
		return nil
	})
	return balance, err
}

func writeBalance(txn *badger.Txn, key []byte, balance uint64) error {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], balance)
	return txn.Set(key, encoded[:])
}

// adjust applies a signed delta to an account's indexed balance.
func adjust(txn *badger.Txn, pubKeyHash []byte, delta int64) error {
	if len(pubKeyHash) == 0 {
		return nil
	}
	key := balanceKey(pubKeyHash)
	current, err := readBalance(txn, key)
	if err != nil {
		return err
	}
	if delta < 0 {
		debit := uint64(-delta)
		if debit > current {
			// Should be unreachable: transactions are balance-checked before
			// admission. Surfaced rather than silently wrapping around, which
			// is what unsigned arithmetic would otherwise do.
			return fmt.Errorf("balance underflow: %d held, %d debited", current, debit)
		}
		return writeBalance(txn, key, current-debit)
	}
	return writeBalance(txn, key, current+uint64(delta))
}

// indexBlock applies (sign +1) or reverts (sign -1) a block's effect on the
// balance index.
func indexBlock(txn *badger.Txn, blk *Block, sign int64) error {
	for _, tx := range blk.Transactions() {
		value := int64(tx.Value) * sign
		if tx.IsGenesis() {
			if err := adjust(txn, tx.RecipientHash, value); err != nil {
				return err
			}
			continue
		}
		if err := adjust(txn, tx.SenderHash, -value); err != nil {
			return err
		}
		if err := adjust(txn, tx.RecipientHash, value); err != nil {
			return err
		}
	}
	return nil
}

// connectToIndex records a block as part of the main chain.
func (chain *BlockChain) connectToIndex(blk *Block) error {
	return chain.Database.Update(func(txn *badger.Txn) error {
		if err := indexBlock(txn, blk, +1); err != nil {
			return err
		}
		return txn.Set([]byte(INDEX_HEIGHT_KEY), blk.BlockHash)
	})
}

// disconnectFromIndex reverts a block that is leaving the main chain.
func (chain *BlockChain) disconnectFromIndex(blk *Block) error {
	return chain.Database.Update(func(txn *badger.Txn) error {
		return indexBlock(txn, blk, -1)
	})
}

// IndexedBalance returns an account's balance from the index: one key lookup,
// independent of chain length.
func (chain *BlockChain) IndexedBalance(address string) (uint64, error) {
	pubKeyHash, err := wallet.PubKeyFromAddress(address)
	if err != nil {
		return 0, fmt.Errorf("invalid address: %v", err)
	}
	var balance uint64
	err = chain.Database.View(func(txn *badger.Txn) error {
		balance, err = readBalance(txn, balanceKey(pubKeyHash))
		return err
	})
	return balance, err
}

// RebuildIndex recomputes every balance by replaying the chain from genesis.
//
// Needed when opening a database written before the index existed, and after
// any event that could leave the index inconsistent with the chain.
func (chain *BlockChain) RebuildIndex() error {
	// Collect blocks tip-first, then replay oldest-first so debits never
	// transiently underflow.
	var blocks []*Block
	iter := BlockChainIterator{CurrentHash: chain.LastHash, Database: chain.Database}
	for blk := iter.GetBlockAndIter(); blk != nil; blk = iter.GetBlockAndIter() {
		blocks = append(blocks, blk)
	}

	return chain.Database.Update(func(txn *badger.Txn) error {
		// Drop existing entries.
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		var stale [][]byte
		prefix := []byte(BALANCE_PREFIX)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			stale = append(stale, it.Item().KeyCopy(nil))
		}
		it.Close()
		for _, key := range stale {
			if err := txn.Delete(key); err != nil {
				return err
			}
		}

		for i := len(blocks) - 1; i >= 0; i-- {
			if err := indexBlock(txn, blocks[i], +1); err != nil {
				return err
			}
		}
		if len(blocks) > 0 {
			return txn.Set([]byte(INDEX_HEIGHT_KEY), chain.LastHash)
		}
		return nil
	})
}

// indexIsCurrent reports whether the index reflects the current tip.
func (chain *BlockChain) indexIsCurrent() bool {
	var tip []byte
	err := chain.Database.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(INDEX_HEIGHT_KEY))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			tip = append(tip, val...)
			return nil
		})
	})
	if err != nil {
		return false
	}
	return string(tip) == string(chain.LastHash)
}
