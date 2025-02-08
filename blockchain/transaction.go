package blockchain

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/Roshan310/DaanVeer/wallet"
)

type Transactions struct {
	TxID 		  []byte
	SenderHash    []byte
	RecipientHash []byte
	Value         uint64
	Signature     []byte
	Timestamp     uint64
}

func NewTransaction(srcWallet *wallet.Wallet, destinationAddr string, amount uint64, chain *BlockChain) (*Transactions, error) {
	senderAddress := string(srcWallet.Address)
	senderPubKeyHash, err := wallet.PubKeyFromAddress(senderAddress)
	if err != nil {
		return nil, err
	}

	receiverPubKeyHash, err := wallet.PubKeyFromAddress(destinationAddr)
	if err != nil {
		return nil, err
	}

	newTx := Transactions{	
		SenderHash:    senderPubKeyHash,
		RecipientHash: receiverPubKeyHash,
		Value: amount,
		Timestamp: uint64(time.Now().Unix()),
	}

	newTx.TxID = newTx.Hash()
	newTx.SignTransaction(srcWallet)

	return &newTx, nil

}

func (t *Transactions) Print() {
	fmt.Printf("%s\n", strings.Repeat("-", 40))
	fmt.Printf("Sender Address:    %s\n", t.SenderHash)
	fmt.Printf("Recipient Address: %s\n", t.RecipientHash)
	fmt.Printf("Value:             %d\n", t.Value)
	fmt.Printf("Signature:         %x\n", t.Signature)
	fmt.Printf("Timestamp:         %d\n", t.Timestamp)
}

func (t *Transactions) Hash() []byte {
	temp := *t
	temp.Signature = nil // Exclude the signature becuase it is used only after hashing
	m, _ := json.Marshal(temp)
	hash := sha256.Sum256(m)
	return hash[:]
}

func (t *Transactions) SignTransaction(wallet *wallet.Wallet) error {
	r, s, err := ecdsa.Sign(rand.Reader, wallet.PrivateKey, t.Hash())
	if err != nil {
		return err
	}
	t.Signature = append(r.Bytes(), s.Bytes()...)
	return nil
}

func (t *Transactions) VerifyTransaction(pubKey *ecdsa.PublicKey) bool {
	r := new(big.Int).SetBytes(t.Signature[:len(t.Signature)/2])
	s := new(big.Int).SetBytes(t.Signature[len(t.Signature)/2:])
	hash := t.Hash()
	fmt.Println([]byte(hash))
	return ecdsa.Verify(pubKey, []byte(hash), r, s)
}
