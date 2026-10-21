// Turns a graph into Go source.
//
// Program flow follows exec wires (the white ports): each block's "next"
// output leads to the block that runs after it. Values follow data wires:
// a data input is replaced by the Go expression produced by the block wired
// into it. Roots (func main, func) become top-level declarations.

import { CATALOG_BY_KIND, indent, type EmitIO, type NodeDef, type PortDef } from "../shared/catalog";
import type { GNode, Graph, ParamValue, Wire } from "../shared/graph";

export interface Diagnostic {
  level: "error" | "warning";
  nodeId?: string;
  message: string;
}

export interface GenerateResult {
  source: string;
  diagnostics: Diagnostic[];
  emittedNodeIds: string[];
  // Wires the generator read while it built the program: the flow that runs
  // from func main, and the values that feed it.
  usedWireIds: string[];
}

interface Context {
  graph: Graph;
  nodes: Map<string, GNode>;
  incoming: Map<string, Wire[]>; // key: `${node}.${port}` for wires that end there
  outgoing: Map<string, Wire[]>; // key: `${node}.${port}` for wires that start there
  diagnostics: Diagnostic[];
  imports: Set<string>;
  emitted: Set<string>;
  used: Set<string>;
  // How many loop bodies the generator is inside right now.
  loops: number;
}

const key = (node: string, port: string) => `${node}.${port}`;

function portDef(def: NodeDef, side: "inputs" | "outputs", id: string): PortDef | undefined {
  return def[side].find((p) => p.id === id);
}

function push<K, V>(map: Map<K, V[]>, k: K, v: V) {
  const list = map.get(k);
  if (list) list.push(v);
  else map.set(k, [v]);
}

function indexGraph(graph: Graph): Context {
  const nodes = new Map<string, GNode>();
  for (const n of graph.nodes) nodes.set(n.id, n);
  const ctx: Context = {
    graph,
    nodes,
    incoming: new Map(),
    outgoing: new Map(),
    diagnostics: [],
    imports: new Set(),
    emitted: new Set(),
    used: new Set(),
    loops: 0,
  };
  for (const w of graph.wires) {
    push(ctx.outgoing, key(w.from.node, w.from.port), w);
    push(ctx.incoming, key(w.to.node, w.to.port), w);
  }
  return ctx;
}

function diag(ctx: Context, level: Diagnostic["level"], message: string, nodeId?: string) {
  ctx.diagnostics.push({ level, message, nodeId });
}

function checkWiring(ctx: Context) {
  for (const w of ctx.graph.wires) {
    const from = ctx.nodes.get(w.from.node);
    const to = ctx.nodes.get(w.to.node);
    if (!from || !to) {
      diag(ctx, "error", `wire ${w.id} points at a block that no longer exists`);
      continue;
    }
    const fromDef = CATALOG_BY_KIND.get(from.kind);
    const toDef = CATALOG_BY_KIND.get(to.kind);
    if (!fromDef || !toDef) continue;
    const out = portDef(fromDef, "outputs", w.from.port);
    const inp = portDef(toDef, "inputs", w.to.port);
    if (!out || !inp) {
      diag(ctx, "error", `wire ${w.id} uses a port that ${to.kind} or ${from.kind} does not have`, to.id);
      continue;
    }
    if (out.kind !== inp.kind) {
      diag(ctx, "error", `cannot connect a ${out.kind} output to a ${inp.kind} input`, to.id);
    }
  }
  for (const n of ctx.graph.nodes) {
    const def = CATALOG_BY_KIND.get(n.kind);
    if (!def) {
      diag(ctx, "error", `unknown block kind "${n.kind}"`, n.id);
      continue;
    }
    for (const p of def.outputs) {
      const wires = ctx.outgoing.get(key(n.id, p.id)) ?? [];
      if (p.kind === "exec" && wires.length > 1) {
        diag(ctx, "error", `${def.title}: "${p.label}" leads to more than one block`, n.id);
      }
    }
    for (const p of def.inputs) {
      const wires = ctx.incoming.get(key(n.id, p.id)) ?? [];
      if (p.kind === "data" && wires.length > 1) {
        diag(ctx, "error", `${def.title}: "${p.label}" has more than one value connected`, n.id);
      }
    }
  }
}

function param(ctx: Context, node: GNode, def: NodeDef, name: string): ParamValue {
  const p = def.params.find((d) => d.id === name);
  const value = node.params[name];
  if (value !== undefined) return value;
  return p ? p.default : "";
}

function makeIO(ctx: Context, node: GNode, def: NodeDef, stack: string[]): EmitIO {
  return {
    value(port) {
      const wire = ctx.incoming.get(key(node.id, port))?.[0];
      if (!wire) return undefined;
      ctx.used.add(wire.id);
      return exprOf(ctx, wire.from.node, wire.from.port, [...stack, node.id]);
    },
    chain(port) {
      const wire = ctx.outgoing.get(key(node.id, port))?.[0];
      if (!wire) return [];
      ctx.used.add(wire.id);
      const inBody = def.loopBody === true && port === "body";
      if (inBody) ctx.loops += 1;
      try {
        return blockLines(ctx, wire.to.node, [...stack, node.id]);
      } finally {
        if (inBody) ctx.loops -= 1;
      }
    },
    param: (name) => param(ctx, node, def, name),
    text: (name) => String(param(ctx, node, def, name)),
    inLoop: () => ctx.loops > 0,
  };
}

function emitNode(ctx: Context, nodeId: string, stack: string[]) {
  const node = ctx.nodes.get(nodeId);
  if (!node) return undefined;
  const def = CATALOG_BY_KIND.get(node.kind);
  if (!def) return undefined;
  ctx.emitted.add(node.id);
  const result = def.emit(makeIO(ctx, node, def, stack));
  for (const i of result.imports ?? []) ctx.imports.add(i);
  return { node, def, result };
}

// The statements for an exec block, then the chain that follows it.
function blockLines(ctx: Context, nodeId: string, stack: string[]): string[] {
  if (stack.includes(nodeId)) {
    const node = ctx.nodes.get(nodeId);
    diag(ctx, "error", "the program loops back on itself without a loop block", nodeId);
    return [];
  }
  const emitted = emitNode(ctx, nodeId, stack);
  if (!emitted) return [];
  const { node, def, result } = emitted;
  if (result.decl) {
    diag(ctx, "error", `${def.title} can only be used at the top level`, node.id);
    return [];
  }
  if (result.problem) diag(ctx, "error", result.problem, node.id);
  const lines = [...(result.stmts ?? [])];
  if (def.outputs.some((o) => o.id === "next")) {
    lines.push(...chainFrom(ctx, node.id, "next", [...stack, node.id]));
  }
  return lines;
}

function chainFrom(ctx: Context, nodeId: string, port: string, stack: string[]): string[] {
  const wire = ctx.outgoing.get(key(nodeId, port))?.[0];
  if (!wire) return [];
  ctx.used.add(wire.id);
  return blockLines(ctx, wire.to.node, stack);
}

// The Go expression a data output produces.
function exprOf(ctx: Context, nodeId: string, port: string, stack: string[]): string {
  if (stack.includes(nodeId)) {
    diag(ctx, "error", "a value depends on itself", nodeId);
    return "nil";
  }
  const emitted = emitNode(ctx, nodeId, stack);
  if (!emitted) return "nil";
  const { node, def, result } = emitted;
  if (!portDef(def, "outputs", port)) return "nil";
  if (result.expr === undefined) {
    diag(ctx, "error", `${def.title} does not give a value`, node.id);
    return "nil";
  }
  return result.expr;
}

function checkRequired(ctx: Context) {
  for (const n of ctx.graph.nodes) {
    const def = CATALOG_BY_KIND.get(n.kind);
    if (!def) continue;
    for (const p of def.inputs) {
      if (!p.required || p.kind !== "data") continue;
      if ((ctx.incoming.get(key(n.id, p.id)) ?? []).length > 0) continue;
      const level = ctx.emitted.has(n.id) ? "error" : "warning";
      diag(ctx, level, `${def.title}: connect a value to "${p.label}"`, n.id);
    }
  }
}

function checkOrphans(ctx: Context) {
  for (const n of ctx.graph.nodes) {
    const def = CATALOG_BY_KIND.get(n.kind);
    if (!def || ctx.emitted.has(n.id)) continue;
    if (def.root) continue;
    diag(ctx, "warning", `${def.title} is not connected to the program, so it is ignored`, n.id);
  }
}

export function generate(graph: Graph): GenerateResult {
  const ctx = indexGraph(graph);
  checkWiring(ctx);

  const roots = graph.nodes.filter((n) => CATALOG_BY_KIND.get(n.kind)?.root);
  const mains = roots.filter((n) => n.kind === "main");
  if (mains.length === 0) diag(ctx, "error", "add a func main block to start the program");
  if (mains.length > 1) diag(ctx, "error", "a program can have only one func main block", mains[1].id);

  const decls: string[][] = [];
  for (const root of roots) {
    const emitted = emitNode(ctx, root.id, []);
    if (!emitted || !emitted.result.decl) continue;
    decls.push(emitted.result.decl);
  }

  checkRequired(ctx);
  checkOrphans(ctx);

  const out: string[] = [];
  out.push("package main");
  if (ctx.imports.size > 0) {
    out.push("", "import (", ...[...ctx.imports].sort().map((i) => `\t"${i}"`), ")");
  }
  for (const d of decls) {
    out.push("", ...d);
  }
  out.push("");

  return {
    source: out.join("\n"),
    diagnostics: ctx.diagnostics,
    emittedNodeIds: [...ctx.emitted],
    usedWireIds: [...ctx.used],
  };
}

export { indent };
