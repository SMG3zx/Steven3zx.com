'use strict';

/*
 * HERMES v0.4
 * High-performance single-file ECS/world runtime for Node.js.
 *
 * Run demo + benchmark:
 *   node hermes.js
 *   node hermes.js 1000000 120
 *
 * Import as a module:
 *   const { Hermes } = require('./hermes');
 *
 * Goals:
 *   - TypedArray column storage
 *   - Archetype chunks
 *   - Generational entity IDs
 *   - Cached query plans
 *   - Batch system execution
 *   - Typed command tables
 *   - Deferred structural commit
 *   - Event tables
 *   - No per-entity allocation in hot loops
 */

const TYPE_INFO = new Map([
  [Float64Array, { bytes: 8, name: 'f64' }],
  [Float32Array, { bytes: 4, name: 'f32' }],
  [Uint32Array, { bytes: 4, name: 'u32' }],
  [Int32Array, { bytes: 4, name: 'i32' }],
  [Uint16Array, { bytes: 2, name: 'u16' }],
  [Int16Array, { bytes: 2, name: 'i16' }],
  [Uint8Array, { bytes: 1, name: 'u8' }],
  [Int8Array, { bytes: 1, name: 'i8' }],
]);

const EMPTY = 0xffffffff;
const Phase = Object.freeze({
  INPUT: 'INPUT',
  PRE_UPDATE: 'PRE_UPDATE',
  UPDATE: 'UPDATE',
  POST_UPDATE: 'POST_UPDATE',
  COMMANDS: 'COMMANDS',
  STRUCTURAL: 'STRUCTURAL',
  EVENTS: 'EVENTS',
  COMMIT: 'COMMIT',
});
const PHASE_ORDER = Object.values(Phase);

function assert(cond, msg) {
  if (!cond) throw new Error(`Hermes: ${msg}`);
}

function nowNs() {
  if (typeof process !== 'undefined' && process.hrtime?.bigint) return process.hrtime.bigint();
  return BigInt(Math.round((globalThis.performance?.now?.() ?? Date.now()) * 1e6));
}

function memoryUsage() {
  if (typeof process !== 'undefined' && process.memoryUsage) return process.memoryUsage();
  return { arrayBuffers: 0, heapUsed: 0, rss: 0 };
}
function fmt(n, d = 2) {
  return Number(n).toLocaleString('en-US', { maximumFractionDigits: d });
}

function pairKey(a, b) {
  const sum = a + b;
  return (sum * (sum + 1)) / 2 + b;
}

function createChangeSet(tick) {
  return {
    tick,
    created: [],
    updated: [],
    structural: [],
    destroyed: [],
    dirtyBatches: [],
  };
}

class Component {
  constructor(id, name, schema) {
    this.id = id;
    this.name = name;
    this.fields = [];
    this.bytesPerEntity = 0;

    for (const [field, Type] of Object.entries(schema)) {
      const info = TYPE_INFO.get(Type);
      assert(info, `unsupported TypedArray for ${name}.${field}`);
      this.fields.push({ name: field, Type, bytes: info.bytes });
      this.bytesPerEntity += info.bytes;
    }
  }
}

class Chunk {
  constructor(archetype, capacity) {
    this.archetype = archetype;
    this.capacity = capacity;
    this.count = 0;
    this.entities = new Uint32Array(capacity);
    this.columns = new Array(archetype.components.length);

    for (let ci = 0; ci < archetype.components.length; ci++) {
      const c = archetype.components[ci];
      const cols = Object.create(null);
      for (const f of c.fields) cols[f.name] = new f.Type(capacity);
      this.columns[ci] = cols;
    }
  }
}

class Archetype {
  constructor(world, id, components) {
    this.world = world;
    this.id = id;
    this.components = [...components].sort((a, b) => a.id - b.id);
    this.componentIds = new Uint32Array(this.components.map((c) => c.id));
    this.componentIndex = new Map();
    this.chunks = [];
    this.bytesPerEntity = 4;

    for (let i = 0; i < this.components.length; i++) {
      this.componentIndex.set(this.components[i].id, i);
      this.bytesPerEntity += this.components[i].bytesPerEntity;
    }

    this.chunkCapacity = Math.max(16, Math.floor(world.chunkBytes / this.bytesPerEntity));
  }

  has(component) {
    return this.componentIndex.has(component.id);
  }

  newChunk() {
    const c = new Chunk(this, this.chunkCapacity);
    c.index = this.chunks.length;
    this.chunks.push(c);
    this.world.topologyVersion++;
    return c;
  }

  acquireRow(out = null) {
    let chunk = this.chunks[this.chunks.length - 1];
    if (!chunk || chunk.count >= chunk.capacity) chunk = this.newChunk();
    const row = chunk.count++;
    if (out) {
      out.chunk = chunk;
      out.row = row;
      return out;
    }
    return { chunk, row };
  }
}

class EntityStore {
  constructor(capacity) {
    this.capacity = capacity;
    this.next = 0;
    this.generations = new Uint32Array(capacity);
    this.alive = new Uint8Array(capacity);
    this.archetype = new Uint32Array(capacity);
    this.archetype.fill(EMPTY);
    this.chunk = new Uint32Array(capacity);
    this.chunk.fill(EMPTY);
    this.row = new Uint32Array(capacity);
    this.row.fill(EMPTY);
    this.free = new Uint32Array(capacity);
    this.freeCount = 0;
  }

  create() {
    let id;
    if (this.freeCount) id = this.free[--this.freeCount];
    else {
      assert(this.next < this.capacity, 'entity capacity exhausted');
      id = this.next++;
    }
    this.alive[id] = 1;
    return id;
  }

  destroy(id) {
    if (!this.alive[id]) return;
    this.alive[id] = 0;
    this.generations[id]++;
    this.archetype[id] = this.chunk[id] = this.row[id] = EMPTY;
    this.free[this.freeCount++] = id;
  }

  handle(id) {
    return (BigInt(this.generations[id]) << 32n) | BigInt(id >>> 0);
  }

  resolve(handle) {
    const id = Number(handle & 0xffffffffn) >>> 0;
    const gen = Number((handle >> 32n) & 0xffffffffn) >>> 0;
    return id < this.capacity && this.alive[id] && this.generations[id] === gen ? id : EMPTY;
  }
}

class TypedTable {
  constructor(schema, capacity = 1024, options = {}) {
    this.schema = schema;
    this.capacity = capacity;
    this.count = 0;
    this.overflow = options.overflow ?? 'grow';
    assert(
      ['grow', 'reject', 'drop-oldest'].includes(this.overflow),
      `invalid overflow policy ${this.overflow}`,
    );
    this.rejected = 0;
    this.dropped = 0;
    this.strict = options.strict ?? false;
    this.label = options.label ?? 'typed table';
    this.columns = Object.create(null);
    for (const [name, Type] of Object.entries(schema)) {
      assert(TYPE_INFO.has(Type), `unsupported table type ${name}`);
      this.columns[name] = new Type(capacity);
    }
  }

  ensure(n) {
    if (n <= this.capacity) return true;
    if (this.overflow !== 'grow') return false;
    let cap = this.capacity;
    while (cap < n) cap *= 2;
    for (const [name, Type] of Object.entries(this.schema)) {
      const next = new Type(cap);
      next.set(this.columns[name]);
      this.columns[name] = next;
    }
    this.capacity = cap;
    return true;
  }

  push(values) {
    if (this.strict) {
      assert(values && typeof values === 'object', `${this.label} row must be an object`);
      for (const name of Object.keys(this.schema)) {
        assert(values[name] !== undefined, `${this.label} row is missing ${name}`);
        assert(Number.isFinite(values[name]), `${this.label}.${name} must be finite`);
      }
      for (const name of Object.keys(values))
        assert(this.schema[name], `${this.label} row has unknown field ${name}`);
    }
    const i = this.allocate();
    if (i < 0) return i;
    for (const name of Object.keys(this.schema)) this.columns[name][i] = values[name] ?? 0;
    return i;
  }

  allocate() {
    let i = this.count;
    if (!this.ensure(i + 1)) {
      if (this.overflow === 'reject') {
        this.rejected++;
        return -1;
      }
      for (const name of Object.keys(this.schema)) this.columns[name].copyWithin(0, 1, i);
      this.dropped++;
      i--;
    }
    this.count = i + 1;
    return i;
  }

  stats() {
    return {
      count: this.count,
      capacity: this.capacity,
      overflow: this.overflow,
      rejected: this.rejected,
      dropped: this.dropped,
    };
  }

  clear() {
    this.count = 0;
  }
}

class QueryPlan {
  constructor(world, components) {
    this.world = world;
    this.components = components;
    this.version = -1;
    this.matches = [];
    this.batches = [];
    this.executionBatches = [];
  }

  refresh() {
    if (this.version === this.world.topologyVersion) return this;
    this.matches.length = 0;
    this.batches.length = 0;
    this.executionBatches.length = 0;

    outer: for (const a of this.world.archetypes) {
      const indices = new Int32Array(this.components.length);
      for (let i = 0; i < this.components.length; i++) {
        const idx = a.componentIndex.get(this.components[i].id);
        if (idx === undefined) continue outer;
        indices[i] = idx;
      }
      const match = { archetype: a, indices };
      this.matches.push(match);
      for (let ci = 0; ci < a.chunks.length; ci++) {
        const chunk = a.chunks[ci];
        this.batches.push({
          chunk,
          indices,
          columns: this.components.map((_, i) => chunk.columns[indices[i]]),
        });
      }
    }

    const groupSize = this.world.executionBatchSize || 1;
    for (let i = 0; i < this.batches.length; i += groupSize)
      this.executionBatches.push(this.batches.slice(i, i + groupSize));

    this.version = this.world.topologyVersion;
    return this;
  }
}

class HermesWorld {
  constructor(options = {}) {
    this.entityCapacity = options.entityCapacity ?? 1_000_000;
    this.chunkBytes = options.chunkBytes ?? 32 * 1024;
    this.executionBatchSize = options.executionBatchSize ?? 0;
    this.migrationStrategy = options.migrationStrategy ?? 'grouped';
    this.entities = new EntityStore(this.entityCapacity);
    this.components = [];
    this.componentByName = new Map();
    this.archetypes = [];
    this.archetypeByKey = new Map();
    this.queryCache = new Map();
    this.systems = [];
    this.commands = new Map();
    this.events = new Map();
    this.structural = new TypedTable({ op: Uint8Array, entity: Uint32Array }, 1024);
    this.pendingSpawnArchetypes = [];
    this.pendingSpawnInitials = [];
    this.topologyVersion = 1;
    this.tickNumber = 0;
    this.profileEnabled = false;
    this.profileSamples = [];
    this.phaseSystems = Object.fromEntries(PHASE_ORDER.map((p) => [p, []]));
    this.dependencies = [];
    this.pendingStructural = [];
    this.structuralAddInitials = [];
    this.structuralAdd = new TypedTable({ entity: Uint32Array, component: Uint32Array }, 256);
    this.structuralRemove = new TypedTable({ entity: Uint32Array, component: Uint32Array }, 256);
    this.lastStructuralStats = null;
    this.pendingChanges = createChangeSet(0);
    this.dirtyChanges = new Set();
    this.externalToHandle = new Map();
    this.handleToExternal = new Map();
    this.externalVersions = new Map();
    this.schemaVersion = options.schemaVersion ?? 1;
    this.transactionDepth = 0;
    this.frameBudgetMs = options.frameBudgetMs ?? 16.666666667;
    this.onFrameBudgetExceeded = options.onFrameBudgetExceeded ?? null;
    this.development = options.development ?? false;
    this.lastFrameStats = null;
    this.recording = false;
    this.recordedInputs = [];
    this.inputSequence = 0;
    this.checksumEnabled = options.checksum ?? false;
    this.transitionCache = new Map();
    this.structuralWorkspace = {
      records: [],
      recordCount: 0,
      groups: new Map(),
      groupPool: [],
      groupCount: 0,
      chunks: new Map(),
      chunkPool: [],
      chunkCount: 0,
    };
    this.currentPhase = null;
    this.rowAcquisition = { chunk: null, row: 0 };
  }

  component(name, schema) {
    this.assertPhase('component registration', []);
    assert(!this.componentByName.has(name), `component ${name} already exists`);
    const c = new Component(this.components.length, name, schema);
    this.components.push(c);
    this.componentByName.set(name, c);
    return c;
  }

  archetype(...components) {
    this.assertPhase('archetype creation', [Phase.COMMIT]);
    const sorted = [...components].sort((a, b) => a.id - b.id);
    const key = sorted.map((c) => c.id).join(',');
    let a = this.archetypeByKey.get(key);
    if (a) return a;
    a = new Archetype(this, this.archetypes.length, sorted);
    this.archetypes.push(a);
    this.archetypeByKey.set(key, a);
    this.topologyVersion++;
    return a;
  }

  query(...components) {
    const key = [...components]
      .sort((a, b) => a.id - b.id)
      .map((c) => c.id)
      .join(',');
    let q = this.queryCache.get(key);
    if (!q) {
      q = new QueryPlan(this, components);
      this.queryCache.set(key, q);
    }
    return q.refresh();
  }

  compileQuery(...components) {
    return this.query(...components);
  }

  spawn(archetype, init = null) {
    this.assertPhase('spawn', [Phase.COMMIT]);
    const id = this.entities.create();
    const acquired = archetype.acquireRow(this.rowAcquisition),
      chunk = acquired.chunk,
      row = acquired.row;
    const chunkIndex = archetype.chunks.length - 1;
    chunk.entities[row] = id;
    this.entities.archetype[id] = archetype.id;
    this.entities.chunk[id] = chunkIndex;
    this.entities.row[id] = row;

    if (init) {
      for (let ci = 0; ci < archetype.components.length; ci++) {
        const comp = archetype.components[ci];
        const src = init[comp.name];
        if (!src) continue;
        const dst = chunk.columns[ci];
        for (const f of comp.fields) if (src[f.name] !== undefined) dst[f.name][row] = src[f.name];
      }
    }
    const handle = this.entities.handle(id);
    this.pendingChanges.created.push(handle);
    return handle;
  }

  spawnMany(archetype, rows, externalIds = null) {
    assert(archetype instanceof Archetype, 'spawnMany requires an archetype');
    assert(Array.isArray(rows), 'spawnMany rows must be an array');
    if (externalIds) {
      assert(externalIds.length === rows.length, 'spawnMany externalIds length must match rows');
      const seen = new Set();
      for (const externalId of externalIds) {
        assert(
          externalId !== null && externalId !== undefined,
          'spawnMany external ID is required',
        );
        assert(!seen.has(externalId), `duplicate external ID ${String(externalId)} in spawnMany`);
        assert(
          !this.externalToHandle.has(externalId),
          `duplicate external ID ${String(externalId)}`,
        );
        seen.add(externalId);
      }
    }
    const handles = new Array(rows.length);
    for (let i = 0; i < rows.length; i++) {
      const handle = this.spawn(archetype, rows[i]);
      handles[i] = handle;
      if (externalIds) this.bindExternal(externalIds[i], handle);
    }
    return handles;
  }

  destroy(handleOrId, rawId = false) {
    const id = rawId ? handleOrId : this.entities.resolve(handleOrId);
    if ((id === EMPTY || !this.entities.alive[id]) && this.development)
      assert(false, `destroy received stale entity ${String(handleOrId)}`);
    if (id === EMPTY || !this.entities.alive[id]) return false;
    const row = this.structural.allocate();
    this.structural.columns.op[row] = 1;
    this.structural.columns.entity[row] = id;
    return true;
  }

  destroyImmediate(id) {
    const ai = this.entities.archetype[id];
    if (ai === EMPTY) return;
    const a = this.archetypes[ai];
    const handle = this.entities.handle(id);
    const externalId = this.handleToExternal.get(handle);
    const ci = this.entities.chunk[id];
    const row = this.entities.row[id];
    const chunk = a.chunks[ci];
    const last = chunk.count - 1;

    if (row !== last) {
      const moved = chunk.entities[last];
      chunk.entities[row] = moved;
      for (let k = 0; k < chunk.columns.length; k++) {
        const comp = a.components[k];
        const cols = chunk.columns[k];
        for (const f of comp.fields) cols[f.name][row] = cols[f.name][last];
      }
      this.entities.row[moved] = row;
    }
    chunk.count--;
    this.entities.destroy(id);
    if (externalId !== undefined) {
      this.externalToHandle.delete(externalId);
      this.handleToExternal.delete(handle);
      this.externalVersions.delete(externalId);
    }
    this.pendingChanges.destroyed.push({ handle, externalId, archetype: a.id });
  }

  location(handle) {
    const id = this.entities.resolve(handle);
    if (id === EMPTY) return null;
    const archetype = this.archetypes[this.entities.archetype[id]],
      chunkIndex = this.entities.chunk[id];
    return {
      id,
      archetype,
      chunk: archetype.chunks[chunkIndex],
      chunkIndex,
      row: this.entities.row[id],
    };
  }

  bindExternal(externalId, handle) {
    assert(externalId !== null && externalId !== undefined, 'external ID is required');
    const id = this.entities.resolve(handle);
    assert(id !== EMPTY, `cannot bind stale handle for external ID ${String(externalId)}`);
    assert(!this.externalToHandle.has(externalId), `duplicate external ID ${String(externalId)}`);
    this.externalToHandle.set(externalId, handle);
    this.handleToExternal.set(handle, externalId);
    return handle;
  }

  resolveExternal(externalId) {
    const handle = this.externalToHandle.get(externalId);
    if (handle === undefined) return null;
    if (this.entities.resolve(handle) !== EMPTY) return handle;
    this.externalToHandle.delete(externalId);
    this.handleToExternal.delete(handle);
    return null;
  }

  ingestSnapshot(snapshot, options = {}) {
    assert(snapshot && Array.isArray(snapshot.entities), 'snapshot.entities must be an array');
    const expectedVersion = options.schemaVersion ?? this.schemaVersion;
    let source = snapshot;
    if (source.schemaVersion !== expectedVersion) {
      assert(
        typeof options.migrate === 'function',
        `snapshot schema ${String(source.schemaVersion)} does not match ${String(expectedVersion)}`,
      );
      source = options.migrate(source, expectedVersion);
      assert(
        source && source.schemaVersion === expectedVersion,
        'snapshot migration returned an invalid schema version',
      );
    }
    if (options.validate) assert(options.validate(source) !== false, 'snapshot validation failed');
    const seen = new Set(),
      rowsByArchetype = new Map();
    for (const record of source.entities) {
      assert(
        record && record.id !== undefined && record.id !== null,
        'snapshot entity is missing id',
      );
      for (const field of options.requiredFields ?? [])
        assert(
          record[field] !== undefined && record[field] !== null,
          `snapshot entity ${String(record.id)} is missing required field ${field}`,
        );
      assert(!seen.has(record.id), `duplicate external ID ${String(record.id)} in snapshot`);
      assert(!this.externalToHandle.has(record.id), `duplicate external ID ${String(record.id)}`);
      seen.add(record.id);
      const archetype = options.resolveArchetype
        ? options.resolveArchetype(record, this)
        : record.archetype;
      assert(
        archetype instanceof Archetype,
        `snapshot entity ${String(record.id)} has no valid archetype`,
      );
      const initial = options.mapInit
        ? options.mapInit(record, this)
        : (record.components ?? record.init ?? null);
      if (options.strictComponents)
        for (const component of archetype.components) {
          assert(
            initial && initial[component.name],
            `snapshot entity ${String(record.id)} is missing component ${component.name}`,
          );
          for (const field of component.fields)
            assert(
              initial[component.name][field.name] !== undefined,
              `snapshot entity ${String(record.id)} is missing ${component.name}.${field.name}`,
            );
        }
      let group = rowsByArchetype.get(archetype.id);
      if (!group)
        rowsByArchetype.set(archetype.id, (group = { archetype, rows: [], ids: [], versions: [] }));
      group.rows.push(initial);
      group.ids.push(record.id);
      group.versions.push(record.version ?? record.updatedAt ?? 0);
    }
    const handles = [];
    for (const group of rowsByArchetype.values()) {
      const spawned = this.spawnMany(group.archetype, group.rows, group.ids);
      handles.push(...spawned);
      for (let i = 0; i < group.ids.length; i++)
        this.externalVersions.set(group.ids[i], group.versions[i]);
    }
    return { schemaVersion: expectedVersion, count: handles.length, handles };
  }

  applyUpdates(updates, options = {}) {
    assert(Array.isArray(updates), 'updates must be an array');
    const groups = new Map();
    let stale = 0,
      missing = 0;
    for (const update of updates) {
      assert(
        update && update.id !== undefined && update.id !== null,
        'update is missing external id',
      );
      const handle = this.resolveExternal(update.id);
      if (handle === null) {
        missing++;
        continue;
      }
      const version = update.version ?? update.updatedAt ?? 0;
      const previous = this.externalVersions.get(update.id) ?? -Infinity;
      if (version <= previous && !options.allowEqualVersion) {
        stale++;
        continue;
      }
      const component =
        typeof update.component === 'string'
          ? this.componentByName.get(update.component)
          : update.component;
      assert(
        component instanceof Component,
        `update for ${String(update.id)} has invalid component`,
      );
      const entityId = this.entities.resolve(handle);
      const archetype = this.archetypes[this.entities.archetype[entityId]];
      assert(
        archetype.componentIndex.has(component.id),
        `update for ${String(update.id)} is missing component ${component.name}`,
      );
      let group = groups.get(component.id);
      if (!group)
        groups.set(
          component.id,
          (group = { component, handles: [], values: [], ids: [], versions: [] }),
        );
      group.handles.push(handle);
      group.values.push(update.values ?? update.value);
      group.ids.push(update.id);
      group.versions.push(version);
    }
    let applied = 0;
    for (const group of groups.values()) {
      applied += this.updateMany(group.handles, group.component, group.values);
      for (let i = 0; i < group.ids.length; i++)
        this.externalVersions.set(group.ids[i], group.versions[i]);
    }
    return { applied, stale, missing };
  }

  moveImmediate(id, target) {
    const source = this.archetypes[this.entities.archetype[id]];
    if (source === target) return;
    const oldChunk = source.chunks[this.entities.chunk[id]],
      oldRow = this.entities.row[id];
    const acquired = target.acquireRow(this.rowAcquisition),
      newChunk = acquired.chunk,
      newRow = acquired.row;
    newChunk.entities[newRow] = id;
    for (let ti = 0; ti < target.components.length; ti++) {
      const tc = target.components[ti],
        si = source.componentIndex.get(tc.id);
      const dst = newChunk.columns[ti];
      if (si !== undefined) {
        const src = oldChunk.columns[si];
        for (const f of tc.fields) dst[f.name][newRow] = src[f.name][oldRow];
      }
    }
    this.entities.archetype[id] = target.id;
    this.entities.chunk[id] = target.chunks.length - 1;
    this.entities.row[id] = newRow;
    const last = oldChunk.count - 1;
    if (oldRow !== last) {
      const moved = oldChunk.entities[last];
      oldChunk.entities[oldRow] = moved;
      for (let k = 0; k < oldChunk.columns.length; k++) {
        const comp = source.components[k],
          cols = oldChunk.columns[k];
        for (const f of comp.fields) cols[f.name][oldRow] = cols[f.name][last];
      }
      this.entities.row[moved] = oldRow;
    }
    oldChunk.count--;
  }

  removeRowImmediate(archetype, chunk, row) {
    const last = chunk.count - 1;
    if (row !== last) {
      const moved = chunk.entities[last];
      chunk.entities[row] = moved;
      for (let k = 0; k < chunk.columns.length; k++) {
        const comp = archetype.components[k],
          cols = chunk.columns[k];
        for (const f of comp.fields) cols[f.name][row] = cols[f.name][last];
      }
      this.entities.row[moved] = row;
    }
    chunk.count--;
  }

  moveBatchColumnar(records) {
    // Records are from one source chunk. Removing rows in descending order
    // keeps all unprocessed source rows stable, including swap-moved survivors.
    const byTarget = new Map();
    for (const record of records) {
      record.target.acquireRow(record);
      record.newChunk = record.chunk;
      record.newRow = record.row;
      record.newChunk.entities[record.newRow] = record.id;
      let group = byTarget.get(record.target.id);
      if (!group) byTarget.set(record.target.id, (group = []));
      group.push(record);
      this.entities.archetype[record.id] = record.target.id;
      this.entities.chunk[record.id] = record.target.chunks.length - 1;
      this.entities.row[record.id] = record.newRow;
    }
    for (const group of byTarget.values()) {
      const target = group[0].target;
      for (let ti = 0; ti < target.components.length; ti++) {
        const component = target.components[ti],
          sourceIndex = group[0].source.componentIndex.get(component.id);
        const fields = target.components[ti].fields;
        for (const field of fields) {
          for (const record of group) {
            const dst = record.newChunk.columns[ti][field.name];
            if (sourceIndex !== undefined)
              dst[record.newRow] =
                record.sourceChunk.columns[sourceIndex][field.name][record.oldRow];
            else if (record.initial && record.initial[field.name] !== undefined)
              dst[record.newRow] = record.initial[field.name];
          }
        }
      }
    }
    records.sort((a, b) => b.oldRow - a.oldRow);
    for (const record of records)
      this.removeRowImmediate(record.source, record.sourceChunk, record.oldRow);
  }

  command(name, schema, capacity = 1024, options = {}) {
    assert(!this.commands.has(name), `command ${name} already exists`);
    const t = new TypedTable(schema, capacity, {
      strict: this.development,
      label: `command ${name}`,
      ...options,
    });
    this.commands.set(name, t);
    return t;
  }

  event(name, schema, capacity = 1024, options = {}) {
    assert(!this.events.has(name), `event ${name} already exists`);
    const t = new TypedTable(schema, capacity, {
      strict: this.development,
      label: `event ${name}`,
      ...options,
    });
    this.events.set(name, t);
    return t;
  }

  system(spec) {
    assert(spec && typeof spec.run === 'function', 'system requires run()');
    const query = spec.query ?? [];
    const s = {
      name: spec.name ?? `system${this.systems.length}`,
      components: query,
      plan: this.query(...query),
      run: spec.run,
      read: spec.read ?? query,
      write: spec.write ?? [],
      phase: spec.phase ?? Phase.UPDATE,
      order: this.systems.length,
      columns: new Array(query.length),
      runBatch: typeof spec.runBatch === 'function' ? spec.runBatch : null,
      before: spec.before ?? null,
      after: spec.after ?? null,
    };
    this.systems.push(s);
    assert(this.phaseSystems[s.phase], `unknown phase ${s.phase}`);
    this.phaseSystems[s.phase].push(s);
    this.rebuildSchedules();
    this.rebuildDependencies();
    return s;
  }

  profile(enabled = true) {
    this.profileEnabled = Boolean(enabled);
    if (!enabled) this.profileSamples.length = 0;
    return this;
  }

  rebuildDependencies() {
    this.dependencies.length = 0;
    const overlaps = (a, b) => a.some((x) => b.includes(x));
    for (let i = 0; i < this.systems.length; i++)
      for (let j = i + 1; j < this.systems.length; j++) {
        const a = this.systems[i],
          b = this.systems[j];
        const conflict =
          overlaps(a.write, b.read) || overlaps(a.write, b.write) || overlaps(a.read, b.write);
        this.dependencies.push({ a: a.name, b: b.name, conflict });
      }
    return this.dependencies;
  }

  rebuildSchedules() {
    for (const phase of PHASE_ORDER) {
      const list = this.phaseSystems[phase],
        byName = new Map(list.map((s) => [s.name, s])),
        indegree = new Map(list.map((s) => [s.name, 0])),
        edges = new Map(list.map((s) => [s.name, []]));
      for (const s of list) {
        if (s.before && byName.has(s.before)) {
          edges.get(s.name).push(s.before);
          indegree.set(s.before, indegree.get(s.before) + 1);
        }
        if (s.after && byName.has(s.after)) {
          edges.get(s.after).push(s.name);
          indegree.set(s.name, indegree.get(s.name) + 1);
        }
      }
      const queue = list
          .filter((s) => indegree.get(s.name) === 0)
          .sort((a, b) => a.order - b.order),
        ordered = [];
      while (queue.length) {
        const s = queue.shift();
        ordered.push(s);
        for (const next of edges.get(s.name)) {
          indegree.set(next, indegree.get(next) - 1);
          if (!indegree.get(next)) queue.push(byName.get(next));
        }
      }
      assert(ordered.length === list.length, `cyclic system ordering in phase ${phase}`);
      this.phaseSystems[phase] = ordered;
    }
  }

  getProfile() {
    if (!this.profileSamples.length) return null;
    const sorted = this.profileSamples.slice().sort((a, b) => a.total - b.total);
    const percentile = (p) =>
      sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * p))].total;
    const latest = this.profileSamples[this.profileSamples.length - 1];
    return {
      total: latest.total,
      median: sorted[Math.floor(sorted.length * 0.5)].total,
      p95: percentile(0.95),
      p99: percentile(0.99),
      runtime: latest.runtime,
      systems: latest.systems,
      phases: latest.phases,
      samples: this.profileSamples.length,
    };
  }

  profileReport(entityCount = this.entities.next) {
    const p = this.getProfile();
    if (!p) return null;
    const ns = p.median * 1e6;
    const systemMetrics = Object.fromEntries(
      Object.entries(p.systems).map(([name, time]) => [
        name,
        {
          time,
          nsPerEntity: entityCount ? (time * 1e6) / entityCount : 0,
          percent: p.total ? (time / p.total) * 100 : 0,
        },
      ]),
    );
    return {
      ...p,
      entityCount,
      nsPerEntity: entityCount ? ns / entityCount : 0,
      entitiesPerSecond: ns ? (entityCount * 1e9) / ns : 0,
      ticksPerSecond: p.median ? 1000 / p.median : 0,
      budget60Hz: (p.median / 16.666666667) * 100,
      systemMetrics,
    };
  }

  runSystem(system, dt) {
    const plan = system.plan.refresh();
    if (system.runBatch) {
      const groups = plan.executionBatches;
      for (let gi = 0; gi < groups.length; gi++) {
        const readOnly = this.development
          ? groups[gi].map((batch) => this.readOnlyChecksum(system, batch))
          : null;
        system.runBatch(groups[gi], dt, this);
        if (readOnly)
          for (let bi = 0; bi < groups[gi].length; bi++)
            assert(
              readOnly[bi] === this.readOnlyChecksum(system, groups[gi][bi]),
              `system ${system.name} mutated a component not declared in write`,
            );
        if (system.write.length) for (const batch of groups[gi]) this.markBatchDirty(system, batch);
      }
      return;
    }
    const batches = plan.batches;
    for (let bi = 0; bi < batches.length; bi++) {
      const batch = batches[bi];
      const chunk = batch.chunk;
      if (!chunk.count) continue;
      const readOnly = this.development ? this.readOnlyChecksum(system, batch) : 0;
      system.run(batch.columns, chunk.count, dt, this, chunk.entities);
      if (this.development)
        assert(
          readOnly === this.readOnlyChecksum(system, batch),
          `system ${system.name} mutated a component not declared in write`,
        );
      if (system.write.length) this.markBatchDirty(system, batch);
    }
  }

  readOnlyChecksum(system, batch) {
    const writable = new Set(system.write.map((component) => component.id));
    let hash = 0x811c9dc5;
    for (let ci = 0; ci < system.components.length; ci++) {
      const component = system.components[ci];
      if (writable.has(component.id)) continue;
      for (const field of component.fields) {
        const column = batch.columns[ci][field.name];
        const bytes = new Uint8Array(
          column.buffer,
          column.byteOffset,
          batch.chunk.count * field.bytes,
        );
        for (let i = 0; i < bytes.length; i++) {
          hash ^= bytes[i];
          hash = Math.imul(hash, 0x01000193) >>> 0;
        }
      }
    }
    return hash;
  }

  markBatchDirty(system, batch) {
    this.pendingChanges.dirtyBatches.push({
      archetype: batch.chunk.archetype.id,
      chunk: batch.chunk.index,
      count: batch.chunk.count,
      components: system.write.map((component) => component.id),
    });
  }

  commit() {
    const s = this.structural;
    const op = s.columns.op;
    const entity = s.columns.entity;
    for (let i = 0; i < s.count; i++) {
      if (op[i] === 1) this.destroyImmediate(entity[i]);
    }
    s.clear();
    for (let i = 0; i < this.pendingSpawnArchetypes.length; i++) {
      this.spawn(this.pendingSpawnArchetypes[i], this.pendingSpawnInitials[i]);
    }
    this.pendingSpawnArchetypes.length = 0;
    this.pendingSpawnInitials.length = 0;
    const stats = {
      strategy: this.migrationStrategy,
      requests: 0,
      entities: 0,
      groups: 0,
      bytesCopied: 0,
      bytesInitialized: 0,
      started: nowNs(),
    };
    const migrate = (table, adding) => {
      const workspace = this.structuralWorkspace,
        groups = workspace.groups,
        rows = table.count;
      groups.clear();
      workspace.recordCount = 0;
      workspace.groupCount = 0;
      for (let i = 0; i < rows; i++) {
        const id = table.columns.entity[i],
          source = this.archetypes[this.entities.archetype[id]];
        if (!source) continue;
        const component = this.components[table.columns.component[i]],
          contains = source.componentIndex.has(component.id);
        if ((adding && contains) || (!adding && !contains)) continue;
        const transitionKey = pairKey(pairKey(source.id, component.id), adding ? 1 : 0);
        let target = this.transitionCache.get(transitionKey);
        if (!target) {
          const nextComponents = adding
            ? [...source.components, component]
            : source.components.filter((value) => value.id !== component.id);
          target = this.archetype(...nextComponents);
          this.transitionCache.set(transitionKey, target);
        }
        const key = pairKey(source.id, target.id);
        let group = groups.get(key);
        if (!group) {
          group = workspace.groupPool[workspace.groupCount] ?? [];
          workspace.groupPool[workspace.groupCount++] = group;
          group.length = 0;
          groups.set(key, group);
        }
        const recordIndex = workspace.recordCount++,
          record = workspace.records[recordIndex] ?? {};
        workspace.records[recordIndex] = record;
        record.id = id;
        record.source = source;
        record.target = target;
        record.component = component;
        record.initial = adding ? this.structuralAddInitials[i] : null;
        record.sourceChunk = source.chunks[this.entities.chunk[id]];
        record.oldRow = this.entities.row[id];
        group.push(record);
        for (const targetComponent of target.components) {
          if (source.componentIndex.has(targetComponent.id)) {
            for (const field of targetComponent.fields) stats.bytesCopied += field.bytes;
          } else if (adding && targetComponent.id === component.id) {
            for (const field of targetComponent.fields) stats.bytesInitialized += field.bytes;
          }
        }
      }
      stats.requests += rows;
      stats.entities += [...groups.values()].reduce((n, group) => n + group.length, 0);
      stats.groups += groups.size;
      if (this.migrationStrategy === 'columnar') {
        const chunks = workspace.chunks;
        chunks.clear();
        workspace.chunkCount = 0;
        for (const group of groups.values())
          for (const record of group) {
            const key = pairKey(record.source.id, this.entities.chunk[record.id]);
            let chunkRecords = chunks.get(key);
            if (!chunkRecords) {
              chunkRecords = workspace.chunkPool[workspace.chunkCount] ?? [];
              workspace.chunkPool[workspace.chunkCount++] = chunkRecords;
              chunkRecords.length = 0;
              chunks.set(key, chunkRecords);
            }
            chunkRecords.push(record);
          }
        for (const chunkRecords of chunks.values()) this.moveBatchColumnar(chunkRecords);
      } else {
        for (const group of groups.values())
          for (const record of group) {
            this.moveImmediate(record.id, record.target);
            if (record.initial) {
              const moved = this.location(this.entities.handle(record.id));
              const ci = record.target.componentIndex.get(record.component.id);
              for (const field of record.component.fields)
                if (record.initial[field.name] !== undefined)
                  moved.chunk.columns[ci][field.name][moved.row] = record.initial[field.name];
            }
          }
      }
      for (const group of groups.values())
        for (const record of group)
          this.pendingChanges.structural.push(this.entities.handle(record.id));
      table.clear();
      if (adding) this.structuralAddInitials.length = 0;
    };
    migrate(this.structuralRemove, false);
    migrate(this.structuralAdd, true);
    stats.ms = Number(nowNs() - stats.started) / 1e6;
    delete stats.started;
    this.lastStructuralStats = stats;
  }

  spawnDeferred(archetype, init = null) {
    const index = this.pendingSpawnArchetypes.length;
    this.pendingSpawnArchetypes.push(archetype);
    this.pendingSpawnInitials.push(init);
    return index;
  }
  add(entity, component, initial = null) {
    const id = this.entities.resolve(entity);
    if (id === EMPTY && this.development)
      assert(false, `add ${component.name} received stale entity ${String(entity)}`);
    if (id !== EMPTY) {
      const row = this.structuralAdd.allocate();
      this.structuralAdd.columns.entity[row] = id;
      this.structuralAdd.columns.component[row] = component.id;
      this.structuralAddInitials[row] = initial;
    }
    return id !== EMPTY;
  }
  remove(entity, component) {
    const id = this.entities.resolve(entity);
    if (id === EMPTY && this.development)
      assert(false, `remove ${component.name} received stale entity ${String(entity)}`);
    if (id !== EMPTY) {
      const row = this.structuralRemove.allocate();
      this.structuralRemove.columns.entity[row] = id;
      this.structuralRemove.columns.component[row] = component.id;
    }
    return id !== EMPTY;
  }

  destroyMany(entities) {
    let accepted = 0;
    for (let i = 0; i < entities.length; i++) if (this.destroy(entities[i])) accepted++;
    return accepted;
  }

  addMany(entities, component, initials = null) {
    if (initials)
      assert(initials.length === entities.length, 'addMany initials length must match entities');
    let accepted = 0;
    for (let i = 0; i < entities.length; i++)
      if (this.add(entities[i], component, initials ? initials[i] : null)) accepted++;
    return accepted;
  }

  removeMany(entities, component) {
    let accepted = 0;
    for (let i = 0; i < entities.length; i++) if (this.remove(entities[i], component)) accepted++;
    return accepted;
  }

  updateMany(entities, component, values) {
    this.assertPhase('updateMany', [
      Phase.INPUT,
      Phase.PRE_UPDATE,
      Phase.COMMANDS,
      Phase.STRUCTURAL,
      Phase.COMMIT,
    ]);
    if (Array.isArray(values))
      assert(values.length === entities.length, 'updateMany values length must match entities');
    let updated = 0;
    for (let i = 0; i < entities.length; i++) {
      const handle = entities[i],
        id = this.entities.resolve(handle);
      if (id === EMPTY && this.development)
        assert(false, `updateMany ${component.name} received stale entity ${String(handle)}`);
      if (id === EMPTY) continue;
      const archetype = this.archetypes[this.entities.archetype[id]];
      const componentIndex = archetype.componentIndex.get(component.id);
      if (componentIndex === undefined) continue;
      const chunk = archetype.chunks[this.entities.chunk[id]],
        row = this.entities.row[id];
      const value = Array.isArray(values) ? values[i] : values;
      if (!value) continue;
      const columns = chunk.columns[componentIndex];
      for (const field of component.fields)
        if (value[field.name] !== undefined) columns[field.name][row] = value[field.name];
      const dirtyKey = `${id}:${component.id}`;
      if (!this.dirtyChanges.has(dirtyKey)) {
        this.dirtyChanges.add(dirtyKey);
        this.pendingChanges.updated.push({ handle, component: component.id });
      }
      updated++;
    }
    return updated;
  }

  startRecording(clear = true) {
    if (clear) this.recordedInputs.length = 0;
    this.recording = true;
    return this;
  }

  stopRecording() {
    this.recording = false;
    return this.recordedInputs.slice();
  }

  input(type, payload, handler) {
    assert(typeof type === 'string' && type.length > 0, 'input type is required');
    assert(typeof handler === 'function', `input ${type} requires a handler`);
    const entry = {
      kind: 'input',
      tick: this.tickNumber + 1,
      sequence: this.inputSequence++,
      type,
      payload: typeof structuredClone === 'function' ? structuredClone(payload) : payload,
    };
    if (this.recording) this.recordedInputs.push(entry);
    handler(payload, this);
    return entry;
  }

  checksum() {
    let hash = 0x811c9dc5;
    const add = (value) => {
      hash ^= value;
      hash = Math.imul(hash, 0x01000193) >>> 0;
    };
    for (const archetype of this.archetypes) {
      add(archetype.id);
      for (const chunk of archetype.chunks) {
        add(chunk.count);
        const entityBytes = new Uint8Array(
          chunk.entities.buffer,
          chunk.entities.byteOffset,
          chunk.count * 4,
        );
        for (let i = 0; i < entityBytes.length; i++) add(entityBytes[i]);
        for (let ci = 0; ci < archetype.components.length; ci++) {
          for (const field of archetype.components[ci].fields) {
            const column = chunk.columns[ci][field.name];
            const bytes = new Uint8Array(
              column.buffer,
              column.byteOffset,
              chunk.count * field.bytes,
            );
            for (let i = 0; i < bytes.length; i++) add(bytes[i]);
          }
        }
      }
    }
    return hash.toString(16).padStart(8, '0');
  }

  replay(log, handlers, dt = 1 / 60) {
    assert(Array.isArray(log), 'replay log must be an array');
    const inputs = log
      .filter((entry) => entry.kind === 'input')
      .slice()
      .sort((a, b) => a.tick - b.tick || a.sequence - b.sequence);
    const expected = new Map(
      log.filter((entry) => entry.kind === 'checksum').map((entry) => [entry.tick, entry.checksum]),
    );
    const lastTick = log.reduce((max, entry) => Math.max(max, entry.tick ?? 0), this.tickNumber);
    let cursor = 0;
    while (this.tickNumber < lastTick) {
      const nextTick = this.tickNumber + 1;
      while (cursor < inputs.length && inputs[cursor].tick === nextTick) {
        const entry = inputs[cursor++];
        const handler = typeof handlers === 'function' ? handlers : handlers[entry.type];
        assert(typeof handler === 'function', `no replay handler for input ${entry.type}`);
        handler(entry.payload, this);
      }
      this.step(dt);
      if (expected.has(this.tickNumber))
        assert(
          this.checksum() === expected.get(this.tickNumber),
          `replay checksum mismatch at tick ${this.tickNumber}`,
        );
    }
    return { tick: this.tickNumber, checksum: this.checksum() };
  }

  transaction(callback) {
    this.assertPhase('transaction', []);
    assert(typeof callback === 'function', 'transaction requires a callback');
    const operations = [];
    const transaction = {
      spawnMany: (archetype, rows, externalIds = null) =>
        operations.push(['spawnMany', archetype, rows, externalIds]),
      destroyMany: (entities) => operations.push(['destroyMany', entities]),
      addMany: (entities, component, initials = null) =>
        operations.push(['addMany', entities, component, initials]),
      removeMany: (entities, component) => operations.push(['removeMany', entities, component]),
      updateMany: (entities, component, values) =>
        operations.push(['updateMany', entities, component, values]),
    };
    const result = callback(transaction);
    this.transactionDepth++;
    try {
      for (const operation of operations) this[operation[0]](...operation.slice(1));
      this.commit();
    } finally {
      this.transactionDepth--;
    }
    return result;
  }

  step(dt = 1 / 60) {
    const frameStarted = nowNs();
    const started = this.profileEnabled ? frameStarted : 0n;
    const systems = Object.create(null),
      phases = Object.create(null);
    try {
      for (const e of this.events.values()) e.clear();
      for (const phase of PHASE_ORDER) {
        this.currentPhase = phase;
        const phaseStart = this.profileEnabled ? nowNs() : 0n;
        for (const s of this.phaseSystems[phase]) {
          const t = this.profileEnabled ? nowNs() : 0n;
          this.runSystem(s, dt);
          if (this.profileEnabled) systems[s.name] = Number(nowNs() - t) / 1e6;
        }
        if (this.profileEnabled) phases[phase] = Number(nowNs() - phaseStart) / 1e6;
      }
      const commitStart = this.profileEnabled ? nowNs() : 0n;
      this.currentPhase = Phase.COMMIT;
      this.commit();
      if (this.profileEnabled) {
        phases[Phase.COMMIT] = Number(nowNs() - commitStart) / 1e6;
        const total = Number(nowNs() - started) / 1e6;
        const phaseTotal = Object.values(phases).reduce((sum, value) => sum + value, 0);
        this.profileSamples.push({
          total,
          runtime: Math.max(0, total - phaseTotal),
          systems,
          phases,
        });
        if (this.profileSamples.length > 240) this.profileSamples.shift();
      }
      this.tickNumber++;
      const elapsedMs = Number(nowNs() - frameStarted) / 1e6;
      const changes = this.pendingChanges;
      changes.tick = this.tickNumber;
      const events = Object.create(null);
      for (const [name, table] of this.events) {
        const columns = Object.create(null);
        for (const column of Object.keys(table.schema))
          columns[column] = table.columns[column].slice(0, table.count);
        events[name] = { count: table.count, columns, stats: table.stats() };
      }
      const frameChecksum = this.recording || this.checksumEnabled ? this.checksum() : null;
      this.lastFrameStats = {
        tick: this.tickNumber,
        elapsedMs,
        budgetMs: this.frameBudgetMs,
        budgetPercent: (elapsedMs / this.frameBudgetMs) * 100,
        overBudget: elapsedMs > this.frameBudgetMs,
        changes:
          changes.created.length +
          changes.updated.length +
          changes.structural.length +
          changes.destroyed.length,
        delta: {
          created: changes.created.length,
          updated: changes.updated.length,
          structural: changes.structural.length,
          destroyed: changes.destroyed.length,
          dirtyBatches: changes.dirtyBatches.length,
        },
        checksum: frameChecksum,
      };
      if (this.recording)
        this.recordedInputs.push({
          kind: 'checksum',
          tick: this.tickNumber,
          checksum: frameChecksum,
        });
      if (this.lastFrameStats.overBudget && this.onFrameBudgetExceeded)
        this.onFrameBudgetExceeded(this.lastFrameStats, this);
      this.pendingChanges = createChangeSet(this.tickNumber + 1);
      this.dirtyChanges.clear();
      return { tick: this.tickNumber, dt, changes, events, stats: this.lastFrameStats };
    } finally {
      this.currentPhase = null;
    }
  }

  tick(dt = 1 / 60) {
    return this.step(dt);
  }

  assertPhase(operation, allowedDuringStep) {
    if (!this.development || this.currentPhase === null) return;
    assert(
      allowedDuringStep.includes(this.currentPhase),
      `${operation} is not allowed during ${this.currentPhase}`,
    );
  }

  filterChanges(changes, options = {}) {
    assert(changes && typeof changes === 'object', 'filterChanges requires a change set');
    const componentIds = new Set(
      (options.components ?? []).map((component) =>
        typeof component === 'number' ? component : component.id,
      ),
    );
    const archetypeIds = options.query
      ? new Set(options.query.refresh().matches.map((match) => match.archetype.id))
      : null;
    const entityMatches = (handle) => {
      const id = this.entities.resolve(handle);
      return id !== EMPTY && (!archetypeIds || archetypeIds.has(this.entities.archetype[id]));
    };
    const componentMatches = (component) => !componentIds.size || componentIds.has(component);
    return {
      tick: changes.tick,
      created: changes.created.filter(entityMatches),
      updated: changes.updated.filter(
        (change) => entityMatches(change.handle) && componentMatches(change.component),
      ),
      structural: changes.structural.filter(entityMatches),
      destroyed: changes.destroyed.filter(
        (change) => !archetypeIds || archetypeIds.has(change.archetype),
      ),
      dirtyBatches: changes.dirtyBatches.filter(
        (batch) =>
          (!archetypeIds || archetypeIds.has(batch.archetype)) &&
          (!componentIds.size || batch.components.some(componentMatches)),
      ),
    };
  }

  stats() {
    const profile = this.getProfile();
    const memory = memoryUsage();
    let queryMatches = 0,
      queryBatches = 0;
    for (const query of this.queryCache.values()) {
      query.refresh();
      queryMatches += query.matches.length;
      queryBatches += query.batches.length;
    }
    return {
      version: Hermes.version,
      tick: this.tickNumber,
      entities: this.entities.next - this.entities.freeCount,
      archetypes: this.archetypes.length,
      queries: { plans: this.queryCache.size, matches: queryMatches, batches: queryBatches },
      systems: profile ? profile.systems : null,
      frame: this.lastFrameStats,
      structural: this.lastStructuralStats,
      commands: Object.fromEntries(
        [...this.commands].map(([name, table]) => [name, table.stats()]),
      ),
      events: Object.fromEntries([...this.events].map(([name, table]) => [name, table.stats()])),
      externalIds: this.externalToHandle.size,
      recording: this.recording,
      pools: {
        structuralRecords: this.structuralWorkspace.records.length,
        transitionGroups: this.structuralWorkspace.groupPool.length,
        chunkGroups: this.structuralWorkspace.chunkPool.length,
        addCapacity: this.structuralAdd.capacity,
        removeCapacity: this.structuralRemove.capacity,
        destroyCapacity: this.structural.capacity,
      },
      memory: {
        arrayBuffers: memory.arrayBuffers,
        heapUsed: memory.heapUsed,
        rss: memory.rss,
      },
    };
  }
}

const Hermes = {
  version: '0.4.0',
  Phase,
  World: HermesWorld,
  Types: {
    f64: Float64Array,
    f32: Float32Array,
    u32: Uint32Array,
    i32: Int32Array,
    u16: Uint16Array,
    i16: Int16Array,
    u8: Uint8Array,
    i8: Int8Array,
  },
  EMPTY,
};

// Keep the runtime consumable from both Node (CommonJS) and Vite/browser
// bundles. In a browser there is no `module` object to assign to.
if (typeof module !== 'undefined' && module.exports) {
  module.exports = { Hermes, HermesWorld, TypedTable };
}
if (typeof globalThis !== 'undefined') {
  globalThis.__HERMES_RUNTIME__ = { Hermes, HermesWorld, TypedTable };
}

function stats(samples) {
  const sorted = samples.slice().sort((a, b) => a - b);
  const pick = (p) => sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * p))];
  return { median: pick(0.5), p95: pick(0.95), p99: pick(0.99) };
}

function structuralWorld(
  count,
  chunkBytes = 256 * 1024,
  retainHandles = true,
  migrationStrategy = 'grouped',
) {
  const world = new HermesWorld({ entityCapacity: count + 1024, chunkBytes, migrationStrategy });
  const Position = world.component('Position', {
    x: Float64Array,
    y: Float64Array,
    z: Float64Array,
  });
  const Velocity = world.component('Velocity', {
    x: Float64Array,
    y: Float64Array,
    z: Float64Array,
  });
  const Health = world.component('Health', { value: Float32Array });
  const Armor = world.component('Armor', { value: Float32Array });
  const A = world.archetype(Position, Velocity, Health),
    B = world.archetype(Position, Velocity, Health, Armor),
    C = world.archetype(Position, Health);
  const handles = retainHandles ? new Array(count) : null;
  for (let i = 0; i < count; i++) {
    const handle = world.spawn(A, {
      Position: { x: i },
      Velocity: { x: 1 },
      Health: { value: 100 },
    });
    if (handles) handles[i] = handle;
  }
  return { world, Position, Velocity, Health, Armor, A, B, C, handles };
}

function measureStructuralTransition(factory, transition, count) {
  const { world, Armor, Velocity, handles } = factory;
  const start = nowNs();
  for (let i = 0; i < count; i++) {
    const h = handles[i];
    if (transition === 'A→B' || transition === 'C→A')
      world.add(h, transition === 'A→B' ? Armor : Velocity);
    else if (transition === 'B→A') world.remove(h, Armor);
    else if (transition === 'A→C') world.remove(h, Velocity);
  }
  world.tick();
  const elapsed = Number(nowNs() - start) / 1e6;
  return {
    ms: elapsed,
    nsPerEntity: (elapsed * 1e6) / count,
    entitiesPerSecond: (count * 1000) / elapsed,
  };
}

function characterize() {
  const count = 1_000_000,
    ticks = 60;
  console.log('\nHERMES v0.4.0 — RUNTIME CHARACTERIZATION');
  console.log('═'.repeat(72));
  console.log(
    `Entities: ${fmt(count, 0)} · measured ticks: ${ticks} · profiling disabled for throughput`,
  );

  const profileWorld = structuralWorld(100_000, 256 * 1024, false);
  profileWorld.world.system({
    name: 'Movement',
    query: [profileWorld.Position, profileWorld.Velocity],
    read: [profileWorld.Velocity],
    write: [profileWorld.Position],
    run(cols, n, dt) {
      const p = cols[0],
        v = cols[1];
      for (let i = 0; i < n; i++) {
        p.x[i] += v.x[i] * dt;
        p.y[i] += v.y[i] * dt;
        p.z[i] += v.z[i] * dt;
      }
    },
  });
  profileWorld.world.profile(true);
  for (let i = 0; i < 20; i++) profileWorld.world.tick();
  const profile = profileWorld.world.profileReport(100_000);
  console.log('\nSYSTEM PROFILE');
  console.log(JSON.stringify(profile, null, 2));

  console.log('\nCHUNK SWEEP');
  for (const bytes of [32, 64, 128, 256, 512, 1024]) {
    if (global.gc) global.gc();
    const w = structuralWorld(count, bytes * 1024, false),
      P = w.Position,
      V = w.Velocity;
    w.world.system({
      name: 'Movement',
      query: [P, V],
      run(cols, n, dt) {
        const p = cols[0],
          v = cols[1];
        for (let i = 0; i < n; i++) {
          p.x[i] += v.x[i] * dt;
          p.y[i] += v.y[i] * dt;
          p.z[i] += v.z[i] * dt;
        }
      },
    });
    for (let i = 0; i < 15; i++) w.world.tick();
    const samples = [];
    for (let i = 0; i < ticks; i++) {
      const t = nowNs();
      w.world.tick();
      samples.push(Number(nowNs() - t) / 1e6);
    }
    const s = stats(samples),
      ns = s.median * 1e6,
      mem = process.memoryUsage();
    console.log(
      `${String(bytes).padStart(4)} KiB · capacity ${w.A.chunkCapacity} · chunks ${w.A.chunks.length} · median ${fmt(s.median, 3)} ms · p95 ${fmt(s.p95, 3)} · p99 ${fmt(s.p99, 3)} · ns/entity ${fmt(ns / count, 3)} · entities/sec ${fmt((count * 1e9) / ns, 0)} · 60Hz ${fmt((s.median / 16.666666667) * 100, 1)}% · ArrayBuffers ${fmt(mem.arrayBuffers / 1024 / 1024, 2)} MiB`,
    );
  }

  console.log('\nSTRUCTURAL BENCHMARK');
  for (const n of [1, 1_000, 10_000, 100_000]) {
    const f = structuralWorld(n);
    const spawnStart = nowNs();
    const spawned = structuralWorld(n);
    const spawnMs = Number(nowNs() - spawnStart) / 1e6;
    const destroy = measureStructuralTransition(f, 'A→C', n);
    console.log(
      `${String(n).padStart(6)} entities · spawn ${fmt(spawnMs, 3)} ms · A→C ${fmt(destroy.nsPerEntity, 2)} ns/entity · chunks ${spawned.A.chunks.length}`,
    );
  }
  const migration = structuralWorld(100_000);
  for (const transition of ['A→B', 'B→A', 'A→C', 'C→A'])
    console.log(
      `${transition} · ${JSON.stringify(measureStructuralTransition(migration, transition, 100_000))}`,
    );

  console.log('\nSEQUENTIAL VS RANDOM');
  const seq = structuralWorld(100_000),
    sequential = measureStructuralTransition(seq, 'A→B', 100_000);
  const random = structuralWorld(100_000),
    shuffled = random.handles;
  let seed = 0x9e3779b9;
  for (let i = shuffled.length - 1; i > 0; i--) {
    seed = (seed * 1664525 + 1013904223) >>> 0;
    const j = seed % (i + 1);
    [shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]];
  }
  const randomStart = nowNs();
  for (let i = 0; i < shuffled.length; i++) random.world.add(shuffled[i], random.Armor);
  random.world.tick();
  const randomMs = Number(nowNs() - randomStart) / 1e6;
  console.log(
    `sequential ${fmt(sequential.nsPerEntity, 2)} ns/entity · random ${fmt((randomMs * 1e6) / 100_000, 2)} ns/entity · seed 0x9e3779b9`,
  );

  console.log('\nMIGRATION STRATEGIES');
  for (const strategy of ['individual', 'grouped', 'columnar']) {
    const candidate = structuralWorld(100_000, 256 * 1024, true, strategy);
    const result = measureStructuralTransition(candidate, 'A→B', 100_000);
    console.log(
      `${strategy.padEnd(10)} · ${fmt(result.nsPerEntity, 2)} ns/entity · groups ${candidate.world.lastStructuralStats.groups} · ${fmt(candidate.world.lastStructuralStats.ms, 3)} ms commit`,
    );
  }

  console.log('\nCOMMAND / QUERY / MEMORY');
  const commandWorld = structuralWorld(100_000),
    command = commandWorld.world.command(
      'Damage',
      { target: Uint32Array, amount: Float32Array },
      100_000,
    ),
    before = process.memoryUsage();
  const commandStart = nowNs();
  for (let i = 0; i < 100_000; i++) command.push({ target: i, amount: 1 });
  const commandMs = Number(nowNs() - commandStart) / 1e6;
  const query = commandWorld.world.query(commandWorld.Position),
    versionBefore = query.version;
  commandWorld.world.add(commandWorld.handles[0], commandWorld.Armor);
  commandWorld.world.tick();
  query.refresh();
  const after = process.memoryUsage();
  console.log(
    `commands ${fmt(100_000 / (commandMs / 1000), 0)}/sec · ${fmt((commandMs * 1e6) / 100_000, 2)} ns/command`,
  );
  console.log(
    `query plans 1 · matching archetypes ${query.matches.length} · version ${versionBefore}→${query.version}`,
  );
  console.log(
    `ArrayBuffers ${(after.arrayBuffers - before.arrayBuffers) / 1024 / 1024} MiB delta · RSS ${(after.rss - before.rss) / 1024 / 1024} MiB delta`,
  );
  console.log('\nMODE NOTES');
  console.log(
    'Profile measurements include high-resolution timing at system boundaries and are not throughput results.',
  );
  console.log(
    'Chunk sweep runs in one process; use `node --expose-gc Hermes.js --characterize` for explicit GC boundaries.',
  );
  console.log(
    'Canonical kernel comparison: `node Hermes.js 1000000 120 32768` (profiling disabled).',
  );
  console.log('\nCorrectness: run `npm test` before accepting characterization results.');
}

// =============================================================================
// Demo / benchmark when run directly
// =============================================================================

if (typeof require !== 'undefined' && typeof module !== 'undefined' && require.main === module) {
  const hasFlag = (...flags) => flags.some((flag) => process.argv.includes(flag));
  if (hasFlag('--characterize', '-characterize')) {
    characterize();
    process.exit(0);
  }
  const sweep = hasFlag('--sweep', '-sweep');
  if (sweep) {
    console.log('HERMES v0.4 chunk-size sweep (same workload per run)');
    for (const bytes of [32, 64, 128, 256, 512, 1024]) {
      const count = 1_000_000;
      const sweepWorld = new HermesWorld({ entityCapacity: count + 16, chunkBytes: bytes * 1024 });
      const P = sweepWorld.component('P', { x: Float64Array, y: Float64Array, z: Float64Array });
      const V = sweepWorld.component('V', { x: Float64Array, y: Float64Array, z: Float64Array });
      const archetype = sweepWorld.archetype(P, V);
      sweepWorld.system({
        name: 'Movement',
        query: [P, V],
        run(cols, n, dt) {
          const p = cols[0],
            v = cols[1];
          for (let i = 0; i < n; i++) {
            p.x[i] += v.x[i] * dt;
            p.y[i] += v.y[i] * dt;
            p.z[i] += v.z[i] * dt;
          }
        },
      });
      for (let i = 0; i < count; i++)
        sweepWorld.spawn(archetype, { P: { x: i * 0.001 }, V: { x: 1, y: 1, z: 1 } });
      for (let i = 0; i < 15; i++) sweepWorld.tick();
      const samples = [];
      for (let i = 0; i < 30; i++) {
        const t = nowNs();
        sweepWorld.tick();
        samples.push(Number(nowNs() - t));
      }
      samples.sort((a, b) => a - b);
      const median = samples[15];
      console.log(
        `${String(bytes).padStart(4)} KiB · chunks ${archetype.chunks.length} · ${fmt(median / 1e6, 3)} ms · ${fmt(median / count, 3)} ns/entity`,
      );
    }
    process.exit(0);
  }
  const parsePositive = (value, fallback, label) => {
    const parsed = Number(value ?? fallback);
    if (!Number.isFinite(parsed) || parsed <= 0)
      throw new Error(`Hermes: ${label} must be a positive number.`);
    return parsed;
  };
  const ENTITY_COUNT = parsePositive(process.argv[2], 1_000_000, 'entity count');
  const TICKS = parsePositive(process.argv[3], 120, 'tick count');
  const CHUNK_BYTES = parsePositive(process.argv[4], 32 * 1024, 'chunk bytes');
  const DT = 1 / 60;

  console.log('\nHERMES v0.4 — Node.js World Runtime');
  console.log('═'.repeat(72));
  console.log(`Node:          ${process.version}`);
  console.log(`Entities:      ${fmt(ENTITY_COUNT, 0)}`);
  console.log(`Ticks:         ${fmt(TICKS, 0)}`);
  console.log(`Chunk target:  ${fmt(CHUNK_BYTES / 1024, 0)} KiB`);

  const world = new HermesWorld({ entityCapacity: ENTITY_COUNT + 1024, chunkBytes: CHUNK_BYTES });

  // Float64 won the arithmetic microbenchmark on the user's V8/Zen 4 setup.
  const Position = world.component('Position', {
    x: Float64Array,
    y: Float64Array,
    z: Float64Array,
  });
  const Velocity = world.component('Velocity', {
    x: Float64Array,
    y: Float64Array,
    z: Float64Array,
  });
  const Health = world.component('Health', { value: Float32Array });
  const Team = world.component('Team', { id: Uint16Array });

  const Moving = world.archetype(Position, Velocity, Health, Team);

  const Damage = world.command(
    'Damage',
    { row: Uint32Array, amount: Float32Array },
    Math.max(1024, ENTITY_COUNT >> 4),
  );
  const Damaged = world.event(
    'Damaged',
    { entity: Uint32Array, amount: Float32Array },
    Math.max(1024, ENTITY_COUNT >> 4),
  );

  world.system({
    name: 'Movement',
    query: [Position, Velocity],
    run(cols, count, dt) {
      const p = cols[0];
      const v = cols[1];
      const px = p.x,
        py = p.y,
        pz = p.z;
      const vx = v.x,
        vy = v.y,
        vz = v.z;
      for (let i = 0; i < count; i++) {
        px[i] += vx[i] * dt;
        py[i] += vy[i] * dt;
        pz[i] += vz[i] * dt;
      }
    },
  });

  world.system({
    name: 'DamageApply',
    query: [Health],
    run(cols, count, dt, w, entityIds) {
      // Typed command table is intentionally processed only for rows belonging
      // to this single-archetype demo. A production partition will bucket
      // commands by chunk before this stage.
      if (!Damage.count) return;
      const hp = cols[0].value;
      const rows = Damage.columns.row;
      const amounts = Damage.columns.amount;
      for (let i = 0; i < Damage.count; i++) {
        const r = rows[i];
        if (r < count) hp[r] -= amounts[i];
      }
      Damage.clear();
    },
  });

  console.log('\nSpawning...');
  let t0 = nowNs();
  for (let i = 0; i < ENTITY_COUNT; i++) {
    world.spawn(Moving, {
      Position: { x: i * 0.001, y: i * 0.002, z: i * 0.003 },
      Velocity: { x: 1.1, y: 2.2, z: 3.3 },
      Health: { value: 100 },
      Team: { id: i & 7 },
    });
  }
  let t1 = nowNs();
  console.log(`Spawn time:    ${fmt(Number(t1 - t0) / 1e6)} ms`);
  console.log(`Archetypes:    ${world.archetypes.length}`);
  console.log(`Chunks:        ${Moving.chunks.length}`);
  console.log(`Chunk capacity:${fmt(Moving.chunkCapacity, 0)} entities`);
  console.log(`Bytes/entity:  ${Moving.bytesPerEntity}`);

  // Warm-up.
  for (let i = 0; i < 30; i++) world.tick(DT);

  const samples = [];
  for (let i = 0; i < TICKS; i++) {
    const a = nowNs();
    world.tick(DT);
    const b = nowNs();
    samples.push(Number(b - a));
  }
  samples.sort((a, b) => a - b);
  const median = samples[Math.floor(samples.length * 0.5)];
  const p95 = samples[Math.min(samples.length - 1, Math.floor(samples.length * 0.95))];
  const p99 = samples[Math.min(samples.length - 1, Math.floor(samples.length * 0.99))];

  console.log('\nTick benchmark');
  console.log('─'.repeat(72));
  console.log(`Median tick:   ${fmt(median / 1e6, 3)} ms`);
  console.log(`p95 tick:      ${fmt(p95 / 1e6, 3)} ms`);
  console.log(`p99 tick:      ${fmt(p99 / 1e6, 3)} ms`);
  console.log(`ns/entity:     ${fmt(median / ENTITY_COUNT, 3)} ns`);
  console.log(`ticks/sec:     ${fmt(1e9 / median, 1)}`);
  console.log(`entities/sec:  ${fmt((ENTITY_COUNT * 1e9) / median, 0)}`);
  console.log(`60Hz budget:   ${fmt((median / 16_666_666.667) * 100, 1)}%`);

  const mem = process.memoryUsage();
  console.log('\nMemory');
  console.log('─'.repeat(72));
  console.log(`ArrayBuffers:  ${fmt(mem.arrayBuffers / 1024 / 1024)} MiB`);
  console.log(`RSS:           ${fmt(mem.rss / 1024 / 1024)} MiB`);

  // Verify public handle resolution + deferred destroy.
  const testHandle = world.entities.handle(0);
  const resolved = world.entities.resolve(testHandle);
  console.log('\nSanity');
  console.log('─'.repeat(72));
  console.log(`Entity 0 resolves to: ${resolved}`);
  console.log(
    `Position[0].x:        ${Moving.chunks[0].columns[Moving.componentIndex.get(Position.id)].x[0].toFixed(3)}`,
  );
  console.log(`World ticks:          ${world.tickNumber}`);

  console.log('\nHermes is alive.');
}
