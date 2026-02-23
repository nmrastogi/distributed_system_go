package raft

import (
	"errors"
	"sync"
	"time"
)

// Transporter abstracts the RPC transport; inject a fake in tests.
type Transporter interface {
	Call(peerID int, method string, args any, reply any) bool
}

type State int

const (
	Follower State = iota
	Candidate
	Leader
)

type LogEntry struct {
	Term    int
	Command []byte
}

type ApplyMsg struct {
	CommandValid bool
	Command      []byte
	Index        int
}

type Config struct {
	ID            int
	Addr          string
	Peers         []string
	StateFilePath string
	ApplyCh       chan ApplyMsg
	Transport     Transporter // optional; uses TCP transport if nil
}

type Raft struct {
	mu sync.Mutex

	// Identity
	id    int
	addr  string
	peers []string // index 0..N-1, node IDs are 1..N

	// Persistent state on all servers
	currentTerm int
	votedFor    int
	log         []LogEntry

	// Volatile state on all servers
	commitIndex int
	lastApplied int
	state       State

	// Volatile state on leaders
	nextIndex  []int
	matchIndex []int

	applyCh chan ApplyMsg

	// Timers
	electionReset time.Time

	// Persistence
	persistPath string

	// Transport
	transport Transporter

	// Stop
	stopCh chan struct{}
}

func NewRaft(cfg Config) *Raft {
	r := &Raft{
		id:          cfg.ID,
		addr:        cfg.Addr,
		peers:       cfg.Peers,
		votedFor:    0,
		state:       Follower,
		applyCh:     cfg.ApplyCh,
		persistPath: cfg.StateFilePath,
		stopCh:      make(chan struct{}),
	}

	// Log is 1-indexed for convenience; log[0] is a dummy entry.
	r.log = []LogEntry{{Term: 0, Command: nil}}

	if cfg.Transport != nil {
		r.transport = cfg.Transport
	} else {
		r.transport = NewTransport(cfg.ID, cfg.Peers)
	}

	// Restore persisted state if exists.
	r.restore()

	return r
}

func (r *Raft) Start() {
	r.mu.Lock()
	r.electionReset = time.Now()
	r.mu.Unlock()

	go r.electionTicker()
	go r.heartbeatTicker()
	go r.applierLoop()
}

func (r *Raft) Stop() {
	close(r.stopCh)
}

func (r *Raft) isStopped() bool {
	select {
	case <-r.stopCh:
		return true
	default:
		return false
	}
}

func (r *Raft) Submit(cmd []byte) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != Leader {
		return false, errors.New("not leader")
	}

	// Append to local log.
	r.log = append(r.log, LogEntry{Term: r.currentTerm, Command: cmd})
	r.persist()

	// Leader bookkeeping init.
	if r.nextIndex == nil || len(r.nextIndex) != len(r.peers)+1 {
		r.initLeaderStateLocked()
	}

	// Replicate asynchronously.
	go r.broadcastAppendEntries()

	return true, nil
}

type StatusReply struct {
	ID          int
	State       string
	Term        int
	LeaderID    int
	CommitIndex int
	LogLen      int
}

func (r *Raft) Status() StatusReply {
	r.mu.Lock()
	defer r.mu.Unlock()

	st := "Follower"
	if r.state == Candidate {
		st = "Candidate"
	} else if r.state == Leader {
		st = "Leader"
	}

	leaderID := 0
	if r.state == Leader {
		leaderID = r.id
	}

	return StatusReply{
		ID:          r.id,
		State:       st,
		Term:        r.currentTerm,
		LeaderID:    leaderID,
		CommitIndex: r.commitIndex,
		LogLen:      len(r.log) - 1,
	}
}

func (r *Raft) initLeaderStateLocked() {
	n := len(r.peers)
	r.nextIndex = make([]int, n+1)  // 1..n
	r.matchIndex = make([]int, n+1) // 1..n
	last := len(r.log)
	for i := 1; i <= n; i++ {
		r.nextIndex[i] = last
		r.matchIndex[i] = 0
	}
}

func (r *Raft) applierLoop() {
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()

	for !r.isStopped() {
		<-t.C

		r.mu.Lock()
		for r.lastApplied < r.commitIndex {
			r.lastApplied++
			idx := r.lastApplied
			cmd := r.log[idx].Command
			r.mu.Unlock()

			r.applyCh <- ApplyMsg{
				CommandValid: true,
				Command:      cmd,
				Index:        idx,
			}

			r.mu.Lock()
		}
		r.mu.Unlock()
	}
}
