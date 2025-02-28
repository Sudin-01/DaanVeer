package api

import (
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/Roshan310/DaanVeer/blockchain"
	"github.com/Roshan310/DaanVeer/communication"
	"github.com/Roshan310/DaanVeer/wallet"
)

const PORT = "8080"

func StartServer(wlt *wallet.Wallet, chain *blockchain.BlockChain, port string) {
	// gin.SetMode(gin.ReleaseMode)

	go communication.StartServer(port, chain, wlt)

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
	router.GET("/transaction/pool", GetTxPool)
	router.POST("/transaction/new", PostNewTransaction(wlt, chain))

	// token verification endpoint
	router.GET("/token/sign/:token", SignToken(wlt))
	router.POST("/token/verify", VerifyToken())

	fmt.Println("GIN server started at port: ", PORT)
	router.Run(":" + PORT)
}