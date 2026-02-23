package raft

type RequestVoteArgs struct {
	Term         int
	CandidateID  int
	LastLogIndex int
	LastLogTerm  int
}

type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

type AppendEntriesArgs struct {
	Term         int
	LeaderID     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term       int
	Success    bool
	MatchIndex int
}

type rpcAPI struct {
	r *Raft
}

func (r *Raft) RPC() *rpcAPI {
	return &rpcAPI{r: r}
}

func (api *rpcAPI) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) error {
	api.r.handleRequestVote(args, reply)
	return nil
}

func (api *rpcAPI) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) error {
	api.r.handleAppendEntries(args, reply)
	return nil
}
