// Practice rooms: short programs with a goal. The learner starts from `starter`,
// wires the blocks themselves, and checks the output against `expected`.
// `solution` is a working answer; the tests run it to make sure each room is
// possible.

import type { GNode, Graph, ParamValue, Wire } from "./graph";

export interface Lesson {
  id: string;
  title: string;
  goal: string;
  hint: string;
  expected: string;
  starter: Graph;
  solution: Graph;
}

function node(id: string, kind: string, x: number, y: number, params: Record<string, ParamValue> = {}): GNode {
  return { id, kind, x, y, params };
}

function wire(id: string, from: [string, string], to: [string, string]): Wire {
  return { id, from: { node: from[0], port: from[1] }, to: { node: to[0], port: to[1] } };
}

function program(name: string, nodes: GNode[], wires: Wire[] = []): Graph {
  return { version: 1, name, nodes, wires };
}

const helloStarter = program("lesson-hello", [node("main", "main", 60, 120)]);

const helloSolution = program(
  "lesson-hello",
  [
    node("main", "main", 60, 120),
    node("print", "print", 340, 120),
    node("text", "literal", 100, 300, { type: "string", value: "Hello, GNODE" }),
  ],
  [wire("w1", ["main", "body"], ["print", "in"]), wire("w2", ["text", "value"], ["print", "value"])],
);

const countStarter = program("lesson-count", [node("main", "main", 60, 120)]);

const countSolution = program(
  "lesson-count",
  [
    node("main", "main", 60, 120),
    node("loop", "loop", 300, 120, { variable: "i" }),
    node("times", "literal", 80, 300, { type: "int", value: "3" }),
    node("print", "print", 560, 120),
    node("counter", "ref", 300, 330, { name: "i" }),
    node("one", "literal", 300, 430, { type: "int", value: "1" }),
    node("plus", "binary", 540, 330, { op: "+" }),
  ],
  [
    wire("w1", ["main", "body"], ["loop", "in"]),
    wire("w2", ["times", "value"], ["loop", "count"]),
    wire("w3", ["loop", "body"], ["print", "in"]),
    wire("w4", ["counter", "value"], ["plus", "a"]),
    wire("w5", ["one", "value"], ["plus", "b"]),
    wire("w6", ["plus", "value"], ["print", "value"]),
  ],
);

// The function is already finished in the starter. The learner calls it from main.
const addStarter = program(
  "lesson-add",
  [
    node("main", "main", 60, 120),
    node("add", "func", 380, 120, { name: "add", signature: "a int, b int", result: "int" }),
    node("ret", "return", 660, 120),
    node("sum", "binary", 420, 330, { op: "+" }),
    node("left", "ref", 180, 330, { name: "a" }),
    node("right", "ref", 180, 430, { name: "b" }),
  ],
  [
    wire("w1", ["add", "body"], ["ret", "in"]),
    wire("w2", ["sum", "value"], ["ret", "value"]),
    wire("w3", ["left", "value"], ["sum", "a"]),
    wire("w4", ["right", "value"], ["sum", "b"]),
  ],
);

const addSolution = program(
  "lesson-add",
  [
    ...addStarter.nodes,
    node("print", "print", 60, 480),
    node("call", "callValue", 300, 560, { name: "add", args: "2, 3" }),
  ],
  [
    ...addStarter.wires,
    wire("w5", ["main", "body"], ["print", "in"]),
    wire("w6", ["call", "value"], ["print", "value"]),
  ],
);

export const LESSONS: Lesson[] = [
  {
    id: "hello",
    title: "Say hello",
    goal: "Print the words Hello, GNODE.",
    hint: "Start from func main and wire its body to a print block. The value block's text is what gets printed.",
    expected: "Hello, GNODE",
    starter: helloStarter,
    solution: helloSolution,
  },
  {
    id: "count",
    title: "Count to three",
    goal: "Print 1, 2 and 3, each on its own line.",
    hint: "The loop repeats its body three times, counting from 0. Add 1 to the counter inside the body before printing it.",
    expected: "1\n2\n3",
    starter: countStarter,
    solution: countSolution,
  },
  {
    id: "add",
    title: "Call a function",
    goal: "Call the add function with 2 and 3, and print the result (5).",
    hint: "Use a call (value) block in main to get the result of add, with the arguments 2, 3 written as text. Print that result.",
    expected: "5",
    starter: addStarter,
    solution: addSolution,
  },
];
