// Undo and redo for the editor. The editor records a copy of the graph from
// before each change; undo swaps it back in and keeps the current state so
// redo can return to it. Nothing here knows about blocks or wires.

export class History<T> {
  private past: T[] = [];
  private future: T[] = [];
  private lastKey: string | undefined;
  private lastAt = 0;

  constructor(
    private readonly limit = 200,
    private readonly coalesceMs = 1000,
    private readonly now: () => number = Date.now,
  ) {}

  get canUndo(): boolean {
    return this.past.length > 0;
  }

  get canRedo(): boolean {
    return this.future.length > 0;
  }

  // Call with the state as it was before a change. Edits that share a key
  // (typing into one field, for example) made within coalesceMs of each other
  // count as one step.
  record(before: T, key?: string) {
    const t = this.now();
    if (key !== undefined && key === this.lastKey && t - this.lastAt < this.coalesceMs) {
      this.lastAt = t;
      return;
    }
    this.past.push(before);
    if (this.past.length > this.limit) this.past.shift();
    this.future = [];
    this.lastKey = key;
    this.lastAt = t;
  }

  // Returns the state to go back to, or undefined when there is nothing to undo.
  // `current` is the state being left, so redo can return to it.
  undo(current: T): T | undefined {
    const prev = this.past.pop();
    if (prev === undefined) return undefined;
    this.future.push(current);
    this.lastKey = undefined;
    return prev;
  }

  redo(current: T): T | undefined {
    const next = this.future.pop();
    if (next === undefined) return undefined;
    this.past.push(current);
    this.lastKey = undefined;
    return next;
  }

  clear() {
    this.past = [];
    this.future = [];
    this.lastKey = undefined;
  }
}
