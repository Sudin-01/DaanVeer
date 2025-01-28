package blockchain

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/Roshan310/DaanVeer/wallet"
)

type Transactions struct {
	SenderHash    []byte
	RecipientHash []byte
	Value         float32
	Signature     []byte
	Timestamp     uint64
}

func NewTransaction(sender []byte, recipient []byte, value float32) *Transactions {
	return &Transactions{SenderHash: sender, RecipientHash: recipient, Value: value}
}

func (t *Transactions) Print() {
	fmt.Printf("%s\n", strings.Repeat("-", 40))
	fmt.Printf("Sender Address:    %s\n", t.SenderHash)
	fmt.Printf("Recipient Address: %s\n", t.RecipientHash)
	fmt.Printf("Value:             %.1f\n", t.Value)
	fmt.Printf("Signature:         %x\n", t.Signature)
	fmt.Printf("Timestamp:         %d\n", t.Timestamp)
}

func (t *Transactions) Hash() []byte {
	m, _ := json.Marshal(t)
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
	return ecdsa.Verify(pubKey, hash, r, s)
}
