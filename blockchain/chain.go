package blockchain

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/Sudin-01/DaanVeer/wallet"
	"github.com/dgraph-io/badger/v4"
)

const(
	DB_PATH = "./db"
	LAST_BLOCK_HASH = "last_hash"
	BALANCE_PREFIX = "balance_"
)

type BlockChain struct {
	Database *badger.DB
	LastHash []byte
	// Mempool tracks admitted-but-unmined transactions and the funds they
	// reserve, so a sender cannot spend the same committed balance twice
	// before a block is produced.
	Mempool *Mempool
}

type BlockChainIterator struct {
	CurrentHash []byte
	Database    *badger.DB
}

// DB_PATH_ENV overrides the on-disk database location. Required when running
// several nodes on one host, as every node otherwise opens ./db and BadgerDB
// takes an exclusive lock on it.
const DB_PATH_ENV = "DAANVEER_DB"

// DatabasePath returns the configured database directory.
func DatabasePath() string {
	if path := os.Getenv(DB_PATH_ENV); path != "" {
		return path
	}
	return DB_PATH
}

func InitBlockChain() *BlockChain {
	return InitBlockChainAt(DatabasePath())
}

// VLOG_MB_ENV overrides the BadgerDB value-log file size, in megabytes.
const VLOG_MB_ENV = "DAANVEER_VLOG_MB"

// DEFAULT_VLOG_MB is the value-log size used when VLOG_MB_ENV is unset.
//
// BadgerDB defaults to a 1 GiB value log, which it memory-maps at twice that
// size. For a ledger whose blocks are a few hundred bytes each that is wildly
// oversized: it made running three nodes on one host fail outright with "not
// enough space on the disk", and it would dominate any storage measurement
// (E5) with preallocation rather than actual chain data.
const DEFAULT_VLOG_MB = 64

// BadgerOptions returns tuned database options for the given directory.
func BadgerOptions(path string) badger.Options {
	opts := badger.DefaultOptions(path)
	if os.Getenv("DAANVEER_QUIET_DB") != "" {
		opts.Logger = nil
	}
	sizeMB := int64(DEFAULT_VLOG_MB)
	if raw := os.Getenv(VLOG_MB_ENV); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			sizeMB = parsed
		}
	}
	opts.ValueLogFileSize = sizeMB << 20
	return opts
}

// InitBlockChainAt opens or creates a chain at an explicit location.
func InitBlockChainAt(path string) *BlockChain {
	var lastHash []byte
	db, err := badger.Open(BadgerOptions(path))
	ShowError(err)

	err = db.Update(func(txn *badger.Txn) error {
		if _, err := txn.Get([]byte(LAST_BLOCK_HASH)); err == badger.ErrKeyNotFound {
			genesisBlock := CreateGenesisBlock()
			genesisSerialized, err := genesisBlock.SerializeBlockToGOB()
			ShowError(err)
			err = txn.Set(genesisBlock.BlockHash, genesisSerialized)
			ShowError(err)
			err = txn.Set([]byte(LAST_BLOCK_HASH), genesisBlock.BlockHash)
			lastHash = append(lastHash, genesisBlock.BlockHash...)
			return err
		}

		item, err := txn.Get([]byte(LAST_BLOCK_HASH))
		ShowError(err)
		err = item.Value(func(val []byte) error {
			lastHash = append(lastHash, val...)
			return nil
		})
		return err
	})
	ShowError(err)

	chain := &BlockChain{Database: db, LastHash: lastHash, Mempool: NewMempool()}

	// Databases written before the index existed, or left inconsistent, are
	// rebuilt once at startup rather than silently serving wrong balances.
	if !chain.indexIsCurrent() {
		if err := chain.RebuildIndex(); err != nil {
			ShowError(err)
		}
	}
	return chain
}

// Close releases the database.
func (blockchain *BlockChain) Close() error {
	if blockchain == nil || blockchain.Database == nil {
		return nil
	}
	return blockchain.Database.Close()
}

// AddBlock validates a block and integrates it, extending the tip or
// reorganising onto a longer branch as appropriate.
//
// Validation (hash, validator signature, and every transaction) happens inside
// AcceptBlock. Nothing on this path previously verified transactions at all.
func (blockchain *BlockChain) AddBlock(latestBlock *Block) error {
	status, err := blockchain.AcceptBlock(latestBlock)
	if err != nil {
		return err
	}
	if status == StatusOrphan {
		return ErrOrphanBlock
	}
	return nil
}

func (iter *BlockChainIterator) GetBlockAndIter() *Block {
	if iter.CurrentHash == nil {
		return nil
	}
	var block *Block
	err := iter.Database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(iter.CurrentHash)
		ShowError(err)
		err = item.Value(func(val []byte) error {
			block, err = DeserializeBlockFromGOB(val)
			return err
		})
		return err
	})
	ShowError(err)
	iter.CurrentHash = block.PreviousHash
	return block
}

func (chain *BlockChain) GetChainHeight() (uint64, error) {
	var block *Block
	err := chain.Database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(chain.LastHash)
		ShowError(err)
		err = item.Value(func(val []byte) error {
			block, err = DeserializeBlockFromGOB(val)
			return err
		})
		return err
	})
	return block.Height, err
}

func (chain *BlockChain) GetHeight() uint64 {
	height, _ := chain.GetChainHeight()
	return height
}

func (blockchain *BlockChain) GetLastNBlocks(n uint64) []*Block {
	var lastNBlocks []*Block
	iter := BlockChainIterator{CurrentHash: blockchain.LastHash, Database: blockchain.Database}
	for block, i := iter.GetBlockAndIter(), uint64(0); i < n && block != nil; block, i = iter.GetBlockAndIter(), i+1 {
		lastNBlocks = append(lastNBlocks, block)
		// fmt.Println("Block: ", block)
	}
	// fmt.Println("Last N blocks: ", lastNBlocks)
	return lastNBlocks
}

func (blockchain *BlockChain) GetBlock(blockhash []byte) (*Block, error) {
	itr := &BlockChainIterator{CurrentHash: blockchain.LastHash, Database: blockchain.Database}
	for b := itr.GetBlockAndIter(); b != nil; b = itr.GetBlockAndIter() {
		if bytes.Equal(blockhash, b.BlockHash) {
			return b, nil
		}
	}
	return nil, errors.New("Block not found")
}

func (blockchain *BlockChain) GetBlockHashes(blockHash []byte) [][]byte {
	var hashes [][]byte
	var hashesInOrder [][]byte

	iter := BlockChainIterator{
		CurrentHash: blockchain.LastHash,
		Database:    blockchain.Database,
	}

	// we only need heights after a certain block and not the block with the matching itself
	block := iter.GetBlockAndIter()
	for block != nil && !bytes.Equal(block.BlockHash, blockHash) {
		hashes = append(hashes, block.BlockHash)
		block = iter.GetBlockAndIter()
	}

	for i := len(hashes) - 1; i >= 0; i-- {
		hashesInOrder = append(hashesInOrder, hashes[i])
	}

	return hashesInOrder
}

func (blockchain *BlockChain) GetBlockHashesFromHeight(height uint64) [][]byte {
	var hashes [][]byte
	var hashesInOrder [][]byte

	iter := BlockChainIterator{
		CurrentHash: blockchain.LastHash,
		Database:    blockchain.Database,
	}

	for block := iter.GetBlockAndIter(); block != nil && block.Height != height; block = iter.GetBlockAndIter() {
		hashes = append(hashes, block.BlockHash)
	}

	for i := len(hashes) - 1; i >= 0; i-- {
		hashesInOrder = append(hashesInOrder, hashes[i])
	}

	return hashesInOrder
}


func (blockchain *BlockChain) WalletMinedBlocks(walletAddress string) ([]*Block, error) {
	var minedBlocks []*Block
	iter := BlockChainIterator{
		CurrentHash: blockchain.LastHash,
		Database:    blockchain.Database,
	}
	// pubKeyHash, err := wallet.PubKeyFromAddress(walletAddress)
	// if err != nil {
	// 	return nil, err
	// }
	for block := iter.GetBlockAndIter(); block != nil; block = iter.GetBlockAndIter() {
		if bytes.Equal(block.ValidatorAddress, []byte(walletAddress)) {
			minedBlocks = append(minedBlocks, block)
		}
	}
	return minedBlocks, nil
}

func (blockchain *BlockChain) PrintChain() {
	iter := BlockChainIterator{
		CurrentHash: blockchain.LastHash,
		Database:    blockchain.Database,
	}
	block := iter.GetBlockAndIter()
	for block != nil {
		fmt.Println("Block: ", block)
		block = iter.GetBlockAndIter()
	}
}

func (blockchain *BlockChain) LastBlock() *Block {
	iterator := &BlockChainIterator{CurrentHash: blockchain.LastHash, Database: blockchain.Database}
	block := iterator.GetBlockAndIter()
	return block
}

func (blockchain *BlockChain) GetLastNTxs(n uint64) []*Transactions {
	var lastNTxs []*Transactions
	var txCount uint64

	iter := BlockChainIterator{
		CurrentHash: blockchain.LastHash,
		Database:    blockchain.Database,
	}

	for block := iter.GetBlockAndIter(); txCount <= n && block != nil; block = iter.GetBlockAndIter() {
		if block.TxMerkleTree != nil {
			for _, txNode := range block.TxMerkleTree.Nodes {
				lastNTxs = append(lastNTxs, &txNode.Transaction)
				txCount += 1
			}
		}
	}

	return lastNTxs
}

// BALANCE_SCAN_ENV forces balance queries through the full-chain scan.
//
// Set only to reproduce the pre-index behaviour on identical hardware, which is
// what the E2 before/after comparison requires. It is not a supported operating
// mode: query cost becomes linear in chain length.
const BALANCE_SCAN_ENV = "DAANVEER_BALANCE_SCAN"

var useScanBalance = os.Getenv(BALANCE_SCAN_ENV) != ""

// GetWalletBalance returns an account's balance from the index: a single key
// lookup, independent of chain length.
func (chain *BlockChain) GetWalletBalance(address string) (uint64, error) {
	if useScanBalance {
		return chain.ScanWalletBalance(address)
	}
	return chain.IndexedBalance(address)
}

// ScanWalletBalance recomputes a balance by replaying the whole chain.
//
// This was how every balance query worked, including the check that
// NewTransaction runs before admitting a donation, making query cost linear in
// chain length. It is retained as the reference implementation the index is
// validated against, and as the baseline for E4.
func (chain *BlockChain) ScanWalletBalance(address string) (uint64, error) {
	var balance uint64
	pubKeyHash, err := wallet.PubKeyFromAddress(address)
	if err != nil {
		return 0, fmt.Errorf("invalid address: %v", err)
	}

	iter := BlockChainIterator{CurrentHash: chain.LastHash, Database: chain.Database}
	for block := iter.GetBlockAndIter(); block != nil; block = iter.GetBlockAndIter() {
		// Iterate the block's transaction list. This previously walked
		// TxMerkleTree.Nodes, which after construction holds only the root --
		// so every block past the first transaction was silently ignored.
		for _, tx := range block.Transactions() {
			if tx.IsGenesis() {
				if bytes.Equal(tx.RecipientHash, pubKeyHash) {
					balance += tx.Value
				}
				continue
			}
			if bytes.Equal(tx.SenderHash, pubKeyHash) {
				balance -= tx.Value
			}
			if bytes.Equal(tx.RecipientHash, pubKeyHash) {
				balance += tx.Value
			}
		}
	}
	return balance, nil
}

// SpendableBalance returns the committed balance less any funds already
// reserved by unmined transactions in the mempool.
func (chain *BlockChain) SpendableBalance(address string) (uint64, error) {
	committed, err := chain.GetWalletBalance(address)
	if err != nil {
		return 0, err
	}
	pubKeyHash, err := wallet.PubKeyFromAddress(address)
	if err != nil {
		return 0, err
	}
	pending := chain.Mempool.PendingSpend(pubKeyHash)
	if pending >= committed {
		return 0, nil
	}
	return committed - pending, nil
}