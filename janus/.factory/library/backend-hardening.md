# Backend Hardening Notes

## Current backend state

- Repository interfaces are implemented in `backend/janus-api/internal/core/repositories.go`; handlers, the API server, and actors depend on `core.DataStore` instead of `*core.State` directly.
- Embedded SQL migrations live in `backend/janus-api/internal/core/migrations/*.sql` and are applied by `backend/janus-api/internal/core/migrate.go` through golang-migrate.

## Migration implementation gotchas

- The embedded golang-migrate database driver for pgx v5 is `github.com/golang-migrate/migrate/v4/database/pgx/v5`.
- `//go:embed` can only include files at or below the embedding package, so migration SQL must stay under `backend/janus-api/internal/core/` (currently `internal/core/migrations`).

## Common backend validation commands

- Tests: `cd E:\Janus\backend\janus-api; go test -count=1 -parallel 4 ./...`
- Build/typecheck: `cd E:\Janus\backend\janus-api; go build ./...`
- Vet/lint: `cd E:\Janus\backend\janus-api; go vet ./...`

## Repository test harness

- As of backend-hardening scrutiny round 3, checked-in repository-operation coverage lives in `backend/janus-api/internal/core/state_repo_test.go`.
- `backend/janus-api/internal/core/state.go` defines `State.db` as `*sql.DB`, so repository tests for this layer should use a `database/sql`-compatible mock such as `github.com/DATA-DOG/go-sqlmock` unless the repository implementation is refactored away from `*sql.DB`.
- The current repository suite directly exercises `CreateUser`, `FindUserByEmail`, `CreateProject`, `UpdateProject`, `DeleteProject`, `ClaimBuildJob`, `ClaimDeployment`, and `CreateOperation` through `State` methods; scan/helper-only tests are not sufficient evidence for this scrutiny requirement.

## `/readyz` response shape

- The HTTP response always includes `ok`, `checks`, and `ts`.
- Database readiness is reported as `checks.database`.
- If no actor node is attached, the API reports `checks.actors = "down"`.
- If an actor node is attached, actor readiness checks are exposed as `checks.actors.actor_system`, `checks.actors.command_gateway`, and other `actors.*` entries returned by `Node.ReadyChecks()`.
