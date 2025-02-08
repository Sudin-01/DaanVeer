package blockchain

import (
	"bytes"
	"crypto/sha256"
	"fmt"
)


// MerkleNode represents a node in the Merkle tree.
type MerkleNode struct {
	Left  *MerkleNode
	Right *MerkleNode
	Hash  []byte
	Transaction Transactions
}


func (node *MerkleNode) Print()  {

	if node == nil{
		return
	}
	fmt.Printf("%x\n", node.Hash)
	node.Left.Print()
	node.Right.Print()
	
}
// MerkleTree represents the entire Merkle tree.
type MerkleTree struct {
	Root  *MerkleNode
	Nodes []*MerkleNode // Keeps track of all nodes for generating proofs.
}

// NewMerkleNode creates a new Merkle node from two child nodes or a single transaction.
func NewMerkleNode(left, right *MerkleNode, hash []byte, tx Transactions) *MerkleNode {
	var nodeHash []byte
	if left == nil && right == nil {
		nodeHash = hash
	} else {
		data := append(left.Hash[:], right.Hash[:]...)
		tempHash := sha256.Sum256(data)
		nodeHash = tempHash[:]
	}

	return &MerkleNode{
		Left:  left,
		Right: right,
		Hash:  nodeHash,
		Transaction: tx,
	}
}

// NewMerkleTree constructs a Merkle tree from a list of transactions.
func NewMerkleTree(transactions []Transactions) *MerkleTree {
	if len(transactions) == 0 {
		return &MerkleTree{Root: nil}
	}

	// Create leaf nodes.
	var nodes []*MerkleNode
	for _, tx := range transactions {
		hash := tx.Hash()
		nodes = append(nodes, NewMerkleNode(nil, nil, hash, tx))
	}

	// Build the tree by iteratively hashing pairs of nodes.
	for len(nodes) > 1 {
		if len(nodes)%2 != 0 {
			nodes = append(nodes, nodes[len(nodes)-1]) // Duplicate the last node if odd.
		}

		var newLevel []*MerkleNode
		for i := 0; i < len(nodes); i += 2 {
			newNode := NewMerkleNode(nodes[i], nodes[i+1], []byte{}, transactions[i])
			newLevel = append(newLevel, newNode)
		}
		nodes = newLevel
	}

	// The root is the last remaining node.
	return &MerkleTree{Root: nodes[0], Nodes: nodes}
}

// CalculateMerkleRoot extracts the Merkle root from the tree.
func (mt *MerkleTree) CalculateMerkleRoot() []byte {
	if mt.Root == nil {
		return []byte{}
	}
	return mt.Root.Hash
}

// GenerateMerkleProof generates a Merkle proof for a given transaction hash.
func (mt *MerkleTree) GenerateMerkleProof(txHash []byte) ([]byte, bool) {
	var proof []byte
	var targetNode *MerkleNode

	// Find the node corresponding to the transaction hash.
	for _, node := range mt.Nodes {
		if node.Left == nil && node.Right == nil && bytes.Equal(node.Hash, txHash) {
			targetNode = node
			break
		}
	}

	if targetNode == nil {
		return nil, false // Transaction not found in the tree.
	}

	// Traverse up the tree to collect proof hashes.
	currentNode := targetNode
	for currentNode != mt.Root {
		parentNode := findParent(mt.Root, currentNode)
		if parentNode == nil {
			break
		}

		if parentNode.Left == currentNode {
			proof = append(proof, parentNode.Right.Hash...)
		} else {
			proof = append(proof, parentNode.Left.Hash...)
		}

		currentNode = parentNode
	}

	return proof, true
}

// findParent finds the parent of a given node starting from the root.
func findParent(root, target *MerkleNode) *MerkleNode {
	if root == nil || root.Left == nil || root.Right == nil {
		return nil
	}

	if root.Left == target || root.Right == target {
		return root
	}

	leftSearch := findParent(root.Left, target)
	if leftSearch != nil {
		return leftSearch
	}

	return findParent(root.Right, target)
}
