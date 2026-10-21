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
