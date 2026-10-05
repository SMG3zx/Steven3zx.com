export type EntityHandle = bigint;
export type TypedArrayConstructor =
  | Float64ArrayConstructor
  | Float32ArrayConstructor
  | Uint32ArrayConstructor
  | Int32ArrayConstructor
  | Uint16ArrayConstructor
  | Int16ArrayConstructor
  | Uint8ArrayConstructor
  | Int8ArrayConstructor;

export interface Component {
  readonly id: number;
  readonly name: string;
  readonly bytesPerEntity: number;
}

export interface Archetype {
  readonly id: number;
  readonly components: readonly Component[];
  readonly chunkCapacity: number;
  has(component: Component): boolean;
}

export interface ChangeSet {
  tick: number;
  created: EntityHandle[];
  updated: Array<{ handle: EntityHandle; component: number }>;
  structural: EntityHandle[];
  destroyed: Array<{ handle: EntityHandle; externalId?: string | number; archetype: number }>;
  dirtyBatches: Array<{
    archetype: number;
    chunk: number;
    count: number;
    components: number[];
  }>;
}

export interface FrameResult {
  tick: number;
  dt: number;
  changes: ChangeSet;
  events: Record<
    string,
    {
      count: number;
      columns: Record<string, ArrayBufferView>;
      stats: Record<string, number | string>;
    }
  >;
  stats: Record<string, unknown>;
}

export interface WorldOptions {
  entityCapacity?: number;
  chunkBytes?: number;
  executionBatchSize?: number;
  migrationStrategy?: 'individual' | 'grouped' | 'columnar';
  frameBudgetMs?: number;
  schemaVersion?: string | number;
  checksum?: boolean;
  development?: boolean;
  onFrameBudgetExceeded?: (stats: Record<string, unknown>, world: HermesWorld) => void;
}

export class TypedTable {
  constructor(
    schema: Record<string, TypedArrayConstructor>,
    capacity?: number,
    options?: { overflow?: 'grow' | 'reject' | 'drop-oldest' },
  );
  readonly columns: Record<string, ArrayBufferView>;
  readonly count: number;
  push(values: Record<string, number>): number;
  clear(): void;
  stats(): Record<string, number | string>;
}

export class HermesWorld {
  constructor(options?: WorldOptions);
  readonly tickNumber: number;
  component(name: string, schema: Record<string, TypedArrayConstructor>): Component;
  archetype(...components: Component[]): Archetype;
  query(...components: Component[]): unknown;
  compileQuery(...components: Component[]): unknown;
  spawn(archetype: Archetype, initial?: Record<string, unknown> | null): EntityHandle;
  spawnMany(
    archetype: Archetype,
    rows: Array<Record<string, unknown> | null>,
    externalIds?: Array<string | number> | null,
  ): EntityHandle[];
  destroy(entity: EntityHandle): boolean;
  destroyMany(entities: EntityHandle[]): number;
  add(entity: EntityHandle, component: Component, initial?: Record<string, number> | null): boolean;
  addMany(
    entities: EntityHandle[],
    component: Component,
    initials?: Array<Record<string, number> | null> | null,
  ): number;
  remove(entity: EntityHandle, component: Component): boolean;
  removeMany(entities: EntityHandle[], component: Component): number;
  updateMany(
    entities: EntityHandle[],
    component: Component,
    values: Record<string, number> | Array<Record<string, number>>,
  ): number;
  bindExternal(externalId: string | number, handle: EntityHandle): EntityHandle;
  resolveExternal(externalId: string | number): EntityHandle | null;
  ingestSnapshot(snapshot: unknown, options?: Record<string, unknown>): Record<string, unknown>;
  applyUpdates(updates: unknown[], options?: Record<string, unknown>): Record<string, number>;
  transaction(callback: (transaction: Record<string, Function>) => unknown): unknown;
  command(
    name: string,
    schema: Record<string, TypedArrayConstructor>,
    capacity?: number,
    options?: { overflow?: 'grow' | 'reject' | 'drop-oldest' },
  ): TypedTable;
  event(
    name: string,
    schema: Record<string, TypedArrayConstructor>,
    capacity?: number,
    options?: { overflow?: 'grow' | 'reject' | 'drop-oldest' },
  ): TypedTable;
  system(specification: Record<string, unknown>): unknown;
  step(dt?: number): FrameResult;
  tick(dt?: number): FrameResult;
  filterChanges(
    changes: ChangeSet,
    options?: { query?: unknown; components?: Array<Component | number> },
  ): ChangeSet;
  checksum(): string;
  startRecording(clear?: boolean): this;
  stopRecording(): unknown[];
  input(
    type: string,
    payload: unknown,
    handler: (payload: unknown, world: HermesWorld) => void,
  ): unknown;
  replay(log: unknown[], handlers: Record<string, Function> | Function, dt?: number): unknown;
  stats(): Record<string, unknown>;
}

export const Hermes: {
  version: '0.4.0';
  World: typeof HermesWorld;
  Phase: Record<string, string>;
  Types: Record<string, TypedArrayConstructor>;
  EMPTY: number;
};
