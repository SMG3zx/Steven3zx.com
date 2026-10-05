import { useCallback, useEffect, useMemo, useRef, useState, type ReactElement } from "react";
import { createRoot } from "react-dom/client";
import * as THREE from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import { mergeGeometries } from "three/examples/jsm/utils/BufferGeometryUtils.js";
import { DbConnection, tables } from "./module_bindings";
import { Timestamp } from "spacetimedb";
import "./styles.css";
import "./factory-interaction.css";

type Endpoint = { id: string; address: string; port: number; packets: number; active: boolean };
type Connection = { id: string; source: string; destination: string; packets: number; state: string; active: boolean };
type Packet = { id: string; source: string; destination: string; direction: string; flags: string; payloadLength: number; timestamp: number; sequence?: number; acknowledgment?: number; window?: number };
type PacketInbox = { packets: Packet[]; backlog: PacketBacklog };

// Persist waiting cargo rather than dropping it or keeping an unbounded RAM queue.
// Receipts deduplicate subscription handovers; unfinished cargo replays on reload.
class PacketBacklog {
    readonly inflight = new Set<string>();
    waiting = 0;
    error?: string;
    onError?: (message: string) => void;
    onPacket?: (packet: Packet) => boolean;
    private readonly ready: Promise<IDBDatabase>;
    private releaseOwner?: () => void;
    private closed = false;
    constructor(namespace = "capture") {
        this.ready = new Promise((resolve, reject) => {
            const open = () => {
            const request = indexedDB.open(`tcp-warehouse-backlog-${namespace}`, 2);
            request.onupgradeneeded = () => {
                const db = request.result;
                const cargo = db.objectStoreNames.contains("cargo") ? request.transaction!.objectStore("cargo") : db.createObjectStore("cargo", { keyPath: "id" });
                if (!cargo.indexNames.contains("timestamp")) cargo.createIndex("timestamp", "timestamp");
                if (!cargo.indexNames.contains("state")) cargo.createIndex("state", "state");
                if (!db.objectStoreNames.contains("receipts")) db.createObjectStore("receipts");
                const cursor = cargo.openCursor();
                cursor.onsuccess = () => { const row = cursor.result; if (row) { row.update({ ...row.value, state: 0 }); row.continue(); } };
            };
            request.onsuccess = () => {
                const db = request.result;
                db.onversionchange = () => db.close();
                const tx = db.transaction("cargo", "readwrite"), cursor = tx.objectStore("cargo").index("state").openCursor(1);
                cursor.onsuccess = () => { const row = cursor.result; if (row) { row.update({ ...row.value, state: 0 }); row.continue(); } };
                tx.oncomplete = () => resolve(db); tx.onabort = () => reject(tx.error);
            };
            request.onerror = () => reject(request.error);
            request.onblocked = () => reject(new Error("Close older warehouse tabs to open the packet backlog."));
            };
            if (!navigator.locks) { open(); return; }
            void navigator.locks.request(`tcp-warehouse-owner-${namespace}`, { ifAvailable: true }, async lock => {
                if (!lock) { reject(new Error("Another warehouse tab owns this packet queue. Close it and reload this tab; packets remain on disk.")); return; }
                if (this.closed) { reject(new Error("Warehouse closed before queue acquisition.")); return; }
                const held = new Promise<void>(release => { this.releaseOwner = release; });
                open(); await held;
            }).catch(reject);
        });
        void this.refreshCount();
    }
    private failed(error: unknown): void {
        this.error = `Packet backlog storage failed: ${String(error)}. Capture history remains in SpacetimeDB; animation cannot promise lossless delivery while storage is unavailable.`;
        this.onError?.(this.error);
    }
    async append(packet: Packet): Promise<boolean> {
        try {
            const db = await this.ready;
            let inserted = false;
            await new Promise<void>((resolve, reject) => {
                const tx = db.transaction(["cargo", "receipts"], "readwrite");
                const receipt = tx.objectStore("receipts").getKey(packet.id);
                receipt.onsuccess = () => {
                    if (receipt.result !== undefined) return;
                    const cargo = tx.objectStore("cargo"), existing = cargo.getKey(packet.id);
                    existing.onsuccess = () => { if (existing.result === undefined) { cargo.put({ ...packet, state: 0 }); inserted = true; } };
                };
                tx.oncomplete = () => resolve(); tx.onabort = () => reject(tx.error);
            });
            // Fresh committed arrivals bypass the historical backlog. Each
            // packet still owns its durable row until its package is delivered.
            if (inserted && this.onPacket?.(packet)) this.inflight.add(packet.id);
            return true;
        } catch (error) { this.failed(error); return false; }
    }
    async take(limit: number): Promise<Packet[]> {
        try {
            const db = await this.ready;
            const packets = await new Promise<Packet[]>((resolve, reject) => {
                const tx = db.transaction("cargo", "readwrite"), result: Packet[] = [];
                const cursor = tx.objectStore("cargo").index("state").openCursor(0);
                cursor.onsuccess = () => {
                    const row = cursor.result;
                    if (!row || result.length >= limit) return;
                    const packet = row.value as Packet;
                    if (!this.inflight.has(packet.id)) result.push(packet);
                    row.update({ ...packet, state: 1 });
                    row.continue();
                };
                tx.oncomplete = () => resolve(result); tx.onabort = () => reject(tx.error);
            });
            packets.forEach(packet => this.inflight.add(packet.id));
            return packets;
        } catch (error) { this.failed(error); return []; }
    }
    async complete(ids: string[]): Promise<void> {
        if (!ids.length) return;
        try {
            const db = await this.ready;
            await new Promise<void>((resolve, reject) => {
                const tx = db.transaction(["cargo", "receipts"], "readwrite");
                for (const id of ids) { tx.objectStore("cargo").delete(id); tx.objectStore("receipts").put(true, id); }
                tx.oncomplete = () => resolve(); tx.onabort = () => reject(tx.error);
            });
            ids.forEach(id => this.inflight.delete(id));
        } catch (error) { this.failed(error); }
    }
    async refreshCount(): Promise<void> {
        try {
            const db = await this.ready;
            this.waiting = await new Promise<number>((resolve, reject) => {
                const request = db.transaction("cargo", "readonly").objectStore("cargo").count();
                request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error);
            });
        } catch (error) { this.failed(error); }
    }
    close(): void { this.closed = true; this.onError = undefined; this.onPacket = undefined; this.releaseOwner?.(); void this.ready.then(db => db.close(), () => {}); }
}

const demoEndpoints: Endpoint[] = [
    { id: "client", address: "192.0.2.10", port: 42000, packets: 0, active: false },
    { id: "gateway", address: "10.0.0.1", port: 443, packets: 0, active: false },
    { id: "server", address: "198.51.100.20", port: 443, packets: 0, active: false },
];
const demoConnection: Connection = { id: "demo-flow", source: "192.0.2.10:42000", destination: "198.51.100.20:443", packets: 1, state: "ESTABLISHED", active: true };
const demoPacket: Packet = { id: "demo-pulse", source: "192.0.2.10:42000", destination: "198.51.100.20:443", direction: "outbound", flags: "ACK PSH", payloadLength: 512, timestamp: Date.now() };

function formatNumber(value: number): string { return new Intl.NumberFormat("en-US").format(value); }
function endpointKey(address: string, port: number): string { return `${address}:${port}`; }
const OUTPUT_PAGE_SIZE = 12;
const TARGET_FPS = 120;
// Three matrices per parcel, both CPU and GPU copies. Reserve headroom for
// growth/replacement; this budget is not a measurement of total GPU memory.
const PARCEL_BUFFER_BYTES = 16 * 1024 * 1024;
const PARCEL_BYTES = 3 * 16 * 4 * 2;
const MAX_POOL_CAPACITY = 2 ** Math.floor(Math.log2(PARCEL_BUFFER_BYTES / (PARCEL_BYTES * 2)));
function benchmarkCount(stage: number): number { return stage === 0 ? 0 : 64 * 2 ** (stage - 1); }
type RenderResult = { count: number; fps: number; p95: number; heap?: number; growth?: number; draws: number; pass: boolean };
const BELT_SURFACE_Y = .862;
function parcelHeight(scale: number): number { return BELT_SURFACE_Y + .16 * scale; }
function journeyProgress(clock: number, born: number): number { return Math.max(0, Math.min(1, (clock - born) / 10)); }
// WAREHOUSE_MODEL_BEGIN: bounded, renderer-independent simulation.
const BELT_SPEED = 30;
const ARM_TRANSFER_SECONDS = .08;
type CargoJourney = { packet: Packet; dock: number; born: number; ends: number; points: number[][]; durations: number[]; pickup: number };
function cargoSample(journey: CargoJourney, clock: number): { x: number; y: number; z: number; stage: number; fraction: number } {
    const scale = journey.packet.payloadLength > 1000 ? 1.3 : 1;
    let elapsed = Math.max(0, clock - journey.born), stage = 0;
    while (stage < journey.durations.length - 1 && elapsed > journey.durations[stage]!) elapsed -= journey.durations[stage++]!;
    const fraction = Math.min(1, elapsed / journey.durations[stage]!);
    const a = journey.points[stage]!, b = journey.points[stage + 1]!;
    return { x: a[0]! + (b[0]! - a[0]!) * fraction, y: parcelHeight(scale) + (stage === 4 ? Math.sin(fraction * Math.PI) * .45 : 0), z: a[1]! + (b[1]! - a[1]!) * fraction, stage, fraction };
}
class WarehouseTraffic {
    readonly pending: Packet[] = [];
    readonly active: CargoJourney[] = [];
    readonly docks = new Map<string, number>();
    readonly seen = new Map<string, boolean>();
    readonly queuedAt = new Map<string, number>();
    readonly completed: string[] = [];
    lastClock = 0;
    demoMode = true;
    delivered = 0; version = 0;
    activeBudget = Number.MAX_SAFE_INTEGER; // Renderer applies a byte/frame budget.
    selectedKey?: string;
    keys(): string[] { const keys: string[] = []; for (const [key, slot] of this.docks) keys[slot] = key; return Array.from(keys, key => key ?? ""); }
    dock(key: string): number | undefined {
        const existing = this.docks.get(key); if (existing !== undefined) return existing;
        const used = new Set(this.docks.values());
        let slot = Array.from({ length: 256 }, (_, i) => i).find(i => !used.has(i));
        if (slot === undefined) {
            const pinned = new Set([...this.pending.map(p => p.destination), ...this.active.map(j => j.packet.destination)]);
            if (this.selectedKey) pinned.add(this.selectedKey);
            const idle = [...this.docks].find(([name]) => !pinned.has(name));
            if (!idle) return;
            this.docks.delete(idle[0]); slot = idle[1];
        }
        this.docks.set(key, slot); this.version++; return slot;
    }
    offer(packet: Packet): boolean {
        if (this.demoMode && !packet.id.startsWith("demo")) {
            this.completed.push(...this.pending.map(p => p.id), ...this.active.map(j => j.packet.id));
            this.pending.length = 0; this.active.length = 0; this.queuedAt.clear(); this.docks.clear(); this.seen.clear();
            this.demoMode = false; this.version++;
        }
        if (this.seen.has(packet.id)) return true;
        if (this.pending.length >= 128 || this.dock(packet.destination) === undefined) return false;
        this.seen.set(packet.id, true);
        if (this.seen.size > 512) this.seen.delete(this.seen.keys().next().value!);
        this.pending.push(packet);
        this.queuedAt.set(packet.id, this.lastClock);
        this.dock(packet.source);
        return true;
    }
    step(clock: number): void {
        this.lastClock = clock;
        for (let i = this.active.length - 1; i >= 0; i--) if (clock >= this.active[i]!.ends) { this.completed.push(this.active[i]!.packet.id); this.active.splice(i, 1); this.delivered++; }
        for (let i = 0; i < this.pending.length && this.active.length < this.activeBudget;) {
            const packet = this.pending[i]!;
            const dock = this.docks.get(packet.destination)!;
            const local = dock % 12, lane = local % 6, z = -5 + lane * 2, x = 4 + Math.floor(local / 6) * 4.5;
            let hash = 2166136261;
            for (let k = 0; k < packet.id.length; k++) hash = Math.imul(hash ^ packet.id.charCodeAt(k), 16777619);
            const spread = ((hash >>> 0) / 4294967296 - .5) * .4;
            // NIC information is not yet in the database: all cargo uses one
            // explicitly unmapped input, never a guessed network interface.
            // Stable lateral variation makes simultaneous packets distinct
            // without changing their identity or randomly relocating on updates.
            const points = [[-11.4 + Math.abs(spread), -6 + spread], [-6 + spread, -6 + spread], [-6 + spread, spread], [.9 + spread, spread], [.9 + spread, z + spread], [1.6, z + spread], [x + spread, z + spread], [x + spread, z + 1.15]];
            const durations = points.slice(1).map((b, j) => j === 4 ? ARM_TRANSFER_SECONDS : Math.max(.01, Math.hypot(b[0]! - points[j]![0]!, b[1]! - points[j]![1]!) / BELT_SPEED));
            const lead = durations.slice(0, 4).reduce((a, b) => a + b, 0);
            const born = clock; // Concurrent arrivals; no shared inlet/robot calendar.
            this.active.push({ packet, dock, born, ends: born + durations.reduce((a, b) => a + b, 0) + .08, points, durations, pickup: born + lead });
            this.pending.splice(i, 1);
            this.queuedAt.delete(packet.id);
        }
        console.assert(this.pending.length <= 128 && this.docks.size <= 256 && this.seen.size <= 512, "Warehouse budgets exceeded");
    }
}
// WAREHOUSE_MODEL_END
function endpointName(key: string): string { const split = key.lastIndexOf(":"); const address = key.slice(0, split); return `${address.includes(":") ? `[${address}]` : address}:${key.slice(split + 1)}`; }
function outputPage(keys: string[], page: number): string[] { return keys.slice(Math.min(Math.max(0, page), Math.max(0, Math.ceil(keys.length / OUTPUT_PAGE_SIZE) - 1)) * OUTPUT_PAGE_SIZE).slice(0, OUTPUT_PAGE_SIZE); }
function addressOnly(endpoint: string): string { const separator = endpoint.lastIndexOf(":"); return separator > -1 ? endpoint.slice(0, separator) : endpoint; }
function timestamp(value: unknown): number { if (value instanceof Date) return value.getTime(); if (typeof value === "number") return value; if (typeof value === "bigint") return Number(value / 1_000n); if (value && typeof value === "object" && "microsSinceUnixEpoch" in value) return Number(value.microsSinceUnixEpoch as bigint) / 1000; return Date.now(); }
function packetFromRow(row: any): Packet { return { id: `${row.id}@${row.capturedAt?.microsSinceUnixEpoch ?? timestamp(row.capturedAt)}`, source: row.source, destination: row.destination, direction: row.direction, flags: row.flags, payloadLength: row.payloadLength, timestamp: timestamp(row.capturedAt), sequence: row.sequence, acknowledgment: row.acknowledgment, window: row.window }; }
function readEndpoints(connection: DbConnection): Endpoint[] { return Array.from(connection.db.endpoint.iter()).map((row: any) => ({ id: row.id, address: row.address, port: row.port, packets: Number(row.packetsSent + row.packetsReceived), active: row.active })); }
function readConnections(connection: DbConnection): Connection[] { return Array.from(connection.db.connection.iter()).map((row: any) => ({ id: row.id, source: endpointKey(row.source, row.sourcePort), destination: endpointKey(row.destination, row.destinationPort), packets: Number(row.packets), state: row.state, active: row.active })); }

function FactoryCanvas({ endpoints, connections, packets, inbox, selected, onSelectPacket }: { endpoints: Endpoint[]; connections: Connection[]; packets: Packet[]; inbox: { current: PacketInbox }; selected?: Packet; onSelectPacket: (packet: Packet) => void }): ReactElement {
    const canvasRef = useRef<HTMLCanvasElement>(null);
    const live = useRef({ endpoints, connections, packets, inbox, selected, onSelectPacket });
    live.current = { endpoints, connections, packets, inbox, selected, onSelectPacket };
    const [paused, setPaused] = useState(false);
    const [view, setView] = useState("overview");
    const [metrics, setMetrics] = useState("Measuring renderer…");
    const benchmarkMode = new URLSearchParams(window.location.search).has("benchmark");
    const [benchmarkStatus, setBenchmarkStatus] = useState("Ready: synthetic test; 2 s warm-up + 6 s measurement per load.");
    const [benchmarkResults, setBenchmarkResults] = useState<RenderResult[]>([]);
    const [benchmarkViewport, setBenchmarkViewport] = useState(`${window.innerWidth} × ${window.innerHeight}`);
    const benchmarkCommand = useRef<"start" | "stop" | undefined>(undefined);
    const [page, setPage] = useState(0);
    const [endpointKeys, setEndpointKeys] = useState(() => endpoints.map(e => endpointKey(e.address, e.port)).sort());
    const pageCount = Math.max(1, Math.ceil(endpointKeys.length / OUTPUT_PAGE_SIZE));
    const currentPage = Math.min(page, pageCount - 1);
    const playback = useRef({ paused, view, page: currentPage });
    playback.current = { paused, view, page: currentPage };
    useEffect(() => {
        const canvas = canvasRef.current!;
        const scene = new THREE.Scene();
        scene.background = new THREE.Color(0x18232b);
        scene.fog = new THREE.Fog(0x18232b, 35, 80);
        const renderer = new THREE.WebGLRenderer({ canvas, antialias: false, powerPreference: "low-power" });
        renderer.setPixelRatio(1);
        renderer.shadowMap.enabled = true;
        renderer.shadowMap.type = THREE.PCFShadowMap;
        renderer.toneMapping = THREE.ACESFilmicToneMapping;
        renderer.toneMappingExposure = 1.35;
        const camera = new THREE.PerspectiveCamera(43, 1, .1, 120);
        const controls = new OrbitControls(camera, canvas);
        controls.enableDamping = true;
        controls.target.set(0, 0, 0);
        controls.minDistance = 8;
        controls.maxDistance = 48;
        controls.maxPolarAngle = Math.PI * .48;
        scene.add(new THREE.HemisphereLight(0xd9ecff, 0x746552, 2));
        const sun = new THREE.DirectionalLight(0xffedd0, 3);
        sun.position.set(-8, 18, 6);
        sun.castShadow = true;
        sun.shadow.mapSize.set(1024, 1024);
        Object.assign(sun.shadow.camera, { left: -20, right: 20, top: 16, bottom: -16 });
        scene.add(sun);
        const root = new THREE.Group();
        scene.add(root);
        const steel = new THREE.MeshStandardMaterial({ color: 0x657b89, metalness: .65, roughness: .38 });
        const dark = new THREE.MeshStandardMaterial({ color: 0x273641, roughness: .75 });
        const orange = new THREE.MeshStandardMaterial({ color: 0xf2a442, metalness: .3, roughness: .45 });
        const blue = new THREE.MeshStandardMaterial({ color: 0x3b9eb3, metalness: .3, roughness: .5 });
        const green = new THREE.MeshBasicMaterial({ color: 0x83ffbc });
        const boxGeometry = new THREE.BoxGeometry(1, 1, 1);
        const cylinderGeometry = new THREE.CylinderGeometry(1, 1, 1, 12);
        function box(parent: THREE.Object3D, x: number, y: number, z: number, w: number, h: number, d: number, material: THREE.Material) {
            const mesh = new THREE.Mesh(boxGeometry, material);
            mesh.position.set(x, y, z); mesh.scale.set(w, h, d);
            mesh.castShadow = true; mesh.receiveShadow = true; parent.add(mesh); return mesh;
        }
        function cylinder(parent: THREE.Object3D, x: number, y: number, z: number, radius: number, height: number, material: THREE.Material) {
            const mesh = new THREE.Mesh(cylinderGeometry, material);
            mesh.position.set(x, y, z); mesh.scale.set(radius, height, radius);
            mesh.castShadow = true; parent.add(mesh); return mesh;
        }
        function label(parent: THREE.Object3D, text: string, x: number, y: number, z: number, width = 3) {
            const surface = document.createElement("canvas");
            surface.width = 512; surface.height = 96;
            const ctx = surface.getContext("2d")!;
            ctx.fillStyle = "#20313d"; ctx.fillRect(0, 0, 512, 96);
            ctx.fillStyle = "#f0dfb7"; ctx.font = "bold 28px monospace";
            ctx.textAlign = "center"; ctx.fillText(text.slice(0, 30), 256, 60);
            const texture = new THREE.CanvasTexture(surface);
            const sprite = new THREE.Sprite(new THREE.SpriteMaterial({ map: texture, depthTest: false }));
            sprite.position.set(x, y, z); sprite.scale.set(width, width * .19, 1); parent.add(sprite);
        }
        box(root, 0, -.15, 0, 30, .3, 19, new THREE.MeshStandardMaterial({ color: 0x768281, roughness: .9 }));
        const grid = new THREE.GridHelper(30, 30, 0x929d98, 0x7f8b88); grid.position.y = .02; root.add(grid);
        // Open-sided warehouse: corrugated loading wall, doors and safety bollards.
        box(root, -13.6, 1.9, 0, .3, 3.8, 16, steel);
        for (let z = -8; z <= 8; z += .35) box(root, -13.38, 1.9, z, .06, 3.6, .07, dark);
        const docks: THREE.Vector3[] = [];
        for (let i = 0; i < 4; i++) {
            const z = -6 + i * 4;
            box(root, -13.15, 1.5, z, .12, 2.8, 2.5, dark);
            for (let y = .3; y < 2.8; y += .23) box(root, -13.02, y, z, .08, .045, 2.4, steel);
            box(root, -11.8, .3, z, 2.6, .6, 2.7, blue);
            for (const side of [-1, 1]) cylinder(root, -10.5, .7, z + side * 1.2, .09, 1.4, orange);
            label(root, i === 0 ? "CAPTURE / NIC UNKNOWN" : "SPARE BAY / INACTIVE", -12, 3.4, z, 2.5);
            docks.push(new THREE.Vector3(-11.4, 1.0, z));
        }
        const rollers: THREE.Mesh[] = [];
        const beltMarks: THREE.Mesh[] = [];
        function conveyor(a: THREE.Vector3, b: THREE.Vector3, width = .95, parent: THREE.Object3D = root) {
            const group = new THREE.Group();
            group.position.copy(a).lerp(b, .5);
            group.position.y = 0; // Roller coordinates already include their floor height.
            const length = a.distanceTo(b);
            group.rotation.y = -Math.atan2(b.z - a.z, b.x - a.x);
            parent.add(group);
            box(group, 0, .67, 0, length, .16, width, dark);
            for (const side of [-1, 1]) {
                box(group, 0, .78, side * width * .55, length, .1, .07, steel);
                for (let x = -length / 2 + .3; x < length / 2; x += 1.5) box(group, x, .35, side * width * .43, .07, .7, .07, steel);
            }
            for (let x = -length / 2 + .15; x < length / 2; x += .28) {
                const roller = cylinder(group, x, .77, 0, .07, width, steel);
                roller.rotation.x = Math.PI / 2; rollers.push(roller);
            }
            for (let x = -length / 2; x < length / 2; x += .8) {
                const mark = box(group, x, .855, 0, .055, .012, width * .8, orange);
                mark.userData.dynamic = true;
                mark.userData.start = x; mark.userData.length = length; beltMarks.push(mark);
            }
        }
        for (const dock of docks) conveyor(dock, new THREE.Vector3(-6, 1, dock.z));
        conveyor(new THREE.Vector3(-6, 1, -6), new THREE.Vector3(-6, 1, 6), 1.15);
        conveyor(new THREE.Vector3(-6, 1, 0), new THREE.Vector3(1, 1, 0), 1.25);
        conveyor(new THREE.Vector3(.9, 0, -5), new THREE.Vector3(.9, 0, 5), .95);
        // Barcode scanner gantry with a visible scanning plane.
        for (const z of [-.85, .85]) box(root, -3.5, 1.3, z, .14, 2.6, .14, blue);
        box(root, -3.5, 2.6, 0, .3, .25, 1.9, blue);
        const scanMaterial = new THREE.MeshBasicMaterial({ color: 0x53e4ef, transparent: true, opacity: .2, side: THREE.DoubleSide });
        const scanner = box(root, -3.5, 1.5, 0, .035, 1.4, 1.4, scanMaterial);
        scanner.userData.dynamic = true;
        // Six sorting cells, each with a proper shoulder, elbow and gripper.
        const arms: { shoulder: THREE.Group; elbow: THREE.Group; wrist: THREE.Group }[] = [];
        for (let i = 0; i < 6; i++) {
            const z = -5 + i * 2;
            conveyor(new THREE.Vector3(1, 1, z), new THREE.Vector3(10.5, 1, z));
            const arm = new THREE.Group(); arm.position.set(.6, 0, z - .8); root.add(arm);
            arm.userData.dynamic = true;
            cylinder(arm, 0, .22, 0, .4, .44, dark);
            cylinder(arm, 0, .65, 0, .22, .5, orange);
            const shoulder = new THREE.Group(); shoulder.position.y = .9; arm.add(shoulder);
            cylinder(shoulder, 0, 0, 0, .22, .3, steel).rotation.z = Math.PI / 2;
            box(shoulder, 0, .55, 0, .22, 1.1, .25, orange);
            const elbow = new THREE.Group(); elbow.position.y = 1.1; shoulder.add(elbow);
            cylinder(elbow, 0, 0, 0, .2, .28, steel).rotation.z = Math.PI / 2;
            box(elbow, 0, .45, 0, .18, .9, .2, orange);
            const wrist = new THREE.Group(); wrist.position.y = .9; elbow.add(wrist);
            box(wrist, 0, 0, 0, .4, .18, .25, dark);
            for (const side of [-1, 1]) box(wrist, side * .18, -.16, 0, .05, .3, .1, steel);
            arms.push({ shoulder, elbow, wrist });
        }
        // Each visible endpoint owns a separate spur and unloading platform.
        // Paging keeps the number of models and label textures bounded.
        const machineSlots = Array.from({ length: OUTPUT_PAGE_SIZE }, (_, i) => {
            const lane = i % 6, col = Math.floor(i / 6);
            const group = new THREE.Group(); group.position.set(4 + col * 4.5, 0, -5 + lane * 2);
            group.userData.dynamic = true;
            conveyor(new THREE.Vector3(0, 0, 0), new THREE.Vector3(0, 0, .95), .7, group);
            box(group, 0, BELT_SURFACE_Y - .08, 1.15, 1.0, .16, .7, blue);
            for (const side of [-1, 1]) box(group, side * .4, .35, 1.15, .08, .7, .5, steel);
            box(group, 0, 1.2, 1.5, 1.0, .65, .08, dark);
            const beacon = new THREE.MeshBasicMaterial({ color: 0x34444f });
            cylinder(group, .45, 1.65, 1.5, .035, .2, beacon);
            const surface = document.createElement("canvas"); surface.width = 1024; surface.height = 192;
            const texture = new THREE.CanvasTexture(surface);
            const sign = new THREE.Sprite(new THREE.SpriteMaterial({ map: texture, depthTest: true }));
            sign.position.set(0, 1.95, 1.15); sign.scale.set(3.6, .675, 1); group.add(sign);
            group.visible = false; root.add(group); return { group, surface, texture, beacon, key: "" };
        });
        const positions = new Map<string, number>();
        const traffic = new WarehouseTraffic();
        traffic.activeBudget = MAX_POOL_CAPACITY;
        let topology = "";
        function updateMachines() {
            const keys = outputPage(traffic.keys(), playback.current.page);
            const signature = keys.join("|");
            if (signature === topology) return;
            topology = signature; positions.clear();
            keys.forEach((key, i) => positions.set(key, i));
            console.assert(positions.size === keys.length, "Each endpoint must own exactly one dock");
            machineSlots.forEach((slot, i) => {
                slot.group.visible = !!keys[i];
                if (!keys[i] || slot.key === keys[i]) return;
                slot.key = keys[i]!;
                const ctx = slot.surface.getContext("2d")!;
                ctx.fillStyle = "#20313d"; ctx.fillRect(0, 0, 1024, 192);
                ctx.fillStyle = "#f0dfb7"; ctx.textAlign = "center"; ctx.font = "bold 42px monospace";
                ctx.fillText(`OUTPUT ${playback.current.page * OUTPUT_PAGE_SIZE + i + 1}`, 512, 62);
                const name = endpointName(slot.key);
                ctx.fillStyle = "#ffffff"; ctx.font = `${Math.min(36, Math.floor(1500 / name.length))}px monospace`;
                ctx.fillText(name, 512, 133);
                slot.texture.needsUpdate = true;
            });
        }
        const parcelMaterial = new THREE.MeshStandardMaterial({ color: 0xd5a168, roughness: .9 });
        const tapeMaterial = new THREE.MeshStandardMaterial({ color: 0xf0ddaa, roughness: .8 });
        const barcodeMaterial = new THREE.MeshBasicMaterial({ color: 0x253642 });
        const outlineGeometry = new THREE.EdgesGeometry(boxGeometry);
        const outlineMaterial = new THREE.LineBasicMaterial({ color: 0x83ffbc });
        const part = (x: number, y: number, z: number, w: number, h: number, d: number) => boxGeometry.clone().scale(w, h, d).translate(x, y, z);
        const tapeParts = [part(0, .166, 0, .085, .014, .35), part(0, 0, .176, .085, .32, .015), part(.11, .05, .183, .12, .09, .013)];
        const barcodeParts = Array.from({ length: 5 }, (_, i) => part(.07 + i * .018, .05, .195, .009, .07, .004));
        const parcelGeometries = [part(0, 0, 0, .42, .32, .34), mergeGeometries(tapeParts)!, mergeGeometries(barcodeParts)!];
        [...tapeParts, ...barcodeParts].forEach(geometry => geometry.dispose());
        const parcelMaterials = [parcelMaterial, tapeMaterial, barcodeMaterial];
        let parcelCapacity = 0, parcelBatches: THREE.InstancedMesh[] = [];
        const ensureParcelCapacity = (count: number, trim = false) => {
            const capacity = Math.max(64, 2 ** Math.ceil(Math.log2(Math.max(1, count))));
            if (capacity === parcelCapacity || (!trim && capacity < parcelCapacity)) return;
            console.assert(capacity <= MAX_POOL_CAPACITY, "Parcel matrix memory budget exceeded");
            if (capacity > MAX_POOL_CAPACITY) throw new Error("Parcel pool memory budget exceeded");
            // No old GPU buffers survive growth, shrink, stop or hot reload.
            parcelBatches.forEach(batch => { batch.removeFromParent(); batch.dispose(); });
            parcelCapacity = capacity;
            parcelBatches = parcelGeometries.map((geometry, i) => {
                const batch = new THREE.InstancedMesh(geometry, parcelMaterials[i]!, capacity);
                batch.userData.dynamic = true; batch.instanceMatrix.setUsage(THREE.DynamicDrawUsage);
                batch.count = 0; batch.frustumCulled = false;
                batch.castShadow = true; batch.receiveShadow = true; root.add(batch); return batch;
            });
        };
        ensureParcelCapacity(64);
        const parcelMatrix = new THREE.Matrix4(), parcelPosition = new THREE.Vector3(), parcelScale = new THREE.Vector3(), parcelRotation = new THREE.Quaternion();
        const visiblePackets: Packet[] = [];
        const selectedOutline = new THREE.LineSegments(outlineGeometry, outlineMaterial);
        selectedOutline.visible = false; root.add(selectedOutline);
        // Static objects with identical geometry/material share one GPU draw.
        // Moving robots, signs, output docks and pickable parcels stay separate.
        root.updateMatrixWorld(true);
        const batches = new Map<string, THREE.Mesh[]>();
        root.traverse(object => {
            if (!(object instanceof THREE.Mesh) || Array.isArray(object.material)) return;
            for (let parent: THREE.Object3D | null = object; parent; parent = parent.parent) if (parent.userData.dynamic) return;
            const key = object.geometry.uuid + object.material.uuid;
            const batch = batches.get(key) ?? []; batch.push(object); batches.set(key, batch);
        });
        for (const meshes of batches.values()) {
            if (meshes.length < 2) continue;
            const batch = new THREE.InstancedMesh(meshes[0]!.geometry, meshes[0]!.material, meshes.length);
            meshes.forEach((mesh, i) => { batch.setMatrixAt(i, mesh.matrixWorld); mesh.removeFromParent(); });
            batch.castShadow = true; batch.receiveShadow = true; root.add(batch);
        }
        const routePositions = new Float32Array(8 * 3);
        const routeGeometry = new THREE.BufferGeometry(); routeGeometry.setAttribute("position", new THREE.BufferAttribute(routePositions, 3));
        const route = new THREE.Line(routeGeometry, new THREE.LineBasicMaterial({ color: 0x83ffbc }));
        route.visible = false; root.add(route);
        const raycaster = new THREE.Raycaster(), pointer = new THREE.Vector2();
        let pointerStart = new THREE.Vector2();
        const down = (event: PointerEvent) => { pointerStart.set(event.clientX, event.clientY); };
        const pick = (event: PointerEvent) => {
            if (benchStage >= 0) return; // Synthetic load is not inspectable captured traffic.
            if (pointerStart.distanceTo(new THREE.Vector2(event.clientX, event.clientY)) > 5) return;
            const rect = canvas.getBoundingClientRect();
            pointer.set((event.clientX - rect.left) / rect.width * 2 - 1, -(event.clientY - rect.top) / rect.height * 2 + 1);
            raycaster.setFromCamera(pointer, camera);
            parcelBatches[0]!.computeBoundingSphere();
            const hit = raycaster.intersectObject(parcelBatches[0]!, false)[0];
            const packet = hit?.instanceId === undefined ? undefined : visiblePackets[hit.instanceId];
            if (packet) { live.current.onSelectPacket(packet); playback.current.paused = true; setPaused(true); }
        };
        canvas.addEventListener("pointerdown", down);
        canvas.addEventListener("pointerup", pick);
        const key = (event: KeyboardEvent) => { if (event.code === "Space" && !(event.target instanceof HTMLButtonElement)) { event.preventDefault(); setPaused(value => !value); } };
        window.addEventListener("keydown", key);
        const resize = () => { const rect = canvas.getBoundingClientRect(); renderer.setSize(rect.width, rect.height, false); camera.aspect = rect.width / Math.max(1, rect.height); camera.updateProjectionMatrix(); };
        const observer = new ResizeObserver(resize); observer.observe(canvas); resize();
        let frame = 0, previous = performance.now(), clock = 0, appliedView = "", measuredAt = previous, frames = 0, rafFrames = 0, renderedAt = previous;
        const scratch = new THREE.Vector3();
        let dockVersion = -1, dockPage = -1, selectedId: string | undefined;
        let inspectedJourney: CargoJourney | undefined;
        const timings: number[] = [];
        let benchStage = -1, benchStarted = 0, benchMeasured = 0, benchPrevious = 0, benchFrames = 0;
        let lastRenderWasBenchmark = false;
        let peakVisible = 0;
        let backlogReading = false, backlogPolledAt = 0, disposed = false;
        live.current.inbox.current.backlog.onPacket = packet => {
            const input = live.current.inbox.current;
            if (disposed || playback.current.paused || input.packets.length >= 2048 || traffic.active.length + input.packets.length >= traffic.activeBudget) return false;
            input.packets.push(packet); return true;
        };
        let benchWidth = 0, benchHeight = 0;
        let benchHeapStart: number | undefined, benchHeapPeak: number | undefined;
        const benchIntervals: number[] = [];
        const heapBytes = () => (performance as Performance & { memory?: { usedJSHeapSize: number } }).memory?.usedJSHeapSize;
        const beginStage = (now: number) => {
            benchWidth = canvas.width; benchHeight = canvas.height;
            setBenchmarkViewport(`${benchWidth} × ${benchHeight}`);
            benchStarted = now; benchMeasured = 0; benchPrevious = 0; benchFrames = 0; benchIntervals.length = 0;
            benchHeapStart = heapBytes(); benchHeapPeak = benchHeapStart;
            const count = benchmarkCount(benchStage);
            ensureParcelCapacity(count, benchStage === 0);
            setBenchmarkStatus(`Warming up ${count} synthetic parcels. Target 120 FPS; strict minimum 60 FPS.`);
        };
        const animate = (now: number) => {
            if (!document.hidden) rafFrames++;
            if (benchStage >= 0 && (canvas.width !== benchWidth || canvas.height !== benchHeight)) { benchStage = -1; setBenchmarkStatus("Stopped: canvas resolution changed. Restart for comparable results."); }
            if (document.hidden && benchStage >= 0) { benchStage = -1; setBenchmarkStatus("Stopped: hidden tab invalidates timings."); }
            if (benchStage >= 0 && (playback.current.paused || playback.current.view !== "overview")) { benchStage = -1; setBenchmarkStatus("Stopped: pause or camera preset changed the benchmark workload."); }
            if (document.hidden || now - renderedAt < 1000 / TARGET_FPS - .1) {
                if (document.hidden) { previous = now; renderedAt = now; rafFrames = 0; frames = 0; measuredAt = now; }
                frame = requestAnimationFrame(animate); return;
            }
            // Keep fractional frame debt. Resetting to `now` divides refresh
            // rates (e.g. 180 Hz -> 90 FPS) instead of averaging the 120 target.
            renderedAt += Math.floor((now - renderedAt + .1) / (1000 / TARGET_FPS)) * (1000 / TARGET_FPS);
            const workStarted = performance.now();
            if (benchmarkCommand.current) {
                const command = benchmarkCommand.current; benchmarkCommand.current = undefined;
                benchStage = command === "start" ? 0 : -1;
                if (benchStage >= 0) { setBenchmarkResults([]); beginStage(now); }
                else setBenchmarkStatus("Stopped; adaptive live pool restored.");
            }
            const delta = Math.min((now - previous) / 1000, .05); previous = now;
            if (!playback.current.paused) clock += delta;
            if (appliedView !== playback.current.view) {
                appliedView = playback.current.view;
                camera.position.copy(appliedView === "top" ? new THREE.Vector3(0, 27, .1) : appliedView === "sorter" ? new THREE.Vector3(-5, 6, 9) : new THREE.Vector3(22, 22, 26));
                controls.target.set(appliedView === "sorter" ? -2 : 0, 0, 0);
            }
            traffic.selectedKey = live.current.selected?.destination;
            const input = live.current.inbox.current;
            let accepted = 0;
            while (accepted < input.packets.length) {
                if (!traffic.offer(input.packets[accepted]!)) {
                    if (traffic.pending.length >= 128 && traffic.active.length < traffic.activeBudget && !playback.current.paused) traffic.step(clock);
                    if (!traffic.offer(input.packets[accepted]!)) break;
                }
                accepted++;
            }
            input.packets.splice(0, accepted);
            if (!backlogReading && input.packets.length === 0 && now - backlogPolledAt >= 8) {
                backlogReading = true; backlogPolledAt = now;
                void input.backlog.take(Math.min(2048, Math.max(0, traffic.activeBudget - traffic.active.length))).then(packets => { if (!disposed) input.packets.push(...packets); }).finally(() => { backlogReading = false; });
            }
            if (!playback.current.paused) traffic.step(clock);
            void input.backlog.complete(traffic.completed.splice(0));
            if (traffic.version !== dockVersion || dockPage !== playback.current.page) {
                dockVersion = traffic.version; dockPage = playback.current.page;
                setEndpointKeys(traffic.keys()); updateMachines();
            }
            const selected = live.current.selected;
            if (selected?.id !== selectedId) {
                selectedId = selected?.id;
                inspectedJourney = undefined;
                const index = selected ? traffic.docks.get(selected.destination) : undefined;
                if (index !== undefined && Math.floor(index / 12) !== playback.current.page) setPage(Math.floor(index / 12));
            }
            visiblePackets.length = 0; selectedOutline.visible = false;
            const syntheticCount = benchStage >= 0 ? benchmarkCount(benchStage) : 0;
            // Only upload visible actors, not paged-out or unborn journeys.
            // Arrivals are visible in shared receiving/sorting lanes regardless
            // of which endpoint page is selected. Final docks remain paged.
            const visibleJourneys = benchStage >= 0 ? [] : traffic.active.filter(j => clock >= j.born && (Math.floor(j.dock / 12) === playback.current.page || clock <= j.pickup + ARM_TRANSFER_SECONDS));
            const visibleCount = benchStage >= 0 ? syntheticCount : visibleJourneys.length;
            peakVisible = Math.max(peakVisible, visibleCount);
            ensureParcelCapacity(visibleCount, benchStage < 0 && lastRenderWasBenchmark);
            lastRenderWasBenchmark = benchStage >= 0;
            for (let i = 0; i < visibleCount; i++) {
                let scale = 1;
                if (benchStage >= 0) {
                    const lane = i % 6, rank = Math.floor(i / 6), perLane = Math.ceil(syntheticCount / 6);
                    parcelPosition.set(1.7 + ((rank / Math.max(1, perLane) * 8 + clock * .3) % 8), parcelHeight(1), -5 + lane * 2);
                } else {
                    const journey = visibleJourneys[i]!, sample = cargoSample(journey, clock);
                    scale = journey.packet.payloadLength > 1000 ? 1.3 : 1;
                    parcelPosition.set(sample.x, sample.y, sample.z);
                    visiblePackets.push(journey.packet);
                    if (journey.packet.id === selected?.id) {
                        selectedOutline.visible = true; selectedOutline.position.copy(parcelPosition);
                        selectedOutline.scale.set(.45 * scale, .35 * scale, .37 * scale);
                    }
                    console.assert(sample.y - .16 * scale >= BELT_SURFACE_Y - .0001, "Parcel below belt");
                }
                parcelScale.setScalar(scale); parcelMatrix.compose(parcelPosition, parcelRotation, parcelScale);
                for (const batch of parcelBatches) batch.setMatrixAt(i, parcelMatrix);
            }
            for (const batch of parcelBatches) {
                batch.count = visibleCount;
                batch.instanceMatrix.clearUpdateRanges();
                if (visibleCount) batch.instanceMatrix.addUpdateRange(0, visibleCount * 16);
                batch.instanceMatrix.needsUpdate = true;
                // Bounds are recomputed on inspection, not every frame.
            }
            // A selected route remains available after the parcel is delivered.
            inspectedJourney = traffic.active.find(j => j.packet.id === selected?.id) ?? inspectedJourney;
            const selectedJourney = selected ? inspectedJourney : undefined;
            route.visible = !!selectedJourney && Math.floor(selectedJourney.dock / 12) === playback.current.page;
            if (selectedJourney) {
                selectedJourney.points.forEach((point, i) => routePositions.set([point[0]!, BELT_SURFACE_Y + .28, point[1]!], i * 3));
                route.geometry.attributes.position!.needsUpdate = true;
                route.geometry.computeBoundingSphere();
            }
            machineSlots.forEach((dock, i) => {
                const logical = playback.current.page * 12 + i;
                const delivered = traffic.active.some(j => j.dock === logical && clock >= j.ends - .08);
                dock.beacon.color.setHex(delivered || dock.key === selected?.destination ? 0x83ffbc : 0x34444f);
            });
            for (const mark of beltMarks) mark.position.x = ((mark.userData.start as number) + clock * BELT_SPEED + (mark.userData.length as number) / 2) % (mark.userData.length as number) - (mark.userData.length as number) / 2;
            arms.forEach((arm, lane) => {
                const cargo = traffic.active.find(j => j.dock % 6 === lane && clock >= j.pickup && clock <= j.pickup + ARM_TRANSFER_SECONDS);
                const point = cargo ? cargoSample(cargo, clock) : undefined;
                const dx = point ? point.x - .6 : .45;
                const dz = point ? point.z - (-5 + lane * 2 - .8) : .8;
                const y = point ? point.y + .16 * (cargo!.packet.payloadLength > 1000 ? 1.3 : 1) + .3 : 1.45;
                const radius = Math.hypot(dx, dz), height = y - .9;
                const elbow = Math.acos(THREE.MathUtils.clamp((radius * radius + height * height - 1.1 * 1.1 - .9 * .9) / (2 * 1.1 * .9), -1, 1));
                const shoulder = Math.atan2(radius, height) - Math.atan2(.9 * Math.sin(elbow), 1.1 + .9 * Math.cos(elbow));
                arm.shoulder.rotation.y = -Math.atan2(dz, dx);
                arm.shoulder.rotation.z = -shoulder; arm.elbow.rotation.z = -elbow;
                arm.wrist.rotation.z = shoulder + elbow;
            });
            scanMaterial.opacity = .18 + Math.sin(clock * 6) * .1;
            scanner.position.y = 1.45 + Math.sin(clock * 2) * .3;
            controls.update();
            if (!document.hidden) {
                renderer.render(scene, camera); frames++;
            }
            if (benchStage >= 0) {
                const heap = heapBytes();
                if (heap !== undefined) benchHeapPeak = Math.max(benchHeapPeak ?? 0, heap);
                if (heap !== undefined && heap > 256_000_000) {
                    benchStage = -1; setBenchmarkStatus("Stopped at 256 MB JS heap safety ceiling. GPU memory is not measured.");
                } else if (now - benchStarted >= 2000) {
                    if (!benchMeasured) { benchMeasured = now; setBenchmarkStatus(`Measuring ${benchmarkCount(benchStage)} synthetic parcels for 6 seconds…`); }
                    if (benchPrevious) { benchIntervals.push(now - benchPrevious); benchFrames++; }
                    benchPrevious = now;
                    if (now - benchMeasured >= 6000) {
                        const sorted = [...benchIntervals].sort((a, b) => a - b);
                        const fps = benchFrames * 1000 / (now - benchMeasured), p95 = sorted[Math.floor(sorted.length * .95)] ?? Infinity;
                        const count = benchmarkCount(benchStage), pass = fps >= 60 && p95 <= 1000 / 60;
                        setBenchmarkResults(results => [...results, { count, fps, p95, heap: benchHeapPeak === undefined ? undefined : benchHeapPeak / 1_000_000, growth: benchHeapPeak === undefined || benchHeapStart === undefined ? undefined : (benchHeapPeak - benchHeapStart) / 1_000_000, draws: renderer.info.render.calls, pass }]);
                        if (!pass || benchmarkCount(benchStage + 1) > MAX_POOL_CAPACITY) {
                            benchStage = -1;
                            setBenchmarkStatus(pass ? "Parcel matrix byte budget reached. Largest passing load is a short-run lower bound, not an OOM guarantee." : count === 0 ? "Empty-scene baseline misses 60 FPS / 16.67 ms p95. No packet capacity can be certified on this browser/display." : "First failing load reached. Previous passing load is a short-run lower bound; a longer soak is still needed.");
                        } else { benchStage++; beginStage(now); }
                    }
                }
            }
            if (now - measuredAt >= 1000) {
                const heap = (performance as Performance & { memory?: { usedJSHeapSize: number } }).memory;
                void input.backlog.refreshCount();
                const sorted = [...timings].sort((a, b) => a - b);
                const fps = frames * 1000 / (now - measuredAt), cpuP95 = sorted[Math.floor(sorted.length * .95)] ?? 0;
                if (benchStage < 0) {
                    if (heap && heap.usedJSHeapSize > 256_000_000) traffic.activeBudget = 0;
                    else if (fps < 60 || cpuP95 > 1000 / 60) traffic.activeBudget = Math.max(64, Math.floor(traffic.activeBudget / 2));
                    else if (traffic.activeBudget === 0) traffic.activeBudget = 64;
                    else if (fps > 75 && traffic.active.length >= traffic.activeBudget * .75) traffic.activeBudget = Math.min(MAX_POOL_CAPACITY, traffic.activeBudget * 2);
                }
                setMetrics(`${Math.round(fps)} FPS / 120 target · ${Math.round(rafFrames * 1000 / (now - measuredAt))} browser RAF callbacks/s · ${heap ? (heap.usedJSHeapSize / 1_000_000).toFixed(1) + " MB JS heap" : "JS heap unavailable"} · CPU frame p95 ${cpuP95.toFixed(2)} ms · ${renderer.info.render.calls} draws · ${visibleCount} packages visible (peak ${peakVisible}) · ${traffic.active.length}/${traffic.activeBudget} adaptive active budget · ${traffic.pending.length} staged · ${input.backlog.waiting} unfinished on disk · ${traffic.delivered} delivered · one packet = one package · no queue expiry · receiving interface unknown`);
                measuredAt = now; frames = 0; rafFrames = 0;
            }
            timings.push(performance.now() - workStarted); if (timings.length > 120) timings.shift();
            frame = requestAnimationFrame(animate);
        };
        frame = requestAnimationFrame(animate);
        return () => {
            disposed = true;
            live.current.inbox.current.backlog.onPacket = undefined;
            cancelAnimationFrame(frame); observer.disconnect(); controls.dispose();
            canvas.removeEventListener("pointerdown", down); canvas.removeEventListener("pointerup", pick); window.removeEventListener("keydown", key);
            const geometries = new Set<THREE.BufferGeometry>(), materials = new Set<THREE.Material>();
            scene.traverse(object => { const mesh = object as THREE.Mesh; if (mesh.geometry) geometries.add(mesh.geometry); if (mesh.material) (Array.isArray(mesh.material) ? mesh.material : [mesh.material]).forEach(material => materials.add(material)); });
            scene.traverse(object => { if (object instanceof THREE.InstancedMesh) object.dispose(); });
            geometries.forEach(geometry => geometry.dispose());
            materials.forEach(material => { (material as THREE.SpriteMaterial).map?.dispose(); material.dispose(); });
            sun.shadow.map?.dispose();
            renderer.dispose();
        };
    }, []);
    return <><canvas ref={canvasRef} className="factory-canvas" aria-label="Interactive warehouse packet sorter" />
        {benchmarkMode && <aside className="render-benchmark" aria-label="Renderer capacity benchmark"><h2>Renderer capacity / synthetic cargo</h2><p>{benchmarkStatus}</p><button onClick={() => { setView("overview"); setPaused(false); benchmarkCommand.current = "start"; }}>Run capacity benchmark</button><button onClick={() => { benchmarkCommand.current = "stop"; }}>Stop benchmark</button><button onClick={async () => { if (!new URLSearchParams(location.search).has("demo")) return; setPaused(true); const run = Date.now(); const stored = await Promise.all(Array.from({ length: 512 }, (_, i) => live.current.inbox.current.backlog.append({ ...demoPacket, id: `demo-backlog-${run}-${i}`, timestamp: run + i }))); setBenchmarkStatus(stored.every(Boolean) ? "512 synthetic packets persisted. Resume to drain; reload preserves unfinished cargo." : "Storage failed; inspect the error banner."); }}>Queue 512 demo packets</button><p>120 FPS target · 60 FPS minimum · 256 MB JS heap ceiling · DPR 1 · {benchmarkViewport}. 3 instanced parcel batches · adaptive capacity · 16 MB matrix budget with resize headroom. High loads overlap on belts. Heap excludes GPU/browser-process memory. This is not capture throughput or a long-duration memory test.</p><table><thead><tr><th>Parcels</th><th>FPS</th><th>Frame p95 ms</th><th>Peak heap MB</th><th>Growth MB</th><th>Draws</th><th>Result</th></tr></thead><tbody>{benchmarkResults.map(row => <tr key={row.count}><td>{row.count}</td><td>{row.fps.toFixed(2)}</td><td>{row.p95.toFixed(2)}</td><td>{row.heap?.toFixed(1) ?? "unavailable"}</td><td>{row.growth?.toFixed(1) ?? "unavailable"}</td><td>{row.draws}</td><td>{row.pass ? "PASS" : "FAIL"}</td></tr>)}</tbody></table></aside>}
        <nav className="floor-controls" aria-label="Warehouse controls"><button onClick={() => setPaused(value => !value)}>{paused ? "▶ Resume" : "Ⅱ Pause"}</button>{["overview", "sorter", "top"].map(item => <button key={item} aria-pressed={view === item} onClick={() => setView(item)}>{item}</button>)}<div className="output-pager"><button aria-label="Previous output docks" disabled={currentPage === 0} onClick={() => setPage(currentPage - 1)}>←</button><span>Outputs {currentPage * OUTPUT_PAGE_SIZE + 1}–{Math.min((currentPage + 1) * OUTPUT_PAGE_SIZE, endpointKeys.length)} / {endpointKeys.length}</span><button aria-label="Next output docks" disabled={currentPage === pageCount - 1} onClick={() => setPage(currentPage + 1)}>→</button></div><details className="output-directory"><summary>Dock names</summary><ol start={currentPage * OUTPUT_PAGE_SIZE + 1}>{outputPage(endpointKeys, currentPage).map(key => <li key={key}>{endpointName(key)}</li>)}</ol></details><details className="performance-details"><summary>Performance</summary><span>{metrics.replace("60 target", "120 target")}</span></details><button onClick={() => window.location.assign("/?demo=1&benchmark=1")}>Benchmark</button></nav></>;
}

function App(): ReactElement {
    const demoMode = new URLSearchParams(window.location.search).has("demo");
    const backlog = useMemo(() => new PacketBacklog(demoMode ? "demo" : "capture"), []);
    const inbox = useRef<PacketInbox>({ packets: [], backlog });
    const hasLive = useRef(false);
    const [captureProblem, setCaptureProblem] = useState<string>();
    const [backlogProblem, setBacklogProblem] = useState<string>();
    useEffect(() => {
        backlog.onError = setBacklogProblem;
        if (backlog.error) setBacklogProblem(backlog.error);
        return () => backlog.close();
    }, [backlog]);
    useEffect(() => {
        if (demoMode) return;
        const socket = new WebSocket(import.meta.env.VITE_CAPTURE_URI ?? "ws://127.0.0.1:8787");
        socket.onmessage = event => {
            try { const message = JSON.parse(event.data); if (message.type === "status") setCaptureProblem(message.status === "error" ? message.message ?? "Packet capture failed" : undefined); } catch { /* Ignore malformed bridge messages. */ }
        };
        socket.onerror = () => setCaptureProblem("Capture bridge unavailable. Database connectivity does not imply packets are being captured.");
        return () => { socket.onmessage = null; socket.onerror = null; socket.close(); };
    }, []);
    const [feedOpen, setFeedOpen] = useState(false);
    const [endpoints, setEndpoints] = useState<Endpoint[]>(demoEndpoints); const [connections, setConnections] = useState<Connection[]>([demoConnection]); const [packets, setPackets] = useState<Packet[]>([demoPacket]); const [selectedPacketId, setSelectedPacketId] = useState<string>(); const [pinnedPacket, setPinnedPacket] = useState<Packet>(); const packetRef = useRef<Packet[]>([]); packetRef.current = packets; const [status, setStatus] = useState("DEMO FEED"); const [error, setError] = useState<string>(); const dbRef = useRef(false); const sequence = useRef(8_245_120);
    useEffect(() => {
        if (demoMode) { setStatus("DEMO MODE / NOT LIVE CAPTURE"); return; }
        const uri = import.meta.env.VITE_SPACETIMEDB_URI ?? "ws://127.0.0.1:3000";
        const database = import.meta.env.VITE_SPACETIMEDB_DB ?? "tcp-capture";
        let connection: DbConnection | undefined;
        let stopped = false, dirty = false;
        let cursor = Date.now();
        const writes = new Set<Promise<boolean>>();
        const recent = new Map<string, Packet>();
        let rotateTimer: number | undefined;
        let retryTimer: number | undefined, retryAttempt = 0;
        const retry = () => {
            if (stopped || backlog.error || retryTimer !== undefined) return;
            retryTimer = window.setTimeout(() => { retryTimer = undefined; connect(); }, Math.min(8000, 500 * 2 ** Math.min(retryAttempt++, 4)));
        };
        // Contiguous time windows preserve events through handover/outages while
        // avoiding an archival-table subscription in the browser cache.
        const subscribeWindow = (ctx: DbConnection, cutoff: number) => {
            if (stopped || backlog.error) return;
            const end = cutoff + 2000;
            const handle = ctx.subscriptionBuilder()
                .onError(context => { setError(String(context.event)); ctx.disconnect(); })
                .onApplied(() => {
                    rotateTimer = window.setTimeout(() => {
                        void Promise.all([...writes]).then(results => {
                            if (stopped || !ctx.isActive || handle.isEnded() || results.some(success => !success)) return;
                            handle.unsubscribeThen(() => { cursor = end; subscribeWindow(ctx, cursor); });
                        });
                    }, Math.max(0, end - Date.now() + 100));
                })
                .subscribe(tables.packet.where(row => row.capturedAt.gt(Timestamp.fromDate(new Date(cutoff))).and(row.capturedAt.lte(Timestamp.fromDate(new Date(end))))));
        };
        const flush = window.setInterval(() => {
            if (!dirty || stopped) return;
            dirty = false;
            const current = [...recent.values()].reverse();
            setPackets(current);
            const machines = new Map<string, Endpoint>();
            for (const packet of current) for (const key of [packet.source, packet.destination]) {
                const split = key.lastIndexOf(":");
                machines.set(key, { id: key, address: key.slice(0, split), port: Number(key.slice(split + 1)), packets: 0, active: true });
            }
            setEndpoints([...machines.values()]);
            setStatus("SPACETIMEDB LIVE");
        }, 250);
        const connect = () => {
            try {
                connection = DbConnection.builder().withUri(uri).withDatabaseName(database)
                    .onConnect(ctx => {
                        if (stopped) { ctx.disconnect(); return; }
                        retryAttempt = 0;
                        dbRef.current = true; setStatus("CONNECTED / WAITING FOR CAPTURE"); setError(undefined);
                        ctx.db.packet.onInsert((_event, row) => {
                            if (stopped) return;
                            const packet = packetFromRow(row);
                            // The reducer's connection key contains both IP:port endpoints.
                            const flow = row.connectionId.slice(row.connectionId.indexOf(":") + 1).split("->");
                            if (flow.length === 2) { packet.source = flow[0]!; packet.destination = flow[1]!; }
                            hasLive.current = true;
                            const write = backlog.append(packet);
                            writes.add(write);
                            void write.then(success => { writes.delete(write); if (!success) ctx.disconnect(); });
                            if (recent.has(packet.id)) return;
                            recent.set(packet.id, packet);
                            if (recent.size > 64) recent.delete(recent.keys().next().value!);
                            dirty = true;
                        });
                        subscribeWindow(ctx, cursor);
                    })
                    .onConnectError((_ctx, problem) => { if (stopped) return; dbRef.current = false; setStatus("DATABASE OFFLINE / RETRYING"); setError(problem.message); retry(); })
                    .onDisconnect(() => { if (stopped) return; window.clearTimeout(rotateTimer); dbRef.current = false; setStatus(backlog.error ? "STORAGE BLOCKED / CAPTURE HISTORY PRESERVED" : "DATABASE OFFLINE / RETRYING"); if (!backlog.error) setError("Database disconnected. Reconnecting from the last completed capture window."); retry(); })
                    .build();
            } catch (problem) { setError(problem instanceof Error ? problem.message : String(problem)); retry(); }
        };
        connect();
        return () => { stopped = true; window.clearInterval(flush); window.clearTimeout(rotateTimer); window.clearTimeout(retryTimer); connection?.disconnect(); };
    }, []);
    useEffect(() => { const interval = window.setInterval(() => { if (dbRef.current || hasLive.current) return; const forward = sequence.current++ % 2 === 0; const source = forward ? "192.0.2.10:42000" : "198.51.100.20:443"; const destination = forward ? "198.51.100.20:443" : "192.0.2.10:42000"; const packet: Packet = { id: `demo-${Date.now()}`, source, destination, direction: forward ? "outbound" : "inbound", flags: forward ? "ACK PSH" : "ACK", payloadLength: forward ? 512 : 128, timestamp: Date.now() }; void backlog.append(packet); setPackets((current) => [packet, ...current].slice(0, 64)); setConnections([{ ...demoConnection, source, destination, packets: packets.length + 1 }]); sequence.current += forward ? 512 : 128; }, 1_100); return () => window.clearInterval(interval); }, [packets.length]);
    const displayEndpoints = endpoints.length ? endpoints : demoEndpoints; const displayConnections = connections.length ? connections : [demoConnection]; const displayPackets = packets.length ? packets : [demoPacket]; const packetsPerSecond = displayPackets.filter(packet => Date.now() - packet.timestamp < 1000).length;
    const selectedPacket = pinnedPacket ?? displayPackets.find((packet) => packet.id === selectedPacketId);
    const selectPacket = useCallback((selection: string | Packet) => { const packet = typeof selection === "string" ? packetRef.current.find(packet => packet.id === selection) : selection; setSelectedPacketId(packet?.id); setPinnedPacket(packet); }, []);
    return <main className="factory-app"><FactoryCanvas endpoints={displayEndpoints} connections={displayConnections} packets={displayPackets} inbox={inbox} selected={selectedPacket} onSelectPacket={selectPacket} /><div className="factory-vignette" /><header className="factory-topbar"><div className="brand"><span className="brand-symbol">✦</span><div><span className="eyebrow">TCP / LIVE OPERATIONS</span><h1>Factory Floor</h1></div></div><div className="top-actions"><span className="capture-pill"><i />{status}</span><button onClick={() => window.location.assign(demoMode ? "/" : "/?demo=1")}>{demoMode ? "Live capture" : "Demo scene"}</button><button aria-expanded={feedOpen} onClick={() => setFeedOpen(value => !value)}>{feedOpen ? "Hide feed" : "Packet feed"}</button></div></header><aside className="hud-right" hidden={!selectedPacket && !feedOpen}><div className="hud-heading"><span className="eyebrow">{selectedPacket ? "PACKAGE INSPECTION" : "RECENT CARGO"}</span><b>{formatNumber(displayPackets.length)}</b></div>{selectedPacket ? <div className="inspection"><button className="close-inspection" onClick={() => { setSelectedPacketId(undefined); setPinnedPacket(undefined); }}>×</button><span className="inspection-symbol">▣</span><strong>{selectedPacket.flags}</strong><dl><dt>source endpoint</dt><dd>{endpointName(selectedPacket.source)}</dd><dt>output dock</dt><dd>{endpointName(selectedPacket.destination)}</dd><dt>payload</dt><dd>{selectedPacket.payloadLength} bytes</dd><dt>direction</dt><dd>{selectedPacket.direction}</dd><dt>sequence / acknowledgment</dt><dd>{selectedPacket.sequence ?? "unavailable"} / {selectedPacket.acknowledgment ?? "unavailable"}</dd><dt>TCP window</dt><dd>{selectedPacket.window ?? "unavailable"}</dd><dt>interface</dt><dd>Not provided by capture schema</dd><dt>captured</dt><dd>{new Date(selectedPacket.timestamp).toLocaleTimeString()}</dd></dl></div> : <div className="packet-list">{displayPackets.slice(0, 7).map((packet) => <button className="packet-row" key={packet.id} onClick={() => selectPacket(packet.id)}><span className="cargo-icon">▣</span><div><strong>{packet.flags}</strong><small>{packet.source} → {packet.destination}</small></div><em>{packet.payloadLength} B</em></button>)}</div>}</aside><footer className="factory-footer"><span>{formatNumber(displayEndpoints.length)} endpoints · {formatNumber(displayPackets.length)} recent packets</span><span>Queued packet animation · click parcel to inspect · drag to orbit</span></footer>{(error || captureProblem || backlogProblem) && <div className="factory-error" role="alert">{backlogProblem ?? error ?? captureProblem}</div>}</main>;
}

document.documentElement.classList.add("ready");
const appRoot = createRoot(document.getElementById("root")!);
appRoot.render(<App />);
// Tear down WebGL, RAF, subscriptions and timers before Vite replaces this module.
if (import.meta.hot) import.meta.hot.dispose(() => appRoot.unmount());
