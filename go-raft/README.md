# go-raft

A fault-tolerant distributed key-value store built on top of the [Raft](https://raft.github.io/) consensus algorithm, written in Go using only the standard library.

## Project Structure

```
go-raft/
├── cmd/
│   └── node/
│       └── main.go        # Entry point — starts a Raft node, exposes RPC endpoints
├── raft/
│   ├── raft.go            # Core Raft struct, Transporter interface, lifecycle, applier loop
│   ├── election.go        # Leader election, vote requests, election timer
│   ├── replication.go     # Log replication, AppendEntries, commit advancement
│   ├── persistence.go     # Durable state (currentTerm, votedFor, log) via gob + atomic rename
│   ├── rpc.go             # RPC message types and handler wrappers
│   ├── transport.go       # TCP RPC transport with per-call timeout
│   └── raft_test.go       # Unit tests with in-process fake network
└── storage/
    └── kv.go              # Thread-safe in-memory KV store + gob command encoding
```

## How It Works

### Raft Consensus

- **Leader election** — each node runs an election timer with a randomised timeout (300–550 ms). If no heartbeat arrives before it fires, the node starts an election. The candidate with the most up-to-date log wins a majority vote.
- **Log replication** — the leader appends commands to its log and broadcasts `AppendEntries` RPCs every 80 ms. An entry is committed once a majority of nodes have stored it, then applied to the state machine.
- **Persistence** — `currentTerm`, `votedFor`, and the log are gob-encoded to disk on every state change using an atomic write-then-rename, so a crashed node resumes from its last consistent state.
- **Transport** — each RPC call opens a fresh TCP connection with a 200 ms deadline. The `Transporter` interface allows a fake in-process network to be injected for tests.

### State Machine

Committed log entries are applied to an in-memory `KVStore`. Supported operations: `put(key, value)` and `get(key)`.

## Running a Cluster

**Build:**

```bash
go build -o node ./cmd/node
```

**Start three nodes in separate terminals:**

```bash
# Terminal 1
./node -id 1 -addr :8001 -peers :8001,:8002,:8003 -data ./data/n1

# Terminal 2
./node -id 2 -addr :8002 -peers :8001,:8002,:8003 -data ./data/n2

# Terminal 3
./node -id 3 -addr :8003 -peers :8001,:8002,:8003 -data ./data/n3
```

Within ~500 ms one node is elected leader. Raft state is persisted under the `-data` directory so nodes survive restarts.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-id` | `1` | Node ID — must be unique and in range `1..N` |
| `-addr` | `:8001` | TCP address this node listens on |
| `-peers` | `:8001,:8002,:8003` | Comma-separated list of all peer addresses (including self) |
| `-data` | `./data` | Directory for persisted Raft state |

## Interacting with the Cluster

Each node exposes client RPC endpoints over TCP. Only the leader accepts writes; any node serves reads.

**Write a key (try each node until one succeeds — that one is the leader):**

```go
// put.go
package main
import ("fmt"; "net/rpc")
type A struct{ Key, Value string }
type R struct{ OK bool; Error string }
func main() {
    for _, addr := range []string{":8001", ":8002", ":8003"} {
        c, err := rpc.Dial("tcp", addr)
        if err != nil { continue }
        var r R
        c.Call("Client.Put", &A{"city", "Mumbai"}, &r)
        fmt.Printf("%s → %+v\n", addr, r)
        c.Close()
    }
}
```
```bash
go run put.go
```

**Read a key (any node):**

```go
// get.go
package main
import ("fmt"; "net/rpc")
type A struct{ Key string }
type R struct{ Found bool; Value string }
func main() {
    c, _ := rpc.Dial("tcp", ":8002")
    var r R
    c.Call("Client.Get", &A{"city"}, &r)
    fmt.Println(r)
}
```
```bash
go run get.go
```

## RPC Endpoints

**Internal (Raft protocol)**
- `Raft.RequestVote` — leader elections
- `Raft.AppendEntries` — log replication and heartbeats

**Client-facing**
- `Client.Put` — write a key/value pair (goes through the Raft log)
- `Client.Get` — read a value from the local state machine
- `Client.Status` — inspect node state (role, term, commit index, log length)

## Fault Tolerance

- A 3-node cluster tolerates **1 node failure** and keeps accepting writes.
- Kill the leader (Ctrl+C) — the remaining two nodes elect a new leader within ~500 ms.
- Restart any node — it restores `currentTerm`, `votedFor`, and the full log from disk, then rejoins the cluster and catches up via `AppendEntries`.

## Testing

```bash
go test ./raft/ -v -race -timeout 30s
```

The test suite uses an injectable `Transporter` interface to replace TCP with an in-process fake network. This lets tests control network partitions and node failures deterministically without binding any ports.

| Test | What it covers |
|------|----------------|
| `TestLeaderElection` | Exactly one leader elected in a 3-node cluster |
| `TestLogReplication` | Entry submitted to leader is committed on all nodes |
| `TestConcurrentSubmits` | 10 goroutines submit commands simultaneously |
| `TestLeaderFailure` | Leader is isolated; new leader elected; writes succeed |
| `TestPartitionAndRecover` | Follower is partitioned, heals, and catches up |
| `TestPersistence` | Node restarts and correctly restores term and log from disk |

## Requirements

- Go 1.21+
- No external dependencies
