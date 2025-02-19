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
	ENCRYPTION_KEY   = "my-secure-32-byte-key" 
)

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

func PublicKeyToBytes(publicKey *ecdsa.PublicKey) ([]byte, error) {
	return append(publicKey.X.Bytes(), publicKey.Y.Bytes()...), nil
}

func BytesToPublicKey(pubKeyBytes []byte) (*ecdsa.PublicKey, error) {
	curve := elliptic.P256()
	keyLen := len(pubKeyBytes) / 2
	if keyLen == 0 {
		return nil, errors.New("invalid bytes of public key")
	}
	x := new(big.Int).SetBytes(pubKeyBytes[:keyLen])
	y := new(big.Int).SetBytes(pubKeyBytes[keyLen:])

	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("invalid points on the curve for this public key")
	}

	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

func PublicKeyHashRipeMD160(pubKey *ecdsa.PublicKey) []byte {
	pubKeyBytes, err := PublicKeyToBytes(pubKey)
	if err != nil {
		return nil
	}
	pubKeyHash := sha256.Sum256(pubKeyBytes)
	ripeMDHasher := ripemd160.New()
	_, _ = ripeMDHasher.Write(pubKeyHash[:])
	return pubKeyHash[:]
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

	// Encrypt data
	encryptedData, err := encrypt(data, ENCRYPTION_KEY)
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

	// Split the file into encrypted wallet blocks
	encryptedBlocks := strings.Split(strings.TrimSpace(string(data)), "\n\n")

	var wallets []*Wallet
	for _, block := range encryptedBlocks {
		// Decrypt the wallet block
		decryptedData, err := decrypt(block, ENCRYPTION_KEY)
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