# EVE physical component models

Place vendor or measured GLB files in this directory and start Vite with
`VITE_MODEL_BASE=/models`. The loader recognizes:

- `server-tray.glb`
- `nvlink-switch.glb`
- `power-shelf.glb`
- `bmc-module.glb`
- `network-module.glb`
- `generic-module.glb`

Models should be authored near a one-meter reference scale. EVE clones and
places them in the rack using the ADAM component slot and state; if a file is
missing, the dimensional procedural stand-in remains visible.
