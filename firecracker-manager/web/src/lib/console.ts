// Serial console output is raw terminal text. Strip ANSI escape sequences so it
// reads cleanly in a plain <pre>.
const ANSI_PATTERN = /\x1b\[[0-9;?]*[A-Za-z]/g;

export function stripAnsi(text: string): string {
  return text.replace(ANSI_PATTERN, "");
}

// Keep the browser buffer bounded; the server replays only the tail on connect.
export const MAX_CONSOLE_CHARS = 200_000;

export function appendConsole(buffer: string, chunk: string): string {
  const next = buffer + chunk;
  return next.length > MAX_CONSOLE_CHARS ? next.slice(next.length - MAX_CONSOLE_CHARS) : next;
}
