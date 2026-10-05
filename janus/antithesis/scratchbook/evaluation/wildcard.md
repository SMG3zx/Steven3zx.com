---
sut_path: C:\Users\nerfs\Documents\Projects\Janus
commit: f899a3196ccaa07a107b372ec259cb9c8923e7df
updated: 2026-09-26
external_references: []
---

# Wildcard evaluation

The unusual cross-cutting risk is that API and worker are the same binary but different modes, while the root image installs an unusually broad build/runtime toolchain. This increases the chance that a setup passes basic health checks while a worker-only path fails because of mode-specific environment or tool availability. The first workload should exercise one worker path rather than treating API readiness as proof of worker readiness.

Another risk is that MinIO is modeled as a started dependency rather than a verified dependency. This is not just a topology issue: it can create misleading build/deployment failures that resemble queue bugs. The evaluation action is to make MinIO readiness explicit in setup and to retain separate artifact-boundary properties.
