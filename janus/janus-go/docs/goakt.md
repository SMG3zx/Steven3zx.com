# GoAkt reference for Janus

Janus runs on [GoAkt](https://github.com/Tochemey/goakt) v4 (`github.com/tochemey/goakt/v4`, pinned in `go.mod`). This note records what GoAkt offers, the rules that matter for Janus, and where each feature is, or could be, used here. It was written from the documentation shipped inside the module (`docs/` in the module cache), so it describes the version Janus builds against.

Read "Rules that bite" first. Everything else is a catalogue.

## Rules that bite

1. **Never block inside `Receive`.** Actors do not own goroutines. A fixed pool of `max(GOMAXPROCS, 2)` dispatcher workers runs every actor, and a handler that blocks holds its worker until it returns. On a 2-CPU machine, two blocked handlers stall every actor, including the one that would unblock them. Blocking means: a mutex another goroutine may hold, a channel send or receive, `WaitGroup.Wait`, network, disk or database I/O, `time.Sleep`, and an `Ask` to an actor that may be waiting on you (A asks B while B asks A deadlocks).
   - Do the work with `ctx.PipeTo` / `ctx.PipeToName`; the result arrives as a message.
   - Or ask without blocking: `ctx.Request` / `ctx.RequestName` under a reentrancy policy, and continue in `Then`.
   - Or wait for a precondition with `ctx.Stash` and `ctx.UnstashAll`.
   - `PreStart` and `PostStop` are the exception. They run on the caller's goroutine, so blocking setup and teardown belong there.
   - `WithDispatcherPoolSize` raises the pool for systems whose handlers block by design. Prefer `PipeTo`; a pool much larger than `GOMAXPROCS` slows request/reply dispatch (about 40% on `Ask` benchmarks at 256 workers).
2. **A `PipeTo` task runs off the mailbox thread.** It must not read or write actor fields. Copy what it needs into locals, return a result message, and change state when the result arrives.
3. **`ctx.Response` belongs to one delivery.** `ReceiveContext` objects are pooled and recycled once `Receive` returns, so a goroutine must not call `ctx.Response` later. A reply that needs background work either uses `PipeTo` plus a follow-up message, or has the caller do the work itself. (Janus hit this: the Docker controller could not safely answer a `Run` request from a goroutine, so container runs are now plain function calls.)
4. **Messages are pure data.** No channels, mutexes, wait groups or callbacks inside a message. Locally messages pass by reference, so do not mutate one after sending it.
5. **An unsupervised error suspends the actor.** The default supervisor only has a directive for panics (`Stop`). An error recorded with `ctx.Err`, or a failed `ctx.Ask` inside `Receive`, has no directive, so the actor is suspended and handles no further user messages. Give actors that must keep running `WithAnyErrorDirective` or `WithDirective`, or set `WithDefaultSupervisor` on the system.
6. **`Stop`, `Shutdown`, eviction and a parent stopping its children do not wait for the message in hand.** `PostStop` can run while `Receive` is still handling a message, and queued user messages are discarded. `PoisonPill`, `ctx.Shutdown()` and per-actor passivation stop from the actor's own turn or when idle. State shared between `Receive` and `PostStop` must be safe for concurrent use.
7. **An Ask that expired is skipped, and `ctx.Context()` ends with it.** Pass `ctx.Context()` to database and HTTP calls inside a handler and they stop when nobody waits for the answer. A handler already running is not interrupted, so handlers whose callers retry must be idempotent.
8. **Per-sender FIFO only.** Messages from one sender to one receiver arrive in order. There is no ordering across senders.
9. **Initialise in `PreStart`, not in constructors, and keep state in unexported fields.** A restart reuses the same actor value and runs only `PreStart` again, so state that `PreStart` does not reset survives a restart.

## The model

- **Actor** (`PreStart`, `Receive`, `PostStop`): one message at a time, private state. At most one goroutine is inside a given actor's `Receive`.
- **ActorSystem**: the runtime. Hosts actors, routes messages, runs the scheduler, event stream, optional pub/sub, remoting and clustering, and coordinated shutdown. One per process.
- **PID**: the only actor reference. Local or remote, same `Tell`/`Ask`. `pid.Path()` gives `goakt://system@host:port/path`.
- **Hierarchy**: `/root` → `/system` (dead-letter, scheduler, topic actors) and `/user` (everything spawned). Stopping a parent stops its children first.
- **Mailbox**: one per actor; control messages (`PoisonPill`, supervision, termination) use a separate system mailbox that always has priority.
- **Dispatcher**: the worker pool. Each actor turn handles up to `WithThroughputBudget` messages (default 32) and then yields, so a hot actor cannot starve its peers.

## Actor features

### Spawning and naming
`Spawn`, `SpawnFromFunc`, `SpawnNamedFromFunc` (functional actors), `SpawnRouter`, `SpawnSingleton` (cluster), `SpawnOn` (cluster placement), `ctx.Spawn` / `SpawnChild` for children. A name is unique within its parent; spawning a name held by a running actor returns that actor (standalone) or fails with `ErrActorAlreadyExists`. `ActorOf`, `ActorExists`, `Actors`, `NumActors`, `Kill`, `ReSpawn` (restarts the subtree on the same values), `Inject` (dependency registry).

Spawn options: `WithSupervisor`, `WithMailbox`, `WithStashing`, `WithReentrancy`, `WithPassivationStrategy`, `WithLongLived`, `WithInitTimeout`, `WithDependencies`, `WithRole`, `WithRelocationDisabled`, `AsReliableProducer` / `AsReliableConsumer`.

### Lifecycle
States: running, stopping, suspended, passivating (`pid.IsRunning`, `IsSuspended`, `IsStopping`). `PostStart` is delivered first after every spawn or restart. A failed `PreStart` is retried up to `WithActorInitMaxRetries` (5) within the init timeout (`WithActorInitTimeout`, `WithInitTimeout`); the timeout does not interrupt an attempt in progress, so bound slow setup with your own deadline. A stopped actor's name is free as soon as the stop returns; a restart keeps the name.

### Messaging
- `Tell` (fire and forget), `Ask` (request/response, blocks, replies with `ctx.Response` exactly once), `SendAsync` / `SendSync` by name, `Request` / `RequestName` (non-blocking Ask), `Forward`, `BatchTell`, `BatchAsk`.
- `ctx.Sender()` is the sender PID; scheduled and external messages use `system.NoSender()`.
- Outside any actor: `actor.Tell(ctx, pid, msg)`, `actor.Ask(ctx, pid, msg, timeout)` or `system.NoSender()`.
- Errors: `ErrRequestTimeout` (same locally and remotely), `ErrDead` (send to a stopped actor; never supervised, no dead letter), `ErrMailboxFull`.
- `ctx.Unhandled()` marks a message nobody handles and publishes a `Deadletter` event.

### PipeTo
`ctx.PipeTo(to, task, opts...)` and `PipeToName` run `task func() (any, error)` on a goroutine and deliver the result to `to` (usually `ctx.Self()`). A failure goes to the dead-letter stream, so return failures inside the result message when the actor must react. Options: `WithTimeout`, `WithCircuitBreaker` (only one). Also `pid.PipeTo`. Local actors only (`ErrNotLocal` otherwise).

### Reentrancy
Opt in with `WithReentrancy(reentrancy.New(WithMode(...), WithMaxInFlight(n)))` or `ctx.EnableReentrancy` at runtime. Modes: `Off`, `AllowAll` (handle other messages while a request is in flight), `StashNonReentrant` (stash user messages until the request completes). `ctx.Request(pid, msg, WithRequestTimeout(d), WithReentrancyMode(m))` returns a `RequestCall`; `call.Then(func(resp any, err error))` runs on the mailbox thread, so it may touch actor state. `call.Cancel()`. Errors: `ErrReentrancyDisabled`, `ErrReentrancyInFlightLimit`, `ErrRequestTimeout`, `ErrRequestCanceled`. Passivation does not count an in-flight request as activity.

### Behaviors
`Become`, `BecomeStacked`, `UnBecomeStacked`, `UnBecome` swap the message handler (`type Behavior func(*ReceiveContext)`). Use for real state-machine transitions (not-yet-authenticated vs authenticated, circuit open vs closed), not for changing a value.

### Stashing
`WithStashing()`, then `ctx.Stash()`, `ctx.Unstash()`, `ctx.UnstashAll()`; `pid.StashSize()`. Stashed messages replay FIFO behind the queued ones. A restart keeps the stash; a stop drops it.

### Supervision
Strategies `OneForOne` (default) and `OneForAll`. Directives `Resume`, `Restart` (keeps mailbox and watchers, no `Terminated`), `Stop`, `Escalate` (delivers `PanicSignal` to the parent and leaves the child suspended). Configure with `supervisor.NewSupervisor(WithDirective(errType, d), WithAnyErrorDirective(d), WithRetry(max, window), WithExponentialBackoff(initial, max, resetAfter), WithStrategy(s))` and pass it with `actor.WithSupervisor`. A budget (`WithRetry`) suspends a crash-looping child instead of restarting it forever; `Reinstate` revives a suspended actor. `WithDefaultSupervisor` sets the system fallback. A panic is matched by the type it carries, like `ctx.Err`; unmatched panics fall under `PanicError` (`Stop`).

### Death watch
`ctx.Watch(pid)` / `UnWatch`. The watcher receives `*actor.Terminated` (`ActorPath()`, `TerminatedAt()`) after `PostStop` and after the name is free. A restart is not a death. A parent watches its children automatically. Watches are removed when the watcher stops.

### Mailboxes
`WithMailbox(m)`. Built in: `UnboundedMailbox` (default, lock-free), `UnboundedSegmentedMailbox`, `UnboundedFairMailbox` (round-robin across senders), `BoundedMailbox` (blocks a local producer when full; avoid), `NonBlockingBoundedMailbox` (drops to dead letters), `UnboundedPriorityMailBox`, `UnboundedStablePriorityMailbox`, `BoundedPriorityMailbox`, `BoundedStablePriorityMailbox`. Custom: implement `Mailbox` (`Enqueue`, `Dequeue`, `IsEmpty`, `Len`, `Dispose`) for many producers and one consumer.

### Passivation and eviction
Per actor: `WithPassivationStrategy(passivation.NewTimeBasedStrategy(d))`, `NewMessageCountBasedStrategy(n)`, `WithLongLived()`. Only idle actors passivate. System-wide: `WithEvictionStrategy(actor.NewEvictionStrategy(limit, LRU|LFU|MRU, percent), interval)` stops actors when the count exceeds a limit (can stop any user actor, and does not wait for idle).

### Routers
`SpawnRouter(ctx, name, poolSize, &Worker{}, opts...)`. Messages are wrapped in `actor.NewBroadcast(msg)`. Strategies: `RoundRobinRouting`, `RandomRouting`, `FanOutRouting` (default), consistent hash (`WithConsistentHashRouter(extractor)`, virtual nodes, custom hasher), scatter-gather-first (`AsScatterGatherFirst(within)`), tail-chopping (`AsTailChopping(within, interval)`). Routee failure: `WithStopRouteeOnFailure` (default), `WithRestartRouteeOnFailure(n, d)`, `WithResumeRouteeOnFailure`. Control messages `GetRoutees`, `AdjustRouterPoolSize`. Routers are not cluster-relocatable.

### Scheduling
`system.ScheduleOnce(ctx, msg, pid, delay)`, `Schedule(ctx, msg, pid, interval)`, `ScheduleWithCron(ctx, msg, pid, "sec min hour dom month dow [year]")` (Quartz format, local timezone standalone, UTC in a cluster, where `WithReference` is required and only one node fires each tick). `CancelSchedule`, `PauseSchedule`, `ResumeSchedule`, `ListSchedules`. Options `WithReference(id)`, `WithSender(pid)`. Fire-and-forget, no retry. Use it instead of sleep loops and `time.Ticker` goroutines.

### Singletons and relocation (cluster)
`SpawnSingleton` keeps one instance per name cluster-wide on the oldest node, with `WithSingletonRole`, `WithSingletonSupervisor`. Actors relocate to surviving nodes when a node leaves (graceful or crash, subject to replication settings); disable per actor with `WithRelocationDisabled`, or system-wide with `WithoutRelocation`. Dependencies must be serializable (`encoding.BinaryMarshaler`/`Unmarshaler`) to travel.

## Grains (virtual actors)
Identity-addressed actors that the framework activates on the first message and passivates when idle. Interface `OnActivate`, `OnReceive`, `OnDeactivate`. `actor.GrainOf[*MyGrain](ctx, system, "id")`, then `system.AskGrain` / `TellGrain` (`WithOneWay()` for fire and forget). `GrainContext` mirrors the actor context: `Response`, `NoErr`, `Err`, `AskGrain`, `TellGrain`, `AskActor`, `TellActor`, `RequestGrain`, `RequestActor`, `DeferResponse`, `PipeToSelf`/`PipeToGrain`/`PipeToActor` (failure arrives as `*StatusFailure`). Per-kind defaults with `WithGrainDefaultOptions[T]`; mailbox capacity, `WithGrainDeactivateAfter`, dependencies, eager or disabled relocation. Grain timers (`ctx.Schedule*`, `WithTimerKeepAlive`) live with the activation; `ScheduleGrain*` on the system survives passivation. `OnDeactivate` does not run on a crash, so do not rely on it as the only place state is saved. Janus has no per-identity workload today that needs this.

## System services

### Coordinated shutdown
`actor.WithCoordinatedShutdown(hooks...)` registers `ShutdownHook`s (`Execute(ctx, system) error`, `Recovery()`), run during `system.Stop` before actors stop. Recovery strategies `ShouldFail`, `ShouldRetryAndFail`, `ShouldSkip`, `ShouldRetryAndSkip`. `Stop` order: scheduler/eviction/passivation off, hooks, cluster snapshot, user actors depth-first, grains, system actors, leave cluster, stop remoting. Best effort; bounded by `WithShutdownTimeout`.

### Event stream
In-process feed of system events: `ActorStarted`, `ActorStopped`, `ActorChildCreated`, `ActorPassivated`, `ActorRestarted`, `ActorSuspended`, `ActorReinstated`, `Deadletter`, `NodeJoined`, `NodeLeft`, `LeaderChanged`, `RelocationStarted`, `RelocationFailed`. `sub, _ := system.Subscribe()`, then drain `sub.Iterator()` repeatedly (each call returns a closed snapshot channel), and `system.Unsubscribe(sub)`. Always on.

### Pub/sub
`actor.WithPubSub()` (or any cluster) starts a topic actor. `ctx.Tell(system.TopicActor(), actor.NewSubscribe("topic"))`, `NewPublish(id, "topic", msg)`; subscribers receive the payload directly. Deduplication window `WithMessageRetention` (2 min). `system.TopicStats`. Separate from the event stream.

### Observability
`system.Metric(ctx)` (deadletters, actors, uptime, memory) and `pid.Metric(ctx)` (processed, children, restarts, stash, deadletters, failures, unhandled, idle time). `actor.WithMetrics(opts...)` exports OpenTelemetry instruments (`actorsystem.*`, `actor.*`, `scheduler.*`, `cluster.*`, per-actor `actor.mailbox.size`, and more); initialise the OTel `MeterProvider` before the system starts, and use `WithLowCardinalityMetrics()` for one series per actor kind. Average mailbox wait follows from `actor_mailbox_size / rate(actor_processed_count)`. Traces: the separate `goakt-ebpf` sidecar.

### Extensions and dependencies
`WithExtensions(ext...)`: system-wide plugins with `ID() string`, read with `ctx.Extension(id)`. `WithDependencies(dep...)` at spawn: per-actor resources, serializable, read with `ctx.Dependency(id)`. Useful for handing a database handle or a configuration provider to actors without constructor wiring.

### Serialization
Remote messages only; local ones pass by reference. `ProtoSerializer` is the default for `proto.Message`; `remote.WithSerializers(new(T), remote.NewCBORSerializer())` or the JSON serializer for plain structs; both ends register the same types. Custom `Serializer` (`Serialize`, `Deserialize`) with the shared frame.

### Dead letters
Unhandled messages, failed `PipeTo` tasks, bounded-mailbox drops and grain one-way failures are published as `Deadletter` events and counted in metrics. A send to a stopped actor returns `ErrDead` and produces none.

### Testkit
`testkit.New(ctx, t)` gives a throwaway system; `kit.Spawn`, `kit.NewProbe(ctx)`, then `probe.Send`, `probe.ExpectMessage`, `ExpectMessageOfType`, `ExpectNoMessage`, `ExpectTerminated`, `ExpectChildSpawned`, with a 3 s default timeout. `GrainProbe` and `MultiNodes` for grains and clusters. Assert on messages instead of sleeping or polling.

## Remote and distributed features (not used by Janus today)
- **Remoting** (`WithRemote`, `remote.Config`): multiplexed TCP, TLS and mutual TLS, compression, lanes and ordering, large-message handling, `ContextPropagator` for trace context.
- **Client** (`client` package): talks to a cluster from a process that runs no actor system. `Tell`, `Ask`, `TellGrain`, `AskGrain`, `Spawn`, `ReSpawn`, `Stop`, `Reinstate`, `Exists`, `Kinds`; balancers round-robin, random, least-load.
- **Clustering** (`WithCluster`): Olric-backed registry, Memberlist membership, discovery providers (static, Kubernetes, NATS, mDNS, DNS-SD, Consul, etcd, self-managed), partition hashing (`hash.Hasher`, default xxh3), placement strategies (`RoundRobin`, `Random`, `Local`, `LeastLoad`, `HashBased`, roles), multi-datacenter control planes (NATS JetStream or etcd), leadership and relocation events.
- **Reliable delivery**: `AsReliableProducer("consumer")` / `AsReliableConsumer("producer", WithReliableFlowControlWindow(n))` give confirmed, ordered, flow-controlled point-to-point delivery with optional durable queues; work-pulling distributes jobs across a dynamic worker pool with redelivery when a worker is lost. Consumers must still be idempotent.
- **Distributed data** (`crdt`): `GCounter`, `PNCounter`, `LWWRegister`, `ORSet`, `ORMap`, `Flag`, `MVRegister`, replicated by the Replicator actor (`ClusterConfig.WithCRDT`), eventually consistent, with durable snapshots.
- **Reactive streams** (`stream`): lazy `Source[T]` → `Flow[In,Out]` → `Sink[T]` graphs where each stage is an actor with demand-driven backpressure. Sources (`Of`, `Range`, `Unfold`, `FromChannel`, actor, tick, network), flows (`Map`, `Filter`, windowing, rate control, stateful, parallel), fan-out and fan-in (`Broadcast`, `Balance`, `Partition`, `Merge`, `Zip`, ...), sub-flows, cross-node `SourceRef`/`SinkRef`, stage fusion, overflow strategies, metrics.
- **eGo** (separate module): event sourcing, durable state, CQRS projections and sagas on top of GoAkt.
- **AI-agent patterns**: an agent is an actor (or a grain per session) whose decision comes from a model; supervision handles failures, reentrancy handles tool calls.

## Where Janus stands

The scan covers `janus/janus-go` as of this note. "Used" means the code does it today.

| Feature | Status in Janus | Notes |
| --- | --- | --- |
| Actors, `Tell`/`Ask`, typed responses | Used | 16 actors, about 100 `Ask` calls, each followed by a hand-written type assertion. |
| `Unhandled` | Used | Every actor's default case. |
| Never block in `Receive` | **Violated** | Persistence runs SQL in its handler, and job workers ran a whole build or import inside `Receive` for up to ten minutes. Two such jobs hold every dispatcher worker on a two-CPU machine. Fixed for builds by running the job with `PipeTo`; imports follow in the projects change. |
| `PipeTo` | Not used before this work | Now runs build jobs; see `actors/wasm`. |
| Supervision | Not used | No `WithSupervisor` or `WithDefaultSupervisor`. A panic stops the actor silently, and an ordinary error from a failed `Ask` inside `Receive` suspends it. |
| Death watch | Not used | Nothing watches persistence, routes or the HTTP server. If one dies the process keeps serving errors. |
| Event stream and dead letters | Not used | Dead letters and actor failures are invisible. |
| Coordinated shutdown | Not used | The application actor stops things by hand in a fixed order; a table of groups can drive both start and stop. |
| Scheduler | Not used | No periodic work exists yet. The Wasmtime CPU-time sampler uses its own ticker goroutine. |
| Mailboxes | Default | The audit actor takes every event on an unbounded mailbox. Acceptable; revisit if audit volume grows. |
| Reentrancy | Not used | Would let the projects actor ask persistence without blocking. Not needed while persistence is local and fast. |
| Behaviors | Not used | The application actor's start/stop states are a candidate. |
| Stashing | Not used | No precondition waits today. |
| Routers | Not used | A pool of read-only persistence workers is the candidate if heavy file reads need their own path. |
| Passivation, eviction | Not used | All actors are long-lived services. |
| Extensions, dependencies | Not used | Actors receive their collaborators through constructors and a `Dependencies` struct. |
| OpenTelemetry metrics (`WithMetrics`) | Not used | Janus ships its own OTel setup in the telemetry actor; `WithMetrics` would add per-actor mailbox and failure series. Needs the meter provider before `Start`. |
| Testkit | Not used | Tests start a real system and poll with sleeps. New actor tests can use probes. |
| Remoting, clustering, CRDTs, streams | Not used | Single-node service. These matter once the Distributed Network (ENG-56) needs more than one control-plane node. |

See [the restructuring spec, ENG-61](https://linear.app/gateswaysolutions/issue/ENG-61) for how these findings feed the actor refactor.
