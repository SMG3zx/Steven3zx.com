# janus-go coding standards

Read during review. Each rule is a judgement call that no tool can check. Mechanical rules (dependency direction, banned imports and calls) are enforced by checks, not listed here. The reasons and the detail for every rule are in [docs/architecture.md](docs/architecture.md); the section numbers below point there.

- **Declare an interface in the package that uses it,** with only the methods that package calls, unexported when only that package needs to name it, and document its contract: ordering, errors, what absence looks like, concurrency. A service exports no port. (section 1)
- **Keep rules that more than one service needs in `domain`.** A client or a handler translates and contains none. (section 1)
- **Pass the clock, configuration and other effects into a service through its `Config`,** as values; it reads no environment variable. (section 8)
- **Release every lock before doing I/O or sending a NATS request,** and give each goroutine an owner that can wait for it. (section 10)
- **Test a service through its contract,** on an embedded server from `natstest`, and wait on a reply, an event or a job's final status, never on `time.Sleep`. (section 11)
- **Match the surrounding package:** naming, error wording and comment density follow the code already there.
