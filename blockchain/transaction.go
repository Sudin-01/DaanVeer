package blockchain

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/Sudin-01/DaanVeer/wallet"
)
// TX_VERSION prefixes the canonical encoding so the hash preimage format can
// be changed later without silently colliding with previously signed data.
const TX_VERSION byte = 3

// NONCE_LENGTH is the width of the per-transaction uniqueness nonce.
const NONCE_LENGTH = 8

// GENESIS_SENDER marks the coinbase-style transaction in the genesis block.
var GENESIS_SENDER = []byte("GENESIS")

type Transactions struct {
	TxID          []byte `json:"-"`
	SenderHash    []byte `json:"-"`
	SenderPubKey  []byte `json:"-"`
	RecipientHash []byte `json:"-"`
	Value         uint64 `json:"value"`
	Signature     []byte `json:"-"`
	Timestamp     uint64 `json:"timestamp"`

	// CampaignID earmarks a donation to a named cause, or is empty for an
	// unattributed transfer. Part of the signed preimage, so the earmark
	// cannot be altered after the donor signs.
	CampaignID []byte `json:"-"`

	// Nonce makes each transaction unique.
	//
	// The identifier was previously derived from sender, recipient, value and
	// a second-resolution timestamp, so two identical donations sent in the
	// same second produced the same TxID and the second was silently rejected
	// as a mempool duplicate. That capped donation throughput independently of
	// consensus. A random nonce removes the dependence on clock resolution
	// entirely, rather than merely narrowing the window.
	Nonce []byte `json:"-"`
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


// NewCampaignTransaction creates a donation earmarked to a named campaign.
//
// The earmark is part of the signed preimage, so it cannot be altered after the
// donor signs it, and it is what makes the funds traceable through onward
// transfers.
func NewCampaignTransaction(srcWallet *wallet.Wallet, destinationAddr string, amount uint64, campaign string, chain *BlockChain) (*Transactions, error) {
	tx, err := newTransaction(srcWallet, destinationAddr, amount, CampaignID(campaign), chain)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func NewTransaction(srcWallet *wallet.Wallet, destinationAddr string, amount uint64, chain *BlockChain) (*Transactions, error) {
	return newTransaction(srcWallet, destinationAddr, amount, nil, chain)
}

func newTransaction(srcWallet *wallet.Wallet, destinationAddr string, amount uint64, campaign []byte, chain *BlockChain) (*Transactions, error) {
	senderAddress := string(srcWallet.Address)
	// Check against the spendable balance -- committed funds less those already
	// reserved by unmined transactions. Checking committed state alone let a
	// sender spend the same balance repeatedly before any block was produced.
	senderBalance, err := chain.SpendableBalance(senderAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get sender balance: %v", err)
	}

	if senderBalance < amount {
		return nil, fmt.Errorf("insufficient spendable balance: have %d, need %d", senderBalance, amount)
	}
	senderPubKeyHash, err := wallet.PubKeyFromAddress(senderAddress)
	if err != nil {
		return nil, err
	}

	receiverPubKeyHash, err := wallet.PubKeyFromAddress(destinationAddr)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, NONCE_LENGTH)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("could not generate transaction nonce: %v", err)
	}

	newTx := Transactions{
		SenderHash:    senderPubKeyHash,
		RecipientHash: receiverPubKeyHash,
		CampaignID:    campaign,
		Nonce:         nonce,
		Value:         amount,
		Timestamp:     uint64(time.Now().Unix()),
	}

	// SignTransaction sets SenderPubKey and TxID before signing, since both are
	// part of the signed preimage.
	if err := newTx.SignTransaction(srcWallet); err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %v", err)
	}

	// Reserve the funds so a second call cannot spend them again.
	if err := chain.Mempool.Add(newTx); err != nil {
		return nil, err
	}

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

// writeField length-prefixes a byte slice so that concatenation is injective.
// Without the prefix, ("ab","c") and ("a","bc") would encode identically.
func writeField(buf *bytes.Buffer, b []byte) {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(b)))
	buf.Write(l[:])
	buf.Write(b)
}

// canonicalBytes is the signed preimage of a transaction. It covers every
// field that determines the transaction's meaning.
//
// The previous implementation hashed json.Marshal of a Transactions *value*.
// Because MarshalJSON has a pointer receiver it was never invoked, so encoding
// fell back to the struct tags -- and TxID, SenderHash, RecipientHash and
// Signature are all tagged `json:"-"`. The signed preimage was therefore only
// {"value":N,"timestamp":T}: the recipient was not committed to, and any two
// transactions sharing a value and timestamp collided on TxID.
func (t *Transactions) canonicalBytes() []byte {
	var buf bytes.Buffer
	buf.WriteByte(TX_VERSION)
	writeField(&buf, t.SenderHash)
	writeField(&buf, t.SenderPubKey)
	writeField(&buf, t.RecipientHash)
	writeField(&buf, t.Nonce)
	writeField(&buf, t.CampaignID)
	_ = binary.Write(&buf, binary.BigEndian, t.Value)
	_ = binary.Write(&buf, binary.BigEndian, t.Timestamp)
	return buf.Bytes()
}

// Hash returns the transaction identifier: SHA-256 over the canonical preimage.
// TxID and Signature are excluded -- TxID is this value, and the signature is
// produced from it.
func (t *Transactions) Hash() []byte {
	hash := sha256.Sum256(t.canonicalBytes())
	return hash[:]
}

func (t *Transactions) SignTransaction(w *wallet.Wallet) error {
	if w == nil || w.PrivateKey == nil {
		return errors.New("cannot sign with a nil wallet")
	}
	pubKeyBytes, err := wallet.PublicKeyToBytes(w.PublicKey)
	if err != nil {
		return err
	}
	// The public key is part of the signed preimage, so it must be set before
	// the hash is computed.
	t.SenderPubKey = pubKeyBytes
	t.TxID = t.Hash()

	r, s, err := ecdsa.Sign(rand.Reader, w.PrivateKey, t.TxID)
	if err != nil {
		return err
	}
	t.Signature = append(wallet.PadTo32(r), wallet.PadTo32(s)...)
	return nil
}

// IsGenesis reports whether this is the genesis funding transaction, which by
// construction carries no signature.
func (t *Transactions) IsGenesis() bool {
	return bytes.Equal(t.SenderHash, GENESIS_SENDER)
}

// Verify checks that the transaction is internally consistent and correctly
// signed by the holder of the key its SenderHash commits to.
//
// It takes no public key argument: the key travels with the transaction and is
// bound to SenderHash, so a caller cannot be tricked into verifying against an
// attacker-supplied key.
func (t *Transactions) Verify() error {
	if t.IsGenesis() {
		return nil
	}
	if len(t.SenderPubKey) == 0 {
		return errors.New("transaction carries no sender public key")
	}
	if len(t.Signature) != 64 {
		return fmt.Errorf("malformed signature: %d bytes, want 64", len(t.Signature))
	}

	pubKey, err := wallet.BytesToPublicKey(t.SenderPubKey)
	if err != nil {
		return fmt.Errorf("invalid sender public key: %v", err)
	}

	// Bind the key to the claimed sender, otherwise anyone could sign for anyone.
	if !bytes.Equal(wallet.PublicKeyHashRipeMD160(pubKey), t.SenderHash) {
		return errors.New("sender public key does not match sender hash")
	}

	expectedID := t.Hash()
	if len(t.TxID) != 0 && !bytes.Equal(t.TxID, expectedID) {
		return errors.New("transaction id does not match its contents")
	}

	r := new(big.Int).SetBytes(t.Signature[:32])
	s := new(big.Int).SetBytes(t.Signature[32:])
	if !ecdsa.Verify(pubKey, expectedID, r, s) {
		return errors.New("signature verification failed")
	}
	return nil
}

// VerifyTransaction is retained for compatibility with existing callers.
//
// Deprecated: use Verify, which does not require the caller to supply -- and
// therefore cannot be misled about -- the signing key.
func (t *Transactions) VerifyTransaction(pubKey *ecdsa.PublicKey) bool {
	return t.Verify() == nil
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