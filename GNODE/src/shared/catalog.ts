// The block catalog: every kind of node the editor offers, what ports it has,
// which parameters the inspector shows, and how it turns into Go code.
//
// This file runs in the browser (to draw the palette) and on the server (to
// generate code), so it must not import anything from Node or Bun.

import type { ParamValue } from "./graph";
import { TRASH_HELPERS } from "./trash";

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

export type Category = "program" | "flow" | "values" | "vars" | "lists" | "io" | "calls" | "sites";

// What an emitter may read while it works. Data values are Go expressions,
// chain() returns the Go statements that follow an exec output port, and
// text() / param() return the block's own settings. inLoop() says whether the
// block being emitted sits inside the body of a loop.
export interface EmitIO {
  value(port: string): string | undefined;
  chain(port: string): string[];
  param(name: string): ParamValue;
  text(name: string): string;
  inLoop(): boolean;
}

export interface Emitted {
  // statements inside the current block (exec nodes)
  stmts?: string[];
  // the Go expression this node produces (data nodes)
  expr?: string;
  // top-level declarations (program roots such as func main)
  decl?: string[];
  imports?: string[];
  // Something the program cannot do even though the blocks are wired. It is
  // reported as a problem on the block.
  problem?: string;
  // Code the program needs once, however many blocks use it. Each name is
  // written out one time, after the functions.
  helpers?: { name: string; lines: string[] }[];
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
  // The body port of this block runs once per pass of a loop, so blocks in
  // it are inside a loop (break and continue need that).
  loopBody?: boolean;
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
    loopBody: true,
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

  // ---- more flow ----
  {
    kind: "while",
    title: "repeat while",
    category: "flow",
    help: "Repeats its body for as long as the condition is true. Go has only one loop, written as for condition { }; this block is that loop.",
    inputs: [exec("in"), data("cond", "bool", true, "condition")],
    outputs: [exec("body"), exec("next")],
    params: [],
    loopBody: true,
    emit: (io) => ({ stmts: [`for ${io.value("cond") ?? "false"} {`, ...indent(io.chain("body")), "}"] }),
  },
  {
    kind: "range",
    title: "for each in list",
    category: "flow",
    help: "Runs its body once for every item in a list. The item is read with a variable block that uses the name set here.",
    inputs: [exec("in"), data("list", "any", true)],
    outputs: [exec("body"), exec("next")],
    params: [{ id: "item", label: "item name", type: "text", default: "item", placeholder: "item" }],
    loopBody: true,
    emit: (io) => {
      const name = io.text("item").trim() || "item";
      return { stmts: [`for _, ${name} := range ${io.value("list") ?? "nil"} {`, ...indent(io.chain("body")), "}"] };
    },
  },
  {
    kind: "break",
    title: "break",
    category: "flow",
    help: "Leaves the loop it is inside, and carries on after the loop.",
    inputs: [exec("in")],
    outputs: [],
    params: [],
    emit: (io) => (io.inLoop() ? { stmts: ["break"] } : { stmts: ["break"], problem: "break can only be used inside a loop" }),
  },
  {
    kind: "continue",
    title: "continue",
    category: "flow",
    help: "Skips the rest of this pass of the loop it is inside, and starts the next pass.",
    inputs: [exec("in")],
    outputs: [],
    params: [],
    emit: (io) =>
      io.inLoop() ? { stmts: ["continue"] } : { stmts: ["continue"], problem: "continue can only be used inside a loop" },
  },

  // ---- variables and constants ----
  {
    kind: "declareTyped",
    title: "declare typed variable",
    category: "vars",
    help: "Creates a variable of a chosen type, written as var name type. Connect a value to set it at the start.",
    inputs: [exec("in"), data("value", "any")],
    outputs: [exec("next")],
    params: [
      { id: "name", label: "name", type: "text", default: "x", placeholder: "x" },
      { id: "type", label: "type", type: "select", default: "int", options: ["int", "float64", "string", "bool"] },
    ],
    emit: (io) => {
      const head = `var ${io.text("name")} ${io.text("type")}`;
      const v = io.value("value");
      return { stmts: [v === undefined ? head : `${head} = ${v}`] };
    },
  },
  {
    kind: "constant",
    title: "constant",
    category: "vars",
    help: "Names a fixed value, written as const name = value. It cannot be changed later.",
    inputs: [exec("in"), data("value", "any", true)],
    outputs: [exec("next")],
    params: [{ id: "name", label: "name", type: "text", default: "limit", placeholder: "limit" }],
    emit: (io) => ({ stmts: [`const ${io.text("name")} = ${io.value("value") ?? "0"}`] }),
  },

  // ---- lists ----
  {
    kind: "list",
    title: "list of",
    category: "lists",
    help: "A list (a slice) of values, written as []type{values}. Separate the values with commas.",
    inputs: [],
    outputs: [data("value", "any")],
    params: [
      { id: "type", label: "type", type: "select", default: "int", options: ["int", "float64", "string", "bool"] },
      { id: "values", label: "values", type: "text", default: "1, 2, 3", placeholder: '1, 2, 3 or "a", "b"' },
    ],
    emit: (io) => ({ expr: `[]${io.text("type")}{${io.text("values")}}` }),
  },
  {
    kind: "len",
    title: "length",
    category: "lists",
    help: "The number of items in a list, or of characters in text, written as len(x).",
    inputs: [data("of", "any", true)],
    outputs: [data("value", "any")],
    params: [],
    emit: (io) => ({ expr: `len(${io.value("of") ?? "nil"})` }),
  },
  {
    kind: "index",
    title: "item at",
    category: "lists",
    help: "Reads the item at a position in a list. Positions start at 0.",
    inputs: [data("list", "any", true), data("position", "int", true)],
    outputs: [data("value", "any")],
    params: [],
    emit: (io) => ({ expr: `${io.value("list") ?? "nil"}[${io.value("position") ?? "0"}]` }),
  },
  {
    kind: "setItem",
    title: "set item",
    category: "lists",
    help: "Changes the item at a position in a list variable, written as name[position] = value.",
    inputs: [exec("in"), data("position", "int", true), data("value", "any", true)],
    outputs: [exec("next")],
    params: [{ id: "name", label: "list name", type: "text", default: "xs", placeholder: "xs" }],
    emit: (io) => ({
      stmts: [`${io.text("name")}[${io.value("position") ?? "0"}] = ${io.value("value") ?? "nil"}`],
    }),
  },
  {
    kind: "append",
    title: "add to list",
    category: "lists",
    help: "Adds a value to the end of a list variable, written as name = append(name, value).",
    inputs: [exec("in"), data("value", "any", true)],
    outputs: [exec("next")],
    params: [{ id: "name", label: "list name", type: "text", default: "xs", placeholder: "xs" }],
    emit: (io) => {
      const name = io.text("name");
      return { stmts: [`${name} = append(${name}, ${io.value("value") ?? "nil"})`] };
    },
  },

  // ---- values ----
  {
    kind: "unary",
    title: "not or minus",
    category: "values",
    help: "Flips true and false with !, or negates a number with -.",
    inputs: [data("value", "any", true)],
    outputs: [data("value", "any")],
    params: [{ id: "op", label: "operator", type: "select", default: "!", options: ["!", "-"] }],
    emit: (io) => ({ expr: `(${io.text("op")}${io.value("value") ?? "0"})` }),
  },
  {
    kind: "convert",
    title: "convert to",
    category: "values",
    help: "Changes a number to another number type, written as float64(x) or int(x). Converting to int drops the decimal part.",
    inputs: [data("value", "any", true)],
    outputs: [data("value", "any")],
    params: [{ id: "type", label: "type", type: "select", default: "float64", options: ["int", "float64"] }],
    emit: (io) => ({ expr: `${io.text("type")}(${io.value("value") ?? "0"})` }),
  },
  {
    kind: "pkgCall",
    title: "call library",
    category: "calls",
    help: "Calls a function from the standard library, such as strings.ToUpper or math.Sqrt. The package is imported for you.",
    inputs: [],
    outputs: [data("value", "any")],
    params: [
      { id: "pkg", label: "package", type: "select", default: "strings", options: ["strings", "strconv", "math"] },
      { id: "fn", label: "function", type: "text", default: "ToUpper", placeholder: "ToUpper" },
      { id: "args", label: "arguments", type: "text", default: "", placeholder: '"gnode"' },
    ],
    emit: (io) => {
      const pkg = io.text("pkg");
      return { expr: `${pkg}.${io.text("fn")}(${io.text("args")})`, imports: [pkg] };
    },
  },

  // ---- printing text ----
  {
    kind: "printf",
    title: "print formatted",
    category: "io",
    help: "Prints text built from a format with fmt.Printf. Write the format in Go quotes and end it with \\n for a new line.",
    inputs: [exec("in")],
    outputs: [exec("next")],
    params: [
      { id: "format", label: "format", type: "text", default: '"%v\\n"', placeholder: '"%v\\n"' },
      { id: "args", label: "values", type: "text", default: "", placeholder: "total, name" },
    ],
    emit: (io) => {
      const args = io.text("args").trim();
      return { stmts: [`fmt.Printf(${io.text("format")}${args ? ", " + args : ""})`], imports: ["fmt"] };
    },
  },
  {
    kind: "sprintf",
    title: "format text",
    category: "io",
    help: "Builds text from a format with fmt.Sprintf, such as \"%d items\" with a number. Use the result in print or set variable.",
    inputs: [],
    outputs: [data("value", "any")],
    params: [
      { id: "format", label: "format", type: "text", default: '"%v"', placeholder: '"%d items"' },
      { id: "args", label: "values", type: "text", default: "", placeholder: "count" },
    ],
    emit: (io) => {
      const args = io.text("args").trim();
      return { expr: `fmt.Sprintf(${io.text("format")}${args ? ", " + args : ""})`, imports: ["fmt"] };
    },
  },
  // ---- trash sites (after Earth ReGen's Trash Site and Trash Cleaner) ----
  {
    kind: "trashSite",
    title: "trash site",
    category: "sites",
    help: "A site that holds kilograms of trash and has slots for cleaners. Keep it in a variable, such as site := newSite(...), then stand cleaners on it.",
    inputs: [],
    outputs: [data("site", "any")],
    params: [
      { id: "name", label: "name", type: "text", default: "pit", placeholder: "pit" },
      { id: "reserve", label: "kg of trash", type: "text", default: "500", placeholder: "500" },
      { id: "slots", label: "slots", type: "select", default: "2", options: ["1", "2", "3"] },
    ],
    emit: (io) => ({
      expr: `newSite(${JSON.stringify(io.text("name"))}, ${Number(io.text("reserve")) || 0}, ${Number(io.text("slots")) || 1})`,
      helpers: [{ name: "trash", lines: TRASH_HELPERS }],
    }),
  },
  {
    kind: "trashCleaner",
    title: "trash cleaner",
    category: "sites",
    help: "A cleaner that stands on a site and digs trash out of it into a buffer. Its buffer holds the kg it is given, and a full site refuses another cleaner. Keep it in a variable.",
    inputs: [data("site", "any", true)],
    outputs: [data("cleaner", "any")],
    params: [{ id: "buffer", label: "buffer (kg)", type: "text", default: "5", placeholder: "5" }],
    emit: (io) => ({
      expr: `newCleaner(${io.value("site") ?? "nil"}, ${Number(io.text("buffer")) || 0})`,
      helpers: [{ name: "trash", lines: TRASH_HELPERS }],
    }),
  },
  {
    kind: "digTrash",
    title: "dig with cleaner",
    category: "sites",
    help: "Runs a cleaner for some hours with some workers. Each worker digs 1 kg an hour, as far as the buffer has room and the site has trash left.",
    inputs: [exec("in"), data("cleaner", "any", true), data("workers", "float64", true)],
    outputs: [exec("next")],
    params: [{ id: "hours", label: "hours", type: "text", default: "1", placeholder: "1" }],
    emit: (io) => ({
      stmts: [`${io.value("cleaner") ?? "nil"}.Dig(${io.value("workers") ?? "0"}, ${Number(io.text("hours")) || 0})`],
      helpers: [{ name: "trash", lines: TRASH_HELPERS }],
    }),
  },
  {
    kind: "unloadTrash",
    title: "take out trash",
    category: "sites",
    help: "Empties a cleaner's buffer and gives the kg of trash it held. The buffer is empty afterwards.",
    inputs: [data("cleaner", "any", true)],
    outputs: [data("kg", "any")],
    params: [],
    emit: (io) => ({
      expr: `${io.value("cleaner") ?? "nil"}.Unload()`,
      helpers: [{ name: "trash", lines: TRASH_HELPERS }],
    }),
  },
  {
    kind: "readCleaner",
    title: "read from cleaner",
    category: "sites",
    help: "Reads a number from a cleaner: the trash in its buffer, or the trash left in its site.",
    inputs: [data("cleaner", "any", true)],
    outputs: [data("value", "any")],
    params: [
      { id: "field", label: "reads", type: "select", default: "Buffer", options: ["Buffer", "Site.Reserve"] },
    ],
    emit: (io) => ({
      expr: `${io.value("cleaner") ?? "nil"}.${io.text("field")}`,
      helpers: [{ name: "trash", lines: TRASH_HELPERS }],
    }),
  },
];

export const CATALOG_BY_KIND = new Map<string, NodeDef>(CATALOG.map((d) => [d.kind, d]));
