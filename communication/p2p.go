
//To test the port run the following cmd commnand:
// netstat -ano | findstr LISTENING
// These are the current known nodes
// "192.168.1.75:8080" : Roshan (Home WiFi) or "172.16.1.31:8080" (College WiFi)
//"192.168.1.83:8080  : Sudin (Home WiFi) or "172.16.1.73:8080" (College)
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
	fmt.Printf("Sending an inventory: %x (Inside the sendInv function)\n", data)
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
	fmt.Println("Block 0 hash: ", bChain.GetBlockHashesFromHeight(1))
	fmt.Println("Payload blockHashes: ", blockHashes)
	fmt.Println("Payload block height: ", payload.Block.Height)

	if len(blockHashes) != 0 {
		lastHash := blockHashes[len(blockHashes)-1]
		fmt.Println("Payload previous hash: \n", payload.Block.PreviousHash)
		fmt.Println("Last hash: ", lastHash)

		if !bytes.Equal(payload.Block.PreviousHash, lastHash) {
			log.Printf("Chain of this node invalid at height: %d", payload.Block.Height-1)
			os.Exit(69)
			blocksInTransit = [][]byte{} // empty blocks in transit
		}
	}

	bChain.AddBlock(&payload.Block)

	mutex.Lock()
	defer mutex.Unlock()
	fmt.Printf("blocks in transit before check: %v (length: %d)\n", blocksInTransit, len(blocksInTransit))
	if len(blocksInTransit) > 0 {
		fmt.Println("Processing Blocks in Transit: ", blocksInTransit)
		blockHash := blocksInTransit[0]
		sendGetData(payload.AddrFrom, BLOCK_TYPE, blockHash)
		blocksInTransit = blocksInTransit[1:]
	}
}


func HandleGetBlocks(request []byte, chain *blockchain.BlockChain) {
	var buff bytes.Buffer
	var payload GetBlocks

	buff.Write(request[commandLength:])
	err := gob.NewDecoder(&buff).Decode(&payload)

	if err != nil {
		log.Panic(err)
	}

	blocks := chain.GetBlockHashes(payload.Data)
	fmt.Println(string(buff.Bytes()))
	sendInv(payload.AddrFrom, BLOCK_TYPE, blocks)
}

func HandleGetData(request []byte, chain *blockchain.BlockChain) {
	var buff bytes.Buffer
	var payload GetData

	buff.Write(request[commandLength:])
	err := gob.NewDecoder(&buff).Decode(&payload)

	if err != nil {
		log.Panic(err)
	}

	if payload.Type == BLOCK_TYPE {
		block, err := chain.GetBlock([]byte(payload.Data))

		if err != nil {
			log.Panic(err)
			return
		}

		SendBlock(payload.AddrFrom, block)
	}

	if payload.Type == TX_TYPE {
		txId := hex.EncodeToString(payload.Data)
		tx := MemoryPool[txId]

		SendTx(payload.AddrFrom, tx)
	}
}

func HandleVersion(request []byte, chain *blockchain.BlockChain) {
	var buff bytes.Buffer
	var payload Version

	buff.Write(request[commandLength:])
	err := gob.NewDecoder(&buff).Decode(&payload)

	if err != nil {
		log.Panic(err)
	}

	// height on the current chain
	bestHeight := chain.GetHeight()

	// height of received chain
	otherheight := payload.Height

	// if the best height is less than the height on the network then request get blocks
	if bestHeight < otherheight {
		fmt.Println("Sending Get block request")
		SendGetBlocks(payload.AddressFrom, chain)
	} else if bestHeight > otherheight {
		fmt.Println("Sending version of the current block")
		SendVersion(payload.AddressFrom, chain)
	} else {
		fmt.Printf("Same block height: %d", chain.GetHeight())
	}

	if !contains(KnownNodes, payload.AddressFrom) {
		KnownNodes = append(KnownNodes, payload.AddressFrom)
	}
}

func HandleTx(request []byte, chain *blockchain.BlockChain, wlt *wallet.Wallet) {
	var buff bytes.Buffer
	var payload Tx

	buff.Write(request[commandLength:])
	err := gob.NewDecoder(&buff).Decode(&payload)

	if err != nil {
		log.Panic(err)
	}

	tx, err := blockchain.DeserializeTxFromGOB(payload.Transaction)

	if err != nil {
		return
	}

	txHash:= tx.Hash()
	MemoryPool[hex.EncodeToString(txHash)] = *tx
	fmt.Println("Transaction received and stored in MemoryPool:")
    for id, _ := range MemoryPool {
        fmt.Println("TxID:", id)
    }

	if nodeAddress == KnownNodes[0] {
		for _, node := range KnownNodes {
			if node != nodeAddress && node != payload.AddrFrom {
				sendInv(node, TX_TYPE, [][]byte{txHash})
			}
		}
	} else {
		if len(MemoryPool) >= 2 {
			// txPool := []blockchain.Tx{}

			// for _, tx := range MemoryPool {
			// 	txPool = append(txPool, tx)
			// }

			// block := blockchain.CreateBlock()
			// block.AddTransactionsToBlock(txPool)
			// err := block.MineBlock(chain, wlt)
			// utility.ErrThenLogPanic(err)
			// chain.AddBlock(block)

			// for _, nodes := range KnownNodes {
			// 	SendBlock(nodes, block)
			// }

			// // empty memory pool
			// MemoryPool = map[string]blockchain.Tx{}
		}
	}
}

func HandleInv(request []byte) {
	buff := bytes.NewBuffer(request[commandLength:])
	var payload Inv

	buff.Write(request[commandLength:])
	dec := gob.NewDecoder(buff)
	err := dec.Decode(&payload)

	if err != nil {
		log.Panic(err)
	}

	typeStringMap := map[MESSAGE_TYPE]string{
		BLOCK_TYPE:   "BLOCK",
		TX_TYPE:      "TX",
		VERSION_TYPE: "VERSION",
		INV_TYPE:     "INV",
	}
	fmt.Printf("%x\n", buff.Bytes())
	log.Printf("Received %d inventories of type %s", len(payload.Data), typeStringMap[payload.Type])

	for _, inv := range payload.Data {
		fmt.Printf("%x\n", inv)
	}

	if payload.Type == BLOCK_TYPE {
		mutex.Lock()
		blocksInTransit = payload.Data

		if len(payload.Data) != 0 {
			blockHash := payload.Data[0]
			mutex.Unlock()
			sendGetData(payload.AddrFrom, BLOCK_TYPE, blockHash)
			mutex.Lock()
			newInTransit := [][]byte{}
			for _, b := range blocksInTransit {
				if bytes.Compare(b, blockHash) != 0 {
					newInTransit = append(newInTransit, b)
				}
			}
			blocksInTransit = newInTransit
		}
		mutex.Unlock()
	}

	if payload.Type == TX_TYPE {
		txID := payload.Data[0]
		tx := MemoryPool[hex.EncodeToString(txID)]
		txByte, _ := tx.SerializeTxToGOB()
		if txByte == nil {
			sendGetData(payload.AddrFrom, TX_TYPE, txID)
		}
	}

}

func HandleConnection(conn net.Conn, chain *blockchain.BlockChain, wlt *wallet.Wallet) {

	req, err := ioutil.ReadAll(conn)

	defer conn.Close()
	if err != nil {
		log.Panic(err)
	}

	command := BytesToCommand(req[:12])
	fmt.Println(command)
	switch command {
	default:
		fmt.Println("Unknown command")
		return

	case "inv":
		fmt.Println("Receiving inventory")
		HandleInv(req)

	case "getversion":
		fmt.Println("Sending version")
		HandleVersion(req, chain)

	case "getdata":
		fmt.Println("Sending data of a type")
		HandleGetData(req, chain)

	case "tx":
		fmt.Println("Receiving a Transaction")
		HandleTx(req, chain, wlt)

	case "address":
		fmt.Println("Sending known addresses")
		HandleAddress(req, chain)

	case "block":
		fmt.Println("Receiving a block")
		HandleBlock(req, chain)

	case "getblocks":
		HandleGetBlocks(req, chain)

	}
}

func contains(array []string, val string) bool {
	for _, elem := range array {
		if elem == val {
			return true
		}
	}

	return false
}

func GobEncode(data interface{}) []byte {
	var buff bytes.Buffer

	err := gob.NewEncoder(&buff).Encode(data)

	if err != nil {
		log.Panic(err)
	}

	return buff.Bytes()
}

func readKnownNodesFromJSON() {
	knownNodesByte, err := os.ReadFile("./communication/knownNodes.json")
	blockchain.ShowError(err)

	var payload map[string]interface{}

	KnownNodes = []string{}

	err = json.Unmarshal(knownNodesByte, &payload)
	blockchain.ShowError(err)

	knownNodesList := payload["nodes"].([]interface{})

	for _, node := range knownNodesList {
		KnownNodes = append(KnownNodes, node.(string))
	}
}

func StartServer(nodeId string, chain *blockchain.BlockChain, wlt *wallet.Wallet) {
	fmt.Println("p2p server started at port: ", nodeId)
	nodeAddress = fmt.Sprintf("%s:%s", blockchain.GetNodeAddress(), nodeId)
	fmt.Println("Node address: ", nodeAddress)
	// minerAddress = minerAddress
	ln, err := net.Listen(protocol, nodeAddress)

	readKnownNodesFromJSON()

	if err != nil {
		log.Panic(err)
	}
	defer ln.Close()
	if nodeAddress != KnownNodes[0] {
		for _, node := range KnownNodes {
			if node != nodeAddress {
				SendVersion(node, chain)
			}
		}
	}

	// chain.PrintChain()
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Panic(err)
		}
		go HandleConnection(conn, chain, wlt)

	}
}