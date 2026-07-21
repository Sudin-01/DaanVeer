package communication

// Networked consensus round: propose -> attest -> assemble.
//
// Attestations previously existed only in-process, so a live network ran
// effectively at quorum 1: a proposer signed alone and broadcast a finished
// block. This adds the two-phase exchange that makes the quorum real.
//
//	proposer            validators
//	   |  --- proposal --->  |        validate, sign
//	   |  <--- attest -----  |
//	   |  (quorum reached)   |
//	   |  --- block ------>  |        accept as canonical
//
// A proposal carries a block that is already hash-fixed and proposer-signed, so
// attestations cannot alter its identity. The proposer commits only once the
// configured quorum is met, or gives up when the round times out.

import (
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Sudin-01/DaanVeer/blockchain"
	"github.com/Sudin-01/DaanVeer/wallet"
)

// DefaultRoundTimeout bounds how long a proposer waits for a quorum.
const DefaultRoundTimeout = 5 * time.Second

// Proposal carries a block seeking attestations.
type Proposal struct {
	AddrFrom string
	Block    blockchain.Block
}

// Attest carries one validator's signature for a proposed block.
type Attest struct {
	AddrFrom    string
	BlockHash   []byte
	Attestation blockchain.Attestation
}

// pendingRound is a proposal this node is currently collecting signatures for.
type pendingRound struct {
	block    *blockchain.Block
	required int
	reached  chan struct{}
	once     sync.Once
}

var (
	roundsMu sync.Mutex
	rounds   = map[string]*pendingRound{}
)

func registerRound(blk *blockchain.Block, required int) *pendingRound {
	round := &pendingRound{
		block:    blk,
		required: required,
		reached:  make(chan struct{}),
	}
	roundsMu.Lock()
	rounds[hex.EncodeToString(blk.BlockHash)] = round
	roundsMu.Unlock()
	return round
}

func releaseRound(blk *blockchain.Block) {
	roundsMu.Lock()
	delete(rounds, hex.EncodeToString(blk.BlockHash))
	roundsMu.Unlock()
}

func lookupRound(blockHash []byte) (*pendingRound, bool) {
	roundsMu.Lock()
	defer roundsMu.Unlock()
	round, ok := rounds[hex.EncodeToString(blockHash)]
	return round, ok
}

// SendProposal asks a peer to attest to a block.
func SendProposal(addr string, blk *blockchain.Block) {
	payload := GobEncode(Proposal{AddrFrom: nodeAddress, Block: *blk})
	sendData(addr, append(CommandToBytes("proposal"), payload...))
}

// SendAttest returns a signature for a proposed block to its proposer.
func SendAttest(addr string, blockHash []byte, attestation blockchain.Attestation) {
	payload := GobEncode(Attest{
		AddrFrom:    nodeAddress,
		BlockHash:   blockHash,
		Attestation: attestation,
	})
	sendData(addr, append(CommandToBytes("attest"), payload...))
}

// RunConsensusRound gathers attestations for a locally produced block until the
// quorum is met or the round times out.
//
// It returns nil when the block is ready to commit. The caller keeps ownership
// of the block; attestations are merged into it in place.
func RunConsensusRound(blk *blockchain.Block, timeout time.Duration) error {
	required := blockchain.QuorumSize()
	if required == 0 {
		return errors.New("validator set is empty")
	}

	// Already sufficient on its own -- a single-validator chain, or a quorum
	// of one. No network round is needed.
	if blk.CountAttestations() >= required {
		return nil
	}

	peers := make([]string, 0, len(KnownNodes))
	for _, node := range KnownNodes {
		if node != nodeAddress {
			peers = append(peers, node)
		}
	}
	if len(peers) == 0 {
		return fmt.Errorf("quorum of %d unreachable: no peers to attest (have %d)",
			required, blk.CountAttestations())
	}

	round := registerRound(blk, required)
	defer releaseRound(blk)

	for _, peer := range peers {
		SendProposal(peer, blk)
	}

	select {
	case <-round.reached:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("consensus round timed out: %d of %d attestations after %v",
			blk.CountAttestations(), required, timeout)
	}
}

// HandleProposal validates a proposed block and, if this node is an authorised
// validator, returns its attestation to the proposer.
func HandleProposal(request []byte, chain *blockchain.BlockChain, wlt *wallet.Wallet) {
	var payload Proposal

	body, ok := frameBody(request)
	if !ok {
		fmt.Println("discarding short peer message")
		return
	}
	if err := gob.NewDecoder(bytes.NewBuffer(body)).Decode(&payload); err != nil {
		fmt.Println("discarding malformed proposal:", err)
		return
	}

	blk := payload.Block
	if err := blk.ValidateProposal(); err != nil {
		fmt.Printf("refusing to attest to proposal %x from %s: %v\n",
			blk.BlockHash, payload.AddrFrom, err)
		return
	}

	// Only attest to a block that would extend our own view of the chain,
	// otherwise a validator signs history it has not verified.
	if !bytes.Equal(blk.PreviousHash, chain.LastHash) {
		fmt.Printf("refusing to attest to proposal %x: does not extend local tip\n", blk.BlockHash)
		return
	}

	if _, authorized := blockchain.LookupValidator(wlt.Address); !authorized {
		return
	}

	attested := &blockchain.Block{}
	*attested = blk
	attested.Attestations = nil
	if err := attested.Attest(wlt); err != nil {
		fmt.Printf("could not attest: %v\n", err)
		return
	}
	if len(attested.Attestations) == 0 {
		return
	}

	fmt.Printf("attesting to proposal %x from %s\n", blk.BlockHash, payload.AddrFrom)
	SendAttest(payload.AddrFrom, blk.BlockHash, attested.Attestations[0])
}

// HandleAttest merges an attestation into the matching pending round.
func HandleAttest(request []byte) {
	var payload Attest

	body, ok := frameBody(request)
	if !ok {
		fmt.Println("discarding short peer message")
		return
	}
	if err := gob.NewDecoder(bytes.NewBuffer(body)).Decode(&payload); err != nil {
		fmt.Println("discarding malformed attestation:", err)
		return
	}

	round, found := lookupRound(payload.BlockHash)
	if !found {
		// The round may already have completed or timed out.
		return
	}

	roundsMu.Lock()
	counted := round.block.AddAttestation(payload.Attestation)
	total := round.block.CountAttestations()
	roundsMu.Unlock()

	if !counted {
		fmt.Printf("discarded attestation from %s for %x\n", payload.AddrFrom, payload.BlockHash)
		return
	}
	fmt.Printf("attestation %d/%d for %x from %s\n", total, round.required, payload.BlockHash, payload.AddrFrom)

	if total >= round.required {
		round.once.Do(func() { close(round.reached) })
	}
}
