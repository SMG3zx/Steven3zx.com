// The node editor. The canvas shows blocks and wires; the inspector edits the
// selected block; the bottom panel shows the generated Go, the run output and
// the problems the generator found.

import { CATALOG, CATALOG_BY_KIND, type Category, type NodeDef, type PortDef } from "../shared/catalog";
import { emptyGraph, newId, type GNode, type Graph, type ParamValue, type PortRef, type Wire } from "../shared/graph";
import { History } from "./history";

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
}

interface RunResponse {
  ok?: boolean;
  output?: string;
  error?: string;
  diagnostics?: Diagnostic[];
}

type Selection = { kind: "node"; id: string } | { kind: "wire"; id: string } | null;

const CATEGORY_TITLES: Record<Category, string> = {
  program: "Program",
  flow: "Flow",
  values: "Values",
  vars: "Variables",
  io: "Input / output",
  calls: "Calls",
};

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
let selection: Selection = null;
let diagnostics: Diagnostic[] = [];
let emitted = new Set<string>();
const nodeEls = new Map<string, HTMLElement>();
let generateTimer: ReturnType<typeof setTimeout> | undefined;

function setStatus(text: string) {
  statusEl.textContent = text;
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

// ---------- palette ----------

function buildPalette() {
  const palette = $("palette");
  palette.replaceChildren();
  const order: Category[] = ["program", "flow", "values", "vars", "calls", "io"];
  for (const cat of order) {
    const defs = CATALOG.filter((d) => d.category === cat);
    if (defs.length === 0) continue;
    palette.append(el("h3", "", CATEGORY_TITLES[cat]));
    for (const def of defs) {
      const btn = el("button", "", def.title);
      btn.type = "button";
      btn.title = def.help;
      btn.draggable = true;
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
      palette.append(btn);
    }
  }
}

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
  if (e.target === canvas || e.target === wiresSvg) select(null);
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
  checkpoint();
  graph.nodes = graph.nodes.filter((n) => n.id !== id);
  graph.wires = graph.wires.filter((w) => w.from.node !== id && w.to.node !== id);
  nodeEls.get(id)?.remove();
  nodeEls.delete(id);
  if (selection && (selection.kind === "node" ? selection.id === id : true)) select(null);
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
  renderWires();
}

function buildNodeEl(n: GNode): HTMLElement {
  const def = defOf(n);
  const box = el("div", "node" + (def?.root ? " root" : ""));
  box.dataset.id = n.id;
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
    if ((e.target as HTMLElement).closest(".port")) return;
    select({ kind: "node", id: n.id });
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
  const startX = e.clientX;
  const startY = e.clientY;
  const origX = n.x;
  const origY = n.y;
  const before = structuredClone(graph);
  select({ kind: "node", id: n.id });
  const move = (ev: PointerEvent) => {
    n.x = Math.max(0, origX + ev.clientX - startX);
    n.y = Math.max(0, origY + ev.clientY - startY);
    box.style.left = `${n.x}px`;
    box.style.top = `${n.y}px`;
    renderWires();
  };
  const up = () => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
    if (n.x !== origX || n.y !== origY) {
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
    draft.setAttribute("d", curve(start, end, dir === "out"));
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

function curve(a: { x: number; y: number }, b: { x: number; y: number }, fromOut = true): string {
  const reach = Math.max(60, Math.abs(b.x - a.x) * 0.5);
  const dir = fromOut ? 1 : -1;
  return `M ${a.x} ${a.y} C ${a.x + reach * dir} ${a.y}, ${b.x - reach * dir} ${b.y}, ${b.x} ${b.y}`;
}

function renderWires() {
  wiresSvg.replaceChildren();
  for (const w of graph.wires) {
    const from = nodeById(w.from.node);
    const to = nodeById(w.to.node);
    if (!from || !to) continue;
    const isExec = defOf(from)?.outputs.find((p) => p.id === w.from.port)?.kind === "exec";
    const path = svgEl("path", "wire" + (isExec ? " exec" : "") + (selection?.kind === "wire" && selection.id === w.id ? " selected" : ""));
    path.dataset.id = w.id;
    const a = portCenter(w.from.node, w.from.port, "out");
    const b = portCenter(w.to.node, w.to.port, "in");
    path.setAttribute("d", curve(a, b));
    path.addEventListener("pointerdown", (e) => {
      e.stopPropagation();
      select({ kind: "wire", id: w.id });
    });
    wiresSvg.append(path);
  }
}

// ---------- selection and inspector ----------

function select(next: Selection) {
  selection = next;
  for (const [id, node] of nodeEls) node.classList.toggle("selected", next?.kind === "node" && next.id === id);
  renderWires();
  renderInspector();
}

function renderInspector() {
  inspector.replaceChildren();
  if (!selection) {
    inspector.append(el("p", "empty", "Select a block to edit it, or drag blocks from the left to add them."));
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

// ---------- generation and problems ----------

function refreshNodeStates() {
  for (const [id, node] of nodeEls) {
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
    codeView.textContent = data.formatError && !data.formatted
      ? `${data.source}\n\n// gofmt: ${data.formatError}`
      : data.source;
    renderProblems();
    refreshNodeStates();
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
    li.addEventListener("click", () => {
      if (!d.nodeId) return;
      const node = nodeEls.get(d.nodeId);
      select({ kind: "node", id: d.nodeId });
      node?.scrollIntoView({ block: "center", inline: "center" });
    });
    problemList.append(li);
  }
  if (diagnostics.length === 0) problemList.append(el("li", "", "No problems."));
  problemCount.textContent = diagnostics.length ? `(${diagnostics.length})` : "";
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
    outputView.textContent = [data.output ?? "", data.error ? `\n${data.error}` : ""].join("").trimEnd() || "(no output)";
    if (data.source) codeView.textContent = data.source;
  } catch (err) {
    outputView.textContent = `Could not run: ${(err as Error).message}`;
  }
}

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
  if (target.closest("input, select, textarea")) return;
  const mod = e.ctrlKey || e.metaKey;
  const key = e.key.toLowerCase();
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
  if (e.key !== "Delete" && e.key !== "Backspace") return;
  if (!selection) return;
  e.preventDefault();
  if (selection.kind === "node") removeNode(selection.id);
  else removeWire(selection.id);
});

buildPalette();
renderInspector();
updateHistoryButtons();
(async () => {
  await refreshGraphList();
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
})();


