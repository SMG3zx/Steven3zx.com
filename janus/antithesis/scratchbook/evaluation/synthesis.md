---
sut_path: C:\Users\nerfs\Documents\Projects\Janus
commit: f899a3196ccaa07a107b372ec259cb9c8923e7df
updated: 2026-09-26
external_references: []
---

# Property evaluation synthesis

## Refinements applied

1. Kept worker-loss recovery as a P0 liveness property and made tenant fault availability an explicit open question.
2. Kept object-store/database consistency separate from duplicate-delivery idempotency because neither implies the other.
3. Marked readiness instrumentation as partially present through ordinary HTTP checks, while recording that Antithesis assertions are missing.
4. Added explicit workload prerequisites for build and deployment paths so properties cannot pass vacuously.

## Gaps

- The first workload must exercise at least one real worker build and one deployment/runtime path.
- Setup must add MinIO health verification and preserve separate API/worker containers.

## Biases requiring human judgment

- The catalog favors backend recovery over frontend behavior. This is deliberate for the first Antithesis pass, but the user should redirect priorities if frontend workflow correctness is the primary product risk.
- Recovery properties depend on tenant fault configuration that is not visible in the repository.

## Result

The catalog is concrete enough for `antithesis-setup` and `antithesis-workload` to implement a minimal harness, subject to installing `snouty`, confirming fault availability, and choosing a small build fixture.

## Assumptions and Open Questions

### Assumptions

- No external references were consulted because the user explicitly limited scope to this directory.

### Open Questions

- Install or provide `snouty` before setup validation.
- Confirm tenant fault configuration and first supported build fixture.
