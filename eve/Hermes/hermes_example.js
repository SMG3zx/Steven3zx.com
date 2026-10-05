import { Hermes } from './Hermes';

const world = new Hermes.World();

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

const Health = world.component('Health', {
  value: Float32Array,
});

const Moving = world.archetype(Position, Velocity, Health);

world.system({
  name: 'Movement',
  query: [Position, Velocity],

  run(cols, count, dt) {
    const p = cols[0];
    const v = cols[1];

    for (let i = 0; i < count; i++) {
      p.x[i] += v.x[i] * dt;
      p.y[i] += v.y[i] * dt;
      p.z[i] += v.z[i] * dt;
    }
  },
});
