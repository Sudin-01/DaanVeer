package blockchain

import (
	"log"
)

func ShowError(err error) {
	if err != nil {
		log.Fatal(err)
	}
}