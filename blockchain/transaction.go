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
	"bytes"
	"encoding/gob"
	"encoding/hex"

	"github.com/Roshan310/DaanVeer/wallet"
)
type Transactions struct {
	TxID          []byte `json:"-"`
	SenderHash    []byte `json:"-"`
	RecipientHash []byte `json:"-"`
	Value         uint64 `json:"value"`
	Signature     []byte `json:"-"`
	Timestamp     uint64 `json:"timestamp"`
}

// Struct for JSON conversion
type TransactionsJSON struct {
	TxID          string `json:"txID"`
	SenderHash    string `json:"senderHash"`
	RecipientHash string `json:"recipientHash"`
	Value         uint64 `json:"value"`
	Signature     string `json:"signature"`
	Timestamp     uint64 `json:"timestamp"`
}

// Convert `Transactions` to `TransactionsJSON`
func (tx *Transactions) ToJSON() TransactionsJSON {
	return TransactionsJSON{
		TxID:          hex.EncodeToString(tx.TxID),
		SenderHash:    hex.EncodeToString(tx.SenderHash),
		RecipientHash: hex.EncodeToString(tx.RecipientHash),
		Value:         tx.Value,
		Signature:     hex.EncodeToString(tx.Signature),
		Timestamp:     tx.Timestamp,
	}
}

// Implement `MarshalJSON` for custom JSON encoding
func (tx *Transactions) MarshalJSON() ([]byte, error) {
	return json.Marshal(tx.ToJSON())
}

// Implement `UnmarshalJSON` for decoding JSON properly
func (tx *Transactions) UnmarshalJSON(data []byte) error {
	var txJSON TransactionsJSON
	if err := json.Unmarshal(data, &txJSON); err != nil {
		return err
	}

	// Decode Hex Strings back to []byte
	txID, err := hex.DecodeString(txJSON.TxID)
	if err != nil {
		return fmt.Errorf("invalid txID: %v", err)
	}
	senderHash, err := hex.DecodeString(txJSON.SenderHash)
	if err != nil {
		return fmt.Errorf("invalid sender hash: %v", err)
	}
	recipientHash, err := hex.DecodeString(txJSON.RecipientHash)
	if err != nil {
		return fmt.Errorf("invalid recipient hash: %v", err)
	}
	signature, err := hex.DecodeString(txJSON.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature: %v", err)
	}

	tx.TxID = txID
	tx.SenderHash = senderHash
	tx.RecipientHash = recipientHash
	tx.Value = txJSON.Value
	tx.Signature = signature
	tx.Timestamp = txJSON.Timestamp

	return nil
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

func (t *Transactions) Hash() ([]byte) {
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


func (tx Transactions) SerializeTxToGOB() ([]byte, error) {
	var encoded bytes.Buffer
	err := gob.NewEncoder(&encoded).Encode(tx)
	return encoded.Bytes(), err // if err in encoding then nil is returned anyway
}

func DeserializeTxFromGOB(serializedTx []byte) (*Transactions, error) {
	var tx Transactions
	err := gob.NewDecoder(bytes.NewReader(serializedTx)).Decode(&tx)
	return &tx, err
}