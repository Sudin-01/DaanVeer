
//To test the port run the following cmd commnand:
// netstat -ano | findstr LISTENING
// These are the current known nodes
// "192.168.1.75:139" : Roshan or "172.16.6.90:139"
//"192.168.1.83:139"  : Sudin
package communication

import (
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"sync"

	"github.com/Roshan310/DaanVeer/blockchain"
	"github.com/Roshan310/DaanVeer/wallet"
)



var mutex sync.Mutex

const (
	UNNAMED              = 0x0    // not a full node
	NODE_NETWORK         = 0x01   // full node
	NODE_NETWORK_LIMITED = 0x0400 
	commandLength        = 12     // command will have 12 bytes
	protocol             = "tcp"
)

var (
	KnownNodes  = []string{} 
	nodeAddress string       

	MemoryPool      = make(map[string]blockchain.Transactions)
	blocksInTransit [][]byte
)

const (
	BLOCK_TYPE   = 1
	TX_TYPE      = 2
	VERSION_TYPE = 3
	INV_TYPE     = 4
)

type MESSAGE_TYPE int

type Block struct {
	AddrFrom string
	Block    blockchain.Block
}

type Version struct {
	Timestamp   uint64
	AddressFrom string
	Height      uint64
}

type Address struct {
	AddrList []string
}

type GetData struct {
	AddrFrom string
	Type     MESSAGE_TYPE
	Data     []byte 
}

type GetBlocks struct {
	AddrFrom string
	Data     []byte
	Height   uint64
}

type Tx struct {
	AddrFrom    string
	Transaction []byte
}

type Inv struct {
	AddrFrom string
	Type     MESSAGE_TYPE 
	Data     [][]byte    
}

func CommandToBytes(cmd string) []byte {
	var bytes [commandLength]byte

	for i, c := range cmd {
		bytes[i] = byte(c)
	}

	return bytes[:]
}

func BytesToCommand(bytes []byte) string {
	var cmd []byte

	for _, b := range bytes {
		if b != 0x0 {
			cmd = append(cmd, b)
		}
	}

	return fmt.Sprintf("%s", string(cmd))
}

func sendData(addr string, data []byte) {
	conn, err := net.Dial(protocol, addr)

	if err != nil {
		fmt.Printf("Node %s is not available\n", addr)
		var updatedNodes []string
		// if the address is not available, remove that node
		for _, node := range KnownNodes {
			if node != addr {
				updatedNodes = append(updatedNodes, node)
			}
		}

		KnownNodes = updatedNodes

		return
	}

	defer conn.Close()

	// send the data to the connection
	_, err = io.Copy(conn, bytes.NewReader(data))

	if err != nil {
		log.Panic(err)
	}
}

func SendGetBlocks(addr string, chain *blockchain.BlockChain) {
	// var lastHash []byte
	lastHash := chain.LastHash
	var blocks = GetBlocks{
		AddrFrom: nodeAddress,
		Data:     lastHash,
		Height:   chain.GetHeight(),
	}
	info := append(CommandToBytes("getblocks"), GobEncode(blocks)...)

	sendData(addr, info)
}

func SendBlock(addr string, block *blockchain.Block) {
	var blocks = Block{
		AddrFrom: nodeAddress,
		Block:    *block,
	}
	info := append(CommandToBytes("block"), GobEncode(blocks)...)

	sendData(addr, info)
}

func SendAddress(addr string, block *blockchain.Block) {
	address := Address{AddrList: KnownNodes}

	info := append(CommandToBytes("address"), GobEncode(address)...)

	sendData(addr, info)
}

func sendGetData(addr string, kind MESSAGE_TYPE, id []byte) {
	data := GobEncode(GetData{
		AddrFrom: nodeAddress,
		Type:     kind,
		Data:     id,
	})

	data = append(CommandToBytes("getdata"), data...)
	sendData(addr, data)
}

func SendTx(addr string, tx blockchain.Transactions) {
	serializedData, err := tx.SerializeTxToGOB()

	if err != nil {
		fmt.Printf("Transaction serialization error: %s\n", err)
		return
	}
	data := GobEncode(Tx{
		AddrFrom:    nodeAddress,
		Transaction: serializedData,
	})

	data = append(CommandToBytes("tx"), data...)
	sendData(addr, data)
}

func SendVersion(addr string, bChain *blockchain.BlockChain) {
	height := bChain.GetHeight()
	data := GobEncode(Version{
		AddressFrom: nodeAddress,
		Height:      height,
	})

	data = append(CommandToBytes("getversion"), data...)

	sendData(addr, data)
}

func sendInv(addr string, kind MESSAGE_TYPE, inventories [][]byte) {
	inv := Inv{
		AddrFrom: nodeAddress,
		Type:     kind,
		Data:     inventories,
	}
	data := GobEncode(inv)
	var payload Inv
	gob.NewDecoder(bytes.NewBuffer(data)).Decode(&payload)
	fmt.Printf("%x\n", data)
	data = append(CommandToBytes("inv"), data...)
	sendData(addr, data)
}

func HandleAddress(request []byte, chain *blockchain.BlockChain) {
	var buff bytes.Buffer
	var payload Address

	buff.Write(request[commandLength:])
	dec := gob.NewDecoder(&buff)
	err := dec.Decode(&payload)

	if err != nil {
		log.Panic(err)
	}

	KnownNodes = append(KnownNodes, payload.AddrList...)

	for _, node := range KnownNodes {
		SendGetBlocks(node, chain)
	}
}

func HandleBlock(request []byte, bChain *blockchain.BlockChain) {
	var buff bytes.Buffer
	var payload Block

	buff.Write(request[commandLength:])
	err := gob.NewDecoder(&buff).Decode(&payload)

	if err != nil {
		log.Panic(err)
	}

	fmt.Printf("Received a block of hash: %x\n", payload.Block.BlockHash)

	blockHashes := bChain.GetBlockHashesFromHeight(payload.Block.Height - 1)

	if len(blockHashes) != 0 {
		lastHash := blockHashes[len(blockHashes)-1]

		if !bytes.Equal(payload.Block.PreviousHash, lastHash) {
			log.Printf("Chain of this node invalid at height: %d", payload.Block.Height-1)
			os.Exit(69)
			blocksInTransit = [][]byte{} // empty blocks in transit
		}
	}

	bChain.AddBlock(&payload.Block)

	if len(blocksInTransit) > 0 {
		blockHash := blocksInTransit[0]
		sendGetData(payload.AddrFrom, BLOCK_TYPE, blockHash)

		blocksInTransit = blocksInTransit[1:]
	}
}
