package raft

import "sync"

type State int

const (
	Follower State = iota
	Candidate
	Leader
)

type Raft struct {
	mu    sync.Mutex
	id    int
	peers []string

	state State

	currentTerm int
	votedfor    int
	log         []LogEntry

	commitIndex int
	lastApplied int
}

type LogEntry struct {
	Term     int
	LeaderID int
}

func NewRaft(id int, peers []string) *Raft {
	return &Raft{
		id:    id,
		peers: peers,
		state: Follower,
	}
}
