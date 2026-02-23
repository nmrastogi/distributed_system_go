package raft

import (
	"encoding/gob"
	"os"
)

type persistedState struct {
	CurrentTerm int
	VotedFor    int
	Log         []LogEntry
}

func (r *Raft) persist() {
	if r.persistPath == "" {
		return
	}
	tmp := r.persistPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	defer f.Close()

	enc := gob.NewEncoder(f)
	_ = enc.Encode(persistedState{
		CurrentTerm: r.currentTerm,
		VotedFor:    r.votedFor,
		Log:         r.log,
	})

	_ = os.Rename(tmp, r.persistPath)
}

func (r *Raft) restore() {
	if r.persistPath == "" {
		return
	}
	f, err := os.Open(r.persistPath)
	if err != nil {
		return
	}
	defer f.Close()

	var ps persistedState
	dec := gob.NewDecoder(f)
	if err := dec.Decode(&ps); err != nil {
		return
	}

	r.currentTerm = ps.CurrentTerm
	r.votedFor = ps.VotedFor
	if len(ps.Log) > 0 {
		r.log = ps.Log
	}
}
