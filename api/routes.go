package api

import (
	"fmt"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/Sudin-01/DaanVeer/blockchain"
	"github.com/Sudin-01/DaanVeer/communication"
	"github.com/Sudin-01/DaanVeer/wallet"
)

const PORT = "8080"

// P2P_PORT_ENV overrides the p2p listener port. The p2p listener and the HTTP
// API previously shared a port, which prevented running both reliably and made
// several nodes on one host impossible.
const P2P_PORT_ENV = "DAANVEER_P2P_PORT"

// p2pPort returns the port the p2p listener binds, defaulting to the API port
// plus one.
func p2pPort(apiPort string) string {
	if explicit := os.Getenv(P2P_PORT_ENV); explicit != "" {
		return explicit
	}
	n, err := strconv.Atoi(apiPort)
	if err != nil {
		return apiPort
	}
	return strconv.Itoa(n + 1)
}

func StartServer(wlt *wallet.Wallet, chain *blockchain.BlockChain, port string) {
	go communication.StartServer(p2pPort(port), chain, wlt)

	gin_mode := os.Getenv("GIN_MODE")
	if gin_mode == "" {
		gin_mode = "debug"
	}
	gin.SetMode(gin_mode)

	router := gin.Default()

	// middlewares
	router.Use(CORSMiddleware())

	// block endpoint
	router.GET("/block/last", GetLastBlockResponse(chain))
	router.GET("/block/last/:n", GetLastNBlocksResponse(chain))
	router.POST("/block/mine", PostMineBlock(chain, wlt))

	// general wallet endpoint
	router.GET("/wallet/info/:address", GetWalletInfoResponse(chain))
	// router.GET("/wallet/amount/:address", GetWalletAmountResponse(chain))

	// personal wallet endpoint
	router.GET("/my-wallet/address", GetMyWalletAddressResponse(wlt))
	router.GET("/my-wallet/info", GetMyWalletInfoResponse(wlt, chain))
	router.GET("/my-wallet/balance", GetMyWalletBalanceResponse(wlt, chain))
	// router.GET("/my-wallet/items", GetMyWalletInfoResponse(wlt, chain))

	// transaction endpoint
	router.GET("/transaction/last/:n", GetLastNTxsResponse(chain))
	router.GET("/transaction/pool", GetTxPool(chain))
	router.POST("/transaction/new", PostNewTransaction(wlt, chain))

	// token verification endpoint
	router.GET("/token/sign/:token", SignToken(wlt))
	router.POST("/token/verify", VerifyToken())

	// Previously bound the PORT constant rather than the argument, so the
	// port flag had no effect on the HTTP API.
	fmt.Println("HTTP API listening on port", port)
	if err := router.Run(":" + port); err != nil {
		fmt.Println("HTTP server stopped:", err)
	}
}