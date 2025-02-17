package blockchain

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	// "math/big"

	"github.com/Roshan310/DaanVeer/wallet"
)
type Validator struct {
	PublicKey *ecdsa.PublicKey
	Address   string
	authorized bool
}

// Validators map now stores validator address and public key
var Validators = map[string]Validator{}


// var Validators = map[string]bool{}

func (blk *Block) VerifyProof() bool {
	return true
	// fmt.Println("About to verify the proof of block")
	// fmt.Println("    ")
	// validatorAddr := string(blk.ValidatorAddress)
	// fmt.Println("Validator Address while verifying proof: ", validatorAddr)

	// // Make sure that the validator is in the list of authorized validator
	// validator, exists := Validators[validatorAddr]
	// if !exists {
	// 	fmt.Println("Block rejected: Validator is not authorized.")
	// 	return false
	// }

	// // Decode the block's signature
	// signatureBytes, err := hex.DecodeString(blk.Signature)
	// if err != nil {
	// 	fmt.Println("Invalid block signature:", err)
	// 	return false
	// }
	// // }
	// // Extract r and s values from the signature
	// r := new(big.Int).SetBytes(signatureBytes[:len(signatureBytes)/2])
	// s := new(big.Int).SetBytes(signatureBytes[len(signatureBytes)/2:])

	// blockHash := blk.Hash()
	// hashBytes := sha256.Sum256(blockHash)
	// // Verify the signature using the validator's public key
	// if ecdsa.Verify(validator.PublicKey, hashBytes[:], r, s) {
	// 	fmt.Println("Block verified successfully.")
	// 	return true
	// } else {
	// 	fmt.Println("Block signature verification failed.")
	// 	return false
	// }
}

// ProofOfAuthority signs the block using an authorized validator's private key
func ProofOfAuthority(blk *Block, validatorWallet *wallet.Wallet) error {
	validatorAddr := []byte(validatorWallet.Address)

	// Ensure validator is authorized
	_, exists := Validators[string(validatorAddr)]
	if !exists{
		return errors.New("validator is not authorized")
	}
	fmt.Println("Validator is authorized.")
	blockHash := blk.Hash()
	hashBytes := sha256.Sum256(blockHash)

	// Sign the block using the validator's private key
	r, s, err := ecdsa.Sign(rand.Reader, validatorWallet.PrivateKey, hashBytes[:])
	if err != nil {
		return err
	}

	// Encode the signature
	signature := append(r.Bytes(), s.Bytes()...)
	blk.Signature = hex.EncodeToString(signature)

	
	// Store the validator's address in the block
	blk.ValidatorAddress = validatorAddr
	fmt.Println("Validator Address: ", string(blk.ValidatorAddress))
	fmt.Println("Validator Address expected: ", validatorWallet.Address)
	fmt.Println("Block signed sucess Signature: ", blk.Signature)

	fmt.Println("Block signed by: ", validatorWallet.Address)
	return nil
}
