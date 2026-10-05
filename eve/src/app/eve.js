import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { HUMAN_HEIGHT, startAdamFeed, STALE_AFTER_MS } from '../domain/adam/snapshot.mjs';
import { installAdamUI, renderRackDetails } from '../ui/rack-ui.js';
import { loadPhysicalModel } from '../rendering/component-models.mjs';
import { rackComposition } from '../rendering/rack-compositions.mjs';
import { createWorld } from '../runtime/world.mjs';
import * as C from '../domain/ecs/components.mjs';
import { statusAgeSystem } from '../domain/ecs/systems.mjs';
import '../styles/eve.css';
import '../styles/adam.css';

const $ = (id) => document.getElementById(id);
document.querySelector('#app').innerHTML = `
  <div id="viewport" aria-label="Interactive warehouse world"></div>
  <header class="hud"><button id="home" class="wordmark" aria-label="Open home menu">EVE<span>WAREHOUSE WORLD</span></button><div class="feed"><i id="signal"></i><span id="feedStatus" role="status">CONNECTING</span><small id="freshness"></small></div><button id="profileButton" class="avatar" aria-label="Open profile">ME</button></header>
  <section id="welcome" class="welcome"><div class="welcome-copy"><div class="eyebrow">YOUR WORLD. IN VIEW.</div><h1>A new perspective<br>on your warehouse.</h1><p>Step inside. Explore every aisle.<br>Stay connected to what’s happening on the floor.</p><div class="welcome-menu"><button id="enter" class="primary">Enter warehouse <span>↗</span></button><button id="newsButton">News & updates <span>02</span></button><button id="settingsButton">World settings <span>⚙</span></button></div><div class="welcome-note"><span class="tiny-cross">+</span> ONE WORLD · EVERY PERSPECTIVE</div></div><div class="tour-label"><i></i><span>WAREHOUSE OVERLOOK<small id="tourSource">Shared world · cinematic camera</small></span></div></section>
  <div id="worldUI" hidden><div class="world-toolbar"><button id="destinationsToggle" aria-expanded="false" aria-controls="destinations">☰ <span>Destinations</span></button><div class="breadcrumb">WAREHOUSE <span>/</span> <b id="location">Overview</b></div></div>
  <aside id="destinations" class="drawer" hidden><div class="panel-heading"><h2>Go somewhere</h2><button id="closeDrawer" aria-label="Close destinations">×</button></div><input id="search" type="search" placeholder="Find a pod or location…" aria-label="Search destinations"><div id="destinationList"></div><p class="muted">Choose a destination to move the camera.</p></aside>
  <aside id="inspector" class="inspector" hidden><div class="panel-heading"><span class="eyebrow">IN YOUR VIEW</span><button id="closeInspector" aria-label="Close asset details">×</button></div><h2 id="assetName"></h2><p id="assetId" class="muted"></p><div id="assetStatus" class="asset-state"></div><dl><div><dt>Utilization</dt><dd id="utilization"></dd></div><div><dt>Dimensions</dt><dd id="dimensions"></dd></div><div><dt>Position</dt><dd id="position"></dd></div></dl><button id="approach">View at human height ↗</button><p class="muted">Select an object on the floor to inspect it.</p></aside>
  <footer class="world-footer"><div class="view-switch" role="group" aria-label="View perspective"><button data-view="2d">2D</button><button data-view="2.5d" class="active">2.5D</button><button data-view="3d">3D</button><span></span><button id="freeCam" aria-pressed="false">Free cam</button><button id="perfToggle" aria-pressed="false">Perf</button></div><div id="cameraHelp">Drag to orbit · right-drag to pan · scroll to explore</div><div class="scale">↔ 1 unit = 1 meter <span>Human reference · 6 ft</span></div></footer><div id="perfPanel" class="perf-panel" hidden><strong>FRAME WORK</strong><div id="perfReadout">Waiting for frames…</div><small>F3 toggles · values are rolling averages</small></div><div id="crosshair" hidden>+</div></div>
  <dialog id="modal"><div class="panel-heading"><h2 id="modalTitle"></h2><button id="closeModal" aria-label="Close dialog">×</button></div><div id="modalBody"></div></dialog>
  <div id="notice" role="status"></div>`;

let entered = false,
  free = false,
  selectedId = null,
  assets = [],
  lastUpdate = null,
  feedState = 'connecting';
let receivedAt = null,
  pods = [],
  historical = true;
const ecs = createWorld();
window.eveEcs = ecs;
ecs.addSystem(statusAgeSystem);
installAdamUI();
let zoom = 34,
  desiredPolar = Math.PI / 3,
  activeView = '2.5d',
  flight = null;
let perfEnabled = false,
  perfWindowStart = performance.now(),
  perfFrames = 0,
  perfAccum = { frame: 0, camera: 0, update: 0, render: 0 };
let lodBand = null;
const reducedMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;
let cinematic = !reducedMotion;
const world = new THREE.Scene();
world.background = new THREE.Color('#111e24');
world.fog = new THREE.Fog('#111e24', 60, 150);
const camera = new THREE.PerspectiveCamera(48, innerWidth / innerHeight, 0.05, 1000);
camera.position.set(30, 26, 34);
let renderCamera = camera;
let renderer;
try {
  // Keep the interactive floor responsive on high-density displays. The
  // scene contains thousands of rack/component meshes, so MSAA and a 2x
  // render target multiply fragment work without improving the warehouse view
  // enough to justify the cost.
  renderer = new THREE.WebGLRenderer({ antialias: false, powerPreference: 'high-performance' });
} catch {
  $('viewport').innerHTML =
    '<p class="render-error">EVE needs WebGL to display the warehouse. Enable hardware acceleration and reload.</p>';
  throw new Error('WebGL unavailable');
}
// A 1x render target gives the rack floor headroom above the 60 FPS budget;
// Keep the rack floor responsive on 4K/high-DPI displays.
renderer.setPixelRatio(1);
renderer.setSize(innerWidth, innerHeight);
// Per-mesh shadow maps are disproportionately expensive for a dense rack
// floor. Use ambient + directional lighting without a shadow pass; this keeps
// the physical layout readable while leaving frame time for navigation.
renderer.shadowMap.enabled = false;
renderer.toneMapping = THREE.ACESFilmicToneMapping;
renderer.toneMappingExposure = 1.4;
$('viewport').append(renderer.domElement);
const controls = new OrbitControls(camera, renderer.domElement);
controls.enableDamping = true;
controls.enableZoom = false;
controls.enabled = false;
controls.minPolarAngle = 0.12;
controls.maxPolarAngle = Math.PI / 2 - 0.04;
controls.target.set(0, 0, 0);
world.add(new THREE.HemisphereLight('#d3eef4', '#46544d', 2.4));
const sun = new THREE.DirectionalLight('#fff1d2', 3.5);
sun.position.set(-15, 35, 20);
sun.castShadow = true;
sun.shadow.mapSize.set(2048, 2048);
Object.assign(sun.shadow.camera, { left: -40, right: 40, top: 40, bottom: -40 });
world.add(sun);
function box(parent, width, height, depth, x, y, z, color, metalness = 0.15, unlit = false) {
  const mesh = new THREE.Mesh(
    new THREE.BoxGeometry(width, height, depth),
    unlit
      ? new THREE.MeshBasicMaterial({ color })
      : new THREE.MeshStandardMaterial({ color, roughness: 0.65, metalness }),
  );
  mesh.position.set(x, y, z);
  mesh.castShadow = false;
  mesh.receiveShadow = false;
  parent.add(mesh);
  return mesh;
}
const warehouseBase = new THREE.Group();
world.add(warehouseBase);
function resizeWarehouseBase(nextAssets = []) {
  warehouseBase.traverse((o) => {
    if (o.geometry) o.geometry.dispose();
    if (o.material) {
      o.material.map?.dispose();
      o.material.dispose();
    }
  });
  warehouseBase.clear();
  const xs = nextAssets.map((a) => a.x).filter(Number.isFinite);
  const zs = nextAssets.map((a) => a.z).filter(Number.isFinite);
  const width = Math.max(40, Math.max(...xs, 18) - Math.min(...xs, -18) + 12);
  const depth = Math.max(30, Math.max(...zs, 13) - Math.min(...zs, -13) + 12);
  const cx = (Math.max(...xs, 18) + Math.min(...xs, -18)) / 2;
  const cz = (Math.max(...zs, 13) + Math.min(...zs, -13)) / 2;
  box(warehouseBase, width, 0.2, depth, cx, -0.11, cz, '#586361');
  const grid = new THREE.GridHelper(Math.ceil(width), Math.ceil(width), '#72847d', '#64736c');
  grid.position.set(cx, 0.005, cz);
  grid.scale.z = depth / width;
  warehouseBase.add(grid);
  box(warehouseBase, width, 0.8, 0.25, cx, 0.4, cz - depth / 2, '#8a9993');
  box(warehouseBase, 0.25, 0.8, depth, cx - width / 2, 0.4, cz, '#8a9993');
  for (const x of [cx - width / 2 + 2, cx + width / 2 - 2])
    for (const z of [cz - depth / 2 + 2, cz, cz + depth / 2 - 2])
      box(warehouseBase, 0.32, 6, 0.32, x, 3, z, '#95a6a0');
  for (const z of [cz - depth / 2 + 5, cz, cz + depth / 2 - 5]) {
    box(warehouseBase, width - 6, 0.012, 0.065, cx, 0.02, z, '#d5b565');
    box(warehouseBase, width - 6, 0.012, 0.065, cx, 0.02, z + 1.7, '#d5b565');
  }
}
resizeWarehouseBase();
const person = new THREE.Group();
person.position.set(15, 0, 4);
world.add(person);
box(person, 0.42, 0.64, 0.24, 0, 1.12, 0, '#e7bb5b');
for (const x of [-0.12, 0.12]) box(person, 0.15, 0.8, 0.19, x, 0.4, 0, '#24383d');
for (const x of [-0.29, 0.29]) box(person, 0.14, 0.65, 0.17, x, 1.08, 0, '#e7bb5b');
const head = new THREE.Mesh(
  new THREE.SphereGeometry(0.17, 16, 12),
  new THREE.MeshStandardMaterial({ color: '#c49e80' }),
);
head.position.y = HUMAN_HEIGHT - 0.17;
person.add(head);
const entityGroup = new THREE.Group();
world.add(entityGroup);
const entities = new Map();
const colors = {
  passed: '#72c9ad',
  running: '#75bce8',
  failed: '#ef7366',
  unknown: '#aaa6b5',
  empty: '#566a6d',
};
function dispose(group) {
  group.traverse((o) => {
    o.geometry?.dispose();
    if (o.material) {
      o.material.map?.dispose();
      o.material.dispose();
    }
  });
  group.removeFromParent();
}
function stateColor(state) {
  return colors[state] || colors.unknown;
}
function cableColor(state) {
  return state === 'attention' ? colors.failed : state === 'passed' ? colors.passed : '#4ed0c1';
}
function componentState(components, type) {
  const matching = components.filter((c) => c.type === type);
  if (!matching.length) return 'unknown';
  return matching.some((c) => c.state === 'failed')
    ? 'failed'
    : matching.some((c) => c.state === 'running')
      ? 'running'
      : matching.every((c) => c.state === 'passed')
        ? 'passed'
        : 'unknown';
}
function addProceduralComponent(parent, c, x, y, z, width, height, depth) {
  const color =
    c.type === 'NVSWITCH'
      ? '#b27bdb'
      : c.type === 'POWERSHELF'
        ? '#d0a95b'
        : c.type === 'SERVER'
          ? '#5c9eb0'
          : '#788b91';
  const unit = new THREE.Group();
  unit.position.set(x, y, z);
  unit.userData.slot = c.slot;
  unit.userData.partType = c.type;
  unit.userData.lod = 1;
  box(unit, width, height, depth, 0, 0, 0, c.type === 'SPACE' ? '#22373c' : color, 0.35, true);
  if (c.type !== 'SPACE') {
    const light = box(
      unit,
      width * 0.72,
      height * 0.16,
      0.025,
      0,
      0,
      depth / 2 + 0.015,
      stateColor(c.state),
      0,
      true,
    );
    light.material.emissive?.set(stateColor(c.state));
    if (light.material.emissive) light.material.emissiveIntensity = 0.8;
  }
  parent.add(unit);
  loadPhysicalModel(c.type).then((model) => {
    if (!model || !unit.parent) return;
    unit.clear();
    model.scale.setScalar(Math.min(width, height, depth));
    model.position.y = -height / 2;
    model.userData.slot = c.slot;
    unit.add(model);
    if (c.type !== 'SPACE') {
      const light = box(
        unit,
        width * 0.12,
        height * 0.35,
        0.025,
        0,
        0,
        depth / 2 + 0.015,
        stateColor(c.state),
      );
      light.material.emissive.set(stateColor(c.state));
      light.material.emissiveIntensity = 0.8;
    }
  });
}
function buildAsset(a) {
  const g = new THREE.Group();
  g.position.set(a.x, 0, a.z);
  g.userData.id = a.id;
  g.userData.composition = rackComposition(a.model, a.subModel, a.kind);
  g.rotation.y = a.facing || 0;
  const w = a.width,
    h = a.height,
    d = a.depth;
  const isMgx = a.type === 'rack' && /MGX/i.test(`${a.subModel} ${a.model} ${a.kind}`);
  const isGb300 = a.type === 'rack' && /GB300/i.test(`${a.subModel} ${a.model}`);
  box(
    g,
    w,
    h,
    d,
    0,
    h / 2,
    0,
    isMgx || isGb300 ? '#151d20' : a.subModel?.includes('ZORA') ? '#4d5262' : '#344c53',
  );
  const slots = new THREE.Group();
  g.add(slots);
  const detail = new THREE.Group();
  detail.name = 'rack-detail';
  detail.visible = true;
  g.add(detail);
  if (a.type === 'rack') {
    for (const x of [-w / 2, w / 2]) box(g, 0.07, h, d + 0.08, x, h / 2, 0, '#a1b6b3');
    if (isMgx) {
      // MGX service-face layout: dense black chassis, silver fan walls, and
      // high-visibility yellow/cyan cable looms. Geometry is schematic but
      // preserves the visual landmarks operators use in a real rack.
      const faceZ = d / 2 + 0.045;
      const unitH = h / 42;
      for (let u = 0; u < 42; u++) {
        const y = 0.08 + unitH * (u + 0.5);
        const chassis = box(detail, w - 0.18, unitH * 0.72, 0.08, 0, y, faceZ, '#090e10', 0, true);
        chassis.userData.slot = `U${u + 1}`;
        const led = box(
          detail,
          0.018,
          unitH * 0.32,
          0.02,
          -w * 0.36,
          y,
          faceZ + 0.06,
          '#25d58a',
          0,
          true,
        );
        led.userData.lod = 2;
        led.material.color.set(u % 11 === 0 ? '#f2c21a' : '#25d58a');
        if ([3, 4, 5, 6, 17, 18, 19, 20, 31, 32, 33, 34].includes(u)) {
          for (const fx of [-0.22, -0.07, 0.08, 0.23]) {
            const fan = box(
              detail,
              0.1,
              unitH * 0.5,
              0.018,
              fx,
              y,
              faceZ + 0.05,
              '#b9c0b9',
              0,
              true,
            );
            fan.userData.lod = 2;
            fan.material.color.set(u % 2 ? '#9da89f' : '#d4d8ce');
          }
        }
      }
      for (const side of [-1, 1]) {
        const rail = box(
          detail,
          0.07,
          h - 0.12,
          0.1,
          side * (w / 2 - 0.06),
          h / 2,
          faceZ + 0.02,
          '#777f7a',
          0,
          true,
        );
        rail.userData.mgxRail = true;
      }
      const cableGroup = new THREE.Group();
      cableGroup.name = 'evidence-cables';
      detail.add(cableGroup);
      const findings = a.cables || [];
      const cableState = (side) =>
        findings.find((c) => c.side === side || c.side === 'both')?.state || 'observed';
      for (const side of [-1, 1]) {
        for (let i = 0; i < 10; i++) {
          const y = 0.2 + (i / 10) * (h - 0.4);
          const color = i % 2 ? '#16d5dc' : '#f2c21a';
          const state = cableState(side > 0 ? 'right' : 'left');
          const curve = new THREE.CatmullRomCurve3([
            new THREE.Vector3(side * (w * 0.32), y, faceZ + 0.08),
            new THREE.Vector3(side * (w * 0.55), y + 0.12, faceZ + 0.13),
            new THREE.Vector3(side * (w * 0.55), y - 0.12, faceZ + 0.16),
            new THREE.Vector3(side * (w * 0.35), y, faceZ + 0.08),
          ]);
          const cable = new THREE.Mesh(
            new THREE.TubeGeometry(curve, 5, 0.012, 4, false),
            new THREE.MeshBasicMaterial({ color: state === 'attention' ? '#ef7366' : color }),
          );
          cable.userData.lod = 3;
          cableGroup.add(cable);
        }
      }
    } else if (isGb300) {
      // GB300 NVL72 visual arrangement: 18 compute trays, 9 NVLink switches,
      // 8 power shelves, plus the liquid manifold on the service face.
      const tray = (y, color, slot, c) => {
        const m = box(detail, w - 0.18, 0.065, 0.055, 0, y, d / 2 + 0.045, color);
        m.material.emissive.set(color);
        m.material.emissiveIntensity = 0.35;
        if (slot) m.userData.slot = slot;
        if (c) addProceduralComponent(detail, c, 0, y, d / 2 + 0.08, w - 0.2, 0.055, 0.08);
        if (c?.type === 'SERVER') {
          const parts = new THREE.Group();
          parts.position.set(0, y, d / 2 + 0.11);
          for (let i = 0; i < 4; i++) {
            const gpu = box(parts, 0.12, 0.035, 0.035, -0.24 + i * 0.16, 0, 0, '#6e8790', 0, true);
            gpu.userData.lod = 2;
            gpu.userData.partType = 'BLACKWELL_GPU';
            gpu.userData.slot = `${slot}:GPU${i + 1}`;
          }
          for (let i = 0; i < 2; i++) {
            const cpu = box(
              parts,
              0.08,
              0.035,
              0.035,
              -0.34 + i * 0.68,
              0,
              0.035,
              '#c59b58',
              0,
              true,
            );
            cpu.userData.lod = 2;
            cpu.userData.partType = 'GRACE_CPU';
            cpu.userData.slot = `${slot}:CPU${i + 1}`;
          }
          detail.add(parts);
        }
        return m;
      };
      const byType = (type) => a.components.filter((c) => c.type === type);
      const addStack = (type, count, y0, gap) => {
        const list = byType(type);
        for (let i = 0; i < count; i++) {
          const c = list[i % Math.max(list.length, 1)] || {
            type,
            state: 'unknown',
            slot: `${type}-${i + 1}`,
          };
          tray(y0 + i * gap, stateColor(c.state || 'unknown'), c.slot, c);
        }
      };
      const composition = g.userData.composition;
      addStack('SERVER', composition.computeTrays ?? byType('SERVER').length, 0.24, 0.067);
      addStack('NVSWITCH', composition.nvswitchTrays ?? byType('NVSWITCH').length, 1.52, 0.073);
      addStack('POWERSHELF', composition.powerShelves ?? byType('POWERSHELF').length, 1.12, 0.047);
      const manifold = box(detail, 0.045, 1.78, 0.045, w * 0.32, 1.02, d / 2 + 0.085, '#4ed0c1');
      manifold.material.emissive.set('#4ed0c1');
      manifold.material.emissiveIntensity = 0.8;
      const cableGroup = new THREE.Group();
      cableGroup.name = 'evidence-cables';
      detail.add(cableGroup);
      const findings = a.cables || [];
      const sideState = (side) =>
        findings.find((c) => c.type === 'CX8 loopback' && (c.side === side || c.side === 'both'))
          ?.state || 'observed';
      for (const side of ['left', 'right']) {
        const sx = side === 'left' ? -1 : 1,
          state = sideState(side);
        const curve = new THREE.CatmullRomCurve3([
          new THREE.Vector3(sx * 0.22, 1.62, d / 2 + 0.1),
          new THREE.Vector3(sx * 0.42, 1.62, d / 2 + 0.18),
          new THREE.Vector3(sx * 0.42, 1.38, d / 2 + 0.2),
          new THREE.Vector3(sx * 0.22, 1.38, d / 2 + 0.1),
        ]);
        const tube = new THREE.Mesh(
          new THREE.TubeGeometry(curve, 6, 0.018, 4, false),
          new THREE.MeshStandardMaterial({
            color: cableColor(state),
            emissive: cableColor(state),
            emissiveIntensity: state === 'attention' ? 0.7 : 0.35,
            roughness: 0.45,
          }),
        );
        tube.userData.cableSide = side;
        tube.userData.lod = 3;
        cableGroup.add(tube);
      }
      box(g, w + 0.08, 0.045, d + 0.1, 0, h + 0.03, 0, stateColor(a.status));
    } else {
      // Non-GB300 equipment keeps a restrained inventory schematic.
      a.components.forEach((c, i) => {
        const columns = Math.ceil(a.components.length / 44),
          rows = Math.min(44, a.components.length);
        const sw = (w - 0.16) / columns,
          sh = (h - 0.3) / rows;
        addProceduralComponent(
          slots,
          c,
          -w / 2 + 0.08 + sw * (Math.floor(i / 44) + 0.5),
          0.12 + sh * (rows - (i % 44) - 0.5),
          d / 2 + 0.045,
          sw * 0.86,
          sh * 0.72,
          0.04,
        );
      });
    }
  }
  const indicator = box(g, w - 0.2, 0.08, 0.045, 0, h - 0.15, d / 2 + 0.04, stateColor(a.status));
  indicator.material.emissive.set(stateColor(a.status));
  indicator.material.emissiveIntensity = 0.5;
  const cap = box(g, w, 0.04, d, 0, h + 0.03, 0, stateColor(a.status));
  entityGroup.add(g);
  return {
    group: g,
    indicator,
    cap,
    slots,
    detail,
    cableGroup: g.getObjectByName('evidence-cables'),
    signature: assetSignature(a),
  };
}
function assetSignature(a) {
  return [
    a.width,
    a.height,
    a.depth,
    a.type,
    a.location,
    a.subModel,
    ...a.components.map((c) => `${c.slot}:${c.type}:${c.state}`),
    ...(a.cables || []).map((c) => `${c.type}:${c.side}:${c.state}`),
  ].join('|');
}
function reconcile(snapshot) {
  assets = snapshot.assets;
  resizeWarehouseBase(assets);
  lastUpdate = snapshot.timestamp;
  receivedAt = snapshot.receivedAt;
  historical = snapshot.historical;
  const snapshotIds = new Set();
  for (const a of assets) {
    snapshotIds.add(a.id);
    ecs.upsert(a.id, {
      Transform: C.transform(a),
      Identity: C.identity(a),
      Status: C.status(a),
      Inventory: C.inventory(a),
      Source: C.source(a, snapshot.historical),
    });
  }
  for (const entity of ecs.entities.keys()) if (!snapshotIds.has(entity)) ecs.remove(entity);
  pods = snapshot.pods;
  const ids = new Set(assets.map((a) => a.id));
  for (const [id, e] of entities)
    if (!ids.has(id)) {
      dispose(e.group);
      entities.delete(id);
    }
  for (const a of assets) {
    let e = entities.get(a.id);
    if (e && e.signature !== assetSignature(a)) {
      dispose(e.group);
      entities.delete(a.id);
      e = null;
    }
    if (!e) {
      e = buildAsset(a);
      entities.set(a.id, e);
    }
    e.group.position.set(a.x, 0, a.z);
    e.group.rotation.y = a.facing || 0;
    e.detail.visible = true;
    e.indicator.material.color.set(stateColor(a.status));
    e.indicator.material.emissive.set(stateColor(a.status));
    e.cap.material.color.set(stateColor(a.status));
  }
  if (selectedId && !ids.has(selectedId)) {
    selectedId = null;
    $('inspector').hidden = true;
  }
  updateInspector();
  lodBand = null;
  renderDestinations();
}
function updateStatus(state, message) {
  feedState = state;
  $('feedStatus').textContent = `${state.toUpperCase()} · ADAM`;
  $('signal').dataset.state = state;
  $('feedStatus').title = message;
  updateInspector();
}
function applyLod() {
  const nextBand = zoom < 12 ? 0 : zoom < 24 ? 1 : 2;
  if (nextBand === lodBand) return;
  lodBand = nextBand;
  const maxLod = nextBand === 0 ? 3 : nextBand === 1 ? 2 : 1;
  for (const e of entities.values()) {
    // At overview distance, keep only rack envelopes/status bars. Component
    // geometry returns as the camera enters the mid/near bands.
    e.detail.visible = nextBand !== 2;
    e.detail.traverse((object) => {
      if (object.userData.lod) object.visible = object.userData.lod <= maxLod;
    });
  }
}
const stopFeed = startAdamFeed({
  url: import.meta.env.VITE_WORLD_URL,
  onSnapshot: reconcile,
  onStatus: updateStatus,
});
window.addEventListener('pagehide', stopFeed, { once: true });

function setView() {
  if (free) toggleFree(false);
  activeView = '2.5d';
  zoom = 34;
  desiredPolar = Math.PI / 4;
  controls.enableRotate = true;
}
function enterWorld() {
  entered = true;
  $('welcome').hidden = true;
  $('worldUI').hidden = false;
  controls.enabled = true;
  setView('2.5d');
  camera.position.set(25, 25, 30);
  controls.target.set(0, 0, 0);
  $('destinationsToggle').focus();
}
$('enter').onclick = enterWorld;
$('home').onclick = () => {
  if (free) toggleFree(false);
  entered = false;
  flight = null;
  controls.enabled = false;
  $('welcome').hidden = false;
  $('worldUI').hidden = true;
  $('enter').focus();
};
function drawer(open) {
  $('destinations').hidden = !open;
  $('destinationsToggle').setAttribute('aria-expanded', String(open));
  if (open) $('search').focus();
}
$('destinationsToggle').onclick = () => drawer($('destinations').hidden);
$('closeDrawer').onclick = () => {
  drawer(false);
  $('destinationsToggle').focus();
};
function goTo(x, z, id = null, name = 'Overview') {
  if (!entered) enterWorld();
  if (free) toggleFree(false);
  selectedId = id;
  $('location').textContent = name;
  const asset = assets.find((a) => a.id === id);
  if (asset?.type === 'rack') focusRack(asset);
  else {
    setView('2.5d');
    flight = { target: new THREE.Vector3(x, 0, z) };
  }
  updateInspector();
  if (innerWidth < 800) drawer(false);
}
function focusRack(a) {
  if (!entered) enterWorld();
  setView('3d');
  zoom = 5.2;
  selectedId = a.id;
  const front = new THREE.Vector3(0, 0, 1).applyAxisAngle(
    new THREE.Vector3(0, 1, 0),
    a.facing || 0,
  );
  const target = new THREE.Vector3(a.x, Math.min(1.35, a.height * 0.58), a.z);
  const position = target.clone().addScaledVector(front, Math.max(3.4, a.depth * 2.4 + 1.4));
  position.y = 1.72;
  controls.enabled = false;
  flight = { position, target };
  updateInspector();
}
function renderDestinations() {
  const query = $('search').value.toLowerCase();
  const destinations = [
    { name: 'Building 6 · overview', id: '', x: 0, z: 0 },
    ...pods.map((pod) => {
      const members = assets.filter((a) => a.pod === pod);
      return {
        name: `Pod ${pod}`,
        id: '',
        x: 0,
        z: members.reduce((n, a) => n + (a.podCenterZ ?? a.z), 0) / members.length,
      };
    }),
    ...assets.slice().sort((a, b) => a.location.localeCompare(b.location)),
  ];
  $('destinationList').replaceChildren();
  for (const a of destinations.filter((a) =>
    `${a.name} ${a.id} ${a.subModel || ''} ${(a.components || []).map((c) => c.serial).join(' ')}`
      .toLowerCase()
      .includes(query),
  )) {
    const button = document.createElement('button');
    button.className = 'destination';
    const text = document.createElement('span');
    text.textContent = a.name;
    const meta = document.createElement('small');
    meta.textContent =
      a.type === 'rack'
        ? `${a.status} · ${a.summary.installed} units`
        : a.type === 'empty'
          ? a.status
          : 'AREA';
    button.append(text, meta);
    button.onclick = () => goTo(a.x, a.z, a.id || null, a.name);
    $('destinationList').append(button);
  }
  if (!$('destinationList').children.length)
    $('destinationList').textContent = 'No matching destinations.';
}
$('search').oninput = renderDestinations;
function updateInspector() {
  const a = assets.find((a) => a.id === selectedId);
  // Detail visibility is controlled centrally by applyLod().
  $('inspector').hidden = !a;
  if (!a) return;
  $('assetName').textContent = a.name;
  $('assetId').textContent =
    a.type === 'rack'
      ? `${a.id} · ${a.subModel || a.model}`
      : 'Configured floor location · no rack observation';
  $('assetStatus').textContent = a.status;
  $('assetStatus').style.color = colors[a.status];
  renderRackDetails(a, { historical, feedState });
}
$('closeInspector').onclick = () => {
  selectedId = null;
  updateInspector();
};
$('approach').onclick = () => {
  const a = assets.find((a) => a.id === selectedId);
  if (!a) return;
  const front = new THREE.Vector3(0, 0, 1).applyAxisAngle(
    new THREE.Vector3(0, 1, 0),
    a.facing || 0,
  );
  const target = new THREE.Vector3(a.x, 1.5, a.z);
  toggleFree(true);
  camera.position.copy(target).addScaledVector(front, Math.max(3.4, a.depth * 2.4 + 1.4));
  camera.position.y = 1.7;
  camera.lookAt(target);
  syncLook();
};
document
  .querySelectorAll('[data-view]')
  .forEach((b) => (b.onclick = () => setView(b.dataset.view)));
const keys = new Set();
let yaw = 0,
  pitch = 0,
  dragging = false,
  pointerStart = null;
function syncLook() {
  const e = new THREE.Euler().setFromQuaternion(camera.quaternion, 'YXZ');
  yaw = e.y;
  pitch = e.x;
}
function toggleFree(value = !free) {
  free = value;
  flight = null;
  controls.enabled = entered && !free;
  keys.clear();
  $('freeCam').classList.toggle('active', free);
  $('freeCam').setAttribute('aria-pressed', String(free));
  $('crosshair').hidden = !free;
  $('cameraHelp').textContent = free
    ? 'Drag to look · WASD move · Q/E down/up · Shift faster · Esc exit'
    : 'Drag to orbit · right-drag to pan · scroll to explore';
  if (free) {
    renderCamera = camera;
    syncLook();
  } else {
    const forward = new THREE.Vector3();
    camera.getWorldDirection(forward);
    controls.target.copy(camera.position).addScaledVector(forward, 10);
    controls.target.y = 0;
    zoom = 13;
    setView('3d');
  }
}
$('freeCam').onclick = () => toggleFree();
function togglePerf(value = !perfEnabled) {
  perfEnabled = value;
  $('perfPanel').hidden = !value;
  $('perfToggle').setAttribute('aria-pressed', String(value));
  $('perfToggle').classList.toggle('active', value);
  if (value) {
    perfWindowStart = performance.now();
    perfFrames = 0;
    perfAccum = { frame: 0, camera: 0, update: 0, render: 0 };
  }
}
$('perfToggle').onclick = () => togglePerf();
window.addEventListener('keydown', (e) => {
  if (e.key === 'F3') {
    e.preventDefault();
    togglePerf();
    return;
  }
  if (e.key === 'Escape') {
    if ($('modal').open) return;
    if (free) toggleFree(false);
    else {
      drawer(false);
      selectedId = null;
      updateInspector();
    }
  }
  if (['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement.tagName) || $('modal').open)
    return;
  if (
    free &&
    ['KeyW', 'KeyA', 'KeyS', 'KeyD', 'KeyQ', 'KeyE', 'ShiftLeft', 'ShiftRight'].includes(e.code)
  ) {
    e.preventDefault();
    keys.add(e.code);
  }
});
window.addEventListener('keyup', (e) => keys.delete(e.code));
window.addEventListener('blur', () => {
  keys.clear();
  dragging = false;
});
renderer.domElement.addEventListener('pointerdown', (e) => {
  pointerStart = { x: e.clientX, y: e.clientY };
  dragging = free;
  if (free) {
    renderer.domElement.setPointerCapture(e.pointerId);
    document.activeElement.blur();
  }
});
renderer.domElement.addEventListener('pointermove', (e) => {
  if (!dragging || !free) return;
  yaw -= e.movementX * 0.004;
  pitch = THREE.MathUtils.clamp(pitch - e.movementY * 0.004, -1.5, 1.5);
  camera.quaternion.setFromEuler(new THREE.Euler(pitch, yaw, 0, 'YXZ'));
});
const raycaster = new THREE.Raycaster();
renderer.domElement.addEventListener('pointerup', (e) => {
  dragging = false;
  if (
    !entered ||
    !pointerStart ||
    Math.hypot(e.clientX - pointerStart.x, e.clientY - pointerStart.y) > 5 ||
    e.button !== 0
  )
    return;
  raycaster.setFromCamera(
    new THREE.Vector2((e.clientX / innerWidth) * 2 - 1, 1 - (e.clientY / innerHeight) * 2),
    renderCamera,
  );
  const hit = raycaster.intersectObjects(entityGroup.children, true).find((h) => h.object.isMesh);
  if (hit) {
    let object = hit.object;
    while (!object.userData.id && object.parent) object = object.parent;
    selectedId = object.userData.id;
    const a = assets.find((a) => a.id === selectedId);
    if (a?.type === 'rack') focusRack(a);
    else updateInspector();
    if (hit.object.userData.slot) {
      $('componentSearch').value = hit.object.userData.slot;
      $('componentSearch').dispatchEvent(new Event('input'));
    }
  }
});
renderer.domElement.addEventListener('pointercancel', () => {
  dragging = false;
  pointerStart = null;
});
renderer.domElement.addEventListener(
  'wheel',
  (e) => {
    if (!entered || free) return;
    e.preventDefault();
    zoom = THREE.MathUtils.clamp(zoom * Math.exp(e.deltaY * 0.001), 6, 85);
  },
  { passive: false },
);
controls.addEventListener('start', () => {
  flight = null;
});

function showModal(title, html) {
  keys.clear();
  $('modalTitle').textContent = title;
  $('modalBody').innerHTML = html;
  $('modal').showModal();
}
$('closeModal').onclick = () => $('modal').close();
$('profileButton').onclick = () => {
  showModal(
    'Your profile',
    '<p class="muted">Local explorer profile · stored on this browser.</p><label>Display name<input id="displayName" maxlength="60" placeholder="Explorer"></label><button id="saveProfile" class="primary">Save profile</button><p class="muted">No account service is connected.</p>',
  );
  try {
    $('displayName').value = localStorage.getItem('eve.name') || '';
  } catch {}
  $('saveProfile').onclick = () => {
    try {
      localStorage.setItem('eve.name', $('displayName').value.trim());
      $('profileButton').textContent = ($('displayName').value.trim() || 'ME')
        .slice(0, 2)
        .toUpperCase();
      $('modal').close();
    } catch {
      $('notice').textContent = 'Browser storage is unavailable.';
    }
  };
};
try {
  $('profileButton').textContent = (localStorage.getItem('eve.name') || 'ME')
    .slice(0, 2)
    .toUpperCase();
} catch {}
$('newsButton').onclick = () =>
  showModal(
    'News & updates',
    '<article><span class="eyebrow">ADAM INTEGRATION</span><h3>From pod to component</h3><p>Explore pods 6V and 6W, find equipment by serial or location, and inspect component slots and recorded test results.</p></article><article><span class="eyebrow">DATA PROVENANCE</span><h3>Historical observations</h3><p>The local preview reads saved ADAM snapshots, not live telemetry. Floor geometry and component placement are schematic until measured configuration is available.</p></article>',
  );
$('settingsButton').onclick = () => {
  showModal(
    'World settings',
    '<label class="check"><input id="motionSetting" type="checkbox"> Cinematic welcome camera</label><p class="muted">Names stay in the destination drawer and inspector. The floor itself stays uncluttered so geometry, status, and aisles remain legible.</p><p class="muted">All geometry uses meters. Sample building and asset dimensions must be replaced with verified facility measurements.</p>',
  );
  $('motionSetting').checked = cinematic;
  $('motionSetting').onchange = (e) => (cinematic = e.target.checked);
};
window.addEventListener('resize', () => {
  camera.aspect = innerWidth / innerHeight;
  camera.updateProjectionMatrix();
  renderer.setSize(innerWidth, innerHeight);
});
let previous = performance.now();
function animate(now) {
  requestAnimationFrame(animate);
  const frameStart = performance.now();
  const dt = Math.min((now - previous) / 1000, 0.05);
  previous = now;
  ecs.tick(dt, { selectedId, entered, free });
  const cameraStart = performance.now();
  renderCamera = camera;
  if (!entered) {
    const angle = cinematic ? now * 0.000025 : 0.7;
    camera.position.set(Math.sin(angle) * 38, 22, Math.cos(angle) * 38);
    camera.lookAt(0, 0, 0);
  } else if (free) {
    const speed = (keys.has('ShiftLeft') || keys.has('ShiftRight') ? 12 : 4) * dt;
    const move = new THREE.Vector3(
      Number(keys.has('KeyD')) - Number(keys.has('KeyA')),
      0,
      Number(keys.has('KeyS')) - Number(keys.has('KeyW')),
    );
    if (move.lengthSq()) move.normalize().applyQuaternion(camera.quaternion).multiplyScalar(speed);
    camera.position.add(move);
    camera.position.y += (Number(keys.has('KeyE')) - Number(keys.has('KeyQ'))) * speed;
    camera.position.y = Math.max(0.25, camera.position.y);
  } else {
    if (flight?.position) {
      const alpha = reducedMotion ? 1 : 1 - Math.exp(-dt * 4);
      camera.position.lerp(flight.position, alpha);
      controls.target.lerp(flight.target, alpha);
      camera.lookAt(controls.target);
      if (
        camera.position.distanceTo(flight.position) < 0.025 &&
        controls.target.distanceTo(flight.target) < 0.025
      ) {
        camera.position.copy(flight.position);
        controls.target.copy(flight.target);
        flight = null;
        controls.enabled = true;
      }
    } else if (flight) {
      const next = controls.target
        .clone()
        .lerp(flight.target, reducedMotion ? 1 : 1 - Math.exp(-dt * 5));
      camera.position.add(next.clone().sub(controls.target));
      controls.target.copy(next);
      if (next.distanceTo(flight.target) < 0.02) flight = null;
    }
    const spherical = new THREE.Spherical().setFromVector3(
      camera.position.clone().sub(controls.target),
    );
    spherical.radius = THREE.MathUtils.lerp(spherical.radius, zoom, 1 - Math.exp(-dt * 7));
    // Automatic tilt establishes the zoom bands; orbit azimuth remains under user control.
    spherical.phi = THREE.MathUtils.lerp(spherical.phi, desiredPolar, 1 - Math.exp(-dt * 5));
    camera.position.copy(controls.target).add(new THREE.Vector3().setFromSpherical(spherical));
    controls.update();
  }
  applyLod();
  const cameraMs = performance.now() - cameraStart;
  if (receivedAt) {
    $('freshness').textContent = historical
      ? 'SAVED SNAPSHOTS · NOT LIVE'
      : `Fetched ${Math.floor((Date.now() - receivedAt) / 1000)}s ago`;
    if (feedState === 'live' && lastUpdate && Date.now() - lastUpdate > STALE_AFTER_MS)
      updateStatus('stale', 'Source observations are older than five minutes');
  }
  const updateStart = performance.now();
  for (const [id, e] of entities) {
    e.slots.visible = false;
  }
  const updateMs = performance.now() - updateStart;
  const renderStart = performance.now();
  renderer.render(world, renderCamera);
  const renderMs = performance.now() - renderStart;
  if (perfEnabled) {
    perfFrames++;
    perfAccum.frame += performance.now() - frameStart;
    perfAccum.camera += cameraMs;
    perfAccum.update += updateMs;
    perfAccum.render += renderMs;
    const elapsed = performance.now() - perfWindowStart;
    if (elapsed >= 500) {
      const divisor = perfFrames;
      const fps = (perfFrames * 1000) / elapsed;
      $('perfReadout').innerHTML =
        `FPS <b>${fps.toFixed(1)}</b> · ${fps >= 59 ? '60 FPS target met' : 'below 60 FPS target'} · frame <b>${(perfAccum.frame / divisor).toFixed(2)} ms</b><br>camera ${(perfAccum.camera / divisor).toFixed(2)} ms · scene ${(perfAccum.update / divisor).toFixed(2)} ms · render ${(perfAccum.render / divisor).toFixed(2)} ms<br>draw calls ${renderer.info.render.calls} · triangles ${renderer.info.render.triangles} · entities ${entities.size}`;
      perfWindowStart = performance.now();
      perfFrames = 0;
      perfAccum = { frame: 0, camera: 0, update: 0, render: 0 };
    }
  }
}
requestAnimationFrame(animate);
