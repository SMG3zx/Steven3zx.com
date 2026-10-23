import { describe, expect, test } from "bun:test";
import { generate } from "../src/codegen/generate";
import type { GNode, Graph, ParamValue } from "../src/shared/graph";
import { TOUR_STEPS, tourStarter, type TourFacts } from "../src/shared/tour";

// The editor facts the tour reads, changed by the scripted user below.
interface Ui {
  tracing: boolean;
  selected: boolean;
  runs: number;
}

function node(g: Graph, id: string, kind: string, params: Record<string, ParamValue> = {}): GNode {
  const n: GNode = { id, kind, x: 0, y: 0, params };
  g.nodes.push(n);
  return n;
}

function wire(g: Graph, from: [string, string], to: [string, string]) {
  g.wires.push({
    id: `w-${from.join("-")}-${to.join("-")}`,
    from: { node: from[0], port: from[1] },
    to: { node: to[0], port: to[1] },
  });
}

// What a user does at each step, in order. Steps without a check need nothing.
const doing: Record<string, (g: Graph, ui: Ui) => void> = {
  "add-value": (g) => node(g, "v", "literal", { type: "int", value: "0" }),
  select: (_g, ui) => (ui.selected = true),
  "add-print": (g) => node(g, "p", "print"),
  "flow-wire": (g) => wire(g, ["main", "body"], ["p", "in"]),
  "value-wire": (g) => {
    const v = g.nodes.find((n) => n.id === "v")!;
    v.params = { type: "string", value: "Hello, GNODE" };
    wire(g, ["v", "value"], ["p", "value"]);
  },
  run: (_g, ui) => ui.runs++,
  trace: (_g, ui) => (ui.tracing = true),
  "add-loop": (g) => node(g, "loop", "loop", { variable: "i" }),
  "loop-flow": (g) => {
    // A flow port takes one link, so the print moves into the loop.
    g.wires = g.wires.filter((w) => w.id !== "w-main-body-p-in");
    wire(g, ["main", "body"], ["loop", "in"]);
    wire(g, ["loop", "body"], ["p", "in"]);
  },
  count: (g) => {
    node(g, "three", "literal", { type: "int", value: "3" });
    wire(g, ["three", "value"], ["loop", "count"]);
  },
  counter: (g) => node(g, "i", "ref", { name: "i" }),
  "add-one": (g) => {
    node(g, "plus", "binary", { op: "+" });
    node(g, "one", "literal", { type: "int", value: "1" });
    g.wires = g.wires.filter((w) => w.id !== "w-v-value-p-value");
    wire(g, ["i", "value"], ["plus", "a"]);
    wire(g, ["one", "value"], ["plus", "b"]);
    wire(g, ["plus", "value"], ["p", "value"]);
  },
  "run-count": (_g, ui) => ui.runs++,
};

describe("guided tour", () => {
  test("step ids are unique", () => {
    const ids = TOUR_STEPS.map((s) => s.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  test("every step that waits for the user has a scripted action", () => {
    for (const step of TOUR_STEPS) {
      if (step.check) expect(doing[step.id], step.id).toBeDefined();
    }
  });

  test("walking the steps in order, each check is false before and true after the action", () => {
    const g = tourStarter();
    const ui: Ui = { tracing: false, selected: false, runs: 0 };
    const facts = (): TourFacts => ({ graph: g, ...ui });

    for (const step of TOUR_STEPS) {
      const entry = facts();
      const act = doing[step.id];
      if (!step.check) {
        if (act) act(g, ui);
        continue;
      }
      expect(step.check(entry, entry), `${step.id} should not already be done`).toBe(false);
      act(g, ui);
      expect(step.check(facts(), entry), `${step.id} should be done after the action`).toBe(true);
    }
  });

  test("the finished walk is a program that generates without errors and prints in a loop", () => {
    const g = tourStarter();
    const ui: Ui = { tracing: false, selected: false, runs: 0 };
    for (const step of TOUR_STEPS) doing[step.id]?.(g, ui);

    const { source, diagnostics } = generate(g);
    expect(diagnostics.filter((d) => d.level === "error")).toEqual([]);
    expect(source).toContain("for");
    expect(source).toContain("fmt.Println");
  });
});
