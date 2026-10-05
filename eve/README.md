# EVE warehouse world

Run `npm install`, then `npm run dev -- --host 127.0.0.1`. `npm run build` creates the production build. Run `npm test` for ADAM adapter and legacy feed contract tests.

The browser starts at a cinematic welcome menu over the same world used inside. Enter the warehouse to search destinations, select assets, and use contextual details. Scroll changes perspective: 3D below 22 m camera distance, 2.5D between 22 and 48 m, and an orthographic 2D floor plan above 48 m. Buttons also select each view. Free cam uses drag to look, WASD to move, Q/E to descend/ascend, Shift to speed up, and Escape to return to orbit.

Geometry is in meters; the human reference is exactly 1.8288 m (six feet). The 40 × 30 m floor and 0.8 × 1.2 × 2.2 m equipment proxies are illustrative, not surveyed. Locations are arranged schematically by pod and location code, not physical coordinates. Pods occupy parallel horizontal lanes; each pod alternates its locations into two rows around a central aisle, and rack faces point away from the pod centerline. Component slots are schematic inventory cells, not verified rack-unit positions. Free cam has no collision physics. Profiles are browser-local; news contains built-in product notes.

## Local ADAM preview

The Vite development bridge reads `ADAM_JSON_SCHEMA_6V.json`, `ADAM_JSON_SCHEMA_6W.json`, and `ADAM/{A.D.A.M.} (Advanced Diagnostic Assistance Monitor).html` from `D:/LinuxBackup/Documents/Team_Quant`. Override that directory with the shell environment variable `EVE_ADAM_ROOT` before starting Vite. It extracts the 96 configured locations from the saved HTML and serves the snapshots at `/api/adam-snapshot`. Source files are not modified, copied into public/, or included in production builds. Keep this development server bound to localhost.

The preview displays historical ADAM observations: 50 L11 racks and 12 L10 stations across 6V/6W in the current source files. Search by pod, location, model, rack serial, or component serial. Select equipment to inspect component slots, raw stage/station/status/result, timestamps and recorded stage results. Filter components to failures only. Null results are unknown, and SPACE records are empty component slots, not installed equipment. Completion percentages are withheld until model-specific test weights are provided.

## Connect a current ADAM feed

Set `VITE_WORLD_URL=/api/world` in `.env.local`, then restart/rebuild. The production backend must provide the following wrapper around ADAM responses. The development bridge is not a production API. Use a same-origin authenticated backend; do not put secrets in Vite environment variables.

```json
{
  "source": "live",
  "locations": ["6W101", "6W102"],
  "snapshots": [
    {
      "metadata": { "searchParameter": "location:6W*", "limited": false, "count": 1 },
      "data": {
        "6W101": {
          "LOC": "6W101",
          "RACK_SN": "RACK-SERIAL",
          "TYPE": "L11",
          "MODEL": "A5L_PY",
          "SUB_MODEL": "JUICEBOX48.GB300",
          "TIMESTAMP": "2026-09-16T20:00:00Z",
          "UUTS": {
            "F20": {
              "TYPE": "SERVER",
              "SN": "UNIT-SERIAL",
              "RESULT": "RUNNING",
              "STAGE": "RUNIN",
              "RESULTS": {}
            }
          }
        }
      }
    }
  ]
}
```

Polling occurs every two minutes after each response. Complete responses require a pod-scoped searchParameter, limited=false, and a matching count; only that pod's omitted occupants can be removed. Partial responses retain absent occupants; explicit replacements and rack moves update identity/location. Older pod observations are ignored. Request/validation failures preserve the last valid world. Unknown source timezone stays unknown; explicit timezone-aware collection times older than five minutes are stale. Fetched time and operational event time are distinct. Set source=historical for archived data.

The active modules are `src/domain/adam/` (normalization/feed), `src/ui/rack-ui.js` (inspection), and `src/app/eve.js` (world composition). `src/runtime/world.mjs` is the EVE facade over the shared Hermes runtime in `Hermes/Hermes.js`. Root-level source modules are compatibility entry points; `src/app/legacy-dashboard.js` preserves the earlier prototype and is not loaded by the active entry. Next integration inputs are verified facility/model geometry, test station weights, and authenticated operational log endpoints; no power or repair actions are wired up.

# Architecture

EVE is organized as an ECS-driven world with a Three.js render layer:

- `Hermes/Hermes.js` provides typed ECS storage, scheduling, structural mutation, and diagnostics.
- `src/runtime/world.mjs` adapts Hermes to EVE's ADAM-rich records.
- `src/domain/ecs/` defines EVE component and system helpers.
- `src/app/eve.js` composes world state with Three.js presentation objects; meshes are views, not authoritative state.
