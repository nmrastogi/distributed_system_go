package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"strings"
	"time"

	"github.com/nmrastogi/distributed_system_go/go-raft/raft"
	"github.com/nmrastogi/distributed_system_go/go-raft/storage"
)

type ClientAPI struct {
	R  *raft.Raft
	KV *storage.KVStore
}

// Put writes a key/value. Only leader accepts writes.
func (api *ClientAPI) Put(args *storage.PutArgs, reply *storage.PutReply) error {
	ok, err := api.R.Submit(storage.EncodePut(args.Key, args.Value))
	reply.OK = ok
	if err != nil {
		reply.Error = err.Error()
	}
	return nil
}

// Get reads locally (reads are served from applied state machine).
func (api *ClientAPI) Get(args *storage.GetArgs, reply *storage.GetReply) error {
	v, ok := api.KV.Get(args.Key)
	reply.Found = ok
	reply.Value = v
	return nil
}

// Status returns node state.
func (api *ClientAPI) Status(_ *struct{}, reply *raft.StatusReply) error {
	*reply = api.R.Status()
	return nil
}

func main() {
	var (
		id       = flag.Int("id", 1, "node id (1..N)")
		addr     = flag.String("addr", ":8001", "listen address")
		peersStr = flag.String("peers", ":8001,:8002,:8003", "comma-separated peer addrs")
		dataDir  = flag.String("data", "./data", "data directory")
	)
	flag.Parse()

	peers := strings.Split(*peersStr, ",")
	if *id < 1 || *id > len(peers) {
		log.Fatalf("id must be in [1..%d]", len(peers))
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatal(err)
	}

	kv := storage.NewKVStore()
	applyCh := make(chan raft.ApplyMsg, 256)

	// Apply committed entries to the KV store.
	go func() {
		for msg := range applyCh {
			if msg.CommandValid {
				storage.ApplyToKV(kv, msg.Command)
			}
		}
	}()

	// One persistent file per node.
	statePath := fmt.Sprintf("%s/raft_state_node_%d.gob", *dataDir, *id)

	r := raft.NewRaft(raft.Config{
		ID:            *id,
		Addr:          *addr,
		Peers:         peers,
		StateFilePath: statePath,
		ApplyCh:       applyCh,
	})

	// Register RPC endpoints: Raft internal RPC + client API.
	server := rpc.NewServer()
	if err := server.RegisterName("Raft", r.RPC()); err != nil {
		log.Fatal(err)
	}
	if err := server.RegisterName("Client", &ClientAPI{R: r, KV: kv}); err != nil {
		log.Fatal(err)
	}

	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("node %d listening on %s peers=%v", *id, *addr, peers)

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				continue
			}
			go server.ServeConn(conn)
		}
	}()

	// Kick off Raft timers.
	r.Start()

	// Keep process alive.
	for {
		time.Sleep(10 * time.Second)
	}
}
