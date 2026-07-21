package blockchain

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

// Mempool holds transactions that have been admitted but not yet mined, and
// tracks the funds they reserve.
//
// Without pending-spend accounting a sender's balance is only ever checked
// against committed chain state, so the same balance can be spent repeatedly
// until a block is produced.
type Mempool struct {
	mu  sync.RWMutex
	txs map[string]Transactions
}

func NewMempool() *Mempool {
	return &Mempool{txs: make(map[string]Transactions)}
}

// PendingSpend returns the total value already reserved by unmined
// transactions from the given sender.
func (m *Mempool) PendingSpend(senderHash []byte) uint64 {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	var total uint64
	for _, tx := range m.txs {
		if bytes.Equal(tx.SenderHash, senderHash) {
			total += tx.Value
		}
	}
	return total
}

// Add admits a verified transaction, rejecting duplicates.
func (m *Mempool) Add(tx Transactions) error {
	if m == nil {
		return errors.New("nil mempool")
	}
	if err := tx.Verify(); err != nil {
		return fmt.Errorf("refusing to admit invalid transaction: %v", err)
	}

	key := hex.EncodeToString(tx.TxID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.txs[key]; exists {
		return errors.New("transaction already in mempool")
	}
	m.txs[key] = tx
	return nil
}

// Get returns the pending transaction with the given id.
func (m *Mempool) Get(txID []byte) (Transactions, bool) {
	if m == nil {
		return Transactions{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	tx, ok := m.txs[hex.EncodeToString(txID)]
	return tx, ok
}

// Remove drops a transaction, typically once it has been mined.
func (m *Mempool) Remove(txID []byte) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.txs, hex.EncodeToString(txID))
}

// RemoveMined drops every transaction contained in the given block.
func (m *Mempool) RemoveMined(blk *Block) {
	if m == nil || blk == nil || blk.TxMerkleTree == nil {
		return
	}
	for _, tx := range blk.Transactions() {
		m.Remove(tx.TxID)
	}
}

// All returns a snapshot of the pending transactions.
func (m *Mempool) All() []Transactions {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Transactions, 0, len(m.txs))
	for _, tx := range m.txs {
		out = append(out, tx)
	}
	return out
}

func (m *Mempool) Len() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.txs)
}
