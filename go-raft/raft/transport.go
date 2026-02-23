package raft

import (
	"net/rpc"
	"time"
)

type Transport struct {
	selfID int
	peers  []string // index 0..N-1
}

func NewTransport(selfID int, peers []string) *Transport {
	return &Transport{selfID: selfID, peers: peers}
}

func (t *Transport) Call(peerID int, method string, args any, reply any) bool {
	if peerID < 1 || peerID > len(t.peers) {
		return false
	}
	addr := t.peers[peerID-1]

	c, err := rpc.Dial("tcp", addr)
	if err != nil {
		return false
	}
	defer c.Close()

	done := make(chan error, 1)
	go func() {
		done <- c.Call(method, args, reply)
	}()

	select {
	case err := <-done:
		return err == nil
	case <-time.After(200 * time.Millisecond):
		return false
	}
}
