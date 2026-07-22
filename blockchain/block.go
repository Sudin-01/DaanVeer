package blockchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
	// "encoding/hex"

	"github.com/Sudin-01/DaanVeer/wallet"
	"github.com/dgraph-io/badger/v4"
)

const (
	GENESIS_STRING = "THIS IS THE FIRST BLOCK"
	// DEFAULT_GENESIS_TIMESTAMP is used when bootstrapping a fresh config.
	DEFAULT_GENESIS_TIMESTAMP = 1646919219
	// DEFAULT_GENESIS_AMOUNT is the initial grant written into a fresh config.
	DEFAULT_GENESIS_AMOUNT = 1000
)

func init() {
	log.SetPrefix("Blockchain: ")
}

// BLOCK_VERSION prefixes the canonical block preimage.
const BLOCK_VERSION byte = 2

type Block struct {
	PreviousHash     []byte
	Timestamp        uint64
	BlockHash        []byte
	Height           uint64
	Signature        string
	ValidatorAddress []byte
	TxMerkleTree     *MerkleTree

	// Attestations are signatures from validators other than the proposer.
	// They cover the block hash, which is fixed before any attestation is
	// produced, so gathering them cannot change the block's identity -- and
	// they are therefore excluded from the hash preimage.
	Attestations []Attestation

	// Txs is the authoritative, ordered transaction list for this block.
	//
	// Transactions were previously recovered by walking TxMerkleTree.Nodes,
	// but NewMerkleTree assigns Nodes the final level of the tree -- the root
	// alone -- so every block with more than one transaction lost all but the
	// first when balances were computed. The Merkle tree is now derived from
	// this list rather than being the storage for it.
	Txs []Transactions
}

// Transactions returns the block's transaction list.
func (b *Block) Transactions() []Transactions {
	if b == nil {
		return nil
	}
	return b.Txs
}

// MerkleRoot returns the block's Merkle root, or nil when it holds no
// transactions.
func (b *Block) MerkleRoot() []byte {
	if b == nil || b.TxMerkleTree == nil || b.TxMerkleTree.Root == nil {
		return nil
	}
	return b.TxMerkleTree.Root.Hash
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

// Hash returns the block identifier: SHA-256 over a canonical preimage that
// commits to the header *and* to the Merkle root of the block's transactions.
//
// The previous implementation set TxMerkleTree to nil before hashing, so the
// block hash -- and therefore the validator signature computed over it -- did
// not commit to the transactions at all. A validator could sign a block of
// honest donations and then substitute an entirely different transaction set
// without invalidating either the hash or the signature.
//
// BlockHash and Signature are excluded because they are derived from this value.
func (b *Block) Hash() []byte {
	var buf bytes.Buffer
	buf.WriteByte(BLOCK_VERSION)
	writeField(&buf, b.PreviousHash)
	_ = binary.Write(&buf, binary.BigEndian, b.Timestamp)
	_ = binary.Write(&buf, binary.BigEndian, b.Height)
	writeField(&buf, b.ValidatorAddress)
	writeField(&buf, b.MerkleRoot())

	hash := sha256.Sum256(buf.Bytes())
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
		Height           uint64         `json:"height"`
		BlockHash        string         `json:"block_hash"`
		Timestamp        uint64         `json:"timestamp"`
		PreviousHash     string         `json:"previous_hash"`
		ValidatorAddress string         `json:"validator_address"`
		MerkleRoot       string         `json:"merkle_root"`
		Transactions     []Transactions `json:"transactions"`
	}{
		Height:           b.Height,
		BlockHash:        fmt.Sprintf("%x", b.BlockHash),
		Timestamp:        b.Timestamp,
		PreviousHash:     fmt.Sprintf("%x", b.PreviousHash),
		ValidatorAddress: string(b.ValidatorAddress),
		MerkleRoot:       merkleRootHash,
		// Previously declared but never populated, so every block serialized
		// with "transactions": null regardless of its contents.
		Transactions: b.Txs,
	})
}

// AddTxToBlock sets the block's transaction list and derives its Merkle tree.
// Every non-genesis transaction must carry a valid signature.
func (b *Block) AddTxToBlock(txPool []Transactions) error {
	for i, tx := range txPool {
		if err := tx.Verify(); err != nil {
			return fmt.Errorf("transaction %d rejected: %v", i, err)
		}
	}
	b.Txs = append([]Transactions(nil), txPool...)
	b.TxMerkleTree = NewMerkleTree(b.Txs)
	return nil
}

// blockWire is the serialized form of a block, for both storage and the
// network.
//
// The Merkle tree is deliberately absent. MerkleNode embeds a full copy of a
// transaction in every node -- including internal nodes, which correspond to no
// transaction at all -- and a block previously serialized that tree alongside
// its transaction list. Each transaction was therefore written roughly three
// times, costing ~1259 bytes per transaction against a ~224 byte payload.
//
// The tree is a function of the transaction list, so it is rebuilt on decode
// instead of being transmitted. The Merkle root remains committed to by the
// block hash, so nothing about verification changes.
type blockWire struct {
	PreviousHash     []byte
	Timestamp        uint64
	BlockHash        []byte
	Height           uint64
	Signature        string
	ValidatorAddress []byte
	Attestations     []Attestation
	Txs              []Transactions
}

// GobEncode implements gob.GobEncoder, so every gob path -- storage and the
// p2p wire format alike -- uses the compact form.
func (b Block) GobEncode() ([]byte, error) {
	var encoded bytes.Buffer
	err := gob.NewEncoder(&encoded).Encode(blockWire{
		PreviousHash:     b.PreviousHash,
		Timestamp:        b.Timestamp,
		BlockHash:        b.BlockHash,
		Height:           b.Height,
		Signature:        b.Signature,
		ValidatorAddress: b.ValidatorAddress,
		Attestations:     b.Attestations,
		Txs:              b.Txs,
	})
	return encoded.Bytes(), err
}

// GobDecode implements gob.GobDecoder, rebuilding the Merkle tree from the
// transaction list.
func (b *Block) GobDecode(data []byte) error {
	var wire blockWire
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&wire); err != nil {
		return err
	}
	b.PreviousHash = wire.PreviousHash
	b.Timestamp = wire.Timestamp
	b.BlockHash = wire.BlockHash
	b.Height = wire.Height
	b.Signature = wire.Signature
	b.ValidatorAddress = wire.ValidatorAddress
	b.Attestations = wire.Attestations
	b.Txs = wire.Txs
	b.TxMerkleTree = NewMerkleTree(b.Txs)
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

// CreateGenesisBlock builds the first block from the active chain
// configuration. Genesis parameters were previously compile-time constants,
// which made the chain impossible to re-initialise or reproduce.
func CreateGenesisBlock() *Block {
	cfg, err := ActiveConfig()
	if err != nil {
		log.Panicf("cannot create genesis block: %v", err)
	}
	genesisPubKeyHash, err := wallet.PubKeyFromAddress(cfg.GenesisAddress)
	if err != nil {
		log.Panicf("invalid genesis address: %v", err)
	}

	genesisTx := Transactions{
		SenderHash:    GENESIS_SENDER,
		RecipientHash: genesisPubKeyHash,
		Value:         cfg.GenesisAmount,
		Timestamp:     cfg.GenesisTimestamp,
	}
	genesisTx.TxID = genesisTx.Hash()
	txPool := []Transactions{genesisTx}

	block := Block{
		Timestamp:    cfg.GenesisTimestamp,
		Height:       0,
		Txs:          txPool,
		TxMerkleTree: NewMerkleTree(txPool),
	}
	// The genesis hash is derived like every other block's, so the chain has a
	// single hashing rule rather than a special case that skips the Merkle root.
	block.BlockHash = block.Hash()
	return &block
}

// VerifyTransactions checks every transaction in the block and confirms that
// the stored Merkle tree actually commits to that transaction list.
func (block *Block) VerifyTransactions() error {
	for i, tx := range block.Transactions() {
		if err := tx.Verify(); err != nil {
			return fmt.Errorf("block transaction %d invalid: %v", i, err)
		}
	}

	// Recompute the Merkle root so a block cannot carry a tree that disagrees
	// with the transactions it ships.
	recomputed := NewMerkleTree(block.Txs)
	var want, got []byte
	if recomputed != nil && recomputed.Root != nil {
		want = recomputed.Root.Hash
	}
	got = block.MerkleRoot()
	if !bytes.Equal(want, got) {
		return errors.New("merkle root does not match the block's transactions")
	}
	return nil
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

	// The caller used to be written into the Validators map here, *before*
	// ProofOfAuthority checked authorisation -- so a rejected mining attempt
	// still granted permanent validator status, after which the node could
	// hand-sign blocks that AddBlock would accept. Authorisation is now
	// decided solely by the configured validator set.
	if errr := ProofOfAuthority(block, wlt); errr != nil {
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
