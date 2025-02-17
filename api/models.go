package api

import (
	"encoding/hex"
	"fmt"

	"github.com/Roshan310/DaanVeer/blockchain"
)

// TODO: add binding validation
type NewTxFormInput struct {
	Destination string `json:"destination" binding:"required"`
	Amount      uint64 `json:"amount" binding:"required"`
}

type TokenSignModel struct {
	Token string `json:"token"`
}

type TokenVerifyModel struct {
	OriginalToken string `json:"token"`
	SignedToken   string `json:"signed_token"`
	PublicKey     string `json:"public_key"`
}

type TransactionsModel struct {
	TxID       string `json:"txID"`
	Signature  string `json:"signature"`
	SenderHash string `json:"senderHash"`
	RecipientHash string `json:"recipientHash"`
	Value     uint64 `json:"value"`
	Timestamp  uint64 `json:"timestamp"`
}

func ModelToTx(txModel TransactionsModel) (*blockchain.Transactions, error) {
	var tx blockchain.Transactions
	var err error
	
	tx.TxID, err = hex.DecodeString(txModel.TxID)
	if err != nil {
		return nil, err
	}
	tx.Signature, err = hex.DecodeString(txModel.Signature)
	if err != nil {
		return nil, err
	}

	tx.RecipientHash, err = hex.DecodeString(txModel.RecipientHash)
	fmt.Println("Recipient hash", txModel.RecipientHash)
	if err != nil {
		return nil, err
	}

	tx.SenderHash, err = hex.DecodeString(txModel.SenderHash)
	if err != nil {
		return nil, err
	}

	tx.Value = txModel.Value
	tx.Timestamp = txModel.Timestamp

	return &tx, nil
}