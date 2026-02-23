package raft

import (
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Fake in-process network
// ---------------------------------------------------------------------------

// fakeNetwork routes RPC calls directly between Raft instances in memory.
// Dropped pairs simulate network partitions or crashed nodes.
type fakeNetwork struct {
	mu      sync.Mutex
	nodes   map[int]*Raft
	dropped map[[2]int]bool // dropped[[from,to]] == true means calls are silently dropped
}

func newFakeNetwork() *fakeNetwork {
	return &fakeNetwork{
		nodes:   make(map[int]*Raft),
		dropped: make(map[[2]int]bool),
	}
}

func (fn *fakeNetwork) register(id int, r *Raft) {
	fn.mu.Lock()
	fn.nodes[id] = r
	fn.mu.Unlock()
}

func (fn *fakeNetwork) isolate(id, n int) {
	fn.mu.Lock()
	for i := 1; i <= n; i++ {
		fn.dropped[[2]int{id, i}] = true
		fn.dropped[[2]int{i, id}] = true
	}
	fn.mu.Unlock()
}

func (fn *fakeNetwork) heal(id, n int) {
	fn.mu.Lock()
	for i := 1; i <= n; i++ {
		delete(fn.dropped, [2]int{id, i})
		delete(fn.dropped, [2]int{i, id})
	}
	fn.mu.Unlock()
}

// fakeTransport implements Transporter and dispatches calls in-process.
type fakeTransport struct {
	network *fakeNetwork
	selfID  int
}

func (ft *fakeTransport) Call(peerID int, method string, args any, reply any) bool {
	ft.network.mu.Lock()
	dropped := ft.network.dropped[[2]int{ft.selfID, peerID}]
	target := ft.network.nodes[peerID]
	ft.network.mu.Unlock()

	if dropped || target == nil {
		return false
	}

	switch method {
	case "Raft.RequestVote":
		a := args.(*RequestVoteArgs)
		r := reply.(*RequestVoteReply)
		target.handleRequestVote(a, r)
		return true
	case "Raft.AppendEntries":
		a := args.(*AppendEntriesArgs)
		r := reply.(*AppendEntriesReply)
		target.handleAppendEntries(a, r)
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Cluster helper
// ---------------------------------------------------------------------------

type cluster struct {
	t     *testing.T
	nodes []*Raft
	net   *fakeNetwork
	n     int
}

func newCluster(t *testing.T, n int) *cluster {
	t.Helper()
	fn := newFakeNetwork()
	peers := make([]string, n) // addresses unused with fake transport

	nodes := make([]*Raft, n)
	for i := 0; i < n; i++ {
		id := i + 1
		ft := &fakeTransport{network: fn, selfID: id}
		r := NewRaft(Config{
			ID:        id,
			Peers:     peers,
			ApplyCh:   make(chan ApplyMsg, 256),
			Transport: ft,
		})
		fn.register(id, r)
		nodes[i] = r
	}

	for _, r := range nodes {
		r.Start()
	}
	return &cluster{t: t, nodes: nodes, net: fn, n: n}
}

func (c *cluster) stop() {
	for _, r := range c.nodes {
		r.Stop()
	}
}

// leader polls until exactly one Leader is found, or calls t.Fatal on timeout.
func (c *cluster) leader(timeout time.Duration) *Raft {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, r := range c.nodes {
			if r.Status().State == "Leader" {
				return r
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.t.Fatal("no leader elected within timeout")
	return nil
}

// waitCommit blocks until every node has CommitIndex >= index, or t.Fatal.
func (c *cluster) waitCommit(index int, timeout time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allDone := true
		for _, r := range c.nodes {
			if r.Status().CommitIndex < index {
				allDone = false
				break
			}
		}
		if allDone {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.t.Fatalf("entries not committed on all nodes within timeout (want commitIndex >= %d)", index)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestLeaderElection verifies that exactly one leader is elected.
func TestLeaderElection(t *testing.T) {
	c := newCluster(t, 3)
	defer c.stop()

	c.leader(2 * time.Second) // fails if none elected

	leaderCount := 0
	for _, r := range c.nodes {
		if r.Status().State == "Leader" {
			leaderCount++
		}
	}
	if leaderCount != 1 {
		t.Fatalf("expected exactly 1 leader, got %d", leaderCount)
	}
}

// TestLogReplication verifies that an entry submitted to the leader is
// committed on all nodes.
func TestLogReplication(t *testing.T) {
	c := newCluster(t, 3)
	defer c.stop()

	leader := c.leader(2 * time.Second)

	ok, err := leader.Submit([]byte("hello"))
	if !ok || err != nil {
		t.Fatalf("Submit failed: ok=%v err=%v", ok, err)
	}

	c.waitCommit(1, 2*time.Second)
}

// TestConcurrentSubmits fires 10 goroutines that each submit a command
// simultaneously, then waits for all entries to be committed everywhere.
func TestConcurrentSubmits(t *testing.T) {
	c := newCluster(t, 3)
	defer c.stop()

	leader := c.leader(2 * time.Second)

	const numCmds = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	successCount := 0

	for i := 0; i < numCmds; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, _ := leader.Submit([]byte{byte(i)})
			if ok {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if successCount == 0 {
		t.Fatal("all concurrent submits failed")
	}

	c.waitCommit(successCount, 3*time.Second)
}

// TestLeaderFailure isolates the current leader and verifies that the
// remaining two nodes elect a new leader and accept writes.
func TestLeaderFailure(t *testing.T) {
	c := newCluster(t, 3)
	defer c.stop()

	leader := c.leader(2 * time.Second)
	oldID := leader.Status().ID

	// Partition the leader out of the cluster.
	c.net.isolate(oldID, c.n)

	// Wait for a new leader among the surviving nodes.
	deadline := time.Now().Add(3 * time.Second)
	var newLeader *Raft
	for time.Now().Before(deadline) {
		for _, r := range c.nodes {
			s := r.Status()
			if s.State == "Leader" && s.ID != oldID {
				newLeader = r
			}
		}
		if newLeader != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if newLeader == nil {
		t.Fatal("no new leader elected after isolating old leader")
	}

	// Surviving cluster should still accept writes.
	ok, err := newLeader.Submit([]byte("post-failure"))
	if !ok || err != nil {
		t.Fatalf("Submit on new leader failed: ok=%v err=%v", ok, err)
	}
}

// TestPartitionAndRecover isolates a follower briefly, lets the rest of the
// cluster commit entries, then heals the partition and verifies the isolated
// node catches up to the same commit index.
func TestPartitionAndRecover(t *testing.T) {
	c := newCluster(t, 3)
	defer c.stop()

	leader := c.leader(2 * time.Second)

	// Pick a non-leader to isolate so the leader keeps running uninterrupted.
	isolatedID := 0
	for _, r := range c.nodes {
		if r.Status().ID != leader.Status().ID {
			isolatedID = r.Status().ID
			break
		}
	}

	// Isolate the follower for less than one election timeout (300 ms min),
	// so it won't bump its term and disrupt the leader when healed.
	c.net.isolate(isolatedID, c.n)

	// Commit 3 entries on the majority while the follower is partitioned.
	for i := 0; i < 3; i++ {
		leader.Submit([]byte{byte(i)})
	}
	time.Sleep(200 * time.Millisecond)

	// Heal and let the follower catch up.
	c.net.heal(isolatedID, c.n)

	c.waitCommit(3, 3*time.Second)
}

// TestPersistence stops a follower, restarts it with the same persist path,
// and verifies that currentTerm and the log are correctly restored.
func TestPersistence(t *testing.T) {
	persistDir := t.TempDir()
	fn := newFakeNetwork()
	peers := make([]string, 3)

	makeNode := func(id int) *Raft {
		path := ""
		if id == 2 {
			path = persistDir + "/raft2.gob"
		}
		ft := &fakeTransport{network: fn, selfID: id}
		r := NewRaft(Config{
			ID:            id,
			Peers:         peers,
			ApplyCh:       make(chan ApplyMsg, 256),
			Transport:     ft,
			StateFilePath: path,
		})
		fn.register(id, r)
		return r
	}

	r1 := makeNode(1)
	r2 := makeNode(2)
	r3 := makeNode(3)
	for _, r := range []*Raft{r1, r2, r3} {
		r.Start()
	}
	defer r1.Stop()
	defer r3.Stop()

	// Find leader and commit one entry.
	deadline := time.Now().Add(2 * time.Second)
	var leader *Raft
	for time.Now().Before(deadline) {
		for _, r := range []*Raft{r1, r2, r3} {
			if r.Status().State == "Leader" {
				leader = r
			}
		}
		if leader != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if leader == nil {
		t.Fatal("no leader elected")
	}

	leader.Submit([]byte("persist-me"))
	time.Sleep(300 * time.Millisecond) // let r2 receive and persist the entry

	savedTerm := r2.currentTerm
	savedLogLen := len(r2.log)
	if savedTerm == 0 {
		t.Fatal("expected non-zero term after election")
	}
	if savedLogLen < 2 {
		t.Fatalf("expected log length >= 2 (dummy + 1 entry), got %d", savedLogLen)
	}

	// Stop r2 and remove it from the network.
	r2.Stop()
	fn.register(2, nil)

	// Restart r2 from the same persist file.
	r2new := makeNode(2)

	if r2new.currentTerm != savedTerm {
		t.Errorf("restored term: got %d, want %d", r2new.currentTerm, savedTerm)
	}
	if len(r2new.log) != savedLogLen {
		t.Errorf("restored log length: got %d, want %d", len(r2new.log), savedLogLen)
	}

	r2new.Start()
	defer r2new.Stop()
}
