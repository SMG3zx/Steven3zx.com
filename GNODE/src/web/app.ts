// The node editor. The canvas shows blocks and wires; the inspector edits the
// selected block; the bottom panel shows the generated Go, the run output and
// the problems the generator found.

import { CATALOG, CATALOG_BY_KIND, type NodeDef, type PortDef } from "../shared/catalog";
import {
  chainOf,
  copyFragment,
  emptyGraph,
  newId,
  pasteFragment,
  type Fragment,
  type GNode,
  type Graph,
  type ParamValue,
  type PortRef,
  type Wire,
} from "../shared/graph";
import { History } from "./history";
import { LESSONS, type Lesson } from "../shared/lessons";
import { createTour } from "./tour";
import { tourStarter } from "../shared/tour";
import { bendSlot, wireD, type Pt } from "../shared/route";
import { SECTIONS, SECTION_BY_ID, sectionIcon, type Section } from "./sections";

interface Diagnostic {
  level: "error" | "warning";
  nodeId?: string;
  message: string;
}

interface GenerateResponse {
  source: string;
  formatted: boolean;
  formatError?: string;
  diagnostics: Diagnostic[];
  emittedNodeIds: string[];
  usedWireIds: string[];
}

interface RunResponse {
  ok?: boolean;
  output?: string;
  error?: string;
  diagnostics?: Diagnostic[];
}

type Selection = { kind: "node"; id: string } | { kind: "wire"; id: string } | null;


const $ = <T extends Element = HTMLElement>(id: string): T => {
  const el = document.getElementById(id);
  if (!el) throw new Error(`missing #${id}`);
  return el as unknown as T;
};

const canvas = $("canvas");
const wiresSvg = $<SVGSVGElement>("wires");
const inspector = $("inspector");
const statusEl = $("status");
const codeView = $("code-view");
const outputView = $("output-view");
const problemList = $("problem-list");
const problemCount = $("problem-count");
const graphNameInput = $<HTMLInputElement>("graph-name");
const graphList = $<HTMLSelectElement>("graph-list");
const bottom = $("bottom");

let graph: Graph = emptyGraph("hello");
const history = new History<Graph>();
// Every selected block. `selection` is the one the inspector shows.
const multi = new Set<string>();
let clipboard: Fragment | null = null;
let squareWires = false;
let pasteCount = 0;
let selection: Selection = null;
let diagnostics: Diagnostic[] = [];
let emitted = new Set<string>();
let usedWires = new Set<string>();
let tracing = false;
let findQuery = "";
let findIndex = -1;
let runs = 0;
// The block whose chain is lit up, if any. Set by double-clicking a block.
let chainFocus: string | null = null;
// Flow packets run until this time (performance.now), when a run has just finished.
let packetsUntil = 0;
let packetFrame = 0;
// The index of the problem that Next problem shows last.
let problemAt = -1;
const nodeEls = new Map<string, HTMLElement>();
let generateTimer: ReturnType<typeof setTimeout> | undefined;

function setStatus(text: string) {
  statusEl.textContent = text;
  // The toolbar cuts long messages short, so the whole text is in the tooltip.
  statusEl.title = text;
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text !== undefined) node.textContent = text;
  return node;
}

function defOf(node: GNode): NodeDef | undefined {
  return CATALOG_BY_KIND.get(node.kind);
}

function nodeById(id: string): GNode | undefined {
  return graph.nodes.find((n) => n.id === id);
}

function defaultParams(def: NodeDef): Record<string, ParamValue> {
  const params: Record<string, ParamValue> = {};
  for (const p of def.params) params[p.id] = p.default;
  return params;
}

// ---------- block dock ----------

// The dock along the bottom lists the blocks by section. A section tile opens
// its blocks, Find searches all of them, and the Groups section holds the saved groups.
const palette = $("palette");
const blockFind = $<HTMLInputElement>("block-find");
// The section that is open, or null for the section tiles.
let dockSection: string | null = null;
// The saved groups, by name. Filled by refreshGroups.
let groupNames: string[] = [];

function sectionBlocks(id: string): NodeDef[] {
  return CATALOG.filter((d) => d.category === id);
}

function countLabel(id: string, n: number): string {
  if (id === "groups") return `${n} ${n === 1 ? "group" : "groups"}`;
  return `${n} ${n === 1 ? "block" : "blocks"}`;
}

function matchesBlock(def: NodeDef, q: string): boolean {
  const section = SECTION_BY_ID.get(def.category)?.title ?? "";
  return [def.title, def.help, def.kind, section].join(" ").toLowerCase().includes(q);
}

function blockButton(def: NodeDef): HTMLButtonElement {
  const btn = el("button", "dock-block", def.title);
  btn.type = "button";
  btn.dataset.kind = def.kind;
  btn.title = def.help;
  btn.draggable = true;
  btn.style.setProperty("--accent", SECTION_BY_ID.get(def.category)?.color ?? "#5ad1c3");
  btn.addEventListener("dragstart", (e) => {
    e.dataTransfer?.setData("text/gnode-kind", def.kind);
    e.dataTransfer?.setData("text/plain", def.kind);
  });
  btn.addEventListener("click", () => {
    const wrap = canvas.parentElement as HTMLElement;
    const x = wrap.scrollLeft + wrap.clientWidth / 2 - 80 + Math.random() * 40;
    const y = wrap.scrollTop + wrap.clientHeight / 2 - 40 + Math.random() * 40;
    addNode(def.kind, x, y);
  });
  return btn;
}

function groupButton(name: string): HTMLButtonElement {
  const btn = el("button", "dock-block group-block", name);
  btn.type = "button";
  btn.title = "Add this group to the canvas";
  btn.addEventListener("click", () => placeGroup(name));
  return btn;
}

function sectionTile(section: Section): HTMLButtonElement {
  const btn = el("button", "dock-sec");
  btn.type = "button";
  btn.dataset.cat = section.id;
  btn.style.setProperty("--accent", section.color);
  const n = section.id === "groups" ? groupNames.length : sectionBlocks(section.id).length;
  btn.innerHTML = `<span class="ds-ic">${sectionIcon(section)}</span><span class="ds-name"></span><span class="ds-n"></span>`;
  btn.querySelector(".ds-name")!.textContent = section.title;
  btn.querySelector(".ds-n")!.textContent = countLabel(section.id, n);
  btn.addEventListener("click", () => {
    dockSection = section.id;
    renderDock();
  });
  return btn;
}

function rowOf(items: HTMLElement[]): HTMLElement {
  const row = el("div", "dock-row");
  row.append(...items);
  return row;
}

// The groups section: the saved groups, and the row that saves the selection.
const groupList = el("div", "group-list");
const groupName = el("input", "group-name-input");
const groupSave = el("div", "group-save");
const saveGroupBtn = el("button", "", "Save selection as group");
saveGroupBtn.type = "button";
saveGroupBtn.addEventListener("click", saveSelectionAsGroup);
groupName.placeholder = "Group name";
groupName.spellcheck = false;
groupName.addEventListener("keydown", (e) => {
  if (e.key === "Enter") saveSelectionAsGroup();
});
groupSave.append(groupName, saveGroupBtn);

function renderDock() {
  palette.replaceChildren();
  const q = blockFind.value.trim().toLowerCase();
  if (q) {
    const blocks = CATALOG.filter((d) => matchesBlock(d, q)).map(blockButton);
    const groups = groupNames.filter((n) => n.includes(q)).map(groupButton);
    const hits = [...blocks, ...groups];
    if (hits.length === 0) palette.append(el("p", "dock-empty", "No block matches that."));
    else palette.append(rowOf(hits));
    return;
  }
  if (dockSection === null) {
    const tiles = el("div", "dock-sections");
    for (const section of SECTIONS) tiles.append(sectionTile(section));
    palette.append(tiles);
    return;
  }
  const section = SECTION_BY_ID.get(dockSection);
  if (!section) {
    dockSection = null;
    renderDock();
    return;
  }
  const top = el("div", "dock-top");
  const back = el("button", "dock-back", "All sections");
  back.type = "button";
  back.addEventListener("click", () => {
    dockSection = null;
    renderDock();
  });
  top.append(back, el("b", "dock-title", section.title), el("span", "dock-blurb", section.blurb));
  palette.append(top);
  if (section.id === "groups") {
    const view = el("div", "group-view");
    view.append(groupList, groupSave);
    palette.append(view);
  } else {
    palette.append(rowOf(sectionBlocks(section.id).map(blockButton)));
  }
}

$("dock-toggle").addEventListener("click", () => {
  const dock = $("dock");
  dock.classList.toggle("collapsed");
  $("dock-toggle").setAttribute("aria-expanded", String(!dock.classList.contains("collapsed")));
});
blockFind.addEventListener("input", renderDock);
blockFind.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  blockFind.value = "";
  renderDock();
  blockFind.blur();
});

canvas.addEventListener("dragover", (e) => {
  if (e.dataTransfer?.types.includes("text/gnode-kind")) e.preventDefault();
});
canvas.addEventListener("drop", (e) => {
  const kind = e.dataTransfer?.getData("text/gnode-kind");
  if (!kind) return;
  e.preventDefault();
  const rect = canvas.getBoundingClientRect();
  addNode(kind, e.clientX - rect.left - 80, e.clientY - rect.top - 16);
});
canvas.addEventListener("pointerdown", (e) => {
  if (e.button !== 0 || (e.target !== canvas && e.target !== wiresSvg)) return;
  startMarquee(e);
});

// ---------- nodes ----------

function addNode(kind: string, x: number, y: number) {
  const def = CATALOG_BY_KIND.get(kind);
  if (!def) return;
  const node: GNode = {
    id: newId("n"),
    kind,
    x: Math.max(0, Math.round(x)),
    y: Math.max(0, Math.round(y)),
    params: defaultParams(def),
  };
  checkpoint();
  graph.nodes.push(node);
  renderNodes();
  select({ kind: "node", id: node.id });
  scheduleGenerate();
}

function removeNode(id: string) {
  removeNodes([id]);
}

function removeNodes(ids: string[]) {
  checkpoint();
  const gone = new Set(ids);
  graph.nodes = graph.nodes.filter((n) => !gone.has(n.id));
  graph.wires = graph.wires.filter((w) => !gone.has(w.from.node) && !gone.has(w.to.node));
  for (const id of gone) {
    nodeEls.get(id)?.remove();
    nodeEls.delete(id);
    multi.delete(id);
  }
  const rest = [...multi];
  select(rest.length ? { kind: "node", id: rest[rest.length - 1] } : null);
  renderWires();
  scheduleGenerate();
}

function removeWire(id: string) {
  checkpoint();
  graph.wires = graph.wires.filter((w) => w.id !== id);
  if (selection?.kind === "wire" && selection.id === id) select(null);
  renderWires();
  scheduleGenerate();
}

function portEl(nodeId: string, portId: string, dir: "in" | "out"): HTMLElement | null {
  return canvas.querySelector<HTMLElement>(`.port[data-node="${nodeId}"][data-port="${portId}"][data-dir="${dir}"]`);
}

function renderNodes() {
  const alive = new Set(graph.nodes.map((n) => n.id));
  for (const id of [...multi]) if (!alive.has(id)) multi.delete(id);
  for (const [id, node] of nodeEls) {
    if (!alive.has(id)) {
      node.remove();
      nodeEls.delete(id);
    }
  }
  for (const n of graph.nodes) {
    let node = nodeEls.get(n.id);
    if (!node) {
      node = buildNodeEl(n);
      nodeEls.set(n.id, node);
      canvas.append(node);
    }
    node.style.left = `${n.x}px`;
    node.style.top = `${n.y}px`;
  }
  refreshNodeStates();
  paintSelection();
  renderWires();
}

function buildNodeEl(n: GNode): HTMLElement {
  const def = defOf(n);
  const box = el("div", "node" + (def?.root ? " root" : ""));
  box.dataset.id = n.id;
  box.dataset.kind = n.kind;
  const head = el("div", "node-head", def?.title ?? `unknown: ${n.kind}`);
  head.title = def?.help ?? "";
  box.append(head);

  const body = el("div", "node-body");
  const inputs = el("div", "node-col in");
  const outputs = el("div", "node-col out");
  for (const p of def?.inputs ?? []) inputs.append(portRow(n.id, p, "in"));
  for (const p of def?.outputs ?? []) outputs.append(portRow(n.id, p, "out"));
  body.append(inputs, outputs);
  box.append(body);

  head.addEventListener("pointerdown", (e) => startNodeDrag(e, n, box));
  box.addEventListener("pointerdown", (e) => {
    if ((e.target as HTMLElement).closest(".port, .node-head")) return;
    if (e.shiftKey) toggleInSelection(n.id);
    else if (!multi.has(n.id)) select({ kind: "node", id: n.id });
  });
  box.addEventListener("dblclick", (e) => {
    if ((e.target as HTMLElement).closest(".port")) return;
    toggleChain(n.id);
  });
  return box;
}

function portRow(nodeId: string, p: PortDef, dir: "in" | "out"): HTMLElement {
  const row = el("div", "port-row" + (p.required ? " required" : ""));
  const port = el("span", `port ${p.kind === "exec" ? "exec" : "t-" + (p.type ?? "any")}`);
  port.dataset.node = nodeId;
  port.dataset.port = p.id;
  port.dataset.dir = dir;
  port.dataset.kind = p.kind;
  port.title = p.label;
  port.addEventListener("pointerdown", (e) => startWireDrag(e, { node: nodeId, port: p.id }, dir, p.kind));
  const label = el("span", "port-label", p.label);
  if (dir === "in") row.append(port, label);
  else row.append(label, port);
  return row;
}

function startNodeDrag(e: PointerEvent, n: GNode, box: HTMLElement) {
  if (e.button !== 0) return;
  e.preventDefault();
  if (e.shiftKey) {
    toggleInSelection(n.id);
    return;
  }
  if (!multi.has(n.id)) select({ kind: "node", id: n.id });
  // Every selected block moves together.
  const group = graph.nodes.filter((g) => multi.has(g.id));
  const origins = new Map(group.map((g) => [g.id, { x: g.x, y: g.y }]));
  const startX = e.clientX;
  const startY = e.clientY;
  const before = structuredClone(graph);
  const move = (ev: PointerEvent) => {
    for (const g of group) {
      const o = origins.get(g.id)!;
      g.x = Math.max(0, o.x + ev.clientX - startX);
      g.y = Math.max(0, o.y + ev.clientY - startY);
      const elt = g.id === n.id ? box : nodeEls.get(g.id);
      if (elt) {
        elt.style.left = `${g.x}px`;
        elt.style.top = `${g.y}px`;
      }
    }
    renderWires();
  };
  const up = () => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
    const moved = group.some((g) => {
      const o = origins.get(g.id)!;
      return g.x !== o.x || g.y !== o.y;
    });
    if (moved) {
      history.record(before);
      updateHistoryButtons();
    }
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
}

// ---------- wires ----------

function startWireDrag(e: PointerEvent, from: PortRef, dir: "in" | "out", kind: "exec" | "data") {
  if (e.button !== 0) return;
  e.preventDefault();
  e.stopPropagation();
  const draft = svgEl("path", "wire draft" + (kind === "exec" ? " exec" : ""));
  wiresSvg.append(draft);
  const start = portCenter(from.node, from.port, dir);
  const move = (ev: PointerEvent) => {
    const end = pointInCanvas(ev);
    draft.setAttribute("d", dir === "out" ? wirePath(start, end) : wirePath(end, start));
  };
  const up = (ev: PointerEvent) => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
    draft.remove();
    const target = document.elementFromPoint(ev.clientX, ev.clientY)?.closest<HTMLElement>(".port");
    if (!target) return;
    const other: PortRef = { node: target.dataset.node ?? "", port: target.dataset.port ?? "" };
    const otherDir = target.dataset.dir;
    if (other.node === from.node && otherDir === dir) return;
    if (otherDir === dir) return;
    const outRef = dir === "out" ? from : other;
    const inRef = dir === "out" ? other : from;
    connect(outRef, inRef, target.dataset.kind === kind);
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
}

function connect(out: PortRef, input: PortRef, kindsMatch: boolean) {
  if (!kindsMatch) {
    setStatus("Exec wires connect to exec ports, and values to values");
    return;
  }
  const outNode = nodeById(out.node);
  const inNode = nodeById(input.node);
  const outKind = outNode && defOf(outNode)?.outputs.find((p) => p.id === out.port)?.kind;
  const inKind = inNode && defOf(inNode)?.inputs.find((p) => p.id === input.port)?.kind;
  if (outKind !== inKind || !outKind) {
    setStatus("Those ports cannot be connected");
    return;
  }
  // Inputs take one source. Exec outputs lead to one block.
  checkpoint();
  graph.wires = graph.wires.filter((w) => !(w.to.node === input.node && w.to.port === input.port));
  if (outKind === "exec") {
    graph.wires = graph.wires.filter((w) => !(w.from.node === out.node && w.from.port === out.port));
  }
  const wire: Wire = { id: newId("w"), from: out, to: input };
  graph.wires.push(wire);
  renderWires();
  scheduleGenerate();
}

const svgNS = "http://www.w3.org/2000/svg";
function svgEl(tag: string, cls?: string): SVGElement {
  const node = document.createElementNS(svgNS, tag);
  if (cls) node.setAttribute("class", cls);
  return node;
}

function pointInCanvas(e: { clientX: number; clientY: number }): { x: number; y: number } {
  const rect = canvas.getBoundingClientRect();
  return { x: e.clientX - rect.left, y: e.clientY - rect.top };
}

function portCenter(nodeId: string, portId: string, dir: "in" | "out"): { x: number; y: number } {
  const p = portEl(nodeId, portId, dir);
  if (!p) return { x: 0, y: 0 };
  const rect = p.getBoundingClientRect();
  const canvasRect = canvas.getBoundingClientRect();
  return { x: rect.left - canvasRect.left + rect.width / 2, y: rect.top - canvasRect.top + rect.height / 2 };
}

// The path of a wire from its output point to its input point. Square wires
// turn at their bends; curved wires ignore them.
function wirePath(out: Pt, inn: Pt, bends: Pt[] = []): string {
  return wireD(out, inn, bends, squareWires);
}

// A wire's bends as canvas points. They are stored relative to the block the wire leaves.
function bendPoints(w: Wire): Pt[] {
  const from = nodeById(w.from.node);
  if (!from || !w.bends) return [];
  return w.bends.map((b) => ({ x: from.x + b.dx, y: from.y + b.dy }));
}

function renderWires() {
  drawMinimap();
  wiresSvg.replaceChildren();
  const chain = chainFocus ? chainOf(graph, chainFocus) : null;
  for (const w of graph.wires) {
    const from = nodeById(w.from.node);
    const to = nodeById(w.to.node);
    if (!from || !to) continue;
    const isExec = defOf(from)?.outputs.find((p) => p.id === w.from.port)?.kind === "exec";
    const traceClass = !tracing ? "" : usedWires.has(w.id) ? " traced" : " off-path";
    const chainClass = chain && !chain.wires.has(w.id) ? " chain-off" : "";
    const isSelected = selection?.kind === "wire" && selection.id === w.id;
    const path = svgEl("path", "wire" + (isExec ? " exec" : "") + traceClass + chainClass + (isSelected ? " selected" : ""));
    path.dataset.id = w.id;
    const a = portCenter(w.from.node, w.from.port, "out");
    const b = portCenter(w.to.node, w.to.port, "in");
    const d = wirePath(a, b, bendPoints(w));
    path.setAttribute("d", d);
    // A wider, invisible copy of the wire, so it is easy to grab.
    const hit = svgEl("path", "wire-hit");
    hit.setAttribute("d", d);
    hit.addEventListener("pointerdown", (e) => startWireGrab(e, w));
    wiresSvg.append(path, hit);
    if (isSelected && squareWires) {
      bendPoints(w).forEach((p, i) => {
        const handle = svgEl("circle", "bend-handle");
        handle.setAttribute("cx", String(p.x));
        handle.setAttribute("cy", String(p.y));
        handle.setAttribute("r", "5");
        handle.addEventListener("pointerdown", (e) => startWireGrab(e, w, i));
        handle.addEventListener("dblclick", (e) => {
          e.stopPropagation();
          removeBend(w, i);
        });
        wiresSvg.append(handle);
      });
    }
  }
  wiresSvg.append(packetLayer);
}

// Grabbing a wire selects it. In square mode, dragging the wire adds a bend
// where it was grabbed, and dragging a bend handle moves that bend. A press
// without a drag only selects.
function startWireGrab(e: PointerEvent, w: Wire, index?: number) {
  if (e.button !== 0) return;
  e.stopPropagation();
  e.preventDefault();
  if (!(selection?.kind === "wire" && selection.id === w.id)) select({ kind: "wire", id: w.id });
  const from = nodeById(w.from.node);
  if (!from || !squareWires) return;

  const before = structuredClone(graph);
  const startX = e.clientX;
  const startY = e.clientY;
  let at = index ?? -1;
  const move = (ev: PointerEvent) => {
    if (at < 0) {
      if (Math.hypot(ev.clientX - startX, ev.clientY - startY) < 4) return;
      const out = portCenter(w.from.node, w.from.port, "out");
      const inn = portCenter(w.to.node, w.to.port, "in");
      at = bendSlot(out, inn, bendPoints(w), pointInCanvas({ clientX: startX, clientY: startY }));
      w.bends = [...(w.bends ?? [])];
      w.bends.splice(at, 0, { dx: 0, dy: 0 });
    }
    const p = pointInCanvas(ev);
    w.bends![at] = { dx: Math.round(p.x - from.x), dy: Math.round(p.y - from.y) };
    renderWires();
  };
  const up = () => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
    if (JSON.stringify(before.wires) === JSON.stringify(graph.wires)) return;
    history.record(before);
    updateHistoryButtons();
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
}

function removeBend(w: Wire, i: number) {
  checkpoint();
  w.bends = (w.bends ?? []).filter((_, j) => j !== i);
  if (w.bends.length === 0) delete w.bends;
  renderWires();
}

// ---------- selection and inspector ----------

function select(next: Selection) {
  selection = next;
  if (next?.kind === "node") {
    // Picking a block that is already in a group keeps the group.
    if (!multi.has(next.id)) {
      multi.clear();
      multi.add(next.id);
    }
  } else {
    multi.clear();
  }
  paintSelection();
  renderWires();
  renderInspector();
}

function paintSelection() {
  for (const [id, node] of nodeEls) node.classList.toggle("selected", multi.has(id));
}

// Shift-click: add the block to the selection, or take it out again.
function toggleInSelection(id: string) {
  if (multi.has(id)) {
    multi.delete(id);
    const rest = [...multi];
    selection = rest.length ? { kind: "node", id: rest[rest.length - 1] } : null;
  } else {
    multi.add(id);
    selection = { kind: "node", id };
  }
  paintSelection();
  renderWires();
  renderInspector();
}

function selectAll() {
  multi.clear();
  for (const n of graph.nodes) multi.add(n.id);
  const last = graph.nodes[graph.nodes.length - 1];
  selection = last ? { kind: "node", id: last.id } : null;
  paintSelection();
  renderWires();
  renderInspector();
}

function renderInspector() {
  inspector.replaceChildren();
  if (!selection) {
    inspector.append(el("p", "empty", "Select a block to edit it, or drag blocks from the dock below to add them."));
    return;
  }
  if (selection.kind === "wire") {
    const selectedId = selection.id;
    const w = graph.wires.find((x) => x.id === selectedId);
    if (!w) return;
    inspector.append(el("h2", "", "Wire"));
    const from = nodeById(w.from.node);
    const to = nodeById(w.to.node);
    inspector.append(
      el("div", "help", `${defOf(from!)?.title ?? "?"}.${w.from.port} → ${defOf(to!)?.title ?? "?"}.${w.to.port}`),
    );
    const del = el("button", "danger", "Delete wire");
    del.type = "button";
    del.addEventListener("click", () => removeWire(w.id));
    inspector.append(del);
    return;
  }

  const n = nodeById(selection.id);
  const def = n && defOf(n);
  if (!n || !def) return;
  inspector.append(el("h2", "", def.title), el("div", "help", def.help));

  for (const p of def.params) {
    const label = el("label", "", p.label);
    label.htmlFor = `param-${p.id}`;
    inspector.append(label);
    const current = n.params[p.id] ?? p.default;
    let input: HTMLInputElement | HTMLSelectElement;
    if (p.type === "select") {
      const s = el("select");
      for (const opt of p.options ?? []) {
        const o = el("option", "", opt);
        o.value = opt;
        if (String(current) === opt) o.selected = true;
        s.append(o);
      }
      input = s;
      s.addEventListener("change", () => setParam(n, p.id, s.value));
    } else {
      const t = el("input");
      t.type = "text";
      t.value = String(current);
      if (p.placeholder) t.placeholder = p.placeholder;
      t.addEventListener("input", () => setParam(n, p.id, t.value));
      input = t;
    }
    input.id = `param-${p.id}`;
    inspector.append(input);
  }

  const del = el("button", "danger", "Delete block");
  del.type = "button";
  del.addEventListener("click", () => removeNode(n.id));
  inspector.append(del);
}

function setParam(n: GNode, id: string, value: string) {
  // Typing a value updates it on every key press; the key merges those into one undo step.
  checkpoint(`param:${n.id}:${id}`);
  n.params[id] = value;
  scheduleGenerate();
}

// ---------- wire style and map ----------

function toggleSquareWires() {
  squareWires = !squareWires;
  const btn = $("btn-square");
  btn.setAttribute("aria-pressed", String(squareWires));
  btn.classList.toggle("active", squareWires);
  renderWires();
}

// The map is a scaled copy of the whole canvas. The outline shows the part
// you can see; click or drag on the map to scroll there.
const MAP_SCALE = 0.08;
const scroller = (): HTMLElement => canvas.parentElement as HTMLElement;

function drawMinimap() {
  const map = $("minimap");
  for (const old of map.querySelectorAll(".mini-node")) old.remove();
  for (const n of graph.nodes) {
    const elt = nodeEls.get(n.id);
    const mini = el("div", "mini-node" + (multi.has(n.id) ? " on" : ""));
    mini.style.left = `${n.x * MAP_SCALE}px`;
    mini.style.top = `${n.y * MAP_SCALE}px`;
    mini.style.width = `${Math.max(3, (elt?.offsetWidth ?? 170) * MAP_SCALE)}px`;
    mini.style.height = `${Math.max(3, (elt?.offsetHeight ?? 80) * MAP_SCALE)}px`;
    map.insertBefore(mini, $("minimap-view"));
  }
  drawViewport();
}

function drawViewport() {
  const wrap = scroller();
  const view = $("minimap-view");
  view.style.left = `${wrap.scrollLeft * MAP_SCALE}px`;
  view.style.top = `${wrap.scrollTop * MAP_SCALE}px`;
  view.style.width = `${wrap.clientWidth * MAP_SCALE}px`;
  view.style.height = `${wrap.clientHeight * MAP_SCALE}px`;
}

function scrollToMap(e: PointerEvent) {
  const r = $("minimap").getBoundingClientRect();
  const wrap = scroller();
  wrap.scrollTo({
    left: (e.clientX - r.left) / MAP_SCALE - wrap.clientWidth / 2,
    top: (e.clientY - r.top) / MAP_SCALE - wrap.clientHeight / 2,
  });
}

let mapDragging = false;
$("minimap").addEventListener("pointerdown", (e) => {
  mapDragging = true;
  scrollToMap(e);
});
window.addEventListener("pointermove", (e) => {
  if (mapDragging) scrollToMap(e);
});
window.addEventListener("pointerup", () => {
  mapDragging = false;
});
scroller().addEventListener("scroll", drawViewport);
$("btn-square").addEventListener("click", toggleSquareWires);

// ---------- marquee, copy and paste ----------

// Drag on empty canvas to select the blocks the rectangle touches. Shift adds to the selection.
function startMarquee(e: PointerEvent) {
  const additive = e.shiftKey;
  const startX = e.clientX;
  const startY = e.clientY;
  const box = el("div", "marquee");
  canvas.append(box);
  const bounds = (ev: PointerEvent) => ({
    x0: Math.min(startX, ev.clientX),
    x1: Math.max(startX, ev.clientX),
    y0: Math.min(startY, ev.clientY),
    y1: Math.max(startY, ev.clientY),
  });
  const move = (ev: PointerEvent) => {
    const r = canvas.getBoundingClientRect();
    const b = bounds(ev);
    box.style.left = `${b.x0 - r.left}px`;
    box.style.top = `${b.y0 - r.top}px`;
    box.style.width = `${b.x1 - b.x0}px`;
    box.style.height = `${b.y1 - b.y0}px`;
  };
  const up = (ev: PointerEvent) => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
    box.remove();
    const b = bounds(ev);
    if (b.x1 - b.x0 < 4 && b.y1 - b.y0 < 4) {
      if (!additive) select(null);
      return;
    }
    const hit: string[] = [];
    for (const [id, node] of nodeEls) {
      const r = node.getBoundingClientRect();
      if (r.right > b.x0 && r.left < b.x1 && r.bottom > b.y0 && r.top < b.y1) hit.push(id);
    }
    if (!additive) multi.clear();
    for (const id of hit) multi.add(id);
    selection = hit.length ? { kind: "node", id: hit[hit.length - 1] } : null;
    paintSelection();
    renderWires();
    renderInspector();
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
}

function copySelection() {
  if (multi.size === 0) return;
  clipboard = copyFragment(graph, multi);
  pasteCount = 0;
  const count = clipboard.nodes.length;
  setStatus(`Copied ${count} block${count === 1 ? "" : "s"}`);
}

// Each paste lands a little further along, so repeated pastes do not stack.
function pasteClipboard() {
  if (!clipboard || clipboard.nodes.length === 0) return;
  checkpoint();
  pasteCount += 1;
  const offset = 40 * pasteCount;
  const pasted = pasteFragment(clipboard, offset, offset);
  graph.nodes.push(...pasted.nodes);
  graph.wires.push(...pasted.wires);
  multi.clear();
  for (const n of pasted.nodes) multi.add(n.id);
  renderNodes();
  select({ kind: "node", id: pasted.nodes[pasted.nodes.length - 1].id });
  scheduleGenerate();
}

// ---------- generation and problems ----------

function refreshNodeStates() {
  const hits = findMatches();
  if (chainFocus && !nodeById(chainFocus)) chainFocus = null;
  const chain = chainFocus ? chainOf(graph, chainFocus) : null;
  for (const [id, node] of nodeEls) {
    node.classList.toggle("chain-off", !!chain && !chain.nodes.has(id));
    node.classList.toggle("traced", tracing && emitted.has(id));
    node.classList.toggle("match", hits.includes(id));
    node.classList.toggle("current", hits[findIndex] === id);
    const here = diagnostics.filter((d) => d.nodeId === id);
    node.classList.toggle("has-error", here.some((d) => d.level === "error"));
    node.classList.toggle("has-warning", !here.some((d) => d.level === "error") && here.length > 0);
    const owner = nodeById(id);
    node.classList.toggle("unused", !emitted.has(id) && !(owner && defOf(owner)?.root));
    for (const port of node.querySelectorAll<HTMLElement>(".port")) {
      const connected = graph.wires.some(
        (w) =>
          (w.to.node === id && w.to.port === port.dataset.port && port.dataset.dir === "in") ||
          (w.from.node === id && w.from.port === port.dataset.port && port.dataset.dir === "out"),
      );
      port.classList.toggle("connected", connected);
    }
  }
}

function scheduleGenerate() {
  clearTimeout(generateTimer);
  generateTimer = setTimeout(generate, 300);
}

async function generate() {
  try {
    const res = await fetch("/api/generate", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ graph }),
    });
    const data = (await res.json()) as GenerateResponse & { error?: string };
    if (!res.ok) throw new Error(data.error ?? res.statusText);
    diagnostics = data.diagnostics;
    emitted = new Set(data.emittedNodeIds);
    usedWires = new Set(data.usedWireIds);
    codeView.textContent = data.formatError && !data.formatted
      ? `${data.source}\n\n// gofmt: ${data.formatError}`
      : data.source;
    renderProblems();
    refreshNodeStates();
    renderWires();
    const errors = diagnostics.filter((d) => d.level === "error").length;
    setStatus(errors === 0 ? "Code is up to date" : `${errors} error${errors === 1 ? "" : "s"} to fix`);
  } catch (err) {
    setStatus(`Could not generate: ${(err as Error).message}`);
  }
}

function renderProblems() {
  problemList.replaceChildren();
  for (const d of diagnostics) {
    const li = el("li", d.level, `${d.level === "error" ? "Error" : "Warning"}: ${d.message}`);
    li.addEventListener("click", () => showProblem(d));
    problemList.append(li);
  }
  if (diagnostics.length === 0) problemList.append(el("li", "", "No problems."));
  problemCount.textContent = diagnostics.length ? `(${diagnostics.length})` : "";
  const linked = linkedProblems();
  $("btn-problem").hidden = linked.length === 0;
  $("problem-badge").textContent = String(linked.length);
}

// The problems that point at a block that is on the canvas.
function linkedProblems(): Diagnostic[] {
  return diagnostics.filter((d) => d.nodeId !== undefined && nodeEls.has(d.nodeId));
}

function showProblem(d: Diagnostic) {
  if (!d.nodeId) return;
  select({ kind: "node", id: d.nodeId });
  nodeEls.get(d.nodeId)?.scrollIntoView({ block: "center", inline: "center" });
  setStatus(`${d.level === "error" ? "Error" : "Warning"}: ${d.message}`);
}

// Steps through the problems that point at a block, one press at a time.
function nextProblem() {
  const linked = linkedProblems();
  if (linked.length === 0) {
    setStatus("No problems point at a block");
    return;
  }
  problemAt = (problemAt + 1) % linked.length;
  showProblem(linked[problemAt]);
}

async function run() {
  showTab("output");
  outputView.textContent = "Running...";
  try {
    const res = await fetch("/api/run", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ graph }),
    });
    const data = (await res.json()) as RunResponse & { source?: string };
    if (res.status === 422) {
      diagnostics = data.diagnostics ?? diagnostics;
      renderProblems();
      refreshNodeStates();
      outputView.textContent = "Fix the errors before running. See the Problems tab.";
      return;
    }
    if (!res.ok) throw new Error(data.error ?? res.statusText);
    runs += 1;
    packetsUntil = performance.now() + PACKET_RUN_MS;
    startPackets();
    outputView.textContent = [data.output ?? "", data.error ? `\n${data.error}` : ""].join("").trimEnd() || "(no output)";
    if (data.source) codeView.textContent = data.source;
  } catch (err) {
    outputView.textContent = `Could not run: ${(err as Error).message}`;
  }
}

// ---------- trace and find ----------

// Trace marks the blocks and wires the generated program uses, which run from
// func main, and dims the rest. The dimming is applied by renderWires and
// refreshNodeStates.
function toggleTrace() {
  tracing = !tracing;
  const btn = $("btn-trace");
  btn.setAttribute("aria-pressed", String(tracing));
  btn.classList.toggle("active", tracing);
  refreshNodeStates();
  renderWires();
  if (tracing) startPackets();
}

// The blocks whose title, kind or settings contain the find text.
function findMatches(): string[] {
  const q = findQuery.trim().toLowerCase();
  if (!q) return [];
  return graph.nodes
    .filter((n) => {
      const title = defOf(n)?.title ?? "";
      return [title, n.kind, ...Object.values(n.params).map(String)].join(" ").toLowerCase().includes(q);
    })
    .map((n) => n.id);
}

function setFind(query: string) {
  findQuery = query;
  findIndex = -1;
  refreshNodeStates();
}

function gotoMatch(step: number) {
  const hits = findMatches();
  if (hits.length === 0) {
    setStatus("No matching blocks");
    return;
  }
  if (findIndex < 0) findIndex = step > 0 ? 0 : hits.length - 1;
  else findIndex = (findIndex + step + hits.length) % hits.length;
  const id = hits[findIndex];
  select({ kind: "node", id });
  refreshNodeStates();
  nodeEls.get(id)?.scrollIntoView({ block: "center", inline: "center" });
  setStatus(`Match ${findIndex + 1} of ${hits.length}`);
}

// ---------- practice rooms ----------

const DONE_KEY = "gnode.lessonsDone";
let currentLesson: Lesson = LESSONS[0];

// Finished rooms are remembered in this browser only.
function lessonsDone(): string[] {
  try {
    return JSON.parse(localStorage.getItem(DONE_KEY) ?? "[]") as string[];
  } catch {
    return [];
  }
}

function markLessonDone(id: string) {
  try {
    localStorage.setItem(DONE_KEY, JSON.stringify([...new Set([...lessonsDone(), id])]));
  } catch {
    // Storage can be unavailable; the room still works without the mark.
  }
}

function buildLessonList() {
  const list = $<HTMLSelectElement>("lesson-list");
  const done = lessonsDone();
  list.replaceChildren();
  for (const lesson of LESSONS) {
    const o = el("option", "", `${lesson.title}${done.includes(lesson.id) ? " ✓" : ""}`);
    o.value = lesson.id;
    list.append(o);
  }
  list.value = currentLesson.id;
}

function showLesson(id: string) {
  currentLesson = LESSONS.find((l) => l.id === id) ?? LESSONS[0];
  $("lesson-goal").textContent = currentLesson.goal;
  $("lesson-hint").textContent = currentLesson.hint;
  $("lesson-hint").classList.add("hidden");
  $("lesson-result").textContent = "Press Start to load the starting blocks, wire the program, then press Check.";
}

function startLesson() {
  loadGraph(structuredClone(currentLesson.starter));
  setStatus(`Started: ${currentLesson.title}`);
  $("lesson-result").textContent = "Wire the blocks, then press Check.";
}

async function checkLesson() {
  const result = $("lesson-result");
  result.textContent = "Checking...";
  try {
    const res = await fetch("/api/run", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ graph }),
    });
    const data = (await res.json()) as RunResponse & { source?: string };
    if (res.status === 422) {
      diagnostics = data.diagnostics ?? diagnostics;
      renderProblems();
      refreshNodeStates();
      result.textContent = "The program has errors. See the Problems tab.";
      return;
    }
    if (!res.ok) throw new Error(data.error ?? res.statusText);
    const printed = (data.output ?? "").trim();
    outputView.textContent = printed || "(no output)";
    if (data.source) codeView.textContent = data.source;
    if (data.ok && printed === currentLesson.expected.trim()) {
      markLessonDone(currentLesson.id);
      buildLessonList();
      result.textContent = "Correct.";
    } else {
      result.textContent = `Not yet. The program printed:\n${printed || "(nothing)"}`;
    }
  } catch (err) {
    result.textContent = `Could not check: ${(err as Error).message}`;
  }
}

$("lesson-list").addEventListener("change", (e) => showLesson((e.target as HTMLSelectElement).value));
$("btn-lesson-start").addEventListener("click", startLesson);
$("btn-lesson-hint").addEventListener("click", () => $("lesson-hint").classList.toggle("hidden"));
$("btn-lesson-check").addEventListener("click", checkLesson);

// ---------- files ----------

function sanitizeName(raw: string): string {
  return raw.trim().toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63) || "untitled";
}

async function refreshGraphList(selectName?: string) {
  const res = await fetch("/api/graphs");
  const data = (await res.json()) as { graphs: string[] };
  graphList.replaceChildren();
  for (const name of data.graphs) {
    const o = el("option", "", name);
    o.value = name;
    graphList.append(o);
  }
  if (selectName && data.graphs.includes(selectName)) graphList.value = selectName;
}

// ---------- undo and redo ----------

// Call before changing the graph. Stores a copy of the graph as it is now.
function checkpoint(key?: string) {
  history.record(structuredClone(graph), key);
  updateHistoryButtons();
}

function undo() {
  const prev = history.undo(structuredClone(graph));
  if (!prev) return;
  restoreGraph(prev);
  setStatus("Undone");
}

function redo() {
  const next = history.redo(structuredClone(graph));
  if (!next) return;
  restoreGraph(next);
  setStatus("Redone");
}

function updateHistoryButtons() {
  $<HTMLButtonElement>("btn-undo").disabled = !history.canUndo;
  $<HTMLButtonElement>("btn-redo").disabled = !history.canRedo;
}

// Replaces the whole graph and redraws it. Node elements hold references to
// the node objects, so they are rebuilt rather than reused.
function restoreGraph(next: Graph) {
  graph = next;
  graphNameInput.value = graph.name;
  nodeEls.forEach((n) => n.remove());
  nodeEls.clear();
  const stillThere = selection?.kind === "node"
    ? nodeById(selection.id) !== undefined
    : selection?.kind === "wire" && graph.wires.some((w) => w.id === selection?.id);
  if (!stillThere) selection = null;
  renderNodes();
  renderInspector();
  updateHistoryButtons();
  scheduleGenerate();
}

function loadGraph(next: Graph) {
  checkpoint();
  selection = null;
  restoreGraph(next);
}

async function openGraph(name: string) {
  const res = await fetch(`/api/graphs/${encodeURIComponent(name)}`);
  if (!res.ok) {
    setStatus(`Could not open ${name}`);
    return;
  }
  loadGraph(await res.json());
  setStatus(`Opened ${name}`);
}

async function saveGraph() {
  const name = sanitizeName(graphNameInput.value);
  graphNameInput.value = name;
  graph.name = name;
  const res = await fetch(`/api/graphs/${name}`, {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(graph),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    setStatus(`Save failed: ${data.error ?? res.statusText}`);
    return;
  }
  await refreshGraphList(name);
  setStatus(`Saved ${name}`);
}

// ---------- flow packets ----------

// While Trace flow is on, and for a moment after a run, small dots travel along
// the wires the program uses, in the direction the program runs them. Turned
// off when the user asks for reduced motion.
const PACKET_SPEED = 90; // pixels per second
const PACKETS_PER_WIRE = 3;
const PACKET_RUN_MS = 2500;
const packetLayer = svgEl("g", "packets");

function packetsWanted(now: number): boolean {
  if (matchMedia("(prefers-reduced-motion: reduce)").matches) return false;
  return tracing || now < packetsUntil;
}

function startPackets() {
  if (packetFrame === 0) packetFrame = requestAnimationFrame(stepPackets);
}

function stepPackets() {
  packetFrame = 0;
  const now = performance.now();
  if (!packetsWanted(now)) {
    packetLayer.replaceChildren();
    return;
  }
  const dots: SVGElement[] = [];
  for (const path of wiresSvg.querySelectorAll<SVGPathElement>("path.wire")) {
    if (!usedWires.has(path.dataset.id ?? "")) continue;
    const length = path.getTotalLength();
    if (length <= 0) continue;
    const exec = path.classList.contains("exec");
    for (let k = 0; k < PACKETS_PER_WIRE; k++) {
      const along = ((now / 1000) * PACKET_SPEED + (k * length) / PACKETS_PER_WIRE) % length;
      const p = path.getPointAtLength(along);
      const dot = svgEl("circle", exec ? "packet exec" : "packet");
      dot.setAttribute("cx", String(p.x));
      dot.setAttribute("cy", String(p.y));
      dot.setAttribute("r", "3.5");
      dots.push(dot);
    }
  }
  packetLayer.replaceChildren(...dots);
  packetFrame = requestAnimationFrame(stepPackets);
}

// ---------- chain focus ----------

// Double-click a block to light up the blocks that feed it and the blocks it
// feeds. Everything else is dimmed. Escape clears it.
function toggleChain(id: string) {
  chainFocus = chainFocus === id ? null : id;
  setStatus(chainFocus ? "Chain lit. Esc clears." : "Chain cleared");
  refreshNodeStates();
  renderWires();
}

// ---------- block groups ----------

// A group is a saved selection. Groups are listed in the block dock, and placing
// one adds its blocks and wires to the canvas with new ids.
async function refreshGroups() {
  const res = await fetch("/api/groups");
  const data = (await res.json()) as { groups: string[] };
  groupNames = data.groups;
  groupList.replaceChildren();
  if (groupNames.length === 0) {
    groupList.append(el("p", "group-empty", "Select blocks, name them below and save them here."));
  }
  for (const name of groupNames) groupList.append(groupButton(name));
  renderDock();
}

// Func main and func blocks are declarations: a program has one main, and each
// function name appears once. A group cannot hold them, or placing it would
// declare them twice.
function holdsDeclaration(nodes: GNode[]): boolean {
  return nodes.some((n) => CATALOG_BY_KIND.get(n.kind)?.root);
}

async function saveSelectionAsGroup() {
  if (multi.size === 0) {
    setStatus("Select the blocks to save, then name the group");
    return;
  }
  const name = sanitizeName(groupName.value);
  const frag = copyFragment(graph, multi);
  if (holdsDeclaration(frag.nodes)) {
    setStatus("A group cannot hold func main or func blocks");
    return;
  }
  // Stored from the top left corner, so the group can be placed anywhere.
  const minX = Math.min(...frag.nodes.map((n) => n.x));
  const minY = Math.min(...frag.nodes.map((n) => n.y));
  for (const n of frag.nodes) {
    n.x -= minX;
    n.y -= minY;
  }
  const group: Graph = { version: 1, name, nodes: frag.nodes, wires: frag.wires };
  const res = await fetch(`/api/groups/${name}`, {
    method: "PUT",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(group),
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    setStatus(`Could not save group: ${data.error ?? res.statusText}`);
    return;
  }
  groupName.value = name;
  await refreshGroups();
  setStatus(`Saved group ${name}`);
}

async function placeGroup(name: string) {
  const res = await fetch(`/api/groups/${encodeURIComponent(name)}`);
  if (!res.ok) {
    setStatus(`Could not open group ${name}`);
    return;
  }
  const group = (await res.json()) as Graph;
  if (group.nodes.length === 0) return;
  if (holdsDeclaration(group.nodes)) {
    setStatus("A group cannot hold func main or func blocks");
    return;
  }
  const wrap = scroller();
  checkpoint();
  const pasted = pasteFragment(
    { nodes: group.nodes, wires: group.wires },
    Math.round(wrap.scrollLeft + wrap.clientWidth / 2 - 80),
    Math.round(wrap.scrollTop + wrap.clientHeight / 2 - 40),
  );
  graph.nodes.push(...pasted.nodes);
  graph.wires.push(...pasted.wires);
  multi.clear();
  for (const n of pasted.nodes) multi.add(n.id);
  renderNodes();
  select({ kind: "node", id: pasted.nodes[pasted.nodes.length - 1].id });
  scheduleGenerate();
  setStatus(`Added group ${name}`);
}

// ---------- chrome ----------

function showTab(name: string) {
  for (const b of document.querySelectorAll<HTMLButtonElement>("[data-tab]")) b.classList.toggle("active", b.dataset.tab === name);
  for (const panel of document.querySelectorAll<HTMLElement>("[data-panel]")) panel.classList.toggle("hidden", panel.dataset.panel !== name);
  bottom.classList.remove("collapsed");
}

document.querySelectorAll<HTMLButtonElement>("[data-tab]").forEach((b) =>
  b.addEventListener("click", () => showTab(b.dataset.tab ?? "code")),
);
$("btn-toggle-bottom").addEventListener("click", () => bottom.classList.toggle("collapsed"));
$("btn-run").addEventListener("click", run);
$("btn-undo").addEventListener("click", undo);
$("btn-redo").addEventListener("click", redo);
$("btn-trace").addEventListener("click", toggleTrace);
$("btn-problem").addEventListener("click", nextProblem);
const tour = createTour({
  facts: () => ({ graph, tracing, selected: selection !== null, runs }),
  newProgram: () => {
    loadGraph(tourStarter());
    setStatus("New program for the tour.");
  },
});
$("btn-tour").addEventListener("click", () => tour.start());
const findInput = $<HTMLInputElement>("find");
findInput.addEventListener("input", () => setFind(findInput.value));
findInput.addEventListener("keydown", (e) => {
  if (e.key === "Enter") {
    e.preventDefault();
    gotoMatch(e.shiftKey ? -1 : 1);
  } else if (e.key === "Escape") {
    findInput.value = "";
    setFind("");
    findInput.blur();
  }
});
$("btn-save").addEventListener("click", saveGraph);
$("btn-open").addEventListener("click", () => openGraph(graphList.value));
$("btn-new").addEventListener("click", () => {
  const name = sanitizeName(graphNameInput.value || "untitled");
  loadGraph(emptyGraph(name));
  setStatus("New program. Add blocks from the left.");
});
graphNameInput.addEventListener("change", () => {
  graphNameInput.value = sanitizeName(graphNameInput.value);
  graph.name = graphNameInput.value;
});

window.addEventListener("keydown", (e) => {
  const target = e.target as HTMLElement;
  const mod = e.ctrlKey || e.metaKey;
  const key = e.key.toLowerCase();
  if (mod && key === "f") {
    e.preventDefault();
    $<HTMLInputElement>("find").focus();
    $<HTMLInputElement>("find").select();
    return;
  }
  if (target.closest("input, select, textarea")) return;
  if (e.key === "F8") {
    e.preventDefault();
    nextProblem();
    return;
  }
  if (e.key === "Escape" && chainFocus) {
    chainFocus = null;
    setStatus("Chain cleared");
    refreshNodeStates();
    renderWires();
    return;
  }
  if (mod && key === "z") {
    e.preventDefault();
    if (e.shiftKey) redo();
    else undo();
    return;
  }
  if (mod && key === "y") {
    e.preventDefault();
    redo();
    return;
  }
  if (mod && key === "a") {
    e.preventDefault();
    selectAll();
    return;
  }
  if (mod && key === "c") {
    e.preventDefault();
    copySelection();
    return;
  }
  if (mod && key === "v") {
    e.preventDefault();
    pasteClipboard();
    return;
  }
  if (mod && key === "d") {
    e.preventDefault();
    copySelection();
    pasteClipboard();
    return;
  }
  if (e.key !== "Delete" && e.key !== "Backspace") return;
  if (!selection) return;
  e.preventDefault();
  if (selection.kind === "node") removeNodes([...multi]);
  else removeWire(selection.id);
});

renderDock();
renderInspector();
updateHistoryButtons();
buildLessonList();
showLesson(LESSONS[0].id);
(async () => {
  await refreshGraphList();
  await refreshGroups();
  const first = graphList.options[0]?.value;
  if (first) {
    graphList.value = first;
    await openGraph(first);
  } else {
    loadGraph(emptyGraph("hello"));
  }
  // The first program is not an edit, so there is nothing to undo back to.
  history.clear();
  updateHistoryButtons();
  bottom.classList.remove("collapsed");
  showTab("code");
  tour.startIfNew();
})();


