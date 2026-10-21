import { describe, expect, test } from "bun:test";
import { generate } from "../src/codegen/generate";
import { runProgram } from "../src/server/go";
import type { GNode, Graph, ParamValue } from "../src/shared/graph";

// A small way to write a program: each node is [id, kind, params], and each
// wire is "fromNode.port>toNode.port". Params left out take the block's default.
function program(nodes: [string, string, Record<string, ParamValue>?][], wires: string[]): Graph {
  const list: GNode[] = nodes.map(([id, kind, params = {}]) => ({ id, kind, x: 0, y: 0, params }));
  return {
    version: 1,
    name: "blocks",
    nodes: list,
    wires: wires.map((text, i) => {
      const [from, to] = text.split(">");
      const [fromNode, fromPort] = from.split(".");
      const [toNode, toPort] = to.split(".");
      return { id: `w${i}`, from: { node: fromNode, port: fromPort }, to: { node: toNode, port: toPort } };
    }),
  };
}

const errors = (g: Graph) => generate(g).diagnostics.filter((d) => d.level === "error").map((d) => d.message);

// Every block the program uses, compiled and run. Each program prints its
// lines, and the output has to match exactly.
const samples: { name: string; graph: Graph; output: string }[] = [
  {
    name: "range-over-list",
    graph: program(
      [
        ["m", "main"],
        ["xs", "declare", { name: "xs" }],
        ["lst", "list", { type: "int", values: "4, 5, 6" }],
        ["rx", "ref", { name: "xs" }],
        ["loop", "range", { item: "n" }],
        ["rn", "ref", { name: "n" }],
        ["show", "print"],
      ],
      [
        "m.body>xs.in",
        "lst.value>xs.value",
        "xs.next>loop.in",
        "rx.value>loop.list",
        "loop.body>show.in",
        "rn.value>show.value",
      ],
    ),
    output: "4\n5\n6\n",
  },
  {
    name: "while-with-break",
    graph: program(
      [
        ["m", "main"],
        ["zero", "literal", { type: "int", value: "0" }],
        ["t", "declareTyped", { name: "n", type: "int" }],
        ["w", "while"],
        ["lt", "binary", { op: "<" }],
        ["rn1", "ref", { name: "n" }],
        ["five", "literal", { type: "int", value: "5" }],
        ["ifb", "if"],
        ["eq", "binary", { op: "==" }],
        ["rn2", "ref", { name: "n" }],
        ["three", "literal", { type: "int", value: "3" }],
        ["brk", "break"],
        ["asg", "assign", { name: "n" }],
        ["plus", "binary", { op: "+" }],
        ["rn3", "ref", { name: "n" }],
        ["one", "literal", { type: "int", value: "1" }],
        ["show", "print"],
        ["rn4", "ref", { name: "n" }],
      ],
      [
        "m.body>t.in",
        "zero.value>t.value",
        "t.next>w.in",
        "lt.value>w.cond",
        "rn1.value>lt.a",
        "five.value>lt.b",
        "w.body>ifb.in",
        "eq.value>ifb.cond",
        "rn2.value>eq.a",
        "three.value>eq.b",
        "ifb.then>brk.in",
        "ifb.next>asg.in",
        "plus.value>asg.value",
        "rn3.value>plus.a",
        "one.value>plus.b",
        "asg.next>show.in",
        "rn4.value>show.value",
      ],
    ),
    output: "1\n2\n3\n",
  },
  {
    name: "lists",
    graph: program(
      [
        ["m", "main"],
        ["xs", "declare", { name: "xs" }],
        ["lst", "list", { type: "int", values: "1, 2, 3" }],
        ["si", "setItem", { name: "xs" }],
        ["p0", "literal", { type: "int", value: "0" }],
        ["v9", "literal", { type: "int", value: "9" }],
        ["ap", "append", { name: "xs" }],
        ["v4", "literal", { type: "int", value: "4" }],
        ["p1", "print"],
        ["ln", "len"],
        ["rx1", "ref", { name: "xs" }],
        ["p2", "print"],
        ["at", "index"],
        ["rx2", "ref", { name: "xs" }],
        ["z", "literal", { type: "int", value: "0" }],
      ],
      [
        "m.body>xs.in",
        "lst.value>xs.value",
        "xs.next>si.in",
        "p0.value>si.position",
        "v9.value>si.value",
        "si.next>ap.in",
        "v4.value>ap.value",
        "ap.next>p1.in",
        "rx1.value>ln.of",
        "ln.value>p1.value",
        "p1.next>p2.in",
        "rx2.value>at.list",
        "z.value>at.position",
        "at.value>p2.value",
      ],
    ),
    output: "4\n9\n",
  },
  {
    name: "constants-text-and-library",
    graph: program(
      [
        ["m", "main"],
        ["lim", "literal", { type: "int", value: "10" }],
        ["c", "constant", { name: "limit" }],
        ["cv", "convert", { type: "float64" }],
        ["rl", "ref", { name: "limit" }],
        ["four", "literal", { type: "int", value: "4" }],
        ["dv", "binary", { op: "/" }],
        ["tv", "declareTyped", { name: "half", type: "float64" }],
        ["pf", "printf", { format: '"%.2f\\n"', args: "half" }],
        ["pk", "pkgCall", { pkg: "strings", fn: "ToUpper", args: '"gnode"' }],
        ["pp", "print"],
        ["sp", "sprintf", { format: '"%d items"', args: "limit" }],
        ["p5", "print"],
        ["rl2", "ref", { name: "limit" }],
        ["neg", "unary", { op: "-" }],
        ["p6", "print"],
      ],
      [
        "m.body>c.in",
        "lim.value>c.value",
        "c.next>tv.in",
        "dv.value>tv.value",
        "cv.value>dv.a",
        "four.value>dv.b",
        "rl.value>cv.value",
        "tv.next>pf.in",
        "pf.next>pp.in",
        "pk.value>pp.value",
        "pp.next>p5.in",
        "sp.value>p5.value",
        "p5.next>p6.in",
        "neg.value>p6.value",
        "rl2.value>neg.value",
      ],
    ),
    output: "2.50\nGNODE\n10 items\n-10\n",
  },
];

describe("new blocks", () => {
  test("a list block writes its slice literal", () => {
    const { source } = generate(samples[0].graph);
    expect(source).toContain("xs := []int{4, 5, 6}");
    expect(source).toContain("for _, n := range xs {");
  });

  test("a loop body is where break and continue are allowed", () => {
    expect(errors(samples[1].graph)).toEqual([]);
  });

  test("break outside a loop is a problem on the block", () => {
    const g = program([["m", "main"], ["b", "break"]], ["m.body>b.in"]);
    expect(errors(g)).toContain("break can only be used inside a loop");
  });

  test("continue after a loop is a problem too", () => {
    const g = program(
      [
        ["m", "main"],
        ["w", "while"],
        ["t", "literal", { type: "bool", value: "true" }],
        ["c", "continue"],
      ],
      ["m.body>w.in", "t.value>w.cond", "w.next>c.in"],
    );
    expect(errors(g)).toContain("continue can only be used inside a loop");
  });

  test("a library call imports its package", () => {
    const { source } = generate(samples[3].graph);
    expect(source).toContain('"strings"');
    expect(source).toContain('strings.ToUpper("gnode")');
  });

  test("format text imports fmt and writes Sprintf", () => {
    const { source } = generate(samples[3].graph);
    expect(source).toContain('fmt.Sprintf("%d items", limit)');
  });
});

describe("generated programs run", () => {
  for (const sample of samples) {
    test(`${sample.name} prints what it should`, async () => {
      const { source, diagnostics } = generate(sample.graph);
      expect(diagnostics.filter((d) => d.level === "error")).toEqual([]);
      const run = await runProgram(`test-${sample.name}`, source);
      expect(run.output).toBe(sample.output);
    }, 60_000);
  }
});
