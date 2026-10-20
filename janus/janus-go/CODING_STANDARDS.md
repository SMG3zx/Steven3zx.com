# janus-go coding standards

Read during review. Each rule is a judgement call that no tool can check. Mechanical rules (dependency direction, banned imports and calls) are enforced by checks, not listed here. The reasons and the detail for every rule are in [docs/architecture.md](docs/architecture.md); the section numbers below point there.

- **Name a port for the conversation**, such as `Store` or `Builder`, never for the technology behind it. (section 5)
- **Give a port two adapters, one real and one test,** or leave it a concrete type. (section 5)
- **Declare an interface in the package that uses it,** with only the methods that package calls, and document its contract: ordering, errors, what absence looks like, concurrency. (section 5)
- **Keep business rules in `domain` or a use case.** An adapter translates and contains none. (sections 6 and 7)
- **Pass the clock, configuration and other effects into a use case** as arguments or constructor values. (section 6)
- **Release every lock before calling a port,** and give each goroutine an owner that can wait for it. (section 8)
- **Write a test adapter as a real implementation** in its `<port>test` package, and run the port's contract suite against it. (section 9)
- **Match the surrounding package:** naming, error wording and comment density follow the code already there.
