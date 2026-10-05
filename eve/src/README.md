# EVE source layout

The browser entry point is `app/eve.js`. It owns the Three.js scene and UI
composition.

- `runtime/` — Hermes browser entry point and EVE world facade.
- `domain/adam/` — ADAM snapshot normalization and live-feed integration.
- `domain/warehouse/` — measured/base warehouse geometry definitions.
- `domain/ecs/` — domain component and system definitions.
- `rendering/` — Three.js physical-model loading and render helpers.
- `ui/` — rack and component inspection UI.
- `styles/` — application stylesheets.

The small root-level `.mjs`/`.js` files are compatibility entry points for
existing imports. New code should import from the responsibility-specific
directories above.

Hermes is the authoritative numeric simulation state. Rich ADAM records remain
available through the world facade for UI metadata while Three.js stays a
presentation layer.
