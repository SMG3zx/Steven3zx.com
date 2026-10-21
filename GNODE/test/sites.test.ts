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

// Puts a block in a slot of a container.
function inSlot(g: Graph, child: string, parent: string, slot: number): Graph {
  const n = g.nodes.find((x) => x.id === child)!;
  n.parent = parent;
  n.slot = slot;
  return g;
}

// A site with 10 kg and two slots, one cleaner in its first slot, and three
// passes: dig 3 kg, unload it, dig 3 kg again. Then read the site and the buffer.
const digging: Graph = inSlot(
  program(
    [
      ["m", "main"],
      ["site", "trashSite", { name: "pit", reserve: "10", slots: "2" }],
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
      "m.body>site.in",
      "site.next>c.in",
      "c.next>d1.in",
      "cl.cleaner>c.value",
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
  ),
  "cl",
  "site",
  0,
);

const messages = (g: Graph) => generate(g).diagnostics.map((d) => d.message);

describe("blocks in a container", () => {
  test("the cleaner takes the site's name, and the site is declared once", () => {
    const { source, diagnostics } = generate(digging);
    expect(diagnostics.filter((d) => d.level === "error")).toEqual([]);
    expect(source).toContain('pit := newSite("pit", 10, 2)');
    expect(source).toContain("c := newCleaner(pit, 5)");
    expect(source).toContain("c.Dig(3, 1)");
    expect(source).toContain("fmt.Println(c.Unload())");
    expect(source.split("type Cleaner struct").length - 1).toBe(1);
  });

  test("a cleaner that stands in no site is a problem on the cleaner", () => {
    const g = program(
      [["m", "main"], ["cl", "trashCleaner"], ["c", "declare", { name: "c" }]],
      ["m.body>c.in", "cl.cleaner>c.value"],
    );
    expect(messages(g)).toContain("a trash cleaner has to stand in a slot of a trash site");
  });

  test("a block that the container does not host is an error", () => {
    const g = inSlot(program([["m", "main"], ["site", "trashSite"], ["p", "print"]], ["m.body>site.in"]), "p", "site", 0);
    expect(messages(g)).toContain("print cannot stand in trash site");
  });

  test("a slot past the container's slots is an error", () => {
    const g = inSlot(
      program([["m", "main"], ["site", "trashSite", { slots: "2" }], ["cl", "trashCleaner"]], ["m.body>site.in"]),
      "cl",
      "site",
      2,
    );
    expect(messages(g)).toContain("trash site has no slot 3");
  });

  test("two blocks in one slot are an error", () => {
    const g = inSlot(
      program(
        [["m", "main"], ["site", "trashSite"], ["a", "trashCleaner"], ["b", "trashCleaner"]],
        ["m.body>site.in"],
      ),
      "a",
      "site",
      0,
    );
    inSlot(g, "b", "site", 0);
    expect(messages(g)).toContain("trash cleaner and trash cleaner are in the same slot");
  });

  test("a container that is not in the program leaves its blocks with nothing to stand in", () => {
    const g = inSlot(
      program(
        [["m", "main"], ["site", "trashSite"], ["cl", "trashCleaner"], ["c", "declare", { name: "c" }]],
        ["m.body>c.in", "cl.cleaner>c.value"],
      ),
      "cl",
      "site",
      0,
    );
    expect(messages(g)).toContain("trash site is not in the program, so trash cleaner has nothing to stand in");
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
