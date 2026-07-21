package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"bytes"
	"golang.org/x/crypto/ripemd160"
	"github.com/mr-tron/base58"
)

const (
	CHECK_SUM_LENGTH = 4
	// WALLET_KEY_ENV names the environment variable holding the wallet-file
	// encryption passphrase. It was previously a constant compiled into the
	// binary and committed to version control, which made the encryption of
	// wallet files decorative.
	WALLET_KEY_ENV = "DAANVEER_WALLET_KEY"
)

// walletKey returns the wallet encryption passphrase, or an error if unset.
func walletKey() (string, error) {
	key := os.Getenv(WALLET_KEY_ENV)
	if key == "" {
		return "", fmt.Errorf("%s is not set; refusing to read or write wallet files with a default key", WALLET_KEY_ENV)
	}
	if len(key) < 16 {
		return "", fmt.Errorf("%s must be at least 16 characters", WALLET_KEY_ENV)
	}
	return key, nil
}

type Wallet struct {
	PrivateKey *ecdsa.PrivateKey
	PublicKey  *ecdsa.PublicKey
	Address    string
}

func (w *Wallet) GenerateKeyPair() error {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	w.PrivateKey = privateKey
	w.PublicKey = &privateKey.PublicKey
	return nil
}

// COORDINATE_LENGTH is the fixed width of a P-256 field element in bytes.
// Both public key coordinates are padded to this width so that serialization
// is a bijection. See PadTo32.
const COORDINATE_LENGTH = 32

// PadTo32 left-pads a big-endian integer to 32 bytes.
//
// big.Int.Bytes() strips leading zero bytes, so a coordinate or signature
// component smaller than 2^248 serializes short. Any scheme that recovers the
// components by splitting a concatenation at len/2 then misaligns and silently
// produces the wrong value. Fixed-width encoding removes the ambiguity.
func PadTo32(i *big.Int) []byte {
	b := i.Bytes()
	if len(b) >= COORDINATE_LENGTH {
		return b
	}
	out := make([]byte, COORDINATE_LENGTH)
	copy(out[COORDINATE_LENGTH-len(b):], b)
	return out
}

func PublicKeyToBytes(publicKey *ecdsa.PublicKey) ([]byte, error) {
	if publicKey == nil || publicKey.X == nil || publicKey.Y == nil {
		return nil, errors.New("nil public key")
	}
	out := make([]byte, 0, 2*COORDINATE_LENGTH)
	out = append(out, PadTo32(publicKey.X)...)
	out = append(out, PadTo32(publicKey.Y)...)
	return out, nil
}

func BytesToPublicKey(pubKeyBytes []byte) (*ecdsa.PublicKey, error) {
	curve := elliptic.P256()
	if len(pubKeyBytes) != 2*COORDINATE_LENGTH {
		return nil, fmt.Errorf("invalid public key length %d, want %d", len(pubKeyBytes), 2*COORDINATE_LENGTH)
	}
	x := new(big.Int).SetBytes(pubKeyBytes[:COORDINATE_LENGTH])
	y := new(big.Int).SetBytes(pubKeyBytes[COORDINATE_LENGTH:])

	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("invalid points on the curve for this public key")
	}

	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

// PublicKeyHashRipeMD160 returns RIPEMD-160(SHA-256(pubkey)), a 20-byte digest.
//
// The previous implementation constructed the RIPEMD-160 hasher, wrote to it,
// then returned the intermediate SHA-256 digest and discarded the result --
// yielding 32 bytes, not the documented 20.
func PublicKeyHashRipeMD160(pubKey *ecdsa.PublicKey) []byte {
	pubKeyBytes, err := PublicKeyToBytes(pubKey)
	if err != nil {
		return nil
	}
	pubKeyHash := sha256.Sum256(pubKeyBytes)
	ripeMDHasher := ripemd160.New()
	if _, err := ripeMDHasher.Write(pubKeyHash[:]); err != nil {
		return nil
	}
	return ripeMDHasher.Sum(nil)
}

func GenerateAddress(publicKey *ecdsa.PublicKey) string {
	publicKeyHash := PublicKeyHashRipeMD160(publicKey)
	checkSum := calculateCheckSum(publicKeyHash)
	finalHash := append(publicKeyHash, checkSum...)
	return base58.Encode(finalHash)
}

func calculateCheckSum(payload []byte) []byte {
	firstHash := sha256.Sum256(payload)
	secondHash := sha256.Sum256(firstHash[:])
	return secondHash[:CHECK_SUM_LENGTH]
}

// AES Encryption
func encrypt(data, passphrase string) (string, error) {
	key := sha256.Sum256([]byte(passphrase))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}

	ciphertext := make([]byte, aes.BlockSize+len(data))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}

	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(ciphertext[aes.BlockSize:], []byte(data))

	return hex.EncodeToString(ciphertext), nil
}

// AES Decryption
func decrypt(data, passphrase string) (string, error) {
	key := sha256.Sum256([]byte(passphrase))
	ciphertext, err := hex.DecodeString(data)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}

	if len(ciphertext) < aes.BlockSize {
		return "", errors.New("ciphertext too short")
	}
	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(ciphertext, ciphertext)

	return string(ciphertext), nil
}

func (w *Wallet) SaveToFile(fileName string) error {
	// Serialize private key, public key, and address
	pubKeyBytes, err := PublicKeyToBytes(w.PublicKey)
	if err != nil {
		return err
	}
	data := fmt.Sprintf(
		"%x\n%x\n%s",
		w.PrivateKey.D.Bytes(),
		pubKeyBytes,
		w.Address,
	)

	key, err := walletKey()
	if err != nil {
		return err
	}

	// Encrypt data
	encryptedData, err := encrypt(data, key)
	if err != nil {
		return err
	}

	// Open the file in append mode
	file, err := os.OpenFile(fileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	// Write the encrypted wallet data to the file
	_, err = file.WriteString(encryptedData + "\n\n")
	return err
}

func LoadAllWallets(fileName string) ([]*Wallet, error) {
	// Read file data
	data, err := os.ReadFile(fileName)
	if err != nil {
		return nil, err
	}

	key, err := walletKey()
	if err != nil {
		return nil, err
	}

	// Split the file into encrypted wallet blocks
	encryptedBlocks := strings.Split(strings.TrimSpace(string(data)), "\n\n")

	var wallets []*Wallet
	for _, block := range encryptedBlocks {
		// Decrypt the wallet block
		decryptedData, err := decrypt(block, key)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt wallet: %v", err)
		}

		// Split the decrypted data into lines
		lines := strings.Split(decryptedData, "\n")
		if len(lines) < 3 {
			return nil, errors.New("invalid wallet block format")
		}

		// Parse the wallet
		privKeyBytes, err := hex.DecodeString(lines[0])
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %v", err)
		}
		pubKeyBytes, err := hex.DecodeString(lines[1])
		if err != nil {
			return nil, fmt.Errorf("failed to parse public key: %v", err)
		}
		address := lines[2]

		// Construct wallet
		wallet := &Wallet{}
		wallet.PrivateKey = new(ecdsa.PrivateKey)
		wallet.PrivateKey.PublicKey.Curve = elliptic.P256()
		wallet.PrivateKey.D = new(big.Int).SetBytes(privKeyBytes)
		wallet.PrivateKey.PublicKey.X, wallet.PrivateKey.PublicKey.Y = elliptic.P256().ScalarBaseMult(privKeyBytes)

		wallet.PublicKey, err = BytesToPublicKey(pubKeyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to reconstruct public key: %v", err)
		}
		wallet.Address = address

		wallets = append(wallets, wallet)
	}

	return wallets, nil
}

func GenerateWallet(filename string) (*Wallet, error) {

	if _, err := os.Stat(filename); err == nil {
		wallets, err := LoadAllWallets(filename)
		if err == nil && len(wallets) > 0 {
			fmt.Printf("Wallet already exists! Your address: %s\n", wallets[0].Address)
			return wallets[0], nil 
		}
	}

	wallet := &Wallet{}
	if err := wallet.GenerateKeyPair(); err != nil {
		return nil, err
	}
	wallet.Address = GenerateAddress(wallet.PublicKey)
	if err := wallet.SaveToFile(filename); err != nil {
		return nil, err
	}

	fmt.Printf("Your wallet is generated and here is your address %s\n", wallet.Address)
	return wallet, nil
}

func PubKeyFromAddress(address string) ([]byte, error) {
	checksumHash, err := base58.Decode(address)
	if err != nil {
		return nil, err
	}
	checksumOffset := len(checksumHash) - CHECK_SUM_LENGTH
	actualChecksum := checksumHash[checksumOffset:]
	pubKeyHash := checksumHash[0:checksumOffset]
	targetChecksum := calculateCheckSum(pubKeyHash)
	if bytes.Equal(actualChecksum, targetChecksum) {
		return pubKeyHash, nil
	}
	return nil, errors.New("this is not a valid address")
}