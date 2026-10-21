// The block catalog: every kind of node the editor offers, what ports it has,
// which parameters the inspector shows, and how it turns into Go code.
//
// This file runs in the browser (to draw the palette) and on the server (to
// generate code), so it must not import anything from Node or Bun.

import type { ParamValue } from "./graph";

export type PortKind = "exec" | "data";
export type DataType = "int" | "float64" | "string" | "bool" | "any";

export interface PortDef {
  id: string;
  label: string;
  kind: PortKind;
  type?: DataType;
  required?: boolean;
}

export interface ParamDef {
  id: string;
  label: string;
  type: "text" | "select";
  default: ParamValue;
  options?: string[];
  placeholder?: string;
}

export type Category = "program" | "flow" | "values" | "vars" | "io" | "calls";

// What an emitter may read while it works. Data values are Go expressions,
// chain() returns the Go statements that follow an exec output port, and
// text() / param() return the block's own settings.
export interface EmitIO {
  value(port: string): string | undefined;
  chain(port: string): string[];
  param(name: string): ParamValue;
  text(name: string): string;
}

export interface Emitted {
  // statements inside the current block (exec nodes)
  stmts?: string[];
  // the Go expression this node produces (data nodes)
  expr?: string;
  // top-level declarations (program roots such as func main)
  decl?: string[];
  imports?: string[];
}

export interface NodeDef {
  kind: string;
  title: string;
  category: Category;
  help: string;
  inputs: PortDef[];
  outputs: PortDef[];
  params: ParamDef[];
  root?: boolean;
  emit: (io: EmitIO) => Emitted;
}

export function indent(lines: string[]): string[] {
  return lines.map((l) => (l === "" ? l : "\t" + l));
}

function exec(id: string, label?: string): PortDef {
  return { id, label: label ?? id, kind: "exec" };
}

function data(id: string, type: DataType, required = false, label?: string): PortDef {
  return { id, label: label ?? id, kind: "data", type, required };
}

export const CATALOG: NodeDef[] = [
  {
    kind: "main",
    title: "func main",
    category: "program",
    help: "Entry point of the program. The blocks wired from its body run when the program starts.",
    inputs: [],
    outputs: [exec("body")],
    params: [],
    root: true,
    emit: (io) => ({ decl: ["func main() {", ...indent(io.chain("body")), "}"] }),
  },
  {
    kind: "func",
    title: "func",
    category: "program",
    help: "Declares a function other blocks can call. Its body runs whenever it is called.",
    inputs: [],
    outputs: [exec("body")],
    params: [
      { id: "name", label: "name", type: "text", default: "helper", placeholder: "helper" },
      { id: "signature", label: "parameters", type: "text", default: "", placeholder: "a int, b int" },
      { id: "result", label: "returns", type: "text", default: "", placeholder: "int" },
    ],
    root: true,
    emit: (io) => {
      const sig = io.text("signature").trim();
      const result = io.text("result").trim();
      const head = `func ${io.text("name")}(${sig})${result ? " " + result : ""} {`;
      return { decl: [head, ...indent(io.chain("body")), "}"] };
    },
  },
  {
    kind: "if",
    title: "if",
    category: "flow",
    help: "Runs the then blocks when the condition is true, otherwise the else blocks.",
    inputs: [exec("in"), data("cond", "bool", true, "condition")],
    outputs: [exec("then"), exec("else"), exec("next")],
    params: [],
    emit: (io) => {
      const cond = io.value("cond") ?? "false";
      const thenLines = io.chain("then");
      const elseLines = io.chain("else");
      const stmts = [`if ${cond} {`, ...indent(thenLines)];
      if (elseLines.length > 0) stmts.push("} else {", ...indent(elseLines));
      stmts.push("}");
      return { stmts };
    },
  },
  {
    kind: "loop",
    title: "for each",
    category: "flow",
    help: "Repeats the body count times. Read the counter with a variable block that uses the same name.",
    inputs: [exec("in"), data("count", "int", true)],
    outputs: [exec("body"), exec("next")],
    params: [{ id: "variable", label: "counter", type: "text", default: "i", placeholder: "i" }],
    emit: (io) => {
      const v = io.text("variable") || "i";
      const count = io.value("count") ?? "0";
      return { stmts: [`for ${v} := 0; ${v} < ${count}; ${v}++ {`, ...indent(io.chain("body")), "}"] };
    },
  },
  {
    kind: "return",
    title: "return",
    category: "flow",
    help: "Returns from the enclosing function, with a value when one is connected.",
    inputs: [exec("in"), data("value", "any")],
    outputs: [],
    params: [],
    emit: (io) => {
      const v = io.value("value");
      return { stmts: [v === undefined ? "return" : `return ${v}`] };
    },
  },
  {
    kind: "literal",
    title: "value",
    category: "values",
    help: "A constant: a whole number, a decimal, text or true/false.",
    inputs: [],
    outputs: [data("value", "any")],
    params: [
      { id: "type", label: "type", type: "select", default: "int", options: ["int", "float64", "string", "bool"] },
      { id: "value", label: "value", type: "text", default: "0" },
    ],
    emit: (io) => {
      const type = io.text("type");
      const raw = io.text("value");
      if (type === "string") return { expr: JSON.stringify(raw) };
      if (type === "bool") return { expr: raw === "true" ? "true" : "false" };
      const num = Number(raw);
      return { expr: Number.isFinite(num) ? String(num) : "0" };
    },
  },
  {
    kind: "ref",
    title: "variable",
    category: "values",
    help: "Reads a variable, or a loop counter, by name.",
    inputs: [],
    outputs: [data("value", "any")],
    params: [{ id: "name", label: "name", type: "text", default: "x", placeholder: "x" }],
    emit: (io) => ({ expr: io.text("name") }),
  },
  {
    kind: "binary",
    title: "operator",
    category: "values",
    help: "Combines two values with an operator, such as a + b or a < b.",
    inputs: [data("a", "any", true, "left"), data("b", "any", true, "right")],
    outputs: [data("value", "any")],
    params: [
      {
        id: "op",
        label: "operator",
        type: "select",
        default: "+",
        options: ["+", "-", "*", "/", "%", "==", "!=", "<", "<=", ">", ">=", "&&", "||"],
      },
    ],
    emit: (io) => ({ expr: `(${io.value("a") ?? "0"} ${io.text("op")} ${io.value("b") ?? "0"})` }),
  },
  {
    kind: "callValue",
    title: "call (value)",
    category: "calls",
    help: "Calls a function and gives back its result. Arguments are Go source, separated by commas.",
    inputs: [],
    outputs: [data("value", "any")],
    params: [
      { id: "name", label: "function", type: "text", default: "helper", placeholder: "helper" },
      { id: "args", label: "arguments", type: "text", default: "", placeholder: '"GNODE"' },
    ],
    emit: (io) => ({ expr: `${io.text("name")}(${io.text("args")})` }),
  },
  {
    kind: "declare",
    title: "declare variable",
    category: "vars",
    help: "Creates a new variable with a value, written as name := value.",
    inputs: [exec("in"), data("value", "any", true)],
    outputs: [exec("next")],
    params: [{ id: "name", label: "name", type: "text", default: "x", placeholder: "x" }],
    emit: (io) => ({ stmts: [`${io.text("name")} := ${io.value("value") ?? "nil"}`] }),
  },
  {
    kind: "assign",
    title: "set variable",
    category: "vars",
    help: "Changes an existing variable, written as name = value.",
    inputs: [exec("in"), data("value", "any", true)],
    outputs: [exec("next")],
    params: [{ id: "name", label: "name", type: "text", default: "x", placeholder: "x" }],
    emit: (io) => ({ stmts: [`${io.text("name")} = ${io.value("value") ?? "nil"}`] }),
  },
  {
    kind: "print",
    title: "print",
    category: "io",
    help: "Prints a value on its own line with fmt.Println.",
    inputs: [exec("in"), data("value", "any", true)],
    outputs: [exec("next")],
    params: [],
    emit: (io) => ({ stmts: [`fmt.Println(${io.value("value") ?? '""'})`], imports: ["fmt"] }),
  },
  {
    kind: "callStmt",
    title: "call",
    category: "calls",
    help: "Calls a function for its effect and ignores any result.",
    inputs: [exec("in")],
    outputs: [exec("next")],
    params: [
      { id: "name", label: "function", type: "text", default: "helper", placeholder: "helper" },
      { id: "args", label: "arguments", type: "text", default: "", placeholder: '"GNODE"' },
    ],
    emit: (io) => ({ stmts: [`${io.text("name")}(${io.text("args")})`] }),
  },
];

export const CATALOG_BY_KIND = new Map<string, NodeDef>(CATALOG.map((d) => [d.kind, d]));
