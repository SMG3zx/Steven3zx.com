// The guided tour: the steps the editor walks a new user through, and the
// checks that move it on when the user has done the thing a step asks for.
// The checks read the graph and a few editor facts, so they can be tested
// without a browser.

import { emptyGraph, type GNode, type Graph } from "./graph";

export interface TourFacts {
  graph: Graph;
  tracing: boolean;
  selected: boolean;
  // How many times Run has finished successfully in this session.
  runs: number;
}

// `entry` is what the facts were when the step began, for checks that need
// something to happen after the step started (such as a Run).
export type Check = (now: TourFacts, entry: TourFacts) => boolean;

export interface TourStep {
  id: string;
  title: string;
  text: string;
  // CSS selector of the element the card points at. Left out, the card sits
  // in the middle of the screen.
  target?: string;
  // When set, the tour moves on by itself once this becomes true.
  check?: Check;
  // Shown under the text while the check is still waiting.
  hint?: string;
  // Extra button that starts the tour on a new, empty program.
  offer?: "newProgram";
}

export interface Ends {
  node: (n: GNode) => boolean;
  port: string;
}

export const isKind = (kind: string) => (n: GNode) => n.kind === kind;

// True when a wire joins the output port of one node to the input port of another.
export function wired(g: Graph, out: Ends, input: Ends): boolean {
  const byId = new Map(g.nodes.map((n) => [n.id, n]));
  return g.wires.some((w) => {
    const a = byId.get(w.from.node), b = byId.get(w.to.node);
    return !!a && !!b && w.from.port === out.port && out.node(a) && w.to.port === input.port && input.node(b);
  });
}

// A value block holding the given text, with the given type.
export const textValue = (value: string) => (n: GNode) =>
  n.kind === "literal" && n.params.type === "string" && String(n.params.value).trim().toLowerCase() === value.toLowerCase();

export const numberValue = (value: string, type = "int") => (n: GNode) =>
  n.kind === "literal" && n.params.type === type && String(n.params.value).trim() === value;

export const namedRef = (name: string) => (n: GNode) =>
  n.kind === "ref" && String(n.params.name).trim() === name;

export const hasOperator = (op: string) => (n: GNode) => n.kind === "binary" && n.params.op === op;

// The program the tour starts on: an empty func main, nothing else.
export function tourStarter(): Graph {
  const g = emptyGraph("tour");
  g.nodes.push({ id: "main", kind: "main", x: 60, y: 120, params: {} });
  return g;
}

const FLOW_OUT = { node: isKind("main"), port: "body" };

export const TOUR_STEPS: TourStep[] = [
  // Part 1: the editor
  {
    id: "welcome",
    title: "Welcome to GNODE",
    text: "GNODE writes a Go program for you from blocks you wire together. This tour shows you the editor, then teaches the programming. You can skip it at any time and replay it from the Tour button.",
    offer: "newProgram",
  },
  {
    id: "palette",
    title: "The blocks",
    text: "The dock along the bottom holds every block, sorted into sections. Open a section, then drag a block onto the canvas, or click it to add it near the middle of the view.",
    target: "#dock",
  },
  {
    id: "add-value",
    title: "Add a value",
    text: "Add a value block. It holds a number or some text.",
    target: '.dock-sec[data-cat="values"]',
    check: (f) => f.graph.nodes.some(isKind("literal")),
    hint: "Open Values, then click value.",
  },
  {
    id: "select",
    title: "Select and move",
    text: "Click a block to select it. Its settings open in the panel on the right. Drag a block by its title bar to move it.",
    target: ".node",
    check: (f) => f.selected,
    hint: "Click a block on the canvas.",
  },
  {
    id: "ports",
    title: "Ports",
    text: "The small shapes on a block's edges are ports. White diamonds are flow ports: they set the order the blocks run in. Round ports carry values, such as numbers and text.",
    target: ".node .port",
  },
  {
    id: "add-print",
    title: "Add print",
    text: "Add a print block. It prints whatever value it receives.",
    target: '.dock-sec[data-cat="io"]',
    check: (f) => f.graph.nodes.some(isKind("print")),
    hint: "Open Input / output, then click print.",
  },
  {
    id: "flow-wire",
    title: "Wire the flow",
    text: "Drag from the white diamond on func main's body port to the input diamond on print. The program runs main, then print.",
    target: '.port[data-port="body"][data-dir="out"]',
    check: (f) => wired(f.graph, FLOW_OUT, { node: isKind("print"), port: "in" }),
    hint: "Drag from main's body port to print's input.",
  },
  {
    id: "value-wire",
    title: "Wire the value",
    text: "Drag from the round port on the value block to the value port on print. Then select the value block, set its type to string and type Hello, GNODE into its value box.",
    target: '.node[data-kind="literal"] .port[data-dir="out"]',
    check: (f) => wired(f.graph, { node: textValue("Hello, GNODE"), port: "value" }, { node: isKind("print"), port: "value" }),
    hint: "Set a string value to Hello, GNODE and wire it into print.",
  },
  {
    id: "run",
    title: "Run it",
    text: "Press Run. GNODE writes the Go code, builds it with go run and shows what it printed on the Output tab.",
    target: "#btn-run",
    check: (f, entry) => f.runs > entry.runs,
    hint: "Press Run.",
  },
  {
    id: "code",
    title: "Read the code",
    text: "The Go code tab shows the code as you edit. The Problems tab lists anything that would stop the program building. Click a problem to jump to its block.",
    target: '[data-tab="code"]',
  },
  {
    id: "trace",
    title: "Trace the flow",
    text: "Trace flow highlights the blocks and wires that run from func main and dims the rest. Use it to check what your program really does.",
    target: "#btn-trace",
    check: (f) => f.tracing,
    hint: "Press Trace flow.",
  },
  {
    id: "save",
    title: "Save and undo",
    text: "Save writes the program into the graphs folder. Undo and Redo (Ctrl+Z and Ctrl+Y) step back and forward through your changes.",
    target: "#btn-save",
  },

  // Part 2: programming
  {
    id: "how-it-runs",
    title: "How a program runs",
    text: "A program starts at func main. Flow ports decide the order: each block runs, then the block its flow port leads to. Value ports hand a block its data, the way Go passes a value to a function.",
  },
  {
    id: "add-loop",
    title: "Repeat with for each",
    text: "for each runs the blocks on its body port a set number of times. Add one from the Flow section.",
    target: '.dock-sec[data-cat="flow"]',
    check: (f) => f.graph.nodes.some(isKind("loop")),
    hint: "Open Flow, then click for each.",
  },
  {
    id: "loop-flow",
    title: "Put print inside the loop",
    text: "A flow port leads to only one block, so the print has to move. Wire main's body to the loop's input, then wire the loop's body to print's input.",
    target: '.port[data-port="body"][data-dir="out"]',
    check: (f) =>
      wired(f.graph, FLOW_OUT, { node: isKind("loop"), port: "in" }) &&
      wired(f.graph, { node: isKind("loop"), port: "body" }, { node: isKind("print"), port: "in" }),
    hint: "Main's body to the loop, then the loop's body to print.",
  },
  {
    id: "count",
    title: "Set the count",
    text: "The count input says how many times to repeat. Add a value block set to the whole number 3 and wire it into count.",
    target: '.node[data-kind="literal"] .port[data-dir="out"]',
    check: (f) => wired(f.graph, { node: numberValue("3"), port: "value" }, { node: isKind("loop"), port: "count" }),
    hint: "A value block set to 3, wired into count.",
  },
  {
    id: "counter",
    title: "Read the counter",
    text: "The loop's counter is a variable called i. Add a variable block and set its name to i.",
    target: '.dock-sec[data-cat="values"]',
    check: (f) => f.graph.nodes.some(namedRef("i")),
    hint: "Add a variable block named i.",
  },
  {
    id: "add-one",
    title: "Add to the counter",
    text: "Add an operator and set it to +. Wire the variable into its left input and a value of 1 into its right. Then wire the operator's result into print's value port, in place of the old value.",
    target: '.dock-sec[data-cat="values"]',
    check: (f) =>
      wired(f.graph, { node: namedRef("i"), port: "value" }, { node: hasOperator("+"), port: "a" }) &&
      wired(f.graph, { node: hasOperator("+"), port: "value" }, { node: isKind("print"), port: "value" }),
    hint: "Variable i into +, and + into print.",
  },
  {
    id: "run-count",
    title: "Run the loop",
    text: "Press Run again. You should see 1, 2 and 3 on separate lines.",
    target: "#btn-run",
    check: (f, entry) => f.runs > entry.runs,
    hint: "Press Run.",
  },
  {
    id: "practice",
    title: "Practice rooms",
    text: "Practice rooms are short programs with a goal. Each one tells you what to print, and Check runs your program against it. Try Count to three next: it is the loop you just built.",
    target: '[data-tab="practice"]',
  },
  {
    id: "done",
    title: "That is the basics",
    text: "You have seen blocks, wires, flow, values, loops and variables. Functions (func and call) are covered in the Call a function practice room. Replay this tour any time from the Tour button.",
  },
];
