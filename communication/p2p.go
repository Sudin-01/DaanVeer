
//To test the port run the following cmd commnand:
// netstat -ano | findstr LISTENING
// These are the current known nodes
// "192.168.1.75:8080" : Roshan (Home WiFi) or "172.16.1.31:8080" (College WiFi)
//"192.168.1.83:8080  : Sudin (Home WiFi) or "172.16.1.73:8080" (College)
package communication

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/Sudin-01/DaanVeer/blockchain"
	"github.com/Sudin-01/DaanVeer/wallet"
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
		// A write failure to one peer must not take this node down.
		fmt.Printf("failed to send to %s: %v\n", addr, err)
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

// frameBody strips the command header from a peer frame, reporting false when
// the frame is too short to contain one. Slicing request[commandLength:]
// unguarded panics on a short frame, which any peer can send.
func frameBody(request []byte) ([]byte, bool) {
	if len(request) < commandLength {
		return nil, false
	}
	return request[commandLength:], true
}

func HandleAddress(request []byte, chain *blockchain.BlockChain) {
	var buff bytes.Buffer
	var payload Address

	body, ok := frameBody(request)
	if !ok {
		fmt.Println("discarding short peer message")
		return
	}
	buff.Write(body)
	dec := gob.NewDecoder(&buff)
	err := dec.Decode(&payload)

	if err != nil {
		fmt.Println("discarding malformed peer message:", err)
		return
	}

	KnownNodes = append(KnownNodes, payload.AddrList...)

	for _, node := range KnownNodes {
		SendGetBlocks(node, chain)
	}
}

// HandleBlock integrates a block received from a peer.
//
// A block that does not extend the local tip is now resolved by fork choice.
// Previously this called os.Exit(69), so any fork -- or simply an out-of-order
// delivery -- terminated the node. Recovery therefore required a restart,
// which is the most likely explanation for the 40-50 s "block propagation
// delay" reported in the minor project: what was measured was restart-driven
// resynchronisation, not propagation.
func HandleBlock(request []byte, bChain *blockchain.BlockChain) {
	var buff bytes.Buffer
	var payload Block

	body, ok := frameBody(request)
	if !ok {
		fmt.Println("discarding short peer message")
		return
	}
	buff.Write(body)
	if err := gob.NewDecoder(&buff).Decode(&payload); err != nil {
		fmt.Println("discarding malformed block message:", err)
		return
	}

	status, err := bChain.AcceptBlock(&payload.Block)
	switch {
	case errors.Is(err, blockchain.ErrOrphanBlock):
		// Ancestors are missing: ask the sender for the chain leading here.
		fmt.Printf("block %x is an orphan; requesting ancestors from %s\n",
			payload.Block.BlockHash, payload.AddrFrom)
		SendGetBlocks(payload.AddrFrom, bChain)
		return
	case err != nil:
		fmt.Printf("rejected block %x from %s: %v\n",
			payload.Block.BlockHash, payload.AddrFrom, err)
		return
	}

	fmt.Printf("block %x accepted (%s), height now %d\n",
		payload.Block.BlockHash, status, bChain.GetHeight())

	mutex.Lock()
	defer mutex.Unlock()
	if len(blocksInTransit) > 0 {
		blockHash := blocksInTransit[0]
		sendGetData(payload.AddrFrom, BLOCK_TYPE, blockHash)
		blocksInTransit = blocksInTransit[1:]
	}
}


func HandleGetBlocks(request []byte, chain *blockchain.BlockChain) {
	var buff bytes.Buffer
	var payload GetBlocks

	body, ok := frameBody(request)
	if !ok {
		fmt.Println("discarding short peer message")
		return
	}
	buff.Write(body)
	err := gob.NewDecoder(&buff).Decode(&payload)

	if err != nil {
		fmt.Println("discarding malformed peer message:", err)
		return
	}

	blocks := chain.GetBlockHashes(payload.Data)
	fmt.Println(string(buff.Bytes()))
	sendInv(payload.AddrFrom, BLOCK_TYPE, blocks)
}

func HandleGetData(request []byte, chain *blockchain.BlockChain) {
	var buff bytes.Buffer
	var payload GetData

	body, ok := frameBody(request)
	if !ok {
		fmt.Println("discarding short peer message")
		return
	}
	buff.Write(body)
	err := gob.NewDecoder(&buff).Decode(&payload)

	if err != nil {
		fmt.Println("discarding malformed peer message:", err)
		return
	}

	if payload.Type == BLOCK_TYPE {
		block, err := chain.BlockByHash(payload.Data)
		if err != nil {
			fmt.Printf("peer %s requested unknown block %x\n", payload.AddrFrom, payload.Data)
			return
		}

		SendBlock(payload.AddrFrom, block)
	}

	if payload.Type == TX_TYPE {
		tx, found := chain.Mempool.Get(payload.Data)
		if !found {
			fmt.Printf("peer %s requested unknown transaction %x\n", payload.AddrFrom, payload.Data)
			return
		}
		SendTx(payload.AddrFrom, tx)
	}
}

func HandleVersion(request []byte, chain *blockchain.BlockChain) {
	var buff bytes.Buffer
	var payload Version

	body, ok := frameBody(request)
	if !ok {
		fmt.Println("discarding short peer message")
		return
	}
	buff.Write(body)
	err := gob.NewDecoder(&buff).Decode(&payload)

	if err != nil {
		fmt.Println("discarding malformed peer message:", err)
		return
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

	body, ok := frameBody(request)
	if !ok {
		fmt.Println("discarding short peer message")
		return
	}
	buff.Write(body)
	err := gob.NewDecoder(&buff).Decode(&payload)

	if err != nil {
		fmt.Println("discarding malformed peer message:", err)
		return
	}

	tx, err := blockchain.DeserializeTxFromGOB(payload.Transaction)

	if err != nil {
		return
	}

	// Admit into the chain's mempool, which verifies the signature and rejects
	// duplicates. A peer previously wrote straight into a package-level map
	// with no verification at all, so any peer could inject arbitrary
	// transactions into this node's pending set.
	if err := chain.Mempool.Add(*tx); err != nil {
		fmt.Printf("rejected transaction from %s: %v\n", payload.AddrFrom, err)
		return
	}
	txHash := tx.TxID
	fmt.Printf("Transaction %x admitted to mempool (%d pending)\n", txHash, chain.Mempool.Len())

	if isBootstrapNode() {
		for _, node := range KnownNodes {
			if node != nodeAddress && node != payload.AddrFrom {
				sendInv(node, TX_TYPE, [][]byte{txHash})
			}
		}
	}
}

func HandleInv(request []byte, chain *blockchain.BlockChain) {
	var payload Inv

	body, ok := frameBody(request)
	if !ok {
		fmt.Println("discarding short peer message")
		return
	}
	buff := bytes.NewBuffer(body)
	dec := gob.NewDecoder(buff)
	err := dec.Decode(&payload)

	if err != nil {
		fmt.Println("discarding malformed peer message:", err)
		return
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
		if len(payload.Data) == 0 {
			return
		}
		txID := payload.Data[0]
		// Request the transaction only if this node does not already hold it.
		if _, found := chain.Mempool.Get(txID); !found {
			sendGetData(payload.AddrFrom, TX_TYPE, txID)
		}
	}

}

func HandleConnection(conn net.Conn, chain *blockchain.BlockChain, wlt *wallet.Wallet) {

	req, err := ioutil.ReadAll(conn)

	defer conn.Close()
	if err != nil {
		fmt.Println("failed to read from peer:", err)
		return
	}
	// A short frame cannot carry a command; reject rather than slicing past
	// the end of the buffer.
	if len(req) < commandLength {
		fmt.Printf("discarding short message from peer (%d bytes)\n", len(req))
		return
	}

	command := BytesToCommand(req[:12])
	fmt.Println(command)
	switch command {
	default:
		fmt.Println("Unknown command")
		return

	case "inv":
		fmt.Println("Receiving inventory")
		HandleInv(req, chain)

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

	if err := gob.NewEncoder(&buff).Encode(data); err != nil {
		// Encoding our own outbound message should never fail; if it does the
		// caller sends nothing rather than the node dying.
		fmt.Println("failed to encode outbound message:", err)
		return nil
	}

	return buff.Bytes()
}

// PEERS_PATH_ENV overrides the location of the peer list.
const PEERS_PATH_ENV = "DAANVEER_PEERS"

// DefaultPeersPath is the peer list location used when PEERS_PATH_ENV is unset.
var DefaultPeersPath = filepath.Join("config", "knownNodes.json")

type peersFile struct {
	Nodes []string `json:"nodes"`
}

// readKnownNodesFromJSON loads the peer list.
//
// A missing or malformed peer list is not fatal: a node with no peers is a
// valid single-node network, and it must still serve its API. This previously
// called log.Fatal via ShowError, so running the binary from any directory
// other than the repository root killed the process at startup. The unchecked
// map type assertions also panicked on malformed input.
func readKnownNodesFromJSON() {
	path := os.Getenv(PEERS_PATH_ENV)
	if path == "" {
		path = DefaultPeersPath
	}

	KnownNodes = []string{}

	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("no peer list at %s (%v); starting as a single-node network\n", path, err)
		return
	}

	var payload peersFile
	if err := json.Unmarshal(raw, &payload); err != nil {
		fmt.Printf("peer list %s is malformed (%v); starting as a single-node network\n", path, err)
		return
	}

	for _, node := range payload.Nodes {
		if node != "" {
			KnownNodes = append(KnownNodes, node)
		}
	}
	fmt.Printf("loaded %d known peer(s) from %s\n", len(KnownNodes), path)
}

// isBootstrapNode reports whether this node is first in the peer list.
//
// The peer list's first entry acts as an implicit coordinator for transaction
// relay. That is a fragile arrangement -- membership depends on file ordering
// -- and is slated for replacement by the proposer schedule in C1.
func isBootstrapNode() bool {
	return len(KnownNodes) > 0 && nodeAddress == KnownNodes[0]
}

func StartServer(nodeId string, chain *blockchain.BlockChain, wlt *wallet.Wallet) {
	nodeAddress = fmt.Sprintf("%s:%s", blockchain.GetNodeAddress(), nodeId)
	fmt.Println("p2p node address: ", nodeAddress)

	readKnownNodesFromJSON()

	ln, err := net.Listen(protocol, nodeAddress)
	if err != nil {
		fmt.Printf("p2p listener failed on %s: %v; this node will not participate in the network\n", nodeAddress, err)
		return
	}
	defer ln.Close()

	if !isBootstrapNode() {
		for _, node := range KnownNodes {
			if node != nodeAddress {
				SendVersion(node, chain)
			}
		}
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			// A failed accept must not take the node down.
			fmt.Println("p2p accept error:", err)
			continue
		}
		go HandleConnection(conn, chain, wlt)
	}
}