# E2E Pipeline Notes

## GitHub OAuth environment

- `backend/janus-api/internal/api/handler/github_oauth.go` reads `GITHUB_CLIENT_ID` and `GITHUB_CLIENT_SECRET` for the GitHub OAuth start/callback flow.

## Async operation-envelope routes

- The current backend routes that submit async operations are concentrated in `internal/api/handler/auth.go`, `projects.go`, and `builds_deployments.go`.
- Concrete mutation entrypoints currently include auth signup/signin/magic-code/reset flows, project create/update/delete, repo import, deployment create/delete, source-bundle uploads, and artifact uploads.

## Repo import contract

- The e2e-pipeline validation contract treats repo import as a GitHub HTTPS URL flow; generic parseable URIs such as `ftp://...` are not valid repo-import inputs.
- Host validation should use `parsedURL.Hostname()` with an exact `github.com` or `*.github.com` boundary check; a raw `HasSuffix(host, "github.com")` also accepts lookalike domains such as `evilgithub.com`.

## WASM build research source of truth

- `docs/wasm-language-support-research.md` is the reference for multi-language WASM build expectations.
- That research records containerized toolchains as a core requirement and calls out component-specific detection/build requirements for Python, JS/TS, and .NET instead of generic language-file detection.
