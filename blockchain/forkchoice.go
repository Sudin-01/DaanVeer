package blockchain

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/dgraph-io/badger/v4"
)

// AcceptStatus describes what happened to a block offered to the chain.
type AcceptStatus int

const (
	// StatusDuplicate means the block is already known; nothing changed.
	StatusDuplicate AcceptStatus = iota
	// StatusExtended means the block extended the current tip.
	StatusExtended
	// StatusSideBranch means the block was valid and stored, but its branch is
	// not longer than the current one, so the tip did not move.
	StatusSideBranch
	// StatusReorg means the chain switched to a longer branch.
	StatusReorg
	// StatusOrphan means the block's parent is unknown; the caller should
	// fetch ancestors and retry.
	StatusOrphan
)

func (s AcceptStatus) String() string {
	switch s {
	case StatusDuplicate:
		return "duplicate"
	case StatusExtended:
		return "extended"
	case StatusSideBranch:
		return "side-branch"
	case StatusReorg:
		return "reorg"
	case StatusOrphan:
		return "orphan"
	}
	return "unknown"
}

// ErrOrphanBlock is returned when a block's parent is not held locally.
var ErrOrphanBlock = errors.New("parent block is unknown")

// BlockByHash fetches a block by its hash directly, including blocks on side
// branches.
//
// GetBlock walks the main chain from the tip and therefore cannot see side
// branches, which makes it unusable for fork resolution.
func (chain *BlockChain) BlockByHash(hash []byte) (*Block, error) {
	var blk *Block
	err := chain.Database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(hash)
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			var decodeErr error
			blk, decodeErr = DeserializeBlockFromGOB(val)
			return decodeErr
		})
	})
	if err != nil {
		return nil, err
	}
	return blk, nil
}

// HasBlock reports whether the block is stored locally.
func (chain *BlockChain) HasBlock(hash []byte) bool {
	_, err := chain.BlockByHash(hash)
	return err == nil
}

// validateBlock performs all context-free and parent-relative checks.
func (chain *BlockChain) validateBlock(blk *Block, parent *Block) error {
	if !blk.VerifyBlockHash() {
		return errors.New("block hash does not match its contents")
	}
	if !blk.VerifyProof() {
		return errors.New("block is not signed by an authorized validator")
	}
	if err := blk.VerifyTransactions(); err != nil {
		return err
	}
	if parent != nil {
		if !bytes.Equal(blk.PreviousHash, parent.BlockHash) {
			return errors.New("block does not link to the stated parent")
		}
		if blk.Height != parent.Height+1 {
			return fmt.Errorf("block height %d does not follow parent height %d", blk.Height, parent.Height)
		}
	}
	return nil
}

// mainChainHashes returns the set of block hashes on the current main chain.
func (chain *BlockChain) mainChainHashes() map[string]bool {
	seen := map[string]bool{}
	iter := BlockChainIterator{CurrentHash: chain.LastHash, Database: chain.Database}
	for blk := iter.GetBlockAndIter(); blk != nil; blk = iter.GetBlockAndIter() {
		seen[string(blk.BlockHash)] = true
	}
	return seen
}

// branchTo walks back from blk until it reaches a block on the main chain,
// returning the branch in ancestor-first order plus the common ancestor.
func (chain *BlockChain) branchTo(blk *Block, mainChain map[string]bool) ([]*Block, *Block, error) {
	var branch []*Block
	current := blk

	for {
		branch = append([]*Block{current}, branch...)

		if len(current.PreviousHash) == 0 {
			return branch, nil, nil // reached genesis
		}
		if mainChain[string(current.PreviousHash)] {
			ancestor, err := chain.BlockByHash(current.PreviousHash)
			if err != nil {
				return nil, nil, err
			}
			return branch, ancestor, nil
		}

		parent, err := chain.BlockByHash(current.PreviousHash)
		if err != nil {
			return nil, nil, ErrOrphanBlock
		}
		current = parent

		if len(branch) > 10000 {
			return nil, nil, errors.New("branch too long; refusing to reorganise")
		}
	}
}

// AcceptBlock validates a block and integrates it, reorganising onto a longer
// branch when one appears.
//
// The chain is defined by walking PreviousHash from the tip pointer, so
// switching branches is a matter of validating the new branch and moving that
// pointer -- no stored block needs to be rewritten.
//
// Previously any block that did not extend the tip caused the node to call
// os.Exit, so a fork or an out-of-order delivery terminated the process.
func (chain *BlockChain) AcceptBlock(blk *Block) (AcceptStatus, error) {
	if blk == nil {
		return StatusOrphan, errors.New("nil block")
	}
	if chain.HasBlock(blk.BlockHash) {
		return StatusDuplicate, nil
	}

	// Fast path: the block extends the current tip.
	if bytes.Equal(blk.PreviousHash, chain.LastHash) {
		tip, err := chain.BlockByHash(chain.LastHash)
		if err != nil {
			return StatusOrphan, err
		}
		if err := chain.validateBlock(blk, tip); err != nil {
			return StatusOrphan, err
		}
		if err := chain.commit(blk, true); err != nil {
			return StatusOrphan, err
		}
		chain.Mempool.RemoveMined(blk)
		return StatusExtended, nil
	}

	// Otherwise the block belongs to another branch. Its parent must be known.
	parent, err := chain.BlockByHash(blk.PreviousHash)
	if err != nil {
		return StatusOrphan, ErrOrphanBlock
	}
	if err := chain.validateBlock(blk, parent); err != nil {
		return StatusOrphan, err
	}

	currentHeight := chain.GetHeight()
	if blk.Height <= currentHeight {
		// Store it, but keep the current tip: this branch is not longer.
		if err := chain.commit(blk, false); err != nil {
			return StatusOrphan, err
		}
		return StatusSideBranch, nil
	}

	// The new branch is longer. Validate it end to end before switching.
	if err := chain.commit(blk, false); err != nil {
		return StatusOrphan, err
	}
	mainChain := chain.mainChainHashes()
	branch, ancestor, err := chain.branchTo(blk, mainChain)
	if err != nil {
		return StatusOrphan, err
	}

	prev := ancestor
	for _, candidate := range branch {
		if err := chain.validateBlock(candidate, prev); err != nil {
			return StatusSideBranch, fmt.Errorf("refusing to reorganise: %v", err)
		}
		prev = candidate
	}

	// Return transactions from the blocks being disconnected to the mempool.
	chain.restoreDisconnected(mainChain, ancestor)

	if err := chain.setTip(blk.BlockHash); err != nil {
		return StatusSideBranch, err
	}
	for _, candidate := range branch {
		chain.Mempool.RemoveMined(candidate)
	}
	return StatusReorg, nil
}

// restoreDisconnected returns transactions from blocks that are about to leave
// the main chain back to the mempool, so they can be mined again.
func (chain *BlockChain) restoreDisconnected(mainChain map[string]bool, ancestor *Block) {
	if ancestor == nil {
		return
	}
	iter := BlockChainIterator{CurrentHash: chain.LastHash, Database: chain.Database}
	for blk := iter.GetBlockAndIter(); blk != nil; blk = iter.GetBlockAndIter() {
		if bytes.Equal(blk.BlockHash, ancestor.BlockHash) {
			return
		}
		for _, tx := range blk.Transactions() {
			if tx.IsGenesis() {
				continue
			}
			_ = chain.Mempool.Add(tx)
		}
	}
}

// commit stores a block, optionally advancing the tip to it.
func (chain *BlockChain) commit(blk *Block, advanceTip bool) error {
	serialized, err := blk.SerializeBlockToGOB()
	if err != nil {
		return err
	}
	err = chain.Database.Update(func(txn *badger.Txn) error {
		if err := txn.Set(blk.BlockHash, serialized); err != nil {
			return err
		}
		if advanceTip {
			return txn.Set([]byte(LAST_BLOCK_HASH), blk.BlockHash)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if advanceTip {
		chain.LastHash = blk.BlockHash
	}
	return nil
}

// setTip switches the main chain to end at the given block.
func (chain *BlockChain) setTip(hash []byte) error {
	err := chain.Database.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(LAST_BLOCK_HASH), hash)
	})
	if err != nil {
		return err
	}
	chain.LastHash = hash
	return nil
}
