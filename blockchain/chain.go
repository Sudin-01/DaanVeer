package blockchain

import (
	"fmt"
	"strings"

	"github.com/Roshan310/DaanVeer/wallet"
)

type Blockchain struct {
	TransactionPool []Transactions
	Chain           []*Block
	Balances        map[string]float32 // Track balances
	PoA             *PoA               // Proof of Authority mechanism
}

func NewBlockchain(authorityAddresses []string) *Blockchain {
	
	bc := &Blockchain{
		TransactionPool: []Transactions{},
		Chain:           []*Block{},
		Balances:        make(map[string]float32),
		PoA:             NewPoA(authorityAddresses),
	}
	// Create Genesis Block
	genesisBlock := NewBlock([]byte(GENESIS_STRING), []Transactions{})
	bc.Chain = append(bc.Chain, genesisBlock)
	return bc
}

func (bc *Blockchain) LastBlock() *Block {
	return bc.Chain[len(bc.Chain)-1]
}

func (bc *Blockchain) Print() {
	for i, block := range bc.Chain {
		fmt.Printf("%s Chain %d %s\n", strings.Repeat("=", 25), i, strings.Repeat("=", 25))
		block.Print()
	}
	fmt.Printf("%s\n", strings.Repeat("*", 25))
}

func (bc *Blockchain) AddTransaction(sender string, recipient string, value float32, wallet *wallet.Wallet) error {
	// Check sender balance
	if bc.Balances[sender] < value {
		return fmt.Errorf("insufficient balance")
	}

	// Create and sign transaction
	t := NewTransaction([]byte(sender), []byte(recipient), value)
	if err := t.SignTransaction(wallet); err != nil {
		return fmt.Errorf("failed to sign transaction: %v", err)
	}

	//Verify transaction
	// if !t.VerifyTransaction(wallet.PublicKey) {
	// 	return fmt.Errorf("transaction verification failed")
	// }

	// Add transaction to pool
	bc.TransactionPool = append(bc.TransactionPool, *t)
	return nil
}

func (bc *Blockchain) CreateBlock(authorityAddress string) error {
	// Validate authority
	if !bc.PoA.IsAuthorized(authorityAddress) {
		return fmt.Errorf("unauthorized authority")
	}

	// Create new block
	block := NewBlock(bc.LastBlock().BlockHash, bc.TransactionPool)

	// Sign block
	bc.PoA.SignBlock(authorityAddress, block)

	// Append block to chain
	bc.Chain = append(bc.Chain, block)

	// Update balances and clear transaction pool
	for _, tx := range bc.TransactionPool {
		bc.Balances[string(tx.SenderHash)] -= tx.Value
		bc.Balances[string(tx.RecipientHash)] += tx.Value
	}
	bc.TransactionPool = []Transactions{}
	return nil
}
