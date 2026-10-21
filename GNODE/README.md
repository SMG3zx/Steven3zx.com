# GNODE

GNODE is a node editor for Go. You put blocks on a canvas, wire them together the way you would piece together a factory in a game, and GNODE writes the Go source for you. Run it from the editor to see the output.

## Requirements

- [Bun](https://bun.sh) 1.4 or newer
- [Go](https://go.dev) on your `PATH` (for Run). `gofmt` is used to format the generated code when it is available.

## Getting started

```bash
bun install
bun run dev
```

Then open http://127.0.0.1:4830. The server only listens on `127.0.0.1`, because Run executes the generated program on your machine.

Set `PORT` to use another port: `PORT=5000 bun run dev`.

## Using the editor

- **Add a block:** drag it from the palette onto the canvas, or click it to add it near the middle of the view.
- **Move a block:** drag its title bar.
- **Wire blocks:** drag from one port to another port of the opposite side. White diamonds are *flow* ports, which decide the order blocks run in. Round ports carry *values*, such as numbers or text. A flow port can lead to one block; an input takes one source.
- **Edit a block:** select it and change its settings in the right-hand panel.
- **Delete:** select a block or wire and press Delete, or use the Delete button in the panel.
- **Save and open:** programs are saved as JSON in `graphs/`. Pick one in the list and press Open.
- **Read the code:** the bottom panel shows the Go source as you edit it. The Problems tab lists anything that would stop the program from building. Click a problem to jump to its block.
- **Run:** press Run to generate, build and run the program with `go run`. The Output tab shows what it printed.
- **Trace flow:** the Trace flow button highlights the blocks and wires the program runs from func main, and dims the rest. Blocks that are not part of the program are already dimmed.
- **Find a block:** Ctrl+F focuses the Find box. It matches block names and settings. Enter goes to the next match, Shift+Enter to the previous one, and Escape clears it.
- **Practice rooms:** the Practice tab in the bottom panel has short programs with a goal. Press Start to load the starting blocks, use Hint if you are stuck, and press Check to run the program and compare its output with the goal. Finished rooms are marked in this browser only. The rooms are in `src/shared/lessons.ts`.
- **Select several blocks:** drag on empty canvas to select the blocks the box touches, or Ctrl+A for all of them. Shift-click adds a block to the selection or takes it out. Dragging one selected block moves them all, and Delete removes them all.
- **Copy and paste:** Ctrl+C copies the selected blocks and the wires between them. Ctrl+V pastes them, and Ctrl+D duplicates them in one step.
- **Undo and redo:** Ctrl+Z (Cmd+Z on a Mac) undoes the last change, and Ctrl+Shift+Z or Ctrl+Y redoes it. The Undo and Redo buttons do the same. Typing in a setting counts as one change. Opening or starting a program can be undone too, but the first program you open when the editor starts cannot.

## How it works

A program starts at a `func main` block and follows the flow ports from block to block. Value blocks (numbers, variables, operators, function calls) produce expressions, which are inlined into the statements that use them.

The code in `src/` is split into four parts:

| Path | What it does |
| --- | --- |
| `src/shared/catalog.ts` | Every block kind: its ports, settings and the Go it produces. Used by both the editor and the generator. |
| `src/shared/graph.ts` | The saved graph format: blocks, their positions and wires. |
| `src/codegen/generate.ts` | Turns a graph into Go source and reports problems. |
| `src/server/` | The local server: serves the editor, generates code, saves graphs, runs programs. |
| `src/web/` | The editor in the browser: canvas, inspector and bottom panel. |

### Blocks available now

| Block | Category | Produces |
| --- | --- | --- |
| func main | Program | `func main() { ... }` |
| func | Program | a function you can call, with parameters and a return type |
| if | Flow | `if ... { } else { }` |
| for each | Flow | `for i := 0; i < n; i++ { }` |
| return | Flow | `return value` |
| value | Values | a number, text or `true`/`false` |
| variable | Values | reads a variable or loop counter |
| operator | Values | `(a + b)`, comparisons, `&&`, `||` and so on |
| call (value) | Calls | a function call that produces a value |
| declare variable | Variables | `name := value` |
| set variable | Variables | `name = value` |
| print | Input / output | `fmt.Println(value)` |
| call | Calls | a function call for its effect |

### Adding a block

Add an entry to `CATALOG` in `src/shared/catalog.ts` with its title, ports, settings and an `emit` function that returns the Go for it. The editor picks it up automatically. Add a test in `test/codegen.test.ts` if the block has tricky output.

## Tests

```bash
bun test
bun run typecheck
```

## Limits

- Types are not checked by GNODE. Connect an `int` to a `string` operator and the Go compiler reports the error, shown in the output.
- Function parameters and return types are typed as text, for example `a int, b int`.
- Undo keeps the last 200 changes. It is not saved with the program, so it is gone when you reload the page.
- `generated/` holds the programs Run writes. It is ignored by git.
