import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import '../styles/style.css';
import '../styles/context.css';
import '../styles/component-legend.css';

const racks = [
  {
    id: 'A-01',
    row: 'A',
    x: -5.1,
    z: -1.6,
    state: 'healthy',
    temp: 21.4,
    load: 62,
    power: '3.8 kW',
    devices: 34,
    uptime: '99.99%',
    zone: 'North Hall',
  },
  {
    id: 'A-02',
    row: 'A',
    x: -3.7,
    z: -1.6,
    state: 'healthy',
    temp: 22.1,
    load: 74,
    power: '4.2 kW',
    devices: 39,
    uptime: '99.98%',
    zone: 'North Hall',
  },
  {
    id: 'A-03',
    row: 'A',
    x: -2.3,
    z: -1.6,
    state: 'warning',
    temp: 24.8,
    load: 88,
    power: '5.4 kW',
    devices: 41,
    uptime: '99.91%',
    zone: 'North Hall',
  },
  {
    id: 'A-04',
    row: 'A',
    x: -0.9,
    z: -1.6,
    state: 'healthy',
    temp: 20.9,
    load: 48,
    power: '3.1 kW',
    devices: 28,
    uptime: '100%',
    zone: 'North Hall',
  },
  {
    id: 'A-05',
    row: 'A',
    x: 0.5,
    z: -1.6,
    state: 'healthy',
    temp: 21.8,
    load: 58,
    power: '3.6 kW',
    devices: 31,
    uptime: '99.99%',
    zone: 'North Hall',
  },
  {
    id: 'A-06',
    row: 'A',
    x: 1.9,
    z: -1.6,
    state: 'critical',
    temp: 28.7,
    load: 94,
    power: '6.1 kW',
    devices: 44,
    uptime: '99.72%',
    zone: 'North Hall',
  },
  {
    id: 'A-07',
    row: 'A',
    x: 3.3,
    z: -1.6,
    state: 'healthy',
    temp: 22.3,
    load: 69,
    power: '4.0 kW',
    devices: 36,
    uptime: '99.98%',
    zone: 'North Hall',
  },
  {
    id: 'A-08',
    row: 'A',
    x: 4.7,
    z: -1.6,
    state: 'healthy',
    temp: 21.2,
    load: 55,
    power: '3.5 kW',
    devices: 30,
    uptime: '99.99%',
    zone: 'North Hall',
  },
  {
    id: 'B-01',
    row: 'B',
    x: -4.4,
    z: 1.8,
    state: 'healthy',
    temp: 20.7,
    load: 41,
    power: '2.9 kW',
    devices: 25,
    uptime: '100%',
    zone: 'South Hall',
  },
  {
    id: 'B-02',
    row: 'B',
    x: -3,
    z: 1.8,
    state: 'healthy',
    temp: 21.9,
    load: 63,
    power: '3.7 kW',
    devices: 32,
    uptime: '99.99%',
    zone: 'South Hall',
  },
  {
    id: 'B-03',
    row: 'B',
    x: -1.6,
    z: 1.8,
    state: 'healthy',
    temp: 22.4,
    load: 71,
    power: '4.1 kW',
    devices: 37,
    uptime: '99.97%',
    zone: 'South Hall',
  },
  {
    id: 'B-04',
    row: 'B',
    x: -0.2,
    z: 1.8,
    state: 'warning',
    temp: 25.1,
    load: 81,
    power: '4.9 kW',
    devices: 40,
    uptime: '99.93%',
    zone: 'South Hall',
  },
  {
    id: 'B-05',
    row: 'B',
    x: 1.2,
    z: 1.8,
    state: 'healthy',
    temp: 21.1,
    load: 52,
    power: '3.4 kW',
    devices: 29,
    uptime: '99.99%',
    zone: 'South Hall',
  },
  {
    id: 'B-06',
    row: 'B',
    x: 2.6,
    z: 1.8,
    state: 'healthy',
    temp: 20.4,
    load: 47,
    power: '3.0 kW',
    devices: 27,
    uptime: '100%',
    zone: 'South Hall',
  },
  {
    id: 'B-07',
    row: 'B',
    x: 4,
    z: 1.8,
    state: 'healthy',
    temp: 21.6,
    load: 66,
    power: '3.9 kW',
    devices: 35,
    uptime: '99.98%',
    zone: 'South Hall',
  },
];

let selected = racks.find((r) => r.id === 'A-03');
let showFlow = true;
let showLabels = true;
let incidentOpen = true;
const app = document.querySelector('#app');

app.innerHTML = `
  <header class="topbar">
    <div class="brand"><div class="brand-mark">N</div><div><strong>NEXUS <span>DCIM</span></strong><small>DATA CENTER MANAGEMENT</small></div></div>
    <div class="top-actions"><div class="live"><i></i> LIVE <span>14:32:08 UTC</span></div><button class="icon-btn" aria-label="Notifications">♢<b>3</b></button><div class="avatar">JD</div></div>
  </header>
  <main class="shell">
    <aside class="sidebar">
      <div class="site-select"><span class="eyebrow">ACTIVE SITE</span><strong>DC-West / Floor 02</strong><span class="chevron">⌄</span></div>
      <nav><p>WORKSPACE</p><button class="nav-item active">◈ <span>Operations floor</span></button><button class="nav-item">▦ <span>Asset inventory</span></button><button class="nav-item">⌁ <span>Network topology</span></button><button class="nav-item">◒ <span>Capacity planning</span></button><p>MONITORING</p><button class="nav-item">◉ <span>Alerts <em>3</em></span></button><button class="nav-item">◌ <span>Environmental</span></button></nav>
      <div class="sidebar-foot"><div class="health-row"><i></i><span>All systems operational</span></div><small>Last sync 14:31:58 UTC</small><div class="user"><div class="avatar">JD</div><div><strong>Jordan Davis</strong><small>Site administrator</small></div><span>•••</span></div></div>
    </aside>
    <section class="content">
      <div class="page-head"><div><div class="breadcrumb">SITES <span>/</span> DC-WEST <span>/</span> FLOOR 02</div><h1>Operations floor</h1><p>Real-time infrastructure overview</p></div><div class="head-actions"><button class="outline-btn">⇩ Export view</button><button class="primary-btn" id="focusBtn">⌖ Focus mode</button></div></div>
      <div class="kpis"><div class="kpi"><span>RACKS ONLINE</span><strong>14 <small>/ 15</small></strong><i class="up">↑ 6.2%</i><div class="spark green"></div></div><div class="kpi"><span>POWER DRAW</span><strong>68.4 <small>kW</small></strong><i class="down">↓ 2.1%</i><div class="spark blue"></div></div><div class="kpi"><span>AVG. TEMP</span><strong>22.8 <small>°C</small></strong><i class="up">↑ 0.4%</i><div class="spark orange"></div></div><div class="kpi"><span>CAPACITY USED</span><strong>71 <small>%</small></strong><i class="up">↑ 3.8%</i><div class="spark purple"></div></div></div>
      <div class="workspace-card"><div class="card-head"><div><h2>GB300 NVL72 floor map</h2><p>18 compute trays · 9 NVLink switch trays · 8 power shelves per rack</p></div><div class="map-tools"><button class="tool active" id="flowToggle">⌁ Liquid loop</button><button class="tool active" id="labelsToggle">⌗ Labels</button><button class="tool" id="resetView">↺ Reset</button></div></div><div class="map-wrap"><div id="scene"></div><div class="map-legend"><span><i class="dot green-dot"></i> Healthy</span><span><i class="dot amber-dot"></i> Warning</span><span><i class="dot red-dot"></i> Critical</span></div><div class="floor-label north">NORTH HALL</div><div class="floor-label south">SOUTH HALL</div></div></div>
      <div class="bottom-grid"><div class="panel"><div class="panel-head"><div><h2>Active incidents <b>3</b></h2><p>Requires attention</p></div><button class="text-btn">View all →</button></div><div class="incident ${incidentOpen ? '' : 'resolved'}"><div class="incident-icon critical">!</div><div class="incident-copy"><strong>Temperature threshold exceeded</strong><span>Rack A-06 · 28.7°C · 4 min ago</span></div><button class="ack-btn" id="ackBtn">${incidentOpen ? 'Acknowledge' : 'Acknowledged'}</button></div><div class="incident"><div class="incident-icon warning">!</div><div class="incident-copy"><strong>High utilization detected</strong><span>Rack A-03 · 88% capacity · 18 min ago</span></div><button class="more-btn">•••</button></div><div class="incident"><div class="incident-icon warning">!</div><div class="incident-copy"><strong>UPS battery maintenance due</strong><span>Power room B · Due today</span></div><button class="more-btn">•••</button></div></div><div class="panel"><div class="panel-head"><div><h2>Power distribution</h2><p>Current draw by zone</p></div><button class="text-btn">Details →</button></div><div class="bar-chart"><div class="bar-label"><span>North Hall</span><b>42.8 kW</b></div><div class="bar"><i style="width:82%"></i></div><div class="bar-label"><span>South Hall</span><b>25.6 kW</b></div><div class="bar"><i style="width:54%"></i></div><div class="chart-foot"><span>0 kW</span><span>25</span><span>50</span></div></div></div></div>
    </section>
    <aside class="details"><div class="details-head"><div><span class="eyebrow">SELECTED ASSET</span><h2 id="assetTitle">Rack ${selected.id}</h2></div><span class="status-pill" id="assetStatus"><i></i> ${selected.state}</span></div><div class="rack-id">◫ <span>RACK-${selected.id.replace('-', '')}</span><button>⋮</button></div><div class="detail-stats"><div><span>Temperature</span><strong id="assetTemp">${selected.temp}°C</strong><small class="good">Within range</small></div><div><span>Power draw</span><strong id="assetPower">${selected.power}</strong><small>of 8.0 kW max</small></div><div><span>Utilization</span><strong id="assetLoad">${selected.load}%</strong><div class="mini-progress"><i id="assetBar" style="width:${selected.load}%"></i></div></div></div><div class="detail-section"><div class="section-label">ASSET DETAILS</div><div class="detail-row"><span>Zone</span><strong id="assetZone">${selected.zone}</strong></div><div class="detail-row"><span>Devices</span><strong id="assetDevices">${selected.devices} active devices</strong></div><div class="detail-row"><span>Uptime</span><strong id="assetUptime">${selected.uptime}</strong></div><div class="detail-row"><span>Last inspection</span><strong>Aug 24, 2025</strong></div></div><div class="detail-section"><div class="section-label">TEMPERATURE · LAST 24 HOURS <span>Live</span></div><div class="line-chart"><svg viewBox="0 0 280 74" preserveAspectRatio="none"><path d="M0 57 C18 55 19 38 35 43 S54 56 66 48 S87 38 98 42 S113 34 127 39 S141 61 154 49 S174 22 186 31 S204 43 216 34 S228 17 240 27 S254 41 280 10" fill="none" stroke="#f09a4a" stroke-width="2"/><path d="M0 57 C18 55 19 38 35 43 S54 56 66 48 S87 38 98 42 S113 34 127 39 S141 61 154 49 S174 22 186 31 S204 43 216 34 S228 17 240 27 S254 41 280 10 V74 H0Z" fill="url(#fade)" opacity=".18"/><defs><linearGradient id="fade" x1="0" x2="0" y1="0" y2="1"><stop stop-color="#f09a4a"/><stop offset="1" stop-color="#f09a4a" stop-opacity="0"/></linearGradient></defs></svg><div><span>00:00</span><span>06:00</span><span>12:00</span><span>Now</span></div></div></div><button class="full-btn" id="inspectBtn">Open asset inspection →</button></aside>
  </main>`;

document
  .querySelector('.map-wrap')
  .insertAdjacentHTML(
    'beforeend',
    `<div id="contextMenu" class="context-menu"><div class="context-kicker">SELECTED RACK</div><strong id="contextRack">GB300 NVL72 · A-03</strong><div class="context-actions"><button data-action="inspect">◫ Inspect trays</button><button data-action="isolate">◉ Isolate rack</button><button data-action="power">ϟ Power controls</button><button data-action="runbook">↗ Open runbook</button></div></div><div id="contextToast" class="context-toast"></div>`,
  );
document
  .querySelector('.map-wrap')
  .insertAdjacentHTML(
    'beforeend',
    `<div class="component-legend"><span><i class="swatch compute"></i>Compute tray ×18</span><span><i class="swatch nvlink"></i>NVLink switch ×9</span><span><i class="swatch power"></i>Power shelf ×8</span><span><i class="swatch liquid"></i>Liquid manifold</span></div>`,
  );
document.querySelector('#contextMenu').addEventListener('click', (event) => {
  const action = event.target.closest('[data-action]')?.dataset.action;
  if (!action) return;
  const labels = {
    inspect: 'Tray inspection opened',
    isolate: 'Rack isolation staged',
    power: 'Power controls opened',
    runbook: 'Runbook opened in a new panel',
  };
  const toast = document.querySelector('#contextToast');
  toast.textContent = labels[action];
  toast.classList.add('show');
  setTimeout(() => toast.classList.remove('show'), 1800);
});
document.querySelector('.detail-stats').querySelectorAll('small')[1].textContent =
  'of 142 kW rack max';
const scene = document.querySelector('#scene');
scene.style.width = '100%';
scene.style.height = '100%';
const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
renderer.setSize(scene.clientWidth, scene.clientHeight);
renderer.shadowMap.enabled = true;
renderer.outputColorSpace = THREE.SRGBColorSpace;
scene.appendChild(renderer.domElement);
const camera = new THREE.PerspectiveCamera(42, scene.clientWidth / scene.clientHeight, 0.1, 100);
camera.position.set(9, 8.5, 11);
const controls = new OrbitControls(camera, renderer.domElement);
controls.target.set(0, 0, 0);
controls.enableDamping = true;
controls.maxPolarAngle = Math.PI / 2.15;
controls.minDistance = 7;
controls.maxDistance = 18;
const world = new THREE.Scene();
world.background = new THREE.Color('#101923');
world.add(new THREE.HemisphereLight('#dff8ff', '#162832', 3.2));
const key = new THREE.DirectionalLight('#ffffff', 4.5);
key.position.set(-4, 10, 6);
key.castShadow = true;
world.add(key);
const fill = new THREE.PointLight('#58d7d0', 5, 14);
fill.position.set(0, 5, 1);
world.add(fill);
const floor = new THREE.Mesh(
  new THREE.PlaneGeometry(15, 9),
  new THREE.MeshStandardMaterial({ color: '#17242d', roughness: 0.87, metalness: 0.1 }),
);
floor.rotation.x = -Math.PI / 2;
floor.receiveShadow = true;
world.add(floor);
const grid = new THREE.GridHelper(15, 30, '#3b4d55', '#26343d');
grid.position.y = 0.01;
grid.scale.z = 0.6;
world.add(grid);
const raycaster = new THREE.Raycaster();
const pointer = new THREE.Vector2();
const rackMeshes = [];
const colors = { healthy: '#44c59a', warning: '#e9a24e', critical: '#f26c68' };
function makeRack(r) {
  const group = new THREE.Group();
  group.position.set(r.x, 1.7, r.z);
  group.userData = r;
  const shell = new THREE.Mesh(
    new THREE.BoxGeometry(0.96, 3.4, 0.72),
    new THREE.MeshStandardMaterial({
      color: '#607984',
      transparent: true,
      opacity: 0.16,
      depthWrite: false,
    }),
  );
  group.add(shell);
  const frameMat = new THREE.MeshBasicMaterial({ color: '#88a7ad' });
  [-0.48, 0.48].forEach((x) => {
    const post = new THREE.Mesh(new THREE.BoxGeometry(0.07, 3.4, 0.72), frameMat);
    post.position.x = x;
    group.add(post);
  });
  const back = new THREE.Mesh(
    new THREE.BoxGeometry(0.88, 3.28, 0.06),
    new THREE.MeshBasicMaterial({ color: '#263942' }),
  );
  back.position.z = -0.32;
  group.add(back);
  const cap = new THREE.Mesh(
    new THREE.BoxGeometry(1.02, 0.035, 0.78),
    new THREE.MeshBasicMaterial({ color: '#9bb8ba' }),
  );
  cap.position.y = 1.7;
  group.add(cap);
  const accent = new THREE.MeshStandardMaterial({
    color: colors[r.state],
    emissive: colors[r.state],
    emissiveIntensity: 0.9,
  });
  const led = new THREE.Mesh(new THREE.BoxGeometry(0.72, 0.06, 0.025), accent);
  led.position.set(0, 1.5, 0.39);
  group.add(led);
  const addBar = (y, color, height = 0.075, z = 0.38) => {
    const bar = new THREE.Mesh(
      new THREE.BoxGeometry(0.75, height, 0.045),
      new THREE.MeshBasicMaterial({ color }),
    );
    bar.position.set(0, y, z);
    group.add(bar);
  };
  for (let i = 0; i < 2; i++) addBar(1.58 + i * 0.11, '#d89d55', 0.08, 0.38);
  for (let i = 0; i < 4; i++) addBar(1.29 + i * 0.08, '#b98247', 0.06, 0.37);
  for (let i = 0; i < 10; i++)
    addBar(0.39 + i * 0.085, i === 8 ? '#43c4b3' : '#24515a', 0.065, 0.37);
  for (let i = 0; i < 9; i++) addBar(-0.38 + i * 0.08, '#8560a8', 0.055, 0.37);
  for (let i = 0; i < 8; i++)
    addBar(-1.16 + i * 0.08, i === 2 ? '#43c4b3' : '#24515a', 0.065, 0.37);
  for (let i = 0; i < 4; i++) addBar(-1.55 + i * 0.08, '#b98247', 0.06, 0.37);
  const manifold = new THREE.Mesh(
    new THREE.BoxGeometry(0.055, 2.72, 0.055),
    new THREE.MeshBasicMaterial({ color: '#4ed0c1' }),
  );
  manifold.position.set(0.39, 0, 0.42);
  group.add(manifold);
  group.userData.mesh = shell;
  rackMeshes.push(shell);
  return group;
}
const rackGroup = new THREE.Group();
racks.forEach((r) => rackGroup.add(makeRack(r)));
world.add(rackGroup);
const aisle = new THREE.Mesh(
  new THREE.BoxGeometry(13, 0.025, 0.55),
  new THREE.MeshBasicMaterial({ color: '#63b6bc', transparent: true, opacity: 0.08 }),
);
aisle.position.set(0, 0.04, 0.1);
world.add(aisle);
const flowGroup = new THREE.Group();
for (let i = -5; i <= 5; i += 1.25) {
  const arrow = new THREE.Mesh(
    new THREE.ConeGeometry(0.09, 0.35, 4),
    new THREE.MeshBasicMaterial({ color: '#6ac2c6', transparent: true, opacity: 0.55 }),
  );
  arrow.rotation.z = -Math.PI / 2;
  arrow.rotation.y = Math.PI / 2;
  arrow.position.set(i, 0.12, 0.12);
  flowGroup.add(arrow);
}
world.add(flowGroup);
const labelGroup = new THREE.Group();
function sprite(text, x, z) {
  const c = document.createElement('canvas');
  c.width = 256;
  c.height = 64;
  const ctx = c.getContext('2d');
  ctx.fillStyle = 'rgba(8,14,20,.82)';
  ctx.roundRect(0, 8, 256, 44, 8);
  ctx.fill();
  ctx.font = 'bold 24px Arial';
  ctx.fillStyle = '#c8d5da';
  ctx.fillText(text, 14, 38);
  const s = new THREE.Sprite(
    new THREE.SpriteMaterial({ map: new THREE.CanvasTexture(c), transparent: true }),
  );
  s.position.set(x, 2.1, z);
  s.scale.set(1.5, 0.38, 1);
  labelGroup.add(s);
}
racks.forEach((r) => sprite(r.id, r.x, r.z));
world.add(labelGroup);
function animate() {
  requestAnimationFrame(animate);
  controls.update();
  flowGroup.children.forEach(
    (a, i) => (a.position.x = -5.5 + ((i * 1.25 + performance.now() / 180) % 11)),
  );
  renderer.render(world, camera);
}
animate();
function selectRack(r) {
  selected = r;
  document.querySelector('#assetTitle').textContent = `Rack ${r.id}`;
  document.querySelector('#assetStatus').innerHTML = `<i></i> ${r.state}`;
  document.querySelector('#assetStatus').className = `status-pill ${r.state}`;
  document.querySelector('#assetTemp').textContent = `${r.temp}°C`;
  document.querySelector('#assetPower').textContent = r.power;
  document.querySelector('#assetLoad').textContent = `${r.load}%`;
  document.querySelector('#assetBar').style.width = `${r.load}%`;
  document.querySelector('#assetZone').textContent = r.zone;
  document.querySelector('#assetDevices').textContent = `${r.devices} active devices`;
  document.querySelector('#assetUptime').textContent = r.uptime;
  document.querySelector('#contextRack').textContent = `GB300 NVL72 · ${r.id}`;
  document.querySelector('#contextMenu').classList.add('open');
}
renderer.domElement.addEventListener('pointerdown', (e) => {
  const rect = renderer.domElement.getBoundingClientRect();
  pointer.x = ((e.clientX - rect.left) / rect.width) * 2 - 1;
  pointer.y = -((e.clientY - rect.top) / rect.height) * 2 + 1;
  raycaster.setFromCamera(pointer, camera);
  const hit = raycaster.intersectObjects(rackMeshes)[0];
  if (hit) selectRack(hit.object.parent.userData);
});
renderer.domElement.addEventListener('pointerdown', () => {
  setTimeout(() => {
    document.querySelector('.rack-id span').textContent =
      `GB300 NVL72 · RACK-${selected.id.replace('-', '')}`;
  }, 0);
});
window.addEventListener('resize', () => {
  camera.aspect = scene.clientWidth / scene.clientHeight;
  camera.updateProjectionMatrix();
  renderer.setSize(scene.clientWidth, scene.clientHeight);
});
document.querySelector('#flowToggle').onclick = () => {
  showFlow = !showFlow;
  flowGroup.visible = showFlow;
  document.querySelector('#flowToggle').classList.toggle('active', showFlow);
};
document.querySelector('#labelsToggle').onclick = () => {
  showLabels = !showLabels;
  labelGroup.visible = showLabels;
  document.querySelector('#labelsToggle').classList.toggle('active', showLabels);
};
document.querySelector('#resetView').onclick = () => {
  camera.position.set(9, 8.5, 11);
  controls.target.set(0, 0, 0);
};
document.querySelector('#ackBtn').onclick = () => {
  incidentOpen = false;
  document.querySelector('#ackBtn').textContent = 'Acknowledged';
  document.querySelector('.incident').classList.add('resolved');
};
document.querySelector('#focusBtn').onclick = () => document.body.classList.toggle('focus-mode');
document.querySelector('#inspectBtn').onclick = () =>
  document
    .querySelector('#assetTitle')
    .animate([{ color: '#fff' }, { color: '#44c59a' }, { color: '#fff' }], { duration: 700 });
