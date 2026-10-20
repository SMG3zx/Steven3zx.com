# Hexagonal architecture (ports and adapters) and how to apply it in Go

Research for the janus-go restructuring. The question is what the original pattern actually says, what Go's own guidance says about the same problems, and where the two disagree, so that one standard can be written for this repository. The standard itself is `janus/janus-go/docs/architecture.md`; this note is the evidence behind it. Primary sources only. Fetched 2026-10-20.

## Answer

**The pattern is small and the source is short: it asks for an application that anything can drive and that talks to the world only through purpose-named ports, with an adapter per technology. It does not prescribe layers, folder names or a count of ports.** Go's official guidance agrees on almost everything that matters for structure (keep logic in `internal/`, put commands under `cmd/`, define interfaces where they are used, do not create them early), and disagrees in one place worth knowing about: whether to export fakes.

Each finding is tagged by where it comes from.

- **[Cockburn]** Alistair Cockburn, "Hexagonal architecture" (alistair.cockburn.us/hexagonal-architecture/), the original article by the pattern's author.
- **[go.dev layout]** "Organizing a Go module" (go.dev/doc/modules/layout), the Go project's layout guidance.
- **[CRC]** Go Wiki, "Go Code Review Comments" (go.dev/wiki/CodeReviewComments), the Go project's review conventions.
- **[Google style]** Google Go Style Guide, "decisions" and "best practices" (google.github.io/styleguide/go/decisions and /best-practices).

## What the pattern says

1. **Intent [Cockburn, The Pattern, Intent].** Users, programs, automated tests and batch scripts can drive the application equally, and the application can be developed and tested apart from its eventual devices and databases.
2. **Port and adapter [Cockburn, The Pattern; Nature of the Solution].** A port is a point where the application meets the outside world, and its protocol is set by the *purpose of the conversation*, usually written as an API. An adapter translates between a port's API and what one specific device or technology needs. A port usually has several adapters, and the article lists a real database alongside an in-memory one as the ordinary case.
3. **The number of ports is not fixed [Cockburn, Application Notes, How Many Ports?].** The author calls it largely a matter of taste, favours two to four, and says a "wrong" count does not cause real harm. The hexagon is only a drawing device: six sides have no meaning, and the shape exists to show the inside-outside asymmetry and leave room for more ports [Nature of the Solution].
4. **Primary and secondary sides [Cockburn, Application Notes, The Left-Right Asymmetry].** On the primary (driving) side the outside actor starts the conversation: a user, an HTTP client, a test script. On the secondary (driven) side the application starts it: a database, a message service. The terms come from primary and secondary actors in use cases. The article stresses that the inside-outside split is the real asymmetry.
5. **Use cases sit at the application boundary [Cockburn, Application Notes, Use Cases And The Application Boundary].** Business logic must not leak into UI or database code, and use cases are written independent of external technology.
6. **Tests are just another primary adapter [Cockburn, Motivation; Related Patterns].** Automated tests drive the application through its API with no UI or live database, with in-memory stand-ins for secondary actors. The article takes the idea of a "mock" for an in-memory stand-in and contrasts it with the mock-object movement's class-level focus.
7. **Swapping adapters rests on dependency inversion [Cockburn, Related Patterns, Dependency Inversion (Dependency Injection)].** The sample code constructs the application with a repository passed in, chosen by a factory as either a mock or the real one (Sample Code, Stage 3). The article cites dependency inversion and dependency injection as the mechanism.
8. **What the article does not say.** It gives no explicit dependency-direction rule, no folder layout, no naming scheme, and no guidance on how many methods a port should have. Those have to come from elsewhere or be chosen by the project.

## What Go's guidance says about the same problems

1. **Server project layout [go.dev layout, Server project].** Keep the server's Go logic in `internal/`, keep commands together under `cmd/` (one directory per program), and split anything useful to other projects into its own module. Start with packages in `internal/` and promote later if needed, because `internal/` code is not exposed or supported for outside use and can be refactored freely. The page gives no step-by-step procedure for separating a command from its logic beyond this.
2. **Interfaces belong to the consumer [CRC, Interfaces; Google style, Interfaces].** Define an interface in the package that uses its values, with only the methods that package calls; the implementing package returns concrete types. A producer may export an interface only when it is a shared protocol.
3. **Do not create interfaces early [CRC, Interfaces; Google style, Interfaces].** Do not define one before it has a real use, and do not wrap a client in an interface only for abstraction or testing. Keep interfaces small and document their contract, edge cases and errors.
4. **Test doubles [Google style, best practices, Test doubles and fakes].** Put test helpers in a separate package named after the production package plus `test` (the guide's example is `creditcardtest`), and name doubles by behaviour. Where practical, connect the real client to a test double of the *server* instead of hand-writing a client imitation. The same guide's "designing extensible validation APIs" section points at `fs.FS` and its validation helper as the model for a package that ships both an implementation and a way to check implementations.
5. **Tension with the pattern [Google style, decisions, Interfaces].** The same guide says not to export fake implementations or add back doors only for tests, and to test through the real implementation's public API. This reads against Cockburn's in-memory adapter as a routine second adapter. The two fit together if the in-memory adapter is a real, separate package that is a proper implementation of the port (as `testing/fstest` is for `fs.FS`) and not a hook inside the production package.
6. **Names and context [CRC, Package Names; Contexts].** Avoid meaningless package names such as `util`, `common`, `types`, `interfaces` and `model`; name packages for what they provide. Pass `context.Context` as the first parameter and do not store it in a struct, except where an interface signature forces it.
7. **Package size [Google style, best practices, Package size and organization].** Keep tightly coupled types together so they can share unexported detail; give conceptually distinct ideas their own small package; avoid both huge files and many tiny ones.

## Not verified

- The exact wording of Go's rule for who may import an `internal` package could not be read from the `cmd/go` page (the fetched text stopped before that section). The layout page confirms the effect that matters here: other modules cannot import it.
- Dependency direction, "the core imports no I/O package", is not in Cockburn. It is our rule, justified by the dependency-inversion pattern the article cites.
