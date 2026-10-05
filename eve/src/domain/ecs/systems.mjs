export function statusAgeSystem({ world, dt }) {
  for (const entity of world.query('Transform', 'Status'))
    entity.components.Status.age = (entity.components.Status.age || 0) + dt;
}
