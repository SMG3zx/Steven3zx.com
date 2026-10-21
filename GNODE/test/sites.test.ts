import { describe, expect, test } from "bun:test";
import { generate } from "../src/codegen/generate";
import { runProgram } from "../src/server/go";
import type { GNode, Graph, ParamValue } from "../src/shared/graph";

// Each node is [id, kind, params]; each wire is "fromNode.port>toNode.port".
function program(nodes: [string, string, Record<string, ParamValue>?][], wires: string[]): Graph {
  const list: GNode[] = nodes.map(([id, kind, params = {}]) => ({ id, kind, x: 0, y: 0, params }));
  return {
    version: 1,
    name: "sites",
    nodes: list,
    wires: wires.map((text, i) => {
      const [from, to] = text.split(">");
      const [fromNode, fromPort] = from.split(".");
      const [toNode, toPort] = to.split(".");
      return { id: `w${i}`, from: { node: fromNode, port: fromPort }, to: { node: toNode, port: toPort } };
    }),
  };
}

// A site with 10 kg and two slots, one cleaner on it, and three passes:
// dig 3 kg, unload it, dig 3 kg again. Then read the site and the buffer.
const digging: Graph = program(
  [
    ["m", "main"],
    ["site", "trashSite", { name: "pit", reserve: "10", slots: "2" }],
    ["s", "declare", { name: "s" }],
    ["rs", "ref", { name: "s" }],
    ["cl", "trashCleaner", { buffer: "5" }],
    ["c", "declare", { name: "c" }],
    ["rc1", "ref", { name: "c" }],
    ["w1", "literal", { type: "float64", value: "3" }],
    ["d1", "digTrash", { hours: "1" }],
    ["rc2", "ref", { name: "c" }],
    ["ru", "unloadTrash"],
    ["p1", "print"],
    ["w2", "literal", { type: "float64", value: "3" }],
    ["d2", "digTrash", { hours: "1" }],
    ["rc3", "ref", { name: "c" }],
    ["rc4", "ref", { name: "c" }],
    ["rr1", "readCleaner", { field: "Site.Reserve" }],
    ["p2", "print"],
    ["rc5", "ref", { name: "c" }],
    ["rr2", "readCleaner", { field: "Buffer" }],
    ["p3", "print"],
  ],
  [
    "m.body>s.in",
    "site.site>s.value",
    "s.next>c.in",
    "rs.value>cl.site",
    "cl.cleaner>c.value",
    "c.next>d1.in",
    "rc1.value>d1.cleaner",
    "w1.value>d1.workers",
    "d1.next>p1.in",
    "rc2.value>ru.cleaner",
    "ru.kg>p1.value",
    "p1.next>d2.in",
    "rc3.value>d2.cleaner",
    "w2.value>d2.workers",
    "d2.next>p2.in",
    "rc4.value>rr1.cleaner",
    "rr1.value>p2.value",
    "p2.next>p3.in",
    "rc5.value>rr2.cleaner",
    "rr2.value>p3.value",
  ],
);

describe("trash site blocks", () => {
  test("the Go for a site and its cleaner comes with its helper code, written once", () => {
    const { source, diagnostics } = generate(digging);
    expect(diagnostics.filter((d) => d.level === "error")).toEqual([]);
    expect(source).toContain('s := newSite("pit", 10, 2)');
    expect(source).toContain("c := newCleaner(s, 5)");
    expect(source).toContain("c.Dig(3, 1)");
    expect(source).toContain("fmt.Println(c.Unload())");
    expect(source.split("type Cleaner struct").length - 1).toBe(1);
  });

  test("a cleaner with no site is a problem on the cleaner", () => {
    const g = program([["m", "main"], ["cl", "trashCleaner"], ["c", "declare", { name: "c" }]], ["m.body>c.in", "cl.cleaner>c.value"]);
    const messages = generate(g).diagnostics.map((d) => d.message);
    expect(messages).toContain('trash cleaner: connect a value to "site"');
  });

  test("the helper code is left out when no trash block is used", () => {
    const g = program([["m", "main"], ["p", "print"]], ["m.body>p.in"]);
    expect(generate(g).source).not.toContain("type Site struct");
  });
});

describe("trash site programs run", () => {
  test("a cleaner digs from its site and gives up what it held", async () => {
    const { source, diagnostics } = generate(digging);
    expect(diagnostics.filter((d) => d.level === "error")).toEqual([]);
    const run = await runProgram("test-trash-site", source);
    expect(run.output).toBe("3\n4\n3\n");
  }, 60_000);
});
