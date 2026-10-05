'use strict';

/*
===============================================================================
 NANO ECS LAB
===============================================================================

Dependency-free Node.js ECS performance laboratory.

Run:

    node nano_ecs_lab.js

Optional:

    node nano_ecs_lab.js 1000000 9

Arguments:

    [0] Maximum entity count
    [1] Number of benchmark samples

Recommended:

    node nano_ecs_lab.js 1000000 9

The lab tests:

    - Raw TypedArray floor
    - SoA XYZ combined
    - SoA XYZ split
    - Interleaved vectors
    - AoSoA/chunked execution
    - Float32 vs Float64
    - Working-set scaling
    - Entity -> row indirection
    - Direct-row commands
    - Homogeneous command switches
    - Random command switches
    - Grouped command execution
    - Sequential vs random access
    - Branch behavior

Metrics:

    median ns/op
    min ns/op
    p95 ns/op
    operations/sec
    relative slowdown vs raw baseline

IMPORTANT:

Sub-nanosecond numbers are AMORTIZED throughput numbers.

They do NOT mean a JavaScript function call literally takes 0.5 ns.

===============================================================================
*/

const os = require('node:os');

const MAX_ENTITIES = Number(process.argv[2] || 1_000_000);
const SAMPLES = Math.max(3, Number(process.argv[3] || 9));

const DT = Math.fround(1 / 60);

let sink = 0;

// =============================================================================
// Formatting
// =============================================================================

function number(n, digits = 2) {
  return Number(n).toLocaleString('en-US', {
    maximumFractionDigits: digits,
  });
}

function nsNow() {
  return process.hrtime.bigint();
}

function percentile(sorted, p) {
  const i = Math.min(sorted.length - 1, Math.floor((sorted.length - 1) * p));

  return sorted[i];
}

function hr(char = '─', n = 79) {
  console.log(char.repeat(n));
}

function title(text) {
  console.log('');
  hr('═');
  console.log(text);
  hr('═');
}

function section(text) {
  console.log('');
  console.log(text);
  hr();
}

function padRight(value, length) {
  value = String(value);
  return value + ' '.repeat(Math.max(0, length - value.length));
}

function padLeft(value, length) {
  value = String(value);
  return ' '.repeat(Math.max(0, length - value.length)) + value;
}

// =============================================================================
// Benchmark engine
// =============================================================================

const results = [];

function bench({ name, operations, fn, warmup = 5, iterations = 1, baseline = null }) {
  for (let i = 0; i < warmup; ++i) {
    fn();
  }

  const samples = [];

  for (let s = 0; s < SAMPLES; ++s) {
    const start = nsNow();

    for (let i = 0; i < iterations; ++i) {
      fn();
    }

    const end = nsNow();

    const elapsed = Number(end - start);

    samples.push(elapsed / (operations * iterations));
  }

  samples.sort((a, b) => a - b);

  const min = samples[0];
  const median = percentile(samples, 0.5);
  const p95 = percentile(samples, 0.95);

  const opsPerSecond = 1e9 / median;

  const result = {
    name,
    min,
    median,
    p95,
    opsPerSecond,
    slowdown: baseline ? median / baseline.median : 1,
  };

  results.push(result);

  console.log(
    padRight(name, 32) +
      padLeft(number(median, 3), 12) +
      ' ns   ' +
      padLeft(number(opsPerSecond, 0), 18) +
      '/s',
  );

  return result;
}

// =============================================================================
// System information
// =============================================================================

title('NANO ECS LAB');

console.log(`Node:             ${process.version}`);
console.log(`V8:               ${process.versions.v8}`);
console.log(`Platform:         ${process.platform} ${process.arch}`);
console.log(`CPU:              ${os.cpus()[0]?.model || 'unknown'}`);
console.log(`Logical CPUs:     ${os.cpus().length}`);
console.log(`Max entities:     ${number(MAX_ENTITIES, 0)}`);
console.log(`Samples/test:     ${SAMPLES}`);
console.log(`PID:              ${process.pid}`);

// =============================================================================
// Data
// =============================================================================

console.log('');
console.log('Allocating test data...');

const px = new Float32Array(MAX_ENTITIES);
const py = new Float32Array(MAX_ENTITIES);
const pz = new Float32Array(MAX_ENTITIES);

const vx = new Float32Array(MAX_ENTITIES);
const vy = new Float32Array(MAX_ENTITIES);
const vz = new Float32Array(MAX_ENTITIES);

const health = new Float32Array(MAX_ENTITIES);

const interleavedPosition = new Float32Array(MAX_ENTITIES * 3);
const interleavedVelocity = new Float32Array(MAX_ENTITIES * 3);

const f64A = new Float64Array(MAX_ENTITIES);
const f64B = new Float64Array(MAX_ENTITIES);

const entityToRow = new Uint32Array(MAX_ENTITIES);

for (let i = 0; i < MAX_ENTITIES; ++i) {
  px[i] = i * 0.001;
  py[i] = i * 0.002;
  pz[i] = i * 0.003;

  vx[i] = 1.1;
  vy[i] = 2.2;
  vz[i] = 3.3;

  health[i] = 100;

  const j = i * 3;

  interleavedPosition[j] = px[i];
  interleavedPosition[j + 1] = py[i];
  interleavedPosition[j + 2] = pz[i];

  interleavedVelocity[j] = vx[i];
  interleavedVelocity[j + 1] = vy[i];
  interleavedVelocity[j + 2] = vz[i];

  f64A[i] = i * 0.001;
  f64B[i] = 1.1;

  entityToRow[i] = i;
}

// =============================================================================
// Raw baseline
// =============================================================================

section('1. RAW TYPEDARRAY FLOOR');

console.log(padRight('Test', 32) + padLeft('Median', 12) + '      ' + padLeft('Throughput', 18));

hr();

const rawBaseline = bench({
  name: 'Float32 add',
  operations: MAX_ENTITIES,
  iterations: 10,

  fn() {
    const a = px;
    const b = vx;
    const n = MAX_ENTITIES;

    for (let i = 0; i < n; ++i) {
      a[i] += b[i];
    }

    sink += a[n - 1];
  },
});

bench({
  name: 'Float32 multiply + add',
  operations: MAX_ENTITIES,
  iterations: 10,
  baseline: rawBaseline,

  fn() {
    const a = px;
    const b = vx;
    const n = MAX_ENTITIES;
    const dt = DT;

    for (let i = 0; i < n; ++i) {
      a[i] += b[i] * dt;
    }

    sink += a[n - 1];
  },
});

bench({
  name: 'Float64 multiply + add',
  operations: MAX_ENTITIES,
  iterations: 10,
  baseline: rawBaseline,

  fn() {
    const a = f64A;
    const b = f64B;
    const n = MAX_ENTITIES;
    const dt = 1 / 60;

    for (let i = 0; i < n; ++i) {
      a[i] += b[i] * dt;
    }

    sink += a[n - 1];
  },
});

// =============================================================================
// Movement layouts
// =============================================================================

section('2. MOVEMENT LAYOUT');

let soaCombined;

bench({
  name: 'SoA X only',
  operations: MAX_ENTITIES,
  iterations: 5,
  baseline: rawBaseline,

  fn() {
    const x = px;
    const dx = vx;
    const dt = DT;
    const n = MAX_ENTITIES;

    for (let i = 0; i < n; ++i) {
      x[i] += dx[i] * dt;
    }

    sink += x[n - 1];
  },
});

bench({
  name: 'SoA Y only',
  operations: MAX_ENTITIES,
  iterations: 5,
  baseline: rawBaseline,

  fn() {
    const y = py;
    const dy = vy;
    const dt = DT;
    const n = MAX_ENTITIES;

    for (let i = 0; i < n; ++i) {
      y[i] += dy[i] * dt;
    }

    sink += y[n - 1];
  },
});

bench({
  name: 'SoA Z only',
  operations: MAX_ENTITIES,
  iterations: 5,
  baseline: rawBaseline,

  fn() {
    const z = pz;
    const dz = vz;
    const dt = DT;
    const n = MAX_ENTITIES;

    for (let i = 0; i < n; ++i) {
      z[i] += dz[i] * dt;
    }

    sink += z[n - 1];
  },
});

bench({
  name: 'SoA XY combined',
  operations: MAX_ENTITIES,
  iterations: 5,
  baseline: rawBaseline,

  fn() {
    const x = px;
    const y = py;

    const dx = vx;
    const dy = vy;

    const dt = DT;
    const n = MAX_ENTITIES;

    for (let i = 0; i < n; ++i) {
      x[i] += dx[i] * dt;
      y[i] += dy[i] * dt;
    }

    sink += x[n - 1] + y[n - 1];
  },
});

soaCombined = bench({
  name: 'SoA XYZ combined',
  operations: MAX_ENTITIES,
  iterations: 5,
  baseline: rawBaseline,

  fn() {
    const x = px;
    const y = py;
    const z = pz;

    const dx = vx;
    const dy = vy;
    const dz = vz;

    const dt = DT;
    const n = MAX_ENTITIES;

    for (let i = 0; i < n; ++i) {
      x[i] += dx[i] * dt;
      y[i] += dy[i] * dt;
      z[i] += dz[i] * dt;
    }

    sink += x[n - 1] + y[n - 1] + z[n - 1];
  },
});

const soaSplit = bench({
  name: 'SoA XYZ split loops',
  operations: MAX_ENTITIES,
  iterations: 5,
  baseline: rawBaseline,

  fn() {
    const x = px;
    const y = py;
    const z = pz;

    const dx = vx;
    const dy = vy;
    const dz = vz;

    const dt = DT;
    const n = MAX_ENTITIES;

    for (let i = 0; i < n; ++i) {
      x[i] += dx[i] * dt;
    }

    for (let i = 0; i < n; ++i) {
      y[i] += dy[i] * dt;
    }

    for (let i = 0; i < n; ++i) {
      z[i] += dz[i] * dt;
    }

    sink += x[n - 1] + y[n - 1] + z[n - 1];
  },
});

const interleaved = bench({
  name: 'Interleaved XYZ',
  operations: MAX_ENTITIES,
  iterations: 5,
  baseline: rawBaseline,

  fn() {
    const p = interleavedPosition;
    const v = interleavedVelocity;

    const dt = DT;
    const n = MAX_ENTITIES * 3;

    for (let i = 0; i < n; ++i) {
      p[i] += v[i] * dt;
    }

    sink += p[n - 1];
  },
});

// =============================================================================
// Chunk / AoSoA simulation
// =============================================================================

section('3. CHUNK SIZE / AoSoA-LIKE EXECUTION');

const chunkSizes = [32, 64, 128, 256, 512, 1024, 2048, 4096, 16384];

const chunkResults = [];

for (const chunkSize of chunkSizes) {
  const result = bench({
    name: `Chunk ${chunkSize}`,
    operations: MAX_ENTITIES,
    iterations: 3,
    baseline: rawBaseline,

    fn() {
      const x = px;
      const y = py;
      const z = pz;

      const dx = vx;
      const dy = vy;
      const dz = vz;

      const dt = DT;
      const n = MAX_ENTITIES;

      for (let start = 0; start < n; start += chunkSize) {
        const end = Math.min(start + chunkSize, n);

        for (let i = start; i < end; ++i) {
          x[i] += dx[i] * dt;
          y[i] += dy[i] * dt;
          z[i] += dz[i] * dt;
        }
      }

      sink += x[n - 1];
    },
  });

  chunkResults.push({
    chunkSize,
    result,
  });
}

// =============================================================================
// Working-set scaling
// =============================================================================

section('4. WORKING-SET SCALING');

const workingSets = [
  1_000, 10_000, 100_000, 250_000, 500_000, 1_000_000, 2_000_000, 5_000_000, 10_000_000,
].filter((n) => n <= MAX_ENTITIES);

const workingResults = [];

for (const n of workingSets) {
  const iterations = n <= 10_000 ? 1000 : n <= 100_000 ? 100 : n <= 1_000_000 ? 10 : 3;

  const result = bench({
    name: `${number(n, 0)} entities`,
    operations: n,
    iterations,
    baseline: rawBaseline,

    fn() {
      const x = px;
      const dx = vx;
      const dt = DT;

      for (let i = 0; i < n; ++i) {
        x[i] += dx[i] * dt;
      }

      sink += x[n - 1];
    },
  });

  workingResults.push({
    n,
    result,
  });
}

// =============================================================================
// Command data
// =============================================================================

section('5. COMMAND EXECUTION');

const COMMAND_COUNT = Math.min(250_000, MAX_ENTITIES);

const OP_DAMAGE = 0;
const OP_HEAL = 1;
const OP_VELOCITY = 2;
const OP_NOP = 3;

const commandOpcode = new Uint8Array(COMMAND_COUNT);
const commandEntity = new Uint32Array(COMMAND_COUNT);
const commandRow = new Uint32Array(COMMAND_COUNT);
const commandValue = new Float32Array(COMMAND_COUNT);

for (let i = 0; i < COMMAND_COUNT; ++i) {
  commandEntity[i] = i;
  commandRow[i] = i;
  commandValue[i] = 0.001;
}

// Homogeneous command stream.

commandOpcode.fill(OP_DAMAGE);

const homogeneousSwitch = bench({
  name: 'Homogeneous switch',
  operations: COMMAND_COUNT,
  iterations: 20,
  baseline: rawBaseline,

  fn() {
    const op = commandOpcode;
    const entity = commandEntity;
    const value = commandValue;

    const rows = entityToRow;
    const hp = health;

    const n = COMMAND_COUNT;

    for (let i = 0; i < n; ++i) {
      const row = rows[entity[i]];

      switch (op[i]) {
        case OP_DAMAGE:
          hp[row] -= value[i];
          break;

        case OP_HEAL:
          hp[row] += value[i];
          break;

        case OP_VELOCITY:
          vx[row] = value[i];
          break;

        default:
          break;
      }
    }

    sink += hp[n - 1];
  },
});

// Deterministic mixed command stream.
//
// xorshift avoids Math.random() affecting setup.

let rng = 0x12345678;

function xorshift() {
  rng ^= rng << 13;
  rng ^= rng >>> 17;
  rng ^= rng << 5;

  return rng >>> 0;
}

for (let i = 0; i < COMMAND_COUNT; ++i) {
  commandOpcode[i] = xorshift() & 3;
}

const mixedSwitch = bench({
  name: 'Mixed/random switch',
  operations: COMMAND_COUNT,
  iterations: 20,
  baseline: rawBaseline,

  fn() {
    const op = commandOpcode;
    const entity = commandEntity;
    const value = commandValue;

    const rows = entityToRow;
    const hp = health;
    const velocityX = vx;

    const n = COMMAND_COUNT;

    for (let i = 0; i < n; ++i) {
      const row = rows[entity[i]];

      switch (op[i]) {
        case OP_DAMAGE:
          hp[row] -= value[i];
          break;

        case OP_HEAL:
          hp[row] += value[i];
          break;

        case OP_VELOCITY:
          velocityX[row] = value[i];
          break;

        default:
          break;
      }
    }

    sink += hp[n - 1];
  },
});

// Direct-row stream.
//
// This removes:
//
//     entity -> row -> component
//
// and tests:
//
//     row -> component

const directRow = bench({
  name: 'Direct row damage',
  operations: COMMAND_COUNT,
  iterations: 20,
  baseline: rawBaseline,

  fn() {
    const rows = commandRow;
    const value = commandValue;
    const hp = health;

    const n = COMMAND_COUNT;

    for (let i = 0; i < n; ++i) {
      hp[rows[i]] -= value[i];
    }

    sink += hp[n - 1];
  },
});

// Entity indirection without switch.

const entityIndirect = bench({
  name: 'Entity -> row damage',
  operations: COMMAND_COUNT,
  iterations: 20,
  baseline: rawBaseline,

  fn() {
    const entities = commandEntity;
    const rows = entityToRow;

    const value = commandValue;
    const hp = health;

    const n = COMMAND_COUNT;

    for (let i = 0; i < n; ++i) {
      const row = rows[entities[i]];
      hp[row] -= value[i];
    }

    sink += hp[n - 1];
  },
});

// =============================================================================
// Grouped commands
// =============================================================================

section('6. GROUPED COMMAND TABLES');

let damageCount = 0;
let healCount = 0;
let velocityCount = 0;

const damageRow = new Uint32Array(COMMAND_COUNT);
const damageValue = new Float32Array(COMMAND_COUNT);

const healRow = new Uint32Array(COMMAND_COUNT);
const healValue = new Float32Array(COMMAND_COUNT);

const velocityRow = new Uint32Array(COMMAND_COUNT);
const velocityValue = new Float32Array(COMMAND_COUNT);

for (let i = 0; i < COMMAND_COUNT; ++i) {
  const op = commandOpcode[i];

  if (op === OP_DAMAGE) {
    damageRow[damageCount] = commandRow[i];
    damageValue[damageCount] = commandValue[i];
    damageCount++;
  } else if (op === OP_HEAL) {
    healRow[healCount] = commandRow[i];
    healValue[healCount] = commandValue[i];
    healCount++;
  } else if (op === OP_VELOCITY) {
    velocityRow[velocityCount] = commandRow[i];
    velocityValue[velocityCount] = commandValue[i];
    velocityCount++;
  }
}

const groupedOperations = damageCount + healCount + velocityCount;

const grouped = bench({
  name: 'Grouped command tables',
  operations: groupedOperations,
  iterations: 20,
  baseline: rawBaseline,

  fn() {
    const hp = health;
    const velocityX = vx;

    for (let i = 0; i < damageCount; ++i) {
      hp[damageRow[i]] -= damageValue[i];
    }

    for (let i = 0; i < healCount; ++i) {
      hp[healRow[i]] += healValue[i];
    }

    for (let i = 0; i < velocityCount; ++i) {
      velocityX[velocityRow[i]] = velocityValue[i];
    }

    sink += hp[damageRow[damageCount - 1] || 0];
  },
});

console.log('');
console.log(`Damage commands:   ${number(damageCount, 0)}`);
console.log(`Heal commands:     ${number(healCount, 0)}`);
console.log(`Velocity commands: ${number(velocityCount, 0)}`);

// =============================================================================
// Sequential vs random component access
// =============================================================================

section('7. MEMORY ACCESS PATTERN');

const ACCESS_COUNT = Math.min(500_000, MAX_ENTITIES);

const sequentialRows = new Uint32Array(ACCESS_COUNT);
const randomRows = new Uint32Array(ACCESS_COUNT);

for (let i = 0; i < ACCESS_COUNT; ++i) {
  sequentialRows[i] = i;
  randomRows[i] = i;
}

// Fisher-Yates shuffle.

for (let i = ACCESS_COUNT - 1; i > 0; --i) {
  const j = xorshift() % (i + 1);

  const temp = randomRows[i];
  randomRows[i] = randomRows[j];
  randomRows[j] = temp;
}

const sequentialAccess = bench({
  name: 'Sequential component access',
  operations: ACCESS_COUNT,
  iterations: 20,
  baseline: rawBaseline,

  fn() {
    const hp = health;
    const n = ACCESS_COUNT;

    for (let i = 0; i < n; ++i) {
      hp[i] -= 0.00001;
    }

    sink += hp[n - 1];
  },
});

const indexedSequential = bench({
  name: 'Indexed sequential rows',
  operations: ACCESS_COUNT,
  iterations: 20,
  baseline: rawBaseline,

  fn() {
    const hp = health;
    const rows = sequentialRows;
    const n = ACCESS_COUNT;

    for (let i = 0; i < n; ++i) {
      hp[rows[i]] -= 0.00001;
    }

    sink += hp[rows[n - 1]];
  },
});

const randomAccess = bench({
  name: 'Random component rows',
  operations: ACCESS_COUNT,
  iterations: 20,
  baseline: rawBaseline,

  fn() {
    const hp = health;
    const rows = randomRows;
    const n = ACCESS_COUNT;

    for (let i = 0; i < n; ++i) {
      hp[rows[i]] -= 0.00001;
    }

    sink += hp[rows[n - 1]];
  },
});

// =============================================================================
// Branch tests
// =============================================================================

section('8. BRANCH BEHAVIOR');

const branchData = new Uint8Array(MAX_ENTITIES);

branchData.fill(1);

const predictableBranch = bench({
  name: 'Predictable branch',
  operations: MAX_ENTITIES,
  iterations: 10,
  baseline: rawBaseline,

  fn() {
    const branch = branchData;
    const hp = health;
    const n = MAX_ENTITIES;

    for (let i = 0; i < n; ++i) {
      if (branch[i]) {
        hp[i] -= 0.00001;
      }
    }

    sink += hp[n - 1];
  },
});

for (let i = 0; i < MAX_ENTITIES; ++i) {
  branchData[i] = xorshift() & 1;
}

const randomBranch = bench({
  name: 'Random branch',
  operations: MAX_ENTITIES,
  iterations: 10,
  baseline: rawBaseline,

  fn() {
    const branch = branchData;
    const hp = health;
    const n = MAX_ENTITIES;

    for (let i = 0; i < n; ++i) {
      if (branch[i]) {
        hp[i] -= 0.00001;
      }
    }

    sink += hp[n - 1];
  },
});

// =============================================================================
// Analysis
// =============================================================================

title('AUTOMATIC ANALYSIS');

function ratio(a, b) {
  return a.median / b.median;
}

function improvement(oldResult, newResult) {
  return ((oldResult.median - newResult.median) / oldResult.median) * 100;
}

console.log('');

console.log(`Raw Float32 floor:             ` + `${number(rawBaseline.median, 3)} ns/op`);

console.log(`SoA XYZ combined:              ` + `${number(soaCombined.median, 3)} ns/entity`);

console.log(`SoA XYZ split:                 ` + `${number(soaSplit.median, 3)} ns/entity`);

console.log(`Interleaved XYZ:               ` + `${number(interleaved.median, 3)} ns/entity`);

console.log('');

if (soaSplit.median < soaCombined.median) {
  console.log(
    `✓ Splitting XYZ loops improved movement by ` +
      `${number(improvement(soaCombined, soaSplit), 1)}%.`,
  );
} else {
  console.log(`• Combined XYZ is at least as fast as split loops.`);
}

if (interleaved.median < soaCombined.median) {
  console.log(
    `✓ Interleaved vectors improved movement by ` +
      `${number(improvement(soaCombined, interleaved), 1)}%.`,
  );
} else {
  console.log(`• SoA beat the interleaved vector representation.`);
}

// Best chunk.

let bestChunk = chunkResults[0];

for (const candidate of chunkResults) {
  if (candidate.result.median < bestChunk.result.median) {
    bestChunk = candidate;
  }
}

console.log(
  `• Best tested chunk size:      ` +
    `${bestChunk.chunkSize} entities ` +
    `(${number(bestChunk.result.median, 3)} ns/entity)`,
);

console.log('');

// Commands.

console.log(`Homogeneous command switch:    ` + `${number(homogeneousSwitch.median, 3)} ns/cmd`);

console.log(`Mixed command switch:          ` + `${number(mixedSwitch.median, 3)} ns/cmd`);

console.log(`Entity -> row command:         ` + `${number(entityIndirect.median, 3)} ns/cmd`);

console.log(`Direct-row command:            ` + `${number(directRow.median, 3)} ns/cmd`);

console.log(`Grouped command table:         ` + `${number(grouped.median, 3)} ns/cmd`);

console.log('');

if (mixedSwitch.median > homogeneousSwitch.median * 1.25) {
  console.log(`✓ Mixed opcodes significantly hurt command throughput.`);
}

if (directRow.median < entityIndirect.median) {
  console.log(
    `✓ Resolving entity IDs before execution saves about ` +
      `${number(improvement(entityIndirect, directRow), 1)}%.`,
  );
}

if (grouped.median < mixedSwitch.median) {
  console.log(
    `✓ Grouping commands by type saves about ` +
      `${number(improvement(mixedSwitch, grouped), 1)}% ` +
      `versus the mixed switch stream.`,
  );
}

// Access patterns.

console.log('');

console.log(`Sequential access:             ` + `${number(sequentialAccess.median, 3)} ns/op`);

console.log(`Indexed sequential access:     ` + `${number(indexedSequential.median, 3)} ns/op`);

console.log(`Random access:                 ` + `${number(randomAccess.median, 3)} ns/op`);

if (randomAccess.median > sequentialAccess.median * 1.5) {
  console.log(
    `✓ Cache locality is important on this machine: random ` +
      `access is ${number(ratio(randomAccess, sequentialAccess), 2)}× slower.`,
  );
}

// Branches.

console.log('');

console.log(`Predictable branch:            ` + `${number(predictableBranch.median, 3)} ns/op`);

console.log(`Random branch:                 ` + `${number(randomBranch.median, 3)} ns/op`);

if (randomBranch.median > predictableBranch.median * 1.25) {
  console.log(
    `✓ Unpredictable branching costs about ` +
      `${number(ratio(randomBranch, predictableBranch), 2)}× on this workload.`,
  );
}

// Working set.

console.log('');
console.log('Working-set curve:');

for (const entry of workingResults) {
  console.log(
    `  ${padLeft(number(entry.n, 0), 12)} entities  ` +
      `${padLeft(number(entry.result.median, 3), 8)} ns/op`,
  );
}

// =============================================================================
// Architecture recommendation
// =============================================================================

title('CURRENT DESIGN DIRECTION');

console.log(`
Based on the measurements above, the runtime we are moving toward is:

                    Node.js / V8
                          │
                    World Runtime
                          │
              ┌───────────┴───────────┐
              │                       │
         WorldPartition          WorldPartition
         SINGLE WRITER           SINGLE WRITER
              │                       │
              ▼                       ▼
        Archetype Chunks        Archetype Chunks
              │
              │ contiguous
              ▼
        ┌─────────────────┐
        │ Component Data  │
        │                 │
        │ TypedArrays     │
        │ vector columns  │
        └────────┬────────┘
                 │
                 ▼
          Compiled Systems
                 │
                 │
                 ▼
        tight sequential loops

Commands:

        External Entity ID
                │
                ▼
           resolve once
                │
                ▼
          Internal Row ID
                │
                ▼
        ┌───────────────────┐
        │ Command Tables    │
        │                   │
        │ Damage[]          │
        │ Heal[]            │
        │ Movement[]        │
        │ Spawn[]           │
        └─────────┬─────────┘
                  │
                  ▼
          branchless batches

Cold path:

            ECS Commit
                │
                ▼
              WAL
                │
        ┌───────┴───────┐
        ▼               ▼
     Snapshot          Events
        │               │
        ▼               ▼
     Parquet          Parquet
        │               │
        └───────┬───────┘
                ▼
             DuckDB

Next stage after single-core optimization:

        SharedArrayBuffer
                │
        lock-free SPSC rings
                │
      ┌─────────┼─────────┐
      ▼         ▼         ▼
   Worker 0  Worker 1  Worker 2
      │         │         │
 partitions partitions partitions
`);

console.log('Benchmark sink:', Number.isFinite(sink) ? sink.toFixed(3) : sink);

console.log('');

const memory = process.memoryUsage();

console.log(`ArrayBuffer memory: ${number(memory.arrayBuffers / 1024 / 1024, 2)} MB`);

console.log(`RSS:                ${number(memory.rss / 1024 / 1024, 2)} MB`);

console.log('');
console.log('Done.');
