package raft

import "time"

func (r *Raft) heartbeatTicker() {
	for !r.isStopped() {
		time.Sleep(80 * time.Millisecond)

		r.mu.Lock()
		isLeader := r.state == Leader
		r.mu.Unlock()

		if isLeader {
			go r.broadcastAppendEntries()
		}
	}
}

func (r *Raft) broadcastAppendEntries() {
	r.mu.Lock()
	if r.state != Leader {
		r.mu.Unlock()
		return
	}
	term := r.currentTerm
	leaderCommit := r.commitIndex
	n := len(r.peers)
	r.mu.Unlock()

	for peerID := 1; peerID <= n; peerID++ {
		if peerID == r.id {
			continue
		}

		go func(pid int) {
			r.mu.Lock()
			if r.state != Leader || r.currentTerm != term {
				r.mu.Unlock()
				return
			}

			nextIdx := r.nextIndex[pid]
			prevIdx := nextIdx - 1
			prevTerm := r.log[prevIdx].Term

			entries := make([]LogEntry, len(r.log[nextIdx:]))
			copy(entries, r.log[nextIdx:])

			args := &AppendEntriesArgs{
				Term:         r.currentTerm,
				LeaderID:     r.id,
				PrevLogIndex: prevIdx,
				PrevLogTerm:  prevTerm,
				Entries:      entries,
				LeaderCommit: leaderCommit,
			}
			r.mu.Unlock()

			var reply AppendEntriesReply
			ok := r.transport.Call(pid, "Raft.AppendEntries", args, &reply)
			if !ok {
				return
			}

			r.mu.Lock()
			defer r.mu.Unlock()

			if reply.Term > r.currentTerm {
				r.becomeFollowerLocked(reply.Term)
				return
			}
			if r.state != Leader || r.currentTerm != term {
				return
			}

			if reply.Success {
				r.matchIndex[pid] = reply.MatchIndex
				r.nextIndex[pid] = reply.MatchIndex + 1
				r.advanceCommitLocked()
			} else {
				if r.nextIndex[pid] > 1 {
					r.nextIndex[pid]--
				}
			}
		}(peerID)
	}
}

func (r *Raft) handleAppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reply.Success = false
	reply.Term = r.currentTerm
	reply.MatchIndex = 0

	if args.Term < r.currentTerm {
		return
	}

	if args.Term > r.currentTerm {
		r.becomeFollowerLocked(args.Term)
	}

	// Reset election timer on valid leader contact.
	r.state = Follower
	r.electionReset = time.Now()

	// Log consistency check.
	if args.PrevLogIndex >= len(r.log) {
		return
	}
	if r.log[args.PrevLogIndex].Term != args.PrevLogTerm {
		return
	}

	// Append new entries, deleting conflicts.
	insertAt := args.PrevLogIndex + 1
	i := 0
	for ; i < len(args.Entries); i++ {
		pos := insertAt + i
		if pos < len(r.log) {
			if r.log[pos].Term != args.Entries[i].Term {
				r.log = r.log[:pos]
				break
			}
		} else {
			break
		}
	}
	for ; i < len(args.Entries); i++ {
		r.log = append(r.log, args.Entries[i])
	}
	r.persist()

	// Update commit index.
	if args.LeaderCommit > r.commitIndex {
		lastNew := len(r.log) - 1
		if args.LeaderCommit < lastNew {
			r.commitIndex = args.LeaderCommit
		} else {
			r.commitIndex = lastNew
		}
	}

	reply.Success = true
	reply.Term = r.currentTerm
	reply.MatchIndex = len(r.log) - 1
}

func (r *Raft) advanceCommitLocked() {
	// Commit entries replicated on a majority.
	n := len(r.peers)
	for idx := len(r.log) - 1; idx > r.commitIndex; idx-- {
		if r.log[idx].Term != r.currentTerm {
			continue
		}
		count := 1 // leader
		for pid := 1; pid <= n; pid++ {
			if pid == r.id {
				continue
			}
			if r.matchIndex[pid] >= idx {
				count++
			}
		}
		if count > n/2 {
			r.commitIndex = idx
			return
		}
	}
}
