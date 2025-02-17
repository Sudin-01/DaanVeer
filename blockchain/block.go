package blockchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/Roshan310/DaanVeer/wallet"
	"github.com/dgraph-io/badger/v4"
)
const (
	GENESIS_STRING = "THIS IS THE FIRST BLOCK"
	GENESIS_TIMESTAMP = 1646919219
)
func init() {
	log.SetPrefix("Blockchain: ")
}

type Block struct {
	PreviousHash []byte
	Timestamp    uint64
	BlockHash []byte
	Height uint64
	Transactions []Transactions
	Signature	string
	ValidatorAddress []byte
	TxMerkleTree *MerkleTree
}


func CreateBlock() *Block {
	var blk Block
	blk.Timestamp = uint64(time.Now().Unix())
	return &blk
}

func (b *Block) Print() {
	fmt.Printf("Timestamp:       %d\n", b.Timestamp)
	fmt.Printf("Previous Hash:   %x\n", b.PreviousHash)
	fmt.Printf("Block Hash :     %x\n", b.BlockHash)
	for _, t := range b.Transactions {
		t.Print()
	}
}

func (b *Block) Hash() []byte {
    var buff bytes.Buffer
    tempBlock := *b 
    tempBlock.BlockHash = nil

    enc := gob.NewEncoder(&buff)
    err := enc.Encode(tempBlock)
    if err != nil {
        fmt.Println("Error while encoding block:", err)
    }
    hash := sha256.Sum256(buff.Bytes())
    return hash[:]
}

func (b *Block) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Height 	 uint64          `json:"height"`
		BlockHash    []byte          `json:"block_hash"`
		Timestamp    uint64          `json:"timestamp"`
		PreviousHash []byte       `json:"previous_hash"`
		MerkleRoot   []byte       `json:"merkle_root"`
		Transactions []Transactions `json:"transactions"`
	}{
		Height:       b.Height,
		BlockHash:    b.BlockHash,
		Timestamp:    b.Timestamp,
		PreviousHash: b.PreviousHash,
		Transactions: b.Transactions,
	})
}

func (b *Block) AddTxToBlock(txPool []Transactions) error {
	var tree *MerkleTree
	tree = NewMerkleTree(txPool)
	b.TxMerkleTree = tree 
	return nil
}


func (blk *Block) SerializeBlockToGOB() ([]byte, error) {
	var encoded bytes.Buffer
	err := gob.NewEncoder(&encoded).Encode(blk)
	return encoded.Bytes(), err
}

func DeserializeBlockFromGOB(serializedBlock []byte) (*Block, error) {
	var blk Block
	err := gob.NewDecoder(bytes.NewReader(serializedBlock)).Decode(&blk)
	return &blk, err
}

func CreateGenesisBlock() *Block {
	block_hash := sha256.Sum256([]byte(GENESIS_STRING))
	block := Block{
		Timestamp: GENESIS_TIMESTAMP,
		Height: 0,
		BlockHash: block_hash[:],
	}
	return &block
}

func (block *Block) VerifyBlockHash() bool {
	fmt.Println("Inside verify block hash block property: ", block)
	computedBlockHash := block.Hash()
	fmt.Println("Computed Block Hash: ", computedBlockHash)
	fmt.Println("Block Hash: ", block.BlockHash)
	return bytes.Equal(block.Hash(), block.BlockHash)
}

func (block *Block) MineBlock(chain *BlockChain, wlt *wallet.Wallet) error {
	var lastHash []byte
	var lastBlock *Block

	err := chain.Database.View(func(txn *badger.Txn) error {
			lastHashQuery, err := txn.Get([]byte(LAST_BLOCK_HASH))
			if err != nil {
				return err
			}

			err = lastHashQuery.Value(func(val []byte) error {
					lastHash = append(lastHash, val...)
					return nil
			})
			if err != nil {
				return err
			}
			lastBlockQuery, err := txn.Get(lastHash)
			if err != nil {
				return err
			}

			err = lastBlockQuery.Value(func(val []byte) error {
				lastBlock, err = DeserializeBlockFromGOB(val)
				fmt.Println("Last Block: ", lastBlock)
				return err
			})
			return err
	})

	if err != nil {
		return err
	}

	block.PreviousHash = lastHash
	block.Height = lastBlock.Height + 1
	Validators[string(wlt.Address)] = Validator{wlt.PublicKey, wlt.Address, true}
	errr := ProofOfAuthority(block, wlt)
	if errr != nil {
		fmt.Println("Error while signing block: ", err)
		return errr
	}

	if err != nil {
		return err
	}

	// block.ValidatorAddress = validatorAddress
	block.BlockHash = block.Hash()
	fmt.Println("Block information: ", block)

	return nil
}