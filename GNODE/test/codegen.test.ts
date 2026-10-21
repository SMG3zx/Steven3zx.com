import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { generate } from "../src/codegen/generate";
import { emptyGraph, parseGraph, type Graph } from "../src/shared/graph";

const hello = parseGraph(JSON.parse(readFileSync(join(import.meta.dir, "..", "graphs", "hello.json"), "utf8")));

describe("generate", () => {
  test("the sample program becomes Go source with its functions and loop", () => {
    const { source, diagnostics } = generate(hello);
    expect(diagnostics.filter((d) => d.level === "error")).toEqual([]);
    expect(source).toContain("package main");
    expect(source).toContain('import (\n\t"fmt"\n)');
    expect(source).toContain("func main() {");
    expect(source).toContain("total := 0");
    expect(source).toContain("for i := 0; i < 5; i++ {");
    expect(source).toContain("total = (total + i)");
    expect(source).toContain("fmt.Println(total)");
    expect(source).toContain("func greet(name string) {");
    expect(source).toContain('greet("GNODE")');
  });

  test("a graph without func main reports an error", () => {
    const graph = emptyGraph("nothing");
    const { diagnostics } = generate(graph);
    expect(diagnostics.some((d) => d.level === "error" && d.message.includes("func main"))).toBe(true);
  });

  test("an exec output wired to two blocks is an error", () => {
    const graph: Graph = {
      ...hello,
      wires: [
        ...hello.wires,
        { id: "extra", from: { node: "main", port: "body" }, to: { node: "greetPrint", port: "in" } },
      ],
    };
    const { diagnostics } = generate(graph);
    expect(diagnostics.some((d) => d.level === "error" && d.message.includes("more than one block"))).toBe(true);
  });

  test("a required value left unconnected is an error on a reachable block", () => {
    const graph: Graph = { ...hello, wires: hello.wires.filter((w) => w.id !== "w10") };
    const { diagnostics } = generate(graph);
    expect(diagnostics.some((d) => d.level === "error" && d.message.includes('connect a value to "value"'))).toBe(true);
  });

  test("an exec loop does not recurse forever", () => {
    const graph: Graph = {
      version: 1,
      name: "loop",
      nodes: [
        { id: "m", kind: "main", x: 0, y: 0, params: {} },
        { id: "a", kind: "print", x: 0, y: 0, params: {} },
        { id: "b", kind: "print", x: 0, y: 0, params: {} },
      ],
      wires: [
        { id: "1", from: { node: "m", port: "body" }, to: { node: "a", port: "in" } },
        { id: "2", from: { node: "a", port: "next" }, to: { node: "b", port: "in" } },
        { id: "3", from: { node: "b", port: "next" }, to: { node: "a", port: "in" } },
      ],
    };
    const { diagnostics } = generate(graph);
    expect(diagnostics.some((d) => d.message.includes("loops back"))).toBe(true);
  });
});

describe("parseGraph", () => {
  test("rejects a graph without a version", () => {
    expect(() => parseGraph({ name: "x", nodes: [], wires: [] })).toThrow("unsupported graph version");
  });

  test("accepts the sample graph", () => {
    expect(parseGraph(hello).name).toBe("hello");
  });
});
