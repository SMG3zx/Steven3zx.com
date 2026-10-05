---
name: backend-worker
description: Go backend implementation worker for Janus platform
---

# Backend Worker

NOTE: Startup and cleanup are handled by `worker-base`. This skill defines the WORK PROCEDURE.

## When to Use This Skill

Use for features that involve Go backend work:
- Database migrations and schema changes
- Repository pattern implementation
- API handler changes
- Actor system modifications (GoAkt v4)
- Core domain logic
- Backend tests
- Integration with external services (Cloudflare, Stripe) on the backend side

## Work Procedure

1. **Read context**: Read `.factory/library/architecture.md` and `.factory/library/environment.md` for conventions and environment details. Read the feature description carefully.

2. **Understand existing code**: Before writing anything, read the relevant existing files in the area you're modifying. Understand current patterns, imports, and naming conventions. Match the existing style.

3. **Write tests first (TDD)**:
   - Create or update test files (`_test.go`) with failing tests that cover the feature's expected behavior.
   - Use table-driven tests where appropriate (idiomatic Go).
   - Run `cd E:\Janus\backend\janus-api; go test -count=1 -run TestYourNewTests ./path/to/package` to confirm tests fail (red).
   - For actor tests, use GoAkt TestKit (`testkit.New`, probes, `ExpectMessage`).

4. **Implement**:
   - Write idiomatic Go: proper error wrapping (`fmt.Errorf("context: %w", err)`), interfaces for dependencies, no exported globals.
   - DRY: extract shared logic into functions/methods. No duplicated SQL queries.
   - Efficient data structures: use maps for lookups, avoid linear scans on slices where a map is appropriate.
   - Repository pattern: SQL queries go in repository implementations, not in handlers or actors.
   - Follow existing package structure and naming.

5. **Verify tests pass (green)**:
   - Run `cd E:\Janus\backend\janus-api; go test -count=1 ./...` to ensure ALL tests pass (not just new ones).
   - Fix any failures before proceeding.

6. **Manual verification (REQUIRED for API changes)**:
   - For API changes: you MUST start the service and test with `curl.exe` (NOT PowerShell `curl`). Verify request/response matches expected behavior. Include at minimum: one success path and one error path per endpoint changed.
   - For database changes: verify migrations run up/down cleanly.
   - For actor changes: verify actor lifecycle (startup, message handling, shutdown) via tests.
   - Each verification MUST be recorded as an `interactiveChecks` entry in the handoff.
   - If manual verification is impossible (e.g., service won't start, external dependency unavailable), document the blocker explicitly in `whatWasLeftUndone` — do NOT claim `followedProcedure: true` without either performing or documenting why you couldn't.

7. **Run validators**:
   - Run: `cd E:\Janus\backend\janus-api; go vet ./...`
   - Run: `cd E:\Janus\backend\janus-api; go test -count=1 ./...`
   - All must pass.

8. **Update shared knowledge**: If you discover important patterns, gotchas, or environment details, update the relevant `.factory/library/` file.

## Example Handoff

```json
{
  "salientSummary": "Implemented repository pattern for projects domain: created ProjectRepository interface with 6 methods, concrete pgxProjectRepository implementation, migrated all raw SQL out of handlers into repository. All 12 existing tests pass plus 8 new table-driven tests for CRUD operations.",
  "whatWasImplemented": "ProjectRepository interface (Create, GetByID, GetBySlug, ListByOwner, Update, Delete) in internal/core/repositories.go. Concrete pgx implementation in internal/core/pg_project_repo.go. Updated handler/projects.go to use repository interface instead of direct SQL. Added 8 new tests in core/project_repo_test.go covering CRUD, not-found, duplicate-slug, and owner-filtering scenarios.",
  "whatWasLeftUndone": "",
  "verification": {
    "commandsRun": [
      { "command": "cd E:\\Janus\\backend\\janus-api; go test -count=1 ./...", "exitCode": 0, "observation": "All 6 packages pass, including 8 new tests" },
      { "command": "cd E:\\Janus\\backend\\janus-api; go vet ./...", "exitCode": 0, "observation": "No issues" }
    ],
    "interactiveChecks": [
      { "action": "curl.exe -X POST http://localhost:8080/api/v1/projects with valid payload", "observed": "201 Created with project JSON, proper error wrapping on duplicate slug returns 409" },
      { "action": "curl.exe http://localhost:8080/api/v1/projects/nonexistent-id", "observed": "404 with structured error {code: 'not_found', message: 'project not found'}" }
    ]
  },
  "tests": {
    "added": [
      { "file": "internal/core/project_repo_test.go", "cases": [
        { "name": "TestProjectRepo_Create", "verifies": "inserting new project returns ID and timestamps" },
        { "name": "TestProjectRepo_GetByID_NotFound", "verifies": "returns ErrNotFound for nonexistent ID" },
        { "name": "TestProjectRepo_ListByOwner", "verifies": "filters projects by owner_id correctly" }
      ]}
    ]
  },
  "discoveredIssues": []
}
```

## When to Return to Orchestrator

- Feature depends on a database table or migration that doesn't exist yet
- Actor system changes require coordination with another node role
- External service credentials are missing and feature can't be stubbed
- Existing test failures unrelated to this feature block go test ./...
- Requirements are ambiguous about API contract or behavior
