package main

import (
	// "encoding/json"
	// "fmt"
	// "log"
	"fmt"

	"github.com/Roshan310/DaanVeer/api"
	"github.com/Roshan310/DaanVeer/blockchain"
	"github.com/Roshan310/DaanVeer/wallet"
)
// Test for Wallet
// func main() {
// 	// Specify the filename to save the wallet.
// 	walletFile := "my_wallet.txt"

// 	// Generate a new wallet and save it to the file.
// 	myWallet, err := wallet.GenerateWallet(walletFile)
// 	if err != nil {
// 		log.Fatalf("Failed to generate wallet: %v\n", err)
// 	}

// 	// Display the newly generated wallet information.
// 	fmt.Println("New Wallet:")
// 	fmt.Println("Wallet Address:", myWallet.Address)
// 	fmt.Printf("Public Key: %x\n", wallet.PublicKeyToBytes(myWallet.PublicKey))
// 	fmt.Printf("Private Key: %x\n", myWallet.PrivateKey.D.Bytes())

// 	// Load all wallets from the file.
// 	wallets, err := wallet.LoadAllWallets(walletFile)
// 	if err != nil {
// 		log.Fatalf("Failed to load wallets: %v\n", err)
// 	}

// 	// Display all loaded wallets.
// 	fmt.Println("\nLoaded Wallets:")
// 	for i, w := range wallets {
// 		fmt.Printf("Wallet %d:\n", i+1)
// 		fmt.Println("Wallet Address:", w.Address)
// 		fmt.Printf("Public Key: %x\n", wallet.PublicKeyToBytes(w.PublicKey))
// 		fmt.Printf("Private Key: %x\n\n", w.PrivateKey.D.Bytes())
// 	}
// }

func main() {
	chain := blockchain.InitBlockChain()
	wlt, err := wallet.GenerateWallet("my_wallet.txt")
	blockchain.ShowError(err)
	// chain.PrintChain()
	fmt.Println("Starting server at port: ", api.PORT)
	api.StartServer(wlt, chain, api.PORT)
}

