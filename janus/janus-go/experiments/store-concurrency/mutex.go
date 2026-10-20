package main

import "sync"

// coarse: one sync.Mutex over a map. The naive baseline.
type coarse struct {
	mu   sync.Mutex
	m    map[uint64]Record
	work Config
}

func newCoarse(c Config) Store {
	s := &coarse{m: make(map[uint64]Record, c.Records), work: c}
	preload(c.Records, func(id uint64, r Record) { s.m[id] = r })
	return s
}

func (s *coarse) Get(id uint64) (Record, bool) {
	s.mu.Lock()
	spin(s.work.Work)
	r, ok := s.m[id]
	s.mu.Unlock()
	return r, ok
}

func (s *coarse) Update(id uint64, fn func(*Record)) {
	s.mu.Lock()
	spin(s.work.Work)
	r := s.m[id]
	fn(&r)
	s.m[id] = r
	s.mu.Unlock()
}

func (s *coarse) Scan(owner uint32) int {
	s.mu.Lock()
	n := 0
	for _, r := range s.m {
		if r.Owner == owner {
			n++
		}
	}
	s.mu.Unlock()
	return n
}

func (s *coarse) Close() {}

// rw: one sync.RWMutex, so reads run in parallel with each other.
type rw struct {
	mu   sync.RWMutex
	m    map[uint64]Record
	work Config
}

func newRW(c Config) Store {
	s := &rw{m: make(map[uint64]Record, c.Records), work: c}
	preload(c.Records, func(id uint64, r Record) { s.m[id] = r })
	return s
}

func (s *rw) Get(id uint64) (Record, bool) {
	s.mu.RLock()
	spin(s.work.Work)
	r, ok := s.m[id]
	s.mu.RUnlock()
	return r, ok
}

func (s *rw) Update(id uint64, fn func(*Record)) {
	s.mu.Lock()
	spin(s.work.Work)
	r := s.m[id]
	fn(&r)
	s.m[id] = r
	s.mu.Unlock()
}

func (s *rw) Scan(owner uint32) int {
	s.mu.RLock()
	n := 0
	for _, r := range s.m {
		if r.Owner == owner {
			n++
		}
	}
	s.mu.RUnlock()
	return n
}

func (s *rw) Close() {}

// shardedRW: keys hash to one of n shards, each its own RWMutex and map, padded
// to a cache line pair so neighbouring shards do not share a line. This is the
// best a mutex can do for a keyed store, and it is the one to beat.
type shard struct {
	mu sync.RWMutex
	m  map[uint64]Record
	_  [96]byte
}

type shardedRW struct {
	shards []shard
	work   Config
}

func newShardedRW(n int) func(Config) Store {
	return func(c Config) Store {
		s := &shardedRW{shards: make([]shard, n), work: c}
		for i := range s.shards {
			s.shards[i].m = make(map[uint64]Record, c.Records/n+1)
		}
		preload(c.Records, func(id uint64, r Record) { s.shards[shardOf(id, n)].m[id] = r })
		return s
	}
}

func (s *shardedRW) Get(id uint64) (Record, bool) {
	sh := &s.shards[shardOf(id, len(s.shards))]
	sh.mu.RLock()
	spin(s.work.Work)
	r, ok := sh.m[id]
	sh.mu.RUnlock()
	return r, ok
}

func (s *shardedRW) Update(id uint64, fn func(*Record)) {
	sh := &s.shards[shardOf(id, len(s.shards))]
	sh.mu.Lock()
	spin(s.work.Work)
	r := sh.m[id]
	fn(&r)
	sh.m[id] = r
	sh.mu.Unlock()
}

// Scan visits one shard at a time, so it is not a consistent snapshot, and the
// sharded actor and channel variants scan the same way.
func (s *shardedRW) Scan(owner uint32) int {
	n := 0
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.RLock()
		for _, r := range sh.m {
			if r.Owner == owner {
				n++
			}
		}
		sh.mu.RUnlock()
	}
	return n
}

func (s *shardedRW) Close() {}
