// The graph is what the editor saves and what the generator reads.
// It describes blocks (nodes), where they sit on the canvas, and which
// output port is wired to which input port.

export type ParamValue = string | number | boolean;

export interface GNode {
  id: string;
  kind: string;
  x: number;
  y: number;
  params: Record<string, ParamValue>;
  // A block can stand in a slot of a container block (such as a trash site).
  // Then parent is the container's id, slot is the slot's number from 0, and
  // x and y are not used: the slot places the block.
  parent?: string;
  slot?: number;
}

export interface PortRef {
  node: string;
  port: string;
}

// A point a square wire turns at, measured from the top left of the block the
// wire leaves, so the bend moves with that block.
export interface Bend {
  dx: number;
  dy: number;
}

export interface Wire {
  id: string;
  from: PortRef;
  to: PortRef;
  bends?: Bend[];
}

export interface Graph {
  version: 1;
  name: string;
  nodes: GNode[];
  wires: Wire[];
}

export function emptyGraph(name = "untitled"): Graph {
  return { version: 1, name, nodes: [], wires: [] };
}

export function newId(prefix: string): string {
  return `${prefix}${Math.random().toString(36).slice(2, 8)}${Date.now().toString(36).slice(-4)}`;
}

// A copied piece of a graph: the blocks picked and the wires between them.
export interface Fragment {
  nodes: GNode[];
  wires: Wire[];
}

// A block in a slot is copied only with its container, since its slot means nothing without it.
export function copyFragment(graph: Graph, ids: Iterable<string>): Fragment {
  const picked = new Set(ids);
  return {
    nodes: graph.nodes
      .filter((n) => picked.has(n.id) && (n.parent === undefined || picked.has(n.parent)))
      .map((n) => structuredClone(n)),
    wires: graph.wires
      .filter((w) => picked.has(w.from.node) && picked.has(w.to.node))
      .map((w) => structuredClone(w)),
  };
}

// Gives every block and wire a new id and moves the blocks by (dx, dy). Wires
// are re-pointed at the new blocks, so the copy stays wired to itself.
export function pasteFragment(fragment: Fragment, dx: number, dy: number): Fragment {
  const ids = new Map<string, string>();
  const nodes = fragment.nodes.map((n) => {
    const id = newId("n");
    ids.set(n.id, id);
    return { ...structuredClone(n), id, x: Math.max(0, n.x + dx), y: Math.max(0, n.y + dy) };
  });
  // A block in a slot keeps its slot, under the container's new id. Its position
  // comes from the slot, so moving the copy does not move it.
  for (const n of nodes) {
    if (n.parent === undefined) continue;
    const parent = ids.get(n.parent);
    if (parent) n.parent = parent;
    else {
      delete n.parent;
      delete n.slot;
    }
  }
  const wires = fragment.wires.map((w) => ({
    id: newId("w"),
    from: { node: ids.get(w.from.node) ?? w.from.node, port: w.from.port },
    to: { node: ids.get(w.to.node) ?? w.to.node, port: w.to.port },
  }));
  return { nodes, wires };
}

// Converts a graph read from disk or a request body into a Graph, or throws
// with a message that says what is wrong. Nothing else is trusted.
export function parseGraph(value: unknown): Graph {
  if (!value || typeof value !== "object") throw new Error("graph must be an object");
  const g = value as Partial<Graph>;
  if (g.version !== 1) throw new Error("unsupported graph version");
  if (typeof g.name !== "string") throw new Error("graph.name must be a string");
  if (!Array.isArray(g.nodes) || !Array.isArray(g.wires)) throw new Error("graph needs nodes and wires arrays");
  for (const n of g.nodes) {
    if (typeof n.id !== "string" || typeof n.kind !== "string") throw new Error("every node needs an id and kind");
    if (typeof n.x !== "number" || typeof n.y !== "number") throw new Error(`node ${n.id} needs numeric x and y`);
    if (n.parent !== undefined && typeof n.parent !== "string") throw new Error(`node ${n.id} has a parent that is not an id`);
    if (n.slot !== undefined && !(Number.isInteger(n.slot) && n.slot >= 0)) throw new Error(`node ${n.id} has a slot that is not a number from 0`);
    if (!n.params || typeof n.params !== "object") throw new Error(`node ${n.id} needs a params object`);
  }
  for (const w of g.wires) {
    if (typeof w.id !== "string" || !w.from || !w.to) throw new Error("every wire needs an id, from and to");
    if (w.bends === undefined) continue;
    if (!Array.isArray(w.bends)) throw new Error(`wire ${w.id} bends must be a list`);
    for (const b of w.bends) {
      if (!Number.isFinite(b?.dx) || !Number.isFinite(b?.dy)) throw new Error(`wire ${w.id} has a bend without numeric dx and dy`);
    }
  }
  return g as Graph;
}

// The blocks upstream (feeding into it) and downstream (fed by it) of a block,
// with the block itself, and the wires joining two of them.
export function chainOf(graph: Graph, id: string): { nodes: Set<string>; wires: Set<string> } {
  const upstream = (cur: string) => graph.wires.filter((w) => w.to.node === cur).map((w) => w.from.node);
  const downstream = (cur: string) => graph.wires.filter((w) => w.from.node === cur).map((w) => w.to.node);
  const nodes = new Set<string>([id]);
  for (const step of [upstream, downstream]) {
    const seen = new Set<string>([id]);
    const queue = [id];
    while (queue.length > 0) {
      const cur = queue.pop()!;
      for (const next of step(cur)) {
        if (seen.has(next)) continue;
        seen.add(next);
        nodes.add(next);
        queue.push(next);
      }
    }
  }
  const wires = new Set(graph.wires.filter((w) => nodes.has(w.from.node) && nodes.has(w.to.node)).map((w) => w.id));
  return { nodes, wires };
}
