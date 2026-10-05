import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';

// Drop measured/vendor GLB files into public/models and set VITE_MODEL_BASE.
// Until then, EVE uses the dimensional procedural stand-ins in eve.js.
export const MODEL_MANIFEST = Object.freeze({
  SERVER: 'server-tray.glb',
  COMPUTE_TRAY: 'gb300-compute-tray.glb',
  GRACE_CPU: 'grace-cpu.glb',
  BLACKWELL_GPU: 'blackwell-gpu.glb',
  CONNECTX8: 'connectx8-mezzanine.glb',
  BLUEFIELD3: 'bluefield3-dpu.glb',
  NVSWITCH: 'nvlink-switch.glb',
  NVSWITCH_ASIC: 'nvswitch-asic.glb',
  POWERSHELF: 'power-shelf.glb',
  BMC: 'bmc-module.glb',
  NETWORK: 'network-module.glb',
  UNKNOWN: 'generic-module.glb',
});

const loader = new GLTFLoader();
const cache = new Map();
export function loadPhysicalModel(type) {
  const base = import.meta.env.VITE_MODEL_BASE;
  const file = MODEL_MANIFEST[type] || MODEL_MANIFEST.UNKNOWN;
  if (!base) return Promise.resolve(null);
  const url = `${base.replace(/\/$/, '')}/${file}`;
  if (!cache.has(url))
    cache.set(
      url,
      new Promise((resolve) =>
        loader.load(
          url,
          (result) => resolve(result.scene),
          () => resolve(null),
        ),
      ),
    );
  return cache.get(url).then((scene) => scene?.clone(true) || null);
}
