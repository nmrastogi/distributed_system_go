# go-raft

A distributed key-value store built on top of the [Raft](https://raft.github.io/) consensus algorithm, written in Go.

## Overview

This project implements the core Raft protocol from scratch and layers a simple in-memory key-value store on top of it. Multiple nodes form a cluster, elect a leader, and replicate log entries to reach consensus before committing writes to the state machine.

## Project Structure

```
go-raft/
├── cmd/
│   └── node/
│       └── main.go        # Entry point — starts a Raft node and exposes RPC endpoints
├── raft/
│   ├── raft.go            # Core Raft struct, lifecycle (Start/Stop/Submit), applier loop
│   ├── election.go        # Leader election, vote requests, election timer
│   ├── replication.go     # Log replication, AppendEntries, commit advancement
│   ├── persistence.go     # Durable state (currentTerm, votedFor, log) via gob + atomic rename
│   ├── rpc.go             # RPC types and handler wrappers
│   └── transport.go       # TCP RPC transport with per-call timeout
└── storage/
    └── kv.go              # Thread-safe in-memory KV store + gob command encoding
```

## How It Works

### Raft Consensus

- **Leader election** — each node runs an election timer with a randomised timeout (300–550 ms). If no heartbeat is received before the timeout fires, the node starts an election.
- **Log replication** — the leader appends new commands to its log and broadcasts `AppendEntries` RPCs to all peers every 80 ms. An entry is committed once a majority of nodes have stored it.
- **Persistence** — `currentTerm`, `votedFor`, and the log are written to disk (gob-encoded) on every state change using an atomic write-then-rename to avoid partial files.
- **Transport** — each RPC call opens a fresh TCP connection with a 200 ms deadline.

### State Machine

Committed log entries are decoded and applied to an in-memory `KVStore`. The only supported operation today is `put(key, value)`.

## Running a Cluster

Build the binary:

```bash
go build ./cmd/node
```

Start three nodes in separate terminals:

```bash
# Terminal 1
./node -id 1 -addr :8001 -peers :8001,:8002,:8003 -data ./data/node1

# Terminal 2
./node -id 2 -addr :8002 -peers :8001,:8002,:8003 -data ./data/node2

# Terminal 3
./node -id 3 -addr :8003 -peers :8001,:8002,:8003 -data ./data/node3
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-id` | `1` | Node ID (must be unique, in range `1..N`) |
| `-addr` | `:8001` | TCP address this node listens on |
| `-peers` | `:8001,:8002,:8003` | Comma-separated list of all peer addresses (including self) |
| `-data` | `./data` | Directory for persisted Raft state |

## RPC Endpoints

Each node exposes two sets of RPC endpoints over TCP:

**Internal (Raft protocol)**
- `Raft.RequestVote` — used during leader elections
- `Raft.AppendEntries` — used for log replication and heartbeats

**Client-facing**
- `Client.Put` — write a key/value pair (forwarded through the leader's log)
- `Client.Get` — read a value from the local state machine
- `Client.Status` — inspect node state (role, term, commit index, log length)

## Requirements

- Go 1.21+
- No external dependencies (uses only the standard library)
