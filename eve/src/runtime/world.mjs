// EVE application-facing world facade. Hermes owns hot numeric state.
import { HermesWorld, Phase } from './hermes.mjs';

const MAX_ENTITIES = 250_000;

function createRecord(id, components) {
  return { id, components: { ...components } };
}

export function createWorld(options = {}) {
  const runtime = new HermesWorld({
    entityCapacity: options.entityCapacity ?? MAX_ENTITIES,
    chunkBytes: options.chunkBytes ?? 32 * 1024,
  });
  const Transform = runtime.component('Transform', {
    x: Float32Array,
    y: Float32Array,
    z: Float32Array,
  });
  const Status = runtime.component('StatusRuntime', { age: Float32Array });
  const archetype = runtime.archetype(Transform, Status);
  const records = new Map();
  const handles = new Map();
  const systems = [];

  const syncRecord = (record) => {
    const handle = handles.get(record.id);
    const location = handle === undefined ? null : runtime.location(handle);
    if (!location) return record;
    const transform = location.chunk.columns[location.archetype.componentIndex.get(Transform.id)];
    const status = location.chunk.columns[location.archetype.componentIndex.get(Status.id)];
    record.components.Transform = {
      ...(record.components.Transform || {}),
      x: transform.x[location.row],
      y: transform.y[location.row],
      z: transform.z[location.row],
    };
    record.components.Status = {
      ...(record.components.Status || {}),
      age: status.age[location.row],
    };
    return record;
  };

  runtime.system({
    name: 'StatusAge',
    phase: Phase.UPDATE,
    query: [Transform, Status],
    read: [Status],
    write: [Status],
    run(columns, count, dt) {
      const status = columns[1];
      for (let i = 0; i < count; i++) status.age[i] += dt;
    },
  });

  const facade = {
    runtime,
    entities: records,
    systems,
    Transform,
    Status,
    archetype,
    upsert(id, components = {}) {
      let record = records.get(id);
      if (!record) {
        record = createRecord(id, components);
        records.set(id, record);
        const transform = components.Transform || {};
        const status = components.Status || {};
        const handle = runtime.spawn(archetype, {
          Transform: { x: transform.x || 0, y: transform.y || 0, z: transform.z || 0 },
          Status: { age: status.age || 0 },
        });
        runtime.bindExternal(id, handle);
        handles.set(id, handle);
      } else {
        Object.assign(record.components, components);
        const transform = components.Transform;
        if (transform)
          runtime.updateMany([handles.get(id)], Transform, {
            x: transform.x ?? 0,
            y: transform.y ?? 0,
            z: transform.z ?? 0,
          });
        const status = components.Status;
        if (status?.age !== undefined)
          runtime.updateMany([handles.get(id)], Status, { age: status.age });
      }
      return syncRecord(record);
    },
    remove(id) {
      const handle = handles.get(id);
      if (handle !== undefined) runtime.destroy(handle);
      handles.delete(id);
      records.delete(id);
    },
    query(...names) {
      const result = [];
      for (const record of records.values())
        if (names.every((name) => record.components[name] != null)) result.push(syncRecord(record));
      return result;
    },
    addSystem(system) {
      if (system?.name === 'statusAgeSystem') return () => {};
      systems.push(system);
      return () => {
        const index = systems.indexOf(system);
        if (index >= 0) systems.splice(index, 1);
      };
    },
    tick(dt, context) {
      for (const system of systems) system({ dt, world: facade, context });
      const frame = runtime.step(dt);
      for (const record of records.values()) syncRecord(record);
      return frame;
    },
    step(dt, context) {
      return this.tick(dt, context);
    },
    stats() {
      return runtime.stats();
    },
  };
  return facade;
}
