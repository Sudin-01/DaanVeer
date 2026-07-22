package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/Sudin-01/DaanVeer/api"
	"github.com/Sudin-01/DaanVeer/blockchain"
	"github.com/Sudin-01/DaanVeer/wallet"
)

func main() {
	var (
		initChain    = flag.Bool("init", false, "generate a wallet and a fresh chain configuration, then exit")
		addValidator = flag.Bool("add-validator", false, "add this node's wallet to an existing chain configuration, then exit")
		configPath   = flag.String("config", blockchain.CHAIN_CONFIG, "path to the chain configuration")
		walletPath   = flag.String("wallet", "my_wallet.txt", "path to the wallet file")
		dbPath       = flag.String("db", "", "path to the block database (default: $DAANVEER_DB or ./db)")
		port         = flag.String("port", api.PORT, "port to serve the HTTP API and p2p listener on")
	)
	flag.Parse()

	if *dbPath == "" {
		*dbPath = blockchain.DatabasePath()
	}

	switch {
	case *initChain:
		if err := bootstrap(*configPath, *walletPath); err != nil {
			log.Fatalf("init failed: %v", err)
		}
		return
	case *addValidator:
		if err := addValidatorToConfig(*configPath, *walletPath); err != nil {
			log.Fatalf("add-validator failed: %v", err)
		}
		return
	}

	if err := blockchain.LoadChainConfig(*configPath); err != nil {
		log.Fatalf("could not load chain configuration: %v\n\nRun `%s -init` to create one.", err, os.Args[0])
	}

	wlt, err := wallet.GenerateWallet(*walletPath)
	if err != nil {
		log.Fatalf("could not load wallet: %v", err)
	}

	chain := blockchain.InitBlockChainAt(*dbPath)
	defer chain.Close()

	if _, authorized := blockchain.LookupValidator(wlt.Address); authorized {
		fmt.Println("This node is an authorized validator.")
	} else {
		fmt.Println("This node is NOT a validator; it can submit transactions but not mine.")
	}

	fmt.Printf("node %s starting on port %s (db %s)\n", wlt.Address, *port, *dbPath)
	api.StartServer(wlt, chain, *port)
}

// bootstrap creates a wallet and a chain configuration in which that wallet is
// both the genesis recipient and the sole authorised validator.
func bootstrap(configPath, walletPath string) error {
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("%s already exists; delete it (and the database) to re-initialise", configPath)
	}

	wlt, err := wallet.GenerateWallet(walletPath)
	if err != nil {
		return err
	}

	// A load test exhausts a small genesis grant long before it has gathered
	// enough samples, so the amount is configurable.
	amount := uint64(blockchain.DEFAULT_GENESIS_AMOUNT)
	if raw := os.Getenv("DAANVEER_GENESIS_AMOUNT"); raw != "" {
		if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil && parsed > 0 {
			amount = parsed
		}
	}

	cfg, err := blockchain.NewSingleValidatorConfig(
		wlt,
		amount,
		blockchain.DEFAULT_GENESIS_TIMESTAMP,
	)
	if err != nil {
		return err
	}
	if err := blockchain.SaveChainConfig(configPath, cfg); err != nil {
		return err
	}

	fmt.Printf("\nWrote %s\n", configPath)
	fmt.Printf("  genesis address : %s\n", cfg.GenesisAddress)
	fmt.Printf("  genesis amount  : %d\n", cfg.GenesisAmount)
	fmt.Printf("  validators      : %d\n", len(cfg.Validators))
	fmt.Printf("\nWallet saved to %s.\n", walletPath)
	return nil
}

// addValidatorToConfig appends this node's wallet to an existing chain
// configuration, so a multi-validator network can be assembled from several
// independently generated wallets.
func addValidatorToConfig(configPath, walletPath string) error {
	if err := blockchain.LoadChainConfig(configPath); err != nil {
		return err
	}
	cfg, err := blockchain.ActiveConfig()
	if err != nil {
		return err
	}

	wlt, err := wallet.GenerateWallet(walletPath)
	if err != nil {
		return err
	}
	if err := cfg.AddValidator(wlt.PublicKey); err != nil {
		return err
	}
	if err := blockchain.SaveChainConfig(configPath, cfg); err != nil {
		return err
	}

	fmt.Printf("added validator %s to %s (%d total)\n", wlt.Address, configPath, len(cfg.Validators))
	return nil
}
