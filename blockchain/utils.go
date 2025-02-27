package blockchain

import (
	// "fmt"
	"errors"
	"log"
	"net"
	// "strings"
)

func ShowError(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func getForwardSlashPosition(value string) int {
	for i, c := range value {
		if c == '/' {
			return i
		}
	}

	return -1
}


func GetNodeAddress() string {

	addresses, err := net.InterfaceAddrs()
	if err != nil {
		ShowError(err)
	}

	for _, addr := range addresses {
		addr_string := addr.String()
		position := getForwardSlashPosition(addr_string)

		//this is for the college wifi network
		// if strings.HasPrefix(addr_string, "172.16.1.31") {
		// 	fmt.Println("Found address:", addr_string[:position])
		// 	return addr_string[:position]

		// } 
		//this is for my home wifi network! Comment the following code while in college!!!
		//Sudin has to slightly modify this
		if addr_string[:3] == "192" {
			return addr_string[:position]
		}
	}
	err = errors.New("address not found")
	log.Panic(err)
	return ""
}