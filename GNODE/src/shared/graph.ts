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
}

export interface PortRef {
  node: string;
  port: string;
}

export interface Wire {
  id: string;
  from: PortRef;
  to: PortRef;
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

export function copyFragment(graph: Graph, ids: Iterable<string>): Fragment {
  const picked = new Set(ids);
  return {
    nodes: graph.nodes.filter((n) => picked.has(n.id)).map((n) => structuredClone(n)),
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
    if (!n.params || typeof n.params !== "object") throw new Error(`node ${n.id} needs a params object`);
  }
  for (const w of g.wires) {
    if (typeof w.id !== "string" || !w.from || !w.to) throw new Error("every wire needs an id, from and to");
  }
  return g as Graph;
}
