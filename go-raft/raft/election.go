package raft

import (
	"math/rand"
	"time"
)

func (r *Raft) electionTicker() {
	for !r.isStopped() {
		timeout := time.Duration(300+rand.Intn(250)) * time.Millisecond

		time.Sleep(20 * time.Millisecond)

		r.mu.Lock()
		elapsed := time.Since(r.electionReset)
		state := r.state
		r.mu.Unlock()

		if state == Leader {
			continue
		}
		if elapsed >= timeout {
			r.startElection()
		}
	}
}

func (r *Raft) startElection() {
	r.mu.Lock()
	r.state = Candidate
	r.currentTerm++
	term := r.currentTerm
	r.votedFor = r.id
	r.electionReset = time.Now()
	lastIndex := len(r.log) - 1
	lastTerm := r.log[lastIndex].Term
	r.persist()
	r.mu.Unlock()

	votes := 1
	n := len(r.peers)

	for peerID := 1; peerID <= n; peerID++ {
		if peerID == r.id {
			continue
		}

		go func(pid int) {
			args := &RequestVoteArgs{
				Term:         term,
				CandidateID:  r.id,
				LastLogIndex: lastIndex,
				LastLogTerm:  lastTerm,
			}
			var reply RequestVoteReply
			ok := r.transport.Call(pid, "Raft.RequestVote", args, &reply)
			if !ok {
				return
			}

			r.mu.Lock()
			defer r.mu.Unlock()

			if reply.Term > r.currentTerm {
				r.becomeFollowerLocked(reply.Term)
				return
			}
			if r.state != Candidate || r.currentTerm != term {
				return
			}
			if reply.VoteGranted {
				votes++
				if votes > n/2 {
					r.state = Leader
					r.initLeaderStateLocked()
					r.electionReset = time.Now()
				}
			}
		}(peerID)
	}
}

func (r *Raft) handleRequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reply.VoteGranted = false
	reply.Term = r.currentTerm

	if args.Term < r.currentTerm {
		return
	}

	if args.Term > r.currentTerm {
		r.becomeFollowerLocked(args.Term)
	}

	// Enforce log up-to-date rule.
	lastIndex := len(r.log) - 1
	lastTerm := r.log[lastIndex].Term

	upToDate := args.LastLogTerm > lastTerm ||
		(args.LastLogTerm == lastTerm && args.LastLogIndex >= lastIndex)

	if (r.votedFor == 0 || r.votedFor == args.CandidateID) && upToDate {
		r.votedFor = args.CandidateID
		r.electionReset = time.Now()
		r.persist()

		reply.VoteGranted = true
		reply.Term = r.currentTerm
	}
}

func (r *Raft) becomeFollowerLocked(newTerm int) {
	r.state = Follower
	r.currentTerm = newTerm
	r.votedFor = 0
	r.electionReset = time.Now()
	r.persist()
}
