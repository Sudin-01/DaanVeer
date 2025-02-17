package api

import (
	"crypto/rand"
	"math/big"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/Roshan310/DaanVeer/blockchain"
	"github.com/Roshan310/DaanVeer/communication"
	"github.com/Roshan310/DaanVeer/wallet"
)

type ErrorJSON struct {
	ErrorMsg string `json:"error"`
}

// GET Requests

func GetLastBlockResponse(chain *blockchain.BlockChain) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		lastBlock := chain.LastBlock()
		c.JSON(200, lastBlock)
	}
	return fn
}

func GetLastNBlocksResponse(chain *blockchain.BlockChain) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		n, err := strconv.Atoi(c.Param("n"))
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: "invalid height provided: can not be parsed as integer"})
			return
		}
		if n < 0 {
			c.JSON(400, ErrorJSON{ErrorMsg: "negative height provided: block height can't be negative"})
			return
		}
		lastNBlocks := chain.GetLastNBlocks(uint64(n))
		c.JSON(200, lastNBlocks)

	}
	return fn
}

func GetLastNTxsResponse(chain *blockchain.BlockChain) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		n, err := strconv.Atoi(c.Param("n"))
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: "invalid number provided: can not be parsed as integer"})
			return
		}
		if n < 0 {
			c.JSON(400, ErrorJSON{ErrorMsg: "negative number provided: tx count can only be positive"})
			return
		}
		lastNBlocks := chain.GetLastNTxs(uint64(n))
		c.JSON(200, lastNBlocks)
	}
	return fn
}


func GetWalletInfoResponse(chain *blockchain.BlockChain) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		walletAddress := c.Param("address")
		
		minedBlocks, err := chain.WalletMinedBlocks(walletAddress)
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: "bad address: could not derive public key hash from address"})
			return
		}
		walletInfo := map[string]interface{}{
			"mined_blocks": minedBlocks,
		}
		c.JSON(200, walletInfo)
	}
	return fn
}


func GetMyWalletInfoResponse(wlt *wallet.Wallet, chain *blockchain.BlockChain) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		minedBlocks, err := chain.WalletMinedBlocks(string(wlt.Address))
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: "bad address: could not derive public key hash from address"})
			return
		}
		walletInfo := map[string]interface{}{
			"mined_blocks": minedBlocks,
		}
		c.JSON(200, walletInfo)
	}
	return fn
}

func GetMyWalletAddressResponse(wlt *wallet.Wallet) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		walletAddress := string(wlt.Address)
		walletPubkeyHash, err := wallet.PubKeyFromAddress(string(wlt.Address))
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: "bad address: could not derive public key hash from address"})
			return
		}
		walletPublicKey, err := wallet.PublicKeyToBytes(wlt.PublicKey)
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: "bad address: could not derive public key hash from address"})
			return
		}
		walletPubkeyHashHex := hex.EncodeToString(walletPubkeyHash)
		walletPublicKeyHex := hex.EncodeToString(walletPublicKey)
		walletAddressInfo := map[string]interface{}{
			"address":         walletAddress,
			"public_key":      walletPublicKeyHex,
			"public_key_hash": walletPubkeyHashHex,
		}
		c.JSON(200, walletAddressInfo)
	}
	return fn
}

//these functions are for POST request handling

func PostNewTransaction(wlt *wallet.Wallet, chain *blockchain.BlockChain) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		newTxData := NewTxFormInput{}
		if err := c.BindJSON(&newTxData); err != nil {
			c.AbortWithError(400, err)
			return
		}

		newTx, err := blockchain.NewTransaction(wlt, newTxData.Destination, newTxData.Amount, chain)
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: fmt.Sprintf("%v", err)})
			return
		}
		// communication.MemoryPool[string(newTx.TxID)] = *newTx

		for _, nodeAddress := range communication.KnownNodes {
			communication.SendTx(nodeAddress, *newTx)
		}

		c.JSON(200, newTx)
	}
	return fn
}

func VerifyToken() gin.HandlerFunc {
	fn := func(c *gin.Context) {
		signedTokenData := TokenVerifyModel{}
		if err := c.BindJSON(&signedTokenData); err != nil {
			c.AbortWithError(400, err)
			return
		}

		// Compute the SHA-256 hash of the original token
		hashedOriginalToken := sha256.Sum256([]byte(signedTokenData.OriginalToken))

		// Decode the ECDSA signature (r, s values)
		sigBytes, err := hex.DecodeString(signedTokenData.SignedToken)
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: fmt.Sprintf("Invalid signature: %v", err)})
			return
		}

		// Extract r and s from signature
		r := new(big.Int).SetBytes(sigBytes[:len(sigBytes)/2])
		s := new(big.Int).SetBytes(sigBytes[len(sigBytes)/2:])

		// Decode the hex-encoded public key
		pubKeyBytes, err := hex.DecodeString(signedTokenData.PublicKey)
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: fmt.Sprintf("Invalid public key: %v", err)})
			return
		}

		// Convert bytes to ECDSA public key
		publicKey, err := wallet.BytesToPublicKey(pubKeyBytes)
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: fmt.Sprintf("Failed to parse public key: %v", err)})
			return
		}

		// Verify the signature
		if !ecdsa.Verify(publicKey, hashedOriginalToken[:], r, s) {
			c.JSON(400, ErrorJSON{ErrorMsg: "Signature verification failed"})
			return
		}

		// Respond with verification success
		c.JSON(200, gin.H{"verified": true})
	}
	return fn
}

func SignToken(wlt *wallet.Wallet) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		tokenData := c.Param("token")

		// Compute the SHA-256 hash of the token
		hashedToken := sha256.Sum256([]byte(tokenData))

		// Sign the hashed token with ECDSA private key
		r, s, err := ecdsa.Sign(rand.Reader, wlt.PrivateKey, hashedToken[:])
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: fmt.Sprintf("Signing failed: %v", err)})
			return
		}

		// Concatenate r and s values, then encode in hex
		signature := append(r.Bytes(), s.Bytes()...)
		signedTokenHex := hex.EncodeToString(signature)

		// Respond with signed token
		c.JSON(200, gin.H{"signed_token": signedTokenHex})
	}
	return fn
}

func PostMineBlock(chain *blockchain.BlockChain, wlt *wallet.Wallet) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		var txModelPool []TransactionsModel
		if err := c.BindJSON(&txModelPool); err != nil {
			c.AbortWithError(400, err)
			return
		}
		var txPool []blockchain.Transactions
		for _, txModel := range txModelPool {
			tx, err := ModelToTx(txModel)
			if err != nil {
				fmt.Println("Error while converting tx model to tx:", err)
				c.AbortWithError(400, err)
				return
			}
			txPool = append(txPool, *tx)
		}

		newBlock := blockchain.CreateBlock()
		fmt.Println("New block created:", newBlock)
		newBlock.AddTxToBlock(txPool)
		fmt.Println("Transactions added to block:", newBlock)
		err := newBlock.MineBlock(chain, wlt)
		if err != nil {
			fmt.Println("Error while mining block:", err)
		}
		fmt.Println("Block mined:", newBlock)
		err = chain.AddBlock(newBlock)
		if err != nil {
			fmt.Println("Error while adding block to chain:", err)
		}
		fmt.Println("Block added to chain:", newBlock)

		// TODO: we clear the memory pool here but edit in later commit to remove only selected transactions
		communication.MemoryPool = map[string]blockchain.Transactions{}

		c.JSON(200, newBlock)
	}
	return fn
}

func GetTxPool(c *gin.Context) {
	// fn := func(c *gin.Context) {
		var txsInPools []blockchain.Transactions
		for _, tx := range communication.MemoryPool {
			txsInPools = append(txsInPools, tx)
		}
		fmt.Println("txsInPools: ", txsInPools)
		c.JSON(200, txsInPools)
	// }
	// return fn
}