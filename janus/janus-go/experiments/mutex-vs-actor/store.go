// PROTOTYPE: throwaway experiment, mutex-guarded store vs actor-owned store.
// Not production code. Wipe me.
package main

import "time"

// Record is what the stores hold: 64 bytes, copied by value on Get.
type Record struct {
	Owner   uint32
	Version uint64
	Payload [48]byte
}

// Config is what a variant builds a store from.
type Config struct {
	Records int
	// Work is spent inside the critical section (or inside the owning goroutine
	// or actor) on every Get and Update, to model a store whose operations cost
	// something, such as Janus's single database connection.
	Work time.Duration
}

// Store is the one interface every variant implements, so the workloads are
// identical code.
type Store interface {
	Get(id uint64) (Record, bool)
	Update(id uint64, fn func(*Record))
	// Scan counts the records of one owner. It visits everything, so it is the
	// slow operation that can block others.
	Scan(owner uint32) int
	Close()
}

// Variant is one contender.
type Variant struct {
	Name   string
	Family string // mutex, channel or actor
	New    func(Config) Store
}

var bump = func(r *Record) { r.Version++ }

// spin burns d of CPU. A sleep would be useless here: Windows timers are about
// a millisecond coarse, and a real store does CPU or syscall work, not sleep.
func spin(d time.Duration) {
	if d <= 0 {
		return
	}
	end := nanos() + int64(d)
	for nanos() < end {
	}
}

// shardOf spreads sequential ids evenly over n shards.
func shardOf(id uint64, n int) int {
	return int(((id * 0x9E3779B97F4A7C15) >> 32) % uint64(n))
}

func preload(n int, put func(id uint64, r Record)) {
	for id := 0; id < n; id++ {
		put(uint64(id), Record{Owner: uint32(id % 64)})
	}
}

// table is the owner-side logic shared by the channel and actor variants, so
// they run the same code as the mutex variants and differ only in how a caller
// reaches it.
type table struct {
	m    map[uint64]Record
	work time.Duration
}

func newTable(capacity int, work time.Duration) *table {
	return &table{m: make(map[uint64]Record, capacity), work: work}
}

type opKind uint8

const (
	opGet opKind = iota
	opUpdate
	opScan
)

type reply struct {
	rec Record
	ok  bool
	n   int
}

func (t *table) exec(op opKind, id uint64, fn func(*Record), owner uint32) reply {
	switch op {
	case opGet:
		spin(t.work)
		r, ok := t.m[id]
		return reply{rec: r, ok: ok}
	case opUpdate:
		spin(t.work)
		r := t.m[id]
		fn(&r)
		t.m[id] = r
		return reply{}
	default:
		n := 0
		for _, r := range t.m {
			if r.Owner == owner {
				n++
			}
		}
		return reply{n: n}
	}
}
