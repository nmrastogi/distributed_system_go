package storage

import (
	"bytes"
	"encoding/gob"
	"sync"
)

type KVStore struct {
	mu sync.Mutex
	m  map[string]string
}

func NewKVStore() *KVStore {
	return &KVStore{m: make(map[string]string)}
}

func (kv *KVStore) Get(key string) (string, bool) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	v, ok := kv.m[key]
	return v, ok
}

func (kv *KVStore) Put(key, value string) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kv.m[key] = value
}

type PutArgs struct {
	Key   string
	Value string
}
type PutReply struct {
	OK    bool
	Error string
}

type GetArgs struct {
	Key string
}
type GetReply struct {
	Found bool
	Value string
}

type Command struct {
	Op    string
	Key   string
	Value string
}

func EncodePut(key, value string) []byte {
	var buf bytes.Buffer
	_ = gob.NewEncoder(&buf).Encode(Command{Op: "put", Key: key, Value: value})
	return buf.Bytes()
}

func ApplyToKV(kv *KVStore, cmdBytes []byte) {
	var cmd Command
	_ = gob.NewDecoder(bytes.NewReader(cmdBytes)).Decode(&cmd)
	if cmd.Op == "put" {
		kv.Put(cmd.Key, cmd.Value)
	}
}
