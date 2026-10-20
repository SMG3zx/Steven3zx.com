package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	golog "github.com/tochemey/goakt/v4/log"
)

// request is pooled together with its reply channel, so a call allocates
// nothing. A request is used by one caller at a time and its reply channel has
// room for the answer, so the owner never blocks sending it.
type request struct {
	op    opKind
	id    uint64
	fn    func(*Record)
	owner uint32
	reply chan reply
}

var requestPool = sync.Pool{New: func() any { return &request{reply: make(chan reply, 1)} }}

// ---- channel owners: the actor model without a framework -------------------

type chanOwner struct {
	in   chan *request
	t    *table
	done chan struct{}
}

func (o *chanOwner) run() {
	for q := range o.in {
		q.reply <- o.t.exec(q.op, q.id, q.fn, q.owner)
	}
	close(o.done)
}

type chanStore struct{ owners []*chanOwner }

// newChanStore starts n goroutines that each own a slice of the keys and take
// requests from a deep channel. n=1 is one owner for everything.
func newChanStore(n int) func(Config) Store {
	return func(c Config) Store {
		s := &chanStore{}
		for i := 0; i < n; i++ {
			s.owners = append(s.owners, &chanOwner{in: make(chan *request, 4096), t: newTable(c.Records/n+1, c.Work), done: make(chan struct{})})
		}
		preload(c.Records, func(id uint64, r Record) { s.owners[shardOf(id, n)].t.m[id] = r })
		for _, o := range s.owners {
			go o.run()
		}
		return s
	}
}

func (s *chanStore) call(o *chanOwner, op opKind, id uint64, fn func(*Record), owner uint32) reply {
	q := requestPool.Get().(*request)
	q.op, q.id, q.fn, q.owner = op, id, fn, owner
	o.in <- q
	r := <-q.reply
	q.fn = nil
	requestPool.Put(q)
	return r
}

func (s *chanStore) Get(id uint64) (Record, bool) {
	r := s.call(s.owners[shardOf(id, len(s.owners))], opGet, id, nil, 0)
	return r.rec, r.ok
}

func (s *chanStore) Update(id uint64, fn func(*Record)) {
	s.call(s.owners[shardOf(id, len(s.owners))], opUpdate, id, fn, 0)
}

func (s *chanStore) Scan(owner uint32) int {
	n := 0
	for _, o := range s.owners {
		n += s.call(o, opScan, 0, nil, owner).n
	}
	return n
}

func (s *chanStore) Close() {
	for _, o := range s.owners {
		close(o.in)
		<-o.done
	}
}

// ---- goakt actors ----------------------------------------------------------

// askMsg is the request of the Ask variants: a fresh value per call, as Janus
// writes it today.
type askMsg struct {
	op    opKind
	id    uint64
	fn    func(*Record)
	owner uint32
}

type tableActor struct{ t *table }

func (a *tableActor) PreStart(*actor.Context) error { return nil }
func (a *tableActor) PostStop(*actor.Context) error { return nil }

func (a *tableActor) Receive(ctx *actor.ReceiveContext) {
	switch m := ctx.Message().(type) {
	case *request:
		m.reply <- a.t.exec(m.op, m.id, m.fn, m.owner)
	case *askMsg:
		r := a.t.exec(m.op, m.id, m.fn, m.owner)
		ctx.Response(&r)
	default:
		ctx.Unhandled()
	}
}

// ActorParams are the knobs the tuning pass sweeps.
type ActorParams struct {
	Shards     int    // actors that own the keys; 1 is the Janus layout
	Ask        bool   // request/reply with actor.Ask, else Tell plus a pooled reply channel
	Mailbox    string // "default" or "segmented"
	Throughput int    // dispatcher turn budget
}

var (
	sysMu   sync.Mutex
	systems = map[int]actor.ActorSystem{}
	actorID atomic.Int64
)

// systemFor returns one running actor system per throughput budget. The budget
// is a system option, so variants that differ in it need different systems.
func systemFor(budget int) actor.ActorSystem {
	sysMu.Lock()
	defer sysMu.Unlock()
	if s, ok := systems[budget]; ok {
		return s
	}
	s, err := actor.NewActorSystem(fmt.Sprintf("mva-%d", budget), actor.WithLogger(golog.DiscardLogger), actor.WithThroughputBudget(budget))
	if err != nil {
		panic(err)
	}
	if err = s.Start(context.Background()); err != nil {
		panic(err)
	}
	systems[budget] = s
	return s
}

func stopSystems() {
	sysMu.Lock()
	defer sysMu.Unlock()
	for _, s := range systems {
		_ = s.Stop(context.Background())
	}
}

type actorStore struct {
	pids []*actor.PID
	ask  bool
}

func newActorStore(p ActorParams) func(Config) Store {
	return func(c Config) Store {
		sys := systemFor(p.Throughput)
		tables := make([]*table, p.Shards)
		for i := range tables {
			tables[i] = newTable(c.Records/p.Shards+1, c.Work)
		}
		preload(c.Records, func(id uint64, r Record) { tables[shardOf(id, p.Shards)].m[id] = r })
		s := &actorStore{ask: p.Ask}
		for i := range tables {
			var opts []actor.SpawnOption
			if p.Mailbox == "segmented" {
				opts = append(opts, actor.WithMailbox(actor.NewUnboundedSegmentedMailbox()))
			}
			pid, err := sys.Spawn(context.Background(), fmt.Sprintf("table-%d", actorID.Add(1)), &tableActor{t: tables[i]}, opts...)
			if err != nil {
				panic(err)
			}
			s.pids = append(s.pids, pid)
		}
		return s
	}
}

func (s *actorStore) call(pid *actor.PID, op opKind, id uint64, fn func(*Record), owner uint32) reply {
	if s.ask {
		resp, err := actor.Ask(context.Background(), pid, &askMsg{op: op, id: id, fn: fn, owner: owner}, 10*time.Second)
		if err != nil {
			panic(err)
		}
		return *(resp.(*reply))
	}
	q := requestPool.Get().(*request)
	q.op, q.id, q.fn, q.owner = op, id, fn, owner
	if err := actor.Tell(context.Background(), pid, q); err != nil {
		panic(err)
	}
	r := <-q.reply
	q.fn = nil
	requestPool.Put(q)
	return r
}

func (s *actorStore) Get(id uint64) (Record, bool) {
	r := s.call(s.pids[shardOf(id, len(s.pids))], opGet, id, nil, 0)
	return r.rec, r.ok
}

func (s *actorStore) Update(id uint64, fn func(*Record)) {
	s.call(s.pids[shardOf(id, len(s.pids))], opUpdate, id, fn, 0)
}

func (s *actorStore) Scan(owner uint32) int {
	n := 0
	for _, pid := range s.pids {
		n += s.call(pid, opScan, 0, nil, owner).n
	}
	return n
}

func (s *actorStore) Close() {
	for _, pid := range s.pids {
		_ = pid.Shutdown(context.Background())
	}
}
