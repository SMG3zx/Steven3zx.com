import test from 'node:test';
import assert from 'node:assert/strict';
import { createWorld } from '../src/ecs.mjs';

test('ECS stores queryable state independently of render views', () => {
  const world = createWorld();
  assert.equal(world.runtime.constructor.name, 'HermesWorld');
  world.upsert('rack-1', { Transform: { x: 1 }, Status: { value: 'passed' } });
  assert.equal(world.query('Transform', 'Status')[0].components.Status.value, 'passed');
  world.addSystem(({ world, dt }) => {
    world.entities.get('rack-1').components.Status.elapsed = dt;
  });
  const frame = world.step(0.25);
  assert.equal(frame.tick, 1);
  assert.equal(world.entities.get('rack-1').components.Status.elapsed, 0.25);
  world.remove('rack-1');
  assert.equal(world.entities.size, 0);
});
