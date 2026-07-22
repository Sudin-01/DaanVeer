package blockchain

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/dgraph-io/badger/v4"
)

// Equivocation detection.
//
// A validator that signs two different blocks at the same height has produced
// objectively provable misbehaviour: the two signatures are self-contained
// evidence, verifiable by anyone, and no honest validator following the
// protocol can generate them. This is the canonical Byzantine fault, and it is
// distinct from the crash faults measured by the liveness sweep -- a validator
// that withholds its attestation is merely absent, whereas an equivocating
// validator is actively attempting to have two conflicting histories accepted.
//
// Scope, stated plainly because it bounds what may be claimed. Detection here
// is *local*: a node evicts an equivocator it has itself observed. Eviction is
// not itself agreed by consensus, so two nodes that observed different subsets
// of a conflicting broadcast can hold different evicted sets. Making eviction
// consensus-agreed requires evidence transactions carried in blocks, which we
// do not implement. What follows therefore establishes that equivocation is
// detected and that the equivocator's attestations stop counting -- not that
// the validator set converges.

const (
	// proposalPrefix maps (height, validator) -> the block hash that validator
	// proposed at that height.
	proposalPrefix = "eqp_"
	// evictedPrefix marks a validator as having been caught equivocating.
	evictedPrefix = "eqv_"
)

// EquivocationEvidence is the self-contained proof of a validator signing two
// conflicting blocks at one height.
type EquivocationEvidence struct {
	ValidatorAddress string
	Height           uint64
	FirstBlock       []byte
	SecondBlock      []byte
}

func (e EquivocationEvidence) Error() string {
	return fmt.Sprintf("validator %s equivocated at height %d: signed both %x and %x",
		e.ValidatorAddress, e.Height, e.FirstBlock, e.SecondBlock)
}

func proposalKey(height uint64, address string) []byte {
	key := make([]byte, 0, len(proposalPrefix)+8+len(address))
	key = append(key, proposalPrefix...)
	var h [8]byte
	binary.BigEndian.PutUint64(h[:], height)
	key = append(key, h[:]...)
	return append(key, address...)
}

func evictedKey(address string) []byte {
	return append([]byte(evictedPrefix), address...)
}

// RecordProposal registers that a validator proposed a block at a height, and
// reports equivocation if that validator already proposed a different block at
// the same height.
//
// Recording the *first* proposal seen is deliberate: the first is not
// necessarily the honest one, and the protocol makes no such claim. The
// evidence is that two exist.
func (chain *BlockChain) RecordProposal(address string, height uint64, blockHash []byte) (*EquivocationEvidence, error) {
	var evidence *EquivocationEvidence

	err := chain.Database.Update(func(txn *badger.Txn) error {
		key := proposalKey(height, address)

		item, err := txn.Get(key)
		switch {
		case errors.Is(err, badger.ErrKeyNotFound):
			return txn.Set(key, blockHash)
		case err != nil:
			return err
		}

		var seen []byte
		if err := item.Value(func(val []byte) error {
			seen = append(seen, val...)
			return nil
		}); err != nil {
			return err
		}

		if bytes.Equal(seen, blockHash) {
			return nil // the same block again; not equivocation
		}

		evidence = &EquivocationEvidence{
			ValidatorAddress: address,
			Height:           height,
			FirstBlock:       seen,
			SecondBlock:      blockHash,
		}
		// Record the eviction alongside the evidence that justifies it.
		record := append(append([]byte{}, seen...), blockHash...)
		return txn.Set(evictedKey(address), record)
	})
	if err != nil {
		return nil, err
	}
	return evidence, nil
}

// IsEvicted reports whether a validator has been caught equivocating.
func (chain *BlockChain) IsEvicted(address string) bool {
	evicted := false
	_ = chain.Database.View(func(txn *badger.Txn) error {
		_, err := txn.Get(evictedKey(address))
		evicted = err == nil
		return nil
	})
	return evicted
}

// EvictedValidators lists validators caught equivocating, in sorted order.
func (chain *BlockChain) EvictedValidators() []string {
	var out []string
	_ = chain.Database.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(evictedPrefix)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			key := it.Item().KeyCopy(nil)
			out = append(out, string(key[len(prefix):]))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// EvictionEvidence returns the conflicting block hashes recorded for a
// validator, or nil if it has not been evicted.
func (chain *BlockChain) EvictionEvidence(address string) [][]byte {
	var first, second []byte
	err := chain.Database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(evictedKey(address))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			if len(val)%2 != 0 || len(val) == 0 {
				return fmt.Errorf("malformed eviction record: %d bytes", len(val))
			}
			half := len(val) / 2
			first = append(first, val[:half]...)
			second = append(second, val[half:]...)
			return nil
		})
	})
	if err != nil {
		return nil
	}
	return [][]byte{first, second}
}

// activeAttestors counts attestations excluding validators this node has
// caught equivocating.
//
// An equivocator's signature is cryptographically valid, so it would otherwise
// continue to count toward quorum. Excluding it is the point of eviction: a
// Byzantine validator must not be able to help finalise a block.
func (chain *BlockChain) activeAttestations(blk *Block) int {
	counted := map[string]bool{}
	hash := blk.Hash()

	if blk.VerifyProof() && !chain.IsEvicted(string(blk.ValidatorAddress)) {
		counted[string(blk.ValidatorAddress)] = true
	}

	for _, attestation := range blk.Attestations {
		address := string(attestation.ValidatorAddress)
		if counted[address] || chain.IsEvicted(address) {
			continue
		}
		validator, authorized := LookupValidator(address)
		if !authorized {
			continue
		}
		if verifyAttestationSignature(validator, attestation, hash) {
			counted[address] = true
		}
	}
	return len(counted)
}

// DescribeEviction renders an eviction for logs and for the audit trail.
func (chain *BlockChain) DescribeEviction(address string) string {
	evidence := chain.EvictionEvidence(address)
	if evidence == nil {
		return fmt.Sprintf("%s: not evicted", address)
	}
	return fmt.Sprintf("%s evicted: signed conflicting blocks %s and %s",
		address,
		hex.EncodeToString(evidence[0])[:16],
		hex.EncodeToString(evidence[1])[:16])
}
