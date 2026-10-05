# Future Direction

This document is for medium-term direction, not current implementation truth.

## Current Implementation

The production application is the Rust Actix Web binary in `janus-rust`; it embeds the HTML/CSS/JavaScript frontend from `janus-rust/web`. Local development commands are provided by the root `Janus.js` Bun CLI.

## Platform Direction

Janus is moving toward a broader code-to-runtime control plane that can support:

- richer deployment targets
- stronger usage metering and billing
- custom domain automation
- local runner / reverse tunnel support
- distributed compute scenarios

## Guardrails

Near-term feature work should preserve these assumptions:

- one supported backend binary with separate API and worker modes
- Valkey-backed worker orchestration with PostgreSQL as the durable source of truth
- durable operation/build/deployment state
- runtime portability and billing dimensions that do not hardcode a single-host future
