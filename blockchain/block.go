package blockchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"log"
	"time"
	// "encoding/hex"

	"github.com/Roshan310/DaanVeer/wallet"
	"github.com/dgraph-io/badger/v4"
)
const (
	GENESIS_STRING = "THIS IS THE FIRST BLOCK"
	GENESIS_TIMESTAMP = 1646919219
	GENESIS_AMOUNT = 1000
	//genesis address is my wallet address which will have amount of 1000 initially
	GENESIS_ADDRESS = "5Dv7dCeuvoLntY5QueBDsConyi1hckMVjdLCeg6kdeeC6wE8G"
)
func init() {
	log.SetPrefix("Blockchain: ")
}

type Block struct {
	PreviousHash []byte
	Timestamp    uint64
	BlockHash []byte
	Height uint64
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
}

func (b *Block) Hash() []byte {
    tempBlock := *b 
    tempBlock.BlockHash = nil
    tempBlock.Signature = "" 
    tempBlock.TxMerkleTree = nil

    // Use JSON instead of gob
	//gob was causing a lot of problem (hash mis-match) so changed to JSON
    blockJSON, err := json.Marshal(tempBlock)
    if err != nil {
        fmt.Println("Error while encoding block:", err)
    }
    
    hash := sha256.Sum256(blockJSON)
    return hash[:]
}


func (b *Block) MarshalJSON() ([]byte, error) {
	var merkleRootHash string
	if b.TxMerkleTree != nil && b.TxMerkleTree.Root != nil {
		merkleRootHash = fmt.Sprintf("%x", b.TxMerkleTree.Root.Hash)
	} else {
		merkleRootHash = "" 
	}
	return json.Marshal(struct {
		Height 	 uint64          `json:"height"`
		BlockHash    string        `json:"block_hash"`
		Timestamp    uint64          `json:"timestamp"`
		PreviousHash string     `json:"previous_hash"`
		ValidatorAddress string `json: "validator_address"`
		MerkleRoot   string     `json:"merkle_root"`
		Transactions []Transactions `json:"transactions"`
	}{
		Height:       b.Height,
		BlockHash:    fmt.Sprintf("%x", b.BlockHash),
		Timestamp:    b.Timestamp,
		PreviousHash:  fmt.Sprintf("%x", b.PreviousHash),
		ValidatorAddress: string(b.ValidatorAddress),
		MerkleRoot:   merkleRootHash,
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
	genesisPubKeyHash, err := wallet.PubKeyFromAddress(GENESIS_ADDRESS)
	if err != nil {
		log.Panic("invalid genesis address: %v", err)
	}

	genesisTx := Transactions{
		SenderHash: []byte("GENESIS"),
		RecipientHash: genesisPubKeyHash,
		Value: GENESIS_AMOUNT,
		Timestamp: GENESIS_TIMESTAMP,
	}
	genesisTx.TxID = genesisTx.Hash()
	txPool := []Transactions{genesisTx}
	merkleTree := NewMerkleTree(txPool)

	block_hash := sha256.Sum256([]byte(GENESIS_STRING))
	block := Block{
		Timestamp: GENESIS_TIMESTAMP,
		Height: 0,
		BlockHash: block_hash[:],
		TxMerkleTree: merkleTree,
	}
	return &block
}

func (block *Block) VerifyBlockHash() bool {
	// fmt.Println("Inside verify block hash block property: ", block)
	computedBlockHash := block.Hash()
	// fmt.Println("Computed Block Hash: ", computedBlockHash)
	// fmt.Println("Block Hash in byte: ", block.BlockHash)
	// fmt.Println("Block Hash in string: ", hex.EncodeToString(block.BlockHash))
	return bytes.Equal(computedBlockHash, block.BlockHash)
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

	// if err != nil {
	// 	return err
	// }

	// block.ValidatorAddress = []byte(Validators[string(wlt.Address)].Address)
	// fmt.Println("Validator Address while mining the block: ", block.ValidatorAddress)
	block.BlockHash = block.Hash()

	return nil
}