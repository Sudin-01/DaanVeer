# DaanVeer Chain 🔗

> A transparent, immutable blockchain-based donation platform built in Go making every donation traceable and trustworthy.

---

## Table of Contents

- [DaanVeer Chain 🔗](#daanveer-chain-)
  - [Table of Contents](#table-of-contents)
  - [Overview](#overview)
  - [Motivation](#motivation)
  - [System Architecture](#system-architecture)
  - [How It Works — Full Workflow](#how-it-works--full-workflow)
    - [1. Startup](#1-startup)
    - [2. Wallet Creation](#2-wallet-creation)
    - [3. Creating a Donation Transaction](#3-creating-a-donation-transaction)
    - [4. Mining a Block (Proof of Authority)](#4-mining-a-block-proof-of-authority)
    - [5. Block Propagation to Other Nodes](#5-block-propagation-to-other-nodes)
    - [6. Balance Calculation](#6-balance-calculation)
  - [Running the Project](#running-the-project)
    - [Prerequisites](#prerequisites)
    - [Steps](#steps)
  - [Multi-Node P2P Setup (Validators on Different Computers)](#multi-node-p2p-setup-validators-on-different-computers)
    - [Step 1: Find IP addresses](#step-1-find-ip-addresses)
    - [Step 2: Edit `knownNodes.json`](#step-2-edit-knownnodesjson)
    - [Step 3: IP Detection (Important)](#step-3-ip-detection-important)
    - [Step 4: Validators](#step-4-validators)
    - [Step 5: Start nodes](#step-5-start-nodes)
  - [API Reference](#api-reference)
  - [Project Structure](#project-structure)
  - [Technologies Used](#technologies-used)
  - [Key Design Decisions](#key-design-decisions)
  - [Challenges \& Solutions](#challenges--solutions)
  - [Future Improvements](#future-improvements)
  - [Authors](#authors)

---

## Overview

DaanVeer Chain ("DaanVeer" = "Donation Hero" in Nepali/Hindi) is a custom blockchain built from scratch in Go. It records donation transactions on an append-only chain secured by **Proof of Authority (PoA)** consensus, ensuring only authorized validators can add blocks. A lightweight web frontend allows users to send donations, view the chain, and mine new blocks — all without any third-party blockchain framework.

---

## Motivation

Traditional donation systems are opaque — donors have no way to verify where their money actually went. DaanVeer Chain solves this by making every donation a cryptographically signed, publicly auditable transaction on a chain that cannot be altered retroactively.

---

## System Architecture

![System Architecture](images/Images/Blockchain(main).drawio.png)

---

## How It Works — Full Workflow

### 1. Startup

When you run `go run main.go`, two things happen in parallel:

- **Gin HTTP server** starts on port `8080` — serves the REST API consumed by the frontend.
- **P2P TCP server** starts on the same port — listens for messages from other nodes.

On first run, `InitBlockChain()` checks BadgerDB for an existing chain. If none exists, it creates the **Genesis Block** — a hardcoded first block with 1,000 units sent to the genesis wallet address (`5Dv7dCeuvoLntY5QueBDsConyi1hckMVjdLCeg6kdeeC6wE8G`).

`GenerateWallet()` either loads your existing wallet from `my_wallet.txt` (AES-encrypted) or creates a new ECDSA keypair, derives a Base58Check address (like Bitcoin), and saves it.

---

### 2. Wallet Creation

Each wallet is an **ECDSA key pair** on the P-256 curve:

```
Private Key → Public Key → SHA-256 → RIPEMD-160 → + Checksum → Base58 Encode → Address
```

Wallet data is AES-encrypted and stored in `my_wallet.txt`. The API exposes your address, public key, public key hash, and balance at `/my-wallet/address` and `/my-wallet/balance`.

---

### 3. Creating a Donation Transaction

A user fills in the recipient address and amount on `transfer.html` → hits Submit → the frontend POSTs to `/transaction/new`.

Inside `NewTransaction()` in `transaction.go`:
1. Checks sender balance by scanning all blocks in the chain.
2. Derives sender and recipient public key hashes from their addresses.
3. Creates a `Transactions` struct with sender hash, recipient hash, amount, and timestamp.
4. Computes `TxID = SHA-256(transaction data)`.
5. Signs the transaction with the sender's ECDSA private key.
6. Broadcasts the transaction to all known nodes via `SendTx()`.

The transaction is now in every node's **memory pool** (`MemoryPool`), waiting to be mined.

---

### 4. Mining a Block (Proof of Authority)

A validator visits `mineblock.html`, selects pending transactions from the pool, and clicks "Mine Block". This POSTs to `/block/mine`.

Inside `MineBlock()` in `block.go`:
1. Fetches the last block's hash and height from BadgerDB.
2. Sets `PreviousHash` and `Height` on the new block.
3. Calls `ProofOfAuthority()` — checks that the validator's address matches the hardcoded authorized validator address (`25ayHZZT...`).
4. If authorized, signs the block hash with the validator's ECDSA private key.
5. Computes and sets `BlockHash = SHA-256(block metadata as JSON)`.

The signed block is then verified by `VerifyProof()` before being added to the chain via `AddBlock()`, which persists it in BadgerDB and updates the `last_hash` pointer.

Transactions are stored inside the block as a **Merkle Tree** — the root hash provides an efficient way to prove a transaction is in a block.

---

### 5. Block Propagation to Other Nodes

After a block is mined and added locally, it is broadcast to peer nodes over the P2P layer (`communication/p2p.go`). The P2P layer uses a custom 12-byte command prefix protocol over raw TCP:

| Command | Purpose |
|---|---|
| `getversion` | Announce chain height; trigger sync if behind |
| `getblocks` | Request block hashes after a given hash |
| `inv` | Announce available blocks/transactions |
| `getdata` | Request a specific block or transaction |
| `block` | Send a full block |
| `tx` | Send a transaction |
| `address` | Share known node addresses |

When a new node connects, it compares chain heights with known nodes and requests missing blocks to catch up.

---

### 6. Balance Calculation

There are no UTXOs. Balance is calculated by scanning the entire chain from genesis, summing received amounts and subtracting sent amounts for a given public key hash. This is done at query time via `GetWalletBalance()`.

---

## Running the Project

### Prerequisites

- Go 1.19+ installed
- BadgerDB will be auto-installed via `go mod`

### Steps

```bash
# 1. Clone the repository
git clone https://github.com/Roshan310/DaanVeer.git
cd DaanVeer

# 2. Install dependencies
go mod tidy

# 3. Run the node
go run main.go
```

The server starts on `http://localhost:8080`.

Open `frontend/index.html` in your browser to use the dashboard (open as a local file, or serve with a simple HTTP server).

```bash
# Optional: serve the frontend with Python
cd frontend
python3 -m http.server 3000
# Open http://localhost:3000
```

> **Note:** On first run, a `my_wallet.txt` file and a `db/` folder will be created in the project root. Do not delete `my_wallet.txt` — it holds your encrypted keys.

---

## Multi-Node P2P Setup (Validators on Different Computers)

### Step 1: Find IP addresses

On each computer, run:
```bash
# Linux/Mac
ip addr show   # look for 192.168.x.x

# Windows
ipconfig       # look for IPv4 Address
```

Make sure all machines are on the **same local network** (same WiFi/LAN).

### Step 2: Edit `knownNodes.json`

This file (`communication/knownNodes.json`) is the peer discovery list. **Every node must list at least one other known node.**

**On Computer A (e.g., IP: 192.168.1.75):**
```json
{
    "nodes": [
        "192.168.1.83:8080"
    ]
}
```

**On Computer B (e.g., IP: 192.168.1.83):**
```json
{
    "nodes": [
        "192.168.1.75:8080"
    ]
}
```

For more than 2 nodes, list all peers each machine should know about:
```json
{
    "nodes": [
        "192.168.1.75:8080",
        "192.168.1.83:8080",
        "192.168.1.90:8080"
    ]
}
```

### Step 3: IP Detection (Important)

The project auto-detects its IP in `blockchain/utils.go` → `GetNodeAddress()`. It currently looks for addresses starting with `"192"`. If your network uses a different subnet (e.g., `10.x.x.x` or `172.16.x.x`), edit this line:

```go
// In blockchain/utils.go
if addr_string[:3] == "192" {   // ← change "192" to match your subnet prefix
    return addr_string[:position]
}
```

### Step 4: Validators

Currently, only one address is authorized to mine blocks — it is hardcoded in `blockchain/consensus.go`:

```go
authorityAddress := "25ayHZZTtyoMzhNSYwP9ivjpvWABqJw6uhQ9AHoY7eWo1Cb8jT"
```

For another computer to act as a validator, you need to either:

**Option A (Quick):** Copy your `my_wallet.txt` to the other machine — it will load the same wallet and therefore have the authorized private key.

**Option B (Proper multi-validator):** Add the new computer's wallet address and public key to the `Validators` map in `consensus.go` and update the `ProofOfAuthority()` check to allow multiple addresses.

### Step 5: Start nodes

Run `go run main.go` on each machine. The nodes will automatically sync their chains using the version/getblocks handshake.

---

## API Reference

| Method | Endpoint | Description |
|---|---|---|
| GET | `/block/last` | Get the most recent block |
| GET | `/block/last/:n` | Get last N blocks |
| POST | `/block/mine` | Mine a new block with given transactions |
| GET | `/transaction/last/:n` | Get last N transactions |
| GET | `/transaction/pool` | Get pending transactions in memory pool |
| POST | `/transaction/new` | Create and broadcast a new transaction |
| GET | `/my-wallet/address` | Get this node's wallet address and public key |
| GET | `/my-wallet/balance` | Get this node's wallet balance |
| GET | `/my-wallet/info` | Get blocks mined by this wallet |
| GET | `/wallet/info/:address` | Get blocks mined by any wallet address |
| GET | `/token/sign/:token` | Sign a token with this node's private key |
| POST | `/token/verify` | Verify a signed token against a public key |

---

## Project Structure

```
DaanVeer/
├── main.go                     # Entry point
├── go.mod / go.sum             # Go module files
├── my_wallet.txt               # Auto-generated encrypted wallet
├── db/                         # Auto-generated BadgerDB data
│
├── api/
│   ├── handlers.go             # All HTTP handler functions
│   ├── routes.go               # Gin router setup & server start
│   ├── middleware.go           # CORS middleware
│   └── models.go               # Request/response data models
│
├── blockchain/
│   ├── block.go                # Block structure, hashing, mining
│   ├── chain.go                # Blockchain DB operations, iterators
│   ├── consensus.go            # Proof of Authority logic & validators
│   ├── transaction.go          # Transaction creation, signing, verification
│   ├── MerkleRoot.go           # Merkle tree implementation
│   └── utils.go                # Error handling, IP detection
│
├── communication/
│   ├── p2p.go                  # Full P2P protocol (send/handle all commands)
│   ├── node.go                 # Node package declaration
│   └── knownNodes.json         # ← Edit this to connect nodes
│
├── wallet/
│   └── wallet.go               # Key generation, address derivation, AES encryption
│
└── frontend/
    ├── index.html / main.js    # Dashboard — wallet info & navigation
    ├── mineBlock/              # UI to select and mine transactions
    ├── transaction/            # UI to send a donation
    ├── visualise/              # View latest blocks and transactions
    └── walletInfo/             # View blocks mined by this wallet
```

---

## Technologies Used

| Technology | Role |
|---|---|
| **Go** | Core blockchain and server implementation |
| **BadgerDB** | Persistent key-value storage for blocks |
| **Gin** | HTTP REST API framework |
| **ECDSA (P-256)** | Transaction and block signing |
| **SHA-256** | Block and transaction hashing |
| **RIPEMD-160** | Public key hashing for addresses |
| **Base58Check** | Human-readable wallet addresses |
| **AES-CFB** | Wallet file encryption |
| **Merkle Tree** | Efficient transaction integrity within blocks |
| **Raw TCP** | Custom P2P protocol for node communication |
| **Gob encoding** | Binary serialization for P2P messages |
| **Vanilla JS + HTML** | Lightweight frontend, no framework |

---

## Key Design Decisions

**Why Proof of Authority instead of Proof of Work?** PoA is lightweight, instant, and appropriate for a permissioned donation system where trusted validators are known. PoW would waste computation for no benefit here.

**Why BadgerDB?** It is a fast, embeddable key-value store written in Go — no external database server needed. Blocks are stored with their hash as the key.

**Why custom TCP P2P instead of libp2p?** Building from scratch gave full understanding and control of the sync protocol — a deliberate educational choice for a minor project.

**Why no UTXOs?** Balance scanning is simpler to implement and reason about at small scale. A UTXO model would be a natural future improvement for performance.

---

## Challenges & Solutions

**Block hash mismatch across nodes** — Gob encoding produced non-deterministic byte sequences, causing hash verification failures when blocks were received over the network. Fixed by switching to JSON marshalling for the data that feeds into the hash computation.

**Validator synchronization in P2P** — Ensuring nodes agree on chain state required implementing the full version/getblocks/inv/getdata handshake sequence so nodes could detect when they were behind and request only the missing blocks.

**Wallet persistence with security** — Storing raw private keys to disk is dangerous. Solved with AES-CFB encryption keyed by a passphrase before writing to `my_wallet.txt`.

---

## Future Improvements

- **Multi-validator support** — Dynamic validator registration instead of a single hardcoded address
- **UTXO model** — Replace full-chain balance scanning for better performance at scale
- **Smart contracts** — Conditional donation release (e.g., only release funds when a milestone is verified)
- **Web dashboard** — Unified React frontend replacing the scattered HTML pages
- **REST authentication** — JWT or signature-based auth on sensitive API endpoints
- **Wallet UI** — In-browser key generation so users don't need to run a node
- **Block explorer** — Searchable, paginated view of the full chain
- **Subnet auto-detection** — Automatically detect the correct network interface instead of hardcoding `"192"`

---

## Authors

**Roshan** — Blockchain core (block, chain), P2P communication layer, API handlers.

**Sudin** — Project architecture,Transaction (Merkle Tree), Consensus Mechanism, Frontend development, wallet UI, testing and integration

*Minor Project — Bachelor of Computer Engineering*

---

> *"Transparency is the currency of trust."*