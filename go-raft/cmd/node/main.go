package main

import (
	"fmt"

	"github.com/nmrastogi/distributed_system_go/go-raft/raft"
)

func main() {
	peers := []string{":8001", ":8002", ":8003"}
	node := raft.NewRaft(1, peers)
	fmt.Println("Node started:", node)
}
