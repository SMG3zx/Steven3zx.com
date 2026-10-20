package main

import (
	"sync"
	"sync/atomic"
)

// Atomics fit a keyed store only in particular shapes, so three variants are
// compared and each states what it gives up.
//
// None of them has a critical section, so none can be run in the slow-io
// workload: there is no owner to hold for 25us. Work done in a CAS loop would
// run in parallel and be redone on conflict, which is optimistic concurrency,
// not a store bound by one database connection.

// slotsCAS: a fixed array of atomic pointers to immutable records. Reads are
// one atomic load. An update copies the record, applies the function and
// swaps the pointer with CompareAndSwap, retrying on a conflict. General (any
// update function) but needs dense, preallocated keys, and allocates a record
// per update attempt.
type slotsCAS struct{ slots []atomic.Pointer[Record] }

func newSlotsCAS(c Config) Store {
	s := &slotsCAS{slots: make([]atomic.Pointer[Record], c.Records)}
	preload(c.Records, func(id uint64, r Record) {
		rec := r
		s.slots[id].Store(&rec)
	})
	return s
}

func (s *slotsCAS) Get(id uint64) (Record, bool) {
	p := s.slots[id].Load()
	return *p, true
}

func (s *slotsCAS) Update(id uint64, fn func(*Record)) {
	slot := &s.slots[id]
	for {
		old := slot.Load()
		next := *old
		fn(&next)
		if slot.CompareAndSwap(old, &next) {
			return
		}
	}
}

// Scan reads each slot with an atomic load. It never blocks a writer or a
// reader, and it is not a snapshot.
func (s *slotsCAS) Scan(owner uint32) int {
	n := 0
	for i := range s.slots {
		if s.slots[i].Load().Owner == owner {
			n++
		}
	}
	return n
}

func (s *slotsCAS) Close() {}

// syncMapCAS: the same copy-on-write CAS idea over sync.Map, so keys need not
// be dense or known up front.
type syncMapCAS struct{ m sync.Map }

func newSyncMapCAS(c Config) Store {
	s := &syncMapCAS{}
	preload(c.Records, func(id uint64, r Record) {
		rec := r
		s.m.Store(id, &rec)
	})
	return s
}

func (s *syncMapCAS) Get(id uint64) (Record, bool) {
	v, ok := s.m.Load(id)
	if !ok {
		return Record{}, false
	}
	return *v.(*Record), true
}

func (s *syncMapCAS) Update(id uint64, fn func(*Record)) {
	for {
		v, ok := s.m.Load(id)
		var next Record
		if ok {
			next = *v.(*Record)
		}
		fn(&next)
		if !ok {
			if _, loaded := s.m.LoadOrStore(id, &next); !loaded {
				return
			}
			continue
		}
		if s.m.CompareAndSwap(id, v, &next) {
			return
		}
	}
}

func (s *syncMapCAS) Scan(owner uint32) int {
	n := 0
	s.m.Range(func(_, v any) bool {
		if v.(*Record).Owner == owner {
			n++
		}
		return true
	})
	return n
}

func (s *syncMapCAS) Close() {}

// slotsFetchAdd is the ceiling for what atomics can do, and it is NOT
// equivalent to the other variants: it ignores the update function and only
// supports the counter increment this workload happens to use, as an atomic
// add on one field of an in-place record. A real read-modify-write of a whole
// record cannot be done this way.
type counterSlot struct {
	owner   uint32
	version atomic.Uint64
	payload [48]byte
}

type slotsFetchAdd struct{ slots []counterSlot }

func newSlotsFetchAdd(c Config) Store {
	s := &slotsFetchAdd{slots: make([]counterSlot, c.Records)}
	preload(c.Records, func(id uint64, r Record) { s.slots[id].owner = r.Owner })
	return s
}

func (s *slotsFetchAdd) Get(id uint64) (Record, bool) {
	sl := &s.slots[id]
	return Record{Owner: sl.owner, Version: sl.version.Load(), Payload: sl.payload}, true
}

func (s *slotsFetchAdd) Update(id uint64, _ func(*Record)) { s.slots[id].version.Add(1) }

func (s *slotsFetchAdd) Scan(owner uint32) int {
	n := 0
	for i := range s.slots {
		if s.slots[i].owner == owner {
			n++
		}
	}
	return n
}

func (s *slotsFetchAdd) Close() {}
