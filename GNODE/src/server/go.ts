// Calls the Go toolchain that is already installed on this machine.
// Nothing here runs unless the user asks for it from the editor.

import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

export const ROOT = resolve(import.meta.dir, "..", "..");
export const GENERATED_DIR = join(ROOT, "generated");

export interface FormatResult {
  source: string;
  formatted: boolean;
  error?: string;
}

// Runs gofmt on the source. When gofmt is missing or rejects the code, the
// unformatted source is returned with the reason, so the editor can still show it.
export async function formatSource(source: string): Promise<FormatResult> {
  try {
    const proc = Bun.spawn(["gofmt"], { stdin: new Blob([source]), stdout: "pipe", stderr: "pipe" });
    const [stdout, stderr, code] = await Promise.all([
      new Response(proc.stdout).text(),
      new Response(proc.stderr).text(),
      proc.exited,
    ]);
    if (code !== 0) return { source, formatted: false, error: stderr.trim() || `gofmt exited ${code}` };
    return { source: stdout, formatted: true };
  } catch (err) {
    return { source, formatted: false, error: `gofmt not available: ${(err as Error).message}` };
  }
}

export interface RunResult {
  ok: boolean;
  output: string;
  error?: string;
}

// Writes the generated program into generated/<folder> and runs it with go run.
export async function runProgram(folder: string, source: string, timeoutMs = 20_000): Promise<RunResult> {
  const dir = join(GENERATED_DIR, folder);
  await mkdir(dir, { recursive: true });
  await writeFile(join(dir, "go.mod"), `module gnode/${folder}\n\ngo 1.22\n`);
  await writeFile(join(dir, "main.go"), source);

  const proc = Bun.spawn(["go", "run", "."], { cwd: dir, stdout: "pipe", stderr: "pipe" });
  const timer = setTimeout(() => proc.kill(), timeoutMs);
  try {
    const [stdout, stderr, code] = await Promise.all([
      new Response(proc.stdout).text(),
      new Response(proc.stderr).text(),
      proc.exited,
    ]);
    const output = stdout + (stderr ? (stdout ? "\n" : "") + stderr : "");
    if (code === 0) return { ok: true, output };
    return { ok: false, output, error: `go run exited with ${code}` };
  } catch (err) {
    return { ok: false, output: "", error: `could not run go: ${(err as Error).message}` };
  } finally {
    clearTimeout(timer);
  }
}
