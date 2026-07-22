package api

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"

	"github.com/Sudin-01/DaanVeer/blockchain"
	"github.com/Sudin-01/DaanVeer/communication"
	"github.com/Sudin-01/DaanVeer/internal/hrtime"
	"github.com/Sudin-01/DaanVeer/wallet"
	"github.com/gin-gonic/gin"
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

func GetMyWalletBalanceResponse(wlt *wallet.Wallet, chain *blockchain.BlockChain) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		balance, err := chain.GetWalletBalance(string(wlt.Address))
		if err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: fmt.Sprintf("failed to get wallet balance: %v", err)})
			return
		}
		c.JSON(200, gin.H{"address": string(wlt.Address), "balance": balance})
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

// PostMineBlock mines the pending transactions into a new block.
//
// The transaction set comes from the node's own mempool, not from the request
// body. Previously the caller supplied the transactions to mine, which meant an
// unauthenticated client chose a block's contents; errors from block assembly,
// mining and commit were all printed and then discarded, so the endpoint
// returned 200 with an empty block when any of them failed.
func PostMineBlock(chain *blockchain.BlockChain, wlt *wallet.Wallet) gin.HandlerFunc {
	fn := func(c *gin.Context) {
		txPool := chain.Mempool.All()
		if len(txPool) == 0 {
			c.JSON(400, ErrorJSON{ErrorMsg: "no pending transactions to mine"})
			return
		}

		newBlock := blockchain.CreateBlock()
		if err := newBlock.AddTxToBlock(txPool); err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: fmt.Sprintf("could not assemble block: %v", err)})
			return
		}
		if err := newBlock.MineBlock(chain, wlt); err != nil {
			c.JSON(403, ErrorJSON{ErrorMsg: fmt.Sprintf("could not mine block: %v", err)})
			return
		}

		// Gather attestations from peer validators until the quorum is met.
		// Without this the proposer would commit alone and the configured
		// quorum would be decorative.
		// Timed with the performance counter rather than time.Now: a consensus
		// round takes single-digit milliseconds, and on Windows the standard
		// clock advances only about once per millisecond, which would quantise
		// this figure to a handful of levels. See internal/hrtime.
		roundStart := hrtime.Now()
		if err := communication.RunConsensusRound(newBlock, communication.DefaultRoundTimeout); err != nil {
			c.JSON(409, ErrorJSON{ErrorMsg: fmt.Sprintf("consensus failed: %v", err)})
			return
		}
		roundDuration := hrtime.Since(roundStart)

		if err := chain.AddBlock(newBlock); err != nil {
			c.JSON(400, ErrorJSON{ErrorMsg: fmt.Sprintf("could not commit block: %v", err)})
			return
		}

		// AddBlock removes the mined transactions from the mempool, so only
		// the transactions actually included are cleared.
		for _, node := range communication.KnownNodes {
			communication.SendBlock(node, newBlock)
		}

		c.JSON(200, gin.H{
			"block":        newBlock,
			"attestations": newBlock.CountAttestations(),
			"quorum":       blockchain.QuorumSize(),
			"consensus_ms": float64(roundDuration.Nanoseconds()) / 1e6,
		})
	}
	return fn
}

// GetTxPool lists the node's pending transactions.
func GetTxPool(chain *blockchain.BlockChain) gin.HandlerFunc {
	return func(c *gin.Context) {
		txs := chain.Mempool.All()
		if txs == nil {
			txs = []blockchain.Transactions{}
		}
		c.JSON(200, txs)
	}
	// return fn
}
