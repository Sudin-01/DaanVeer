package blockchain

import (
	"fmt"
	"bytes"
	"errors"
	"github.com/dgraph-io/badger/v4"
	// "github.com/Roshan310/DaanVeer/wallet"
)

const(
	DB_PATH = "./db"
	LAST_BLOCK_HASH = "last_hash"
)

type BlockChain struct {
	Database *badger.DB
	LastHash []byte
}

type BlockChainIterator struct {
	CurrentHash []byte
	Database    *badger.DB
}

func InitBlockChain() *BlockChain {
	var lastHash []byte
	db, err := badger.Open(badger.DefaultOptions(DB_PATH))
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

	return &BlockChain{Database: db, LastHash: lastHash}
}

func (blockchain *BlockChain) AddBlock(latestBlock *Block) error {
	if !latestBlock.VerifyBlockHash() {
		return errors.New("hash of the block doesnot match")
	}
	if !latestBlock.VerifyProof() {
		return errors.New("proof of work hasn't been done on the block")
	}
	fmt.Println("INSIDE Add block function now and proof is verified")

	return blockchain.Database.Update(func(txn *badger.Txn) error {
		latestBlockSerialized, err := latestBlock.SerializeBlockToGOB()
		ShowError(err)
		err = txn.Set(latestBlock.BlockHash, latestBlockSerialized)
		ShowError(err)
		err = txn.Set([]byte(LAST_BLOCK_HASH), latestBlock.BlockHash)
		blockchain.LastHash = latestBlock.BlockHash
		return err
	})
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
		fmt.Println("Block: ", block)
	}
	fmt.Println("Last N blocks: ", lastNBlocks)
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