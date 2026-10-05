# authorized-user-cannot-cross-resource-boundary

## Evidence trail

Auth middleware supplies `AuthContext`; application use cases and HTTP handlers accept user identity while repository methods load projects, builds, deployments, operations, and credentials. The repository contains separate owner-aware project and resource operations but does not provide one compact policy document.

## Failure scenario

Two concurrent users create resources, then one replays or guesses an identifier through a different endpoint. The response must remain unauthorized/not found and must not leak or mutate the other user's state.

## Instrumentation status

No Antithesis assertions exist. Workload-level two-user checks are primary; optional SUT assertions can mark authorization decisions at repository boundaries.

## Investigation Log

- 2026-09-26: inspected auth context, auth middleware, project/build/deployment handlers and repository interfaces. Repository-only evidence does not settle whether projects are intentionally shareable across users or teams.

## Open Questions

`(needs human input)` Confirm tenancy/shareability policy for projects and deployments.
