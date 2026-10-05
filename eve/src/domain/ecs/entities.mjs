export function createEntityStore() {
  const entities = new Map();
  return {
    entities,
    upsert(id, components = {}) {
      const entity = entities.get(id) || { id, components: {} };
      Object.assign(entity.components, components);
      entities.set(id, entity);
      return entity;
    },
    remove(id) {
      entities.delete(id);
    },
    query(...names) {
      return [...entities.values()].filter((e) =>
        names.every((name) => e.components[name] != null),
      );
    },
  };
}
