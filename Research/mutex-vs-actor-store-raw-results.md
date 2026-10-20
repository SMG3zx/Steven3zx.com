# Mutex vs actor: raw results

Background for [mutex-vs-actor-store.md](mutex-vs-actor-store.md). Every cell is the median of 3 runs.

Go go1.27.1, windows/amd64, GOMAXPROCS=12, 3 reps of 1.5s per cell, median by throughput. Spread is (max-min)/median ops/s across the reps.

## read95

95% Get, 5% Update, 10k keys, uniform

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 30.29M | 112ns | 112ns | 224ns | 0.0 | 4% |
| mutex/rwmutex | 29.15M | 112ns | 112ns | 224ns | 0.1 | 17% |
| mutex/sharded-1024 | 20.24M | 112ns | 224ns | 320ns | 0.1 | 8% |
| chan/single-owner | 1.48M | 448ns | 4.096µs | 7.168µs | 0.0 | 3% |
| chan/sharded-256 | 1.39M | 512ns | 4.096µs | 8.192µs | 0.0 | 5% |
| actor/janus-today | 543.1k | 1.536µs | 7.168µs | 14.336µs | 4.1 | 4% |
| actor/single-tuned | 867.1k | 768ns | 5.12µs | 10.24µs | 1.1 | 0% |
| actor/sharded-12-ask | 750.1k | 1.024µs | 6.144µs | 12.288µs | 4.0 | 15% |
| actor/sharded-12-tuned | 1.23M | 640ns | 5.12µs | 8.192µs | 0.0 | 38% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 11.50M | 112ns | 20.48µs | 49.152µs | 0.0 | 4% |
| mutex/rwmutex | 9.31M | 112ns | 20.48µs | 81.92µs | 0.0 | 1% |
| mutex/sharded-1024 | 73.33M | 112ns | 224ns | 640ns | 0.0 | 3% |
| chan/single-owner | 3.85M | 1.792µs | 12.288µs | 24.576µs | 0.0 | 4% |
| chan/sharded-256 | 15.21M | 512ns | 1.28µs | 5.12µs | 0.0 | 0% |
| actor/janus-today | 2.74M | 2.56µs | 12.288µs | 65.536µs | 4.1 | 4% |
| actor/single-tuned | 3.50M | 2.048µs | 7.168µs | 40.96µs | 1.1 | 3% |
| actor/sharded-12-ask | 3.96M | 1.792µs | 8.192µs | 65.536µs | 4.1 | 5% |
| actor/sharded-12-tuned | 6.37M | 1.28µs | 4.096µs | 20.48µs | 0.0 | 7% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 9.99M | 112ns | 196.608µs | 393.216µs | 0.1 | 2% |
| mutex/rwmutex | 8.94M | 112ns | 49.152µs | 1.048576ms | 0.1 | 6% |
| mutex/sharded-1024 | 103.66M | 112ns | 224ns | 196.608µs | 0.0 | 10% |
| chan/single-owner | 4.32M | 14.336µs | 40.96µs | 163.84µs | 0.1 | 6% |
| chan/sharded-256 | 16.78M | 640ns | 114.688µs | 458.752µs | 0.0 | 11% |
| actor/janus-today | 3.17M | 20.48µs | 81.92µs | 262.144µs | 4.1 | 7% |
| actor/single-tuned | 4.70M | 14.336µs | 40.96µs | 196.608µs | 1.1 | 7% |
| actor/sharded-12-ask | 5.63M | 2.048µs | 131.072µs | 327.68µs | 4.1 | 10% |
| actor/sharded-12-tuned | 10.29M | 1.536µs | 49.152µs | 114.688µs | 0.1 | 5% |

## mixed50

50% Get, 50% Update, 10k keys, uniform

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 21.00M | 112ns | 224ns | 448ns | 0.5 | 22% |
| mutex/rwmutex | 22.66M | 112ns | 224ns | 448ns | 0.5 | 5% |
| mutex/sharded-1024 | 21.57M | 112ns | 224ns | 320ns | 0.5 | 3% |
| chan/single-owner | 2.01M | 320ns | 3.072µs | 6.144µs | 0.5 | 1% |
| chan/sharded-256 | 1.84M | 448ns | 3.072µs | 5.12µs | 0.5 | 5% |
| actor/janus-today | 820.9k | 896ns | 5.12µs | 10.24µs | 4.5 | 2% |
| actor/single-tuned | 1.26M | 512ns | 3.584µs | 7.168µs | 1.5 | 5% |
| actor/sharded-12-ask | 754.9k | 1.024µs | 5.12µs | 10.24µs | 4.5 | 7% |
| actor/sharded-12-tuned | 1.33M | 512ns | 4.096µs | 7.168µs | 0.5 | 5% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 9.24M | 112ns | 28.672µs | 57.344µs | 0.5 | 4% |
| mutex/rwmutex | 7.48M | 112ns | 32.768µs | 65.536µs | 0.5 | 3% |
| mutex/sharded-1024 | 49.79M | 112ns | 320ns | 8.192µs | 0.5 | 2% |
| chan/single-owner | 3.70M | 2.048µs | 12.288µs | 24.576µs | 0.5 | 3% |
| chan/sharded-256 | 14.09M | 512ns | 1.536µs | 12.288µs | 0.5 | 1% |
| actor/janus-today | 2.51M | 3.072µs | 16.384µs | 81.92µs | 4.5 | 9% |
| actor/single-tuned | 3.24M | 2.56µs | 8.192µs | 57.344µs | 1.5 | 5% |
| actor/sharded-12-ask | 3.70M | 1.792µs | 10.24µs | 98.304µs | 4.5 | 3% |
| actor/sharded-12-tuned | 6.11M | 1.28µs | 4.096µs | 20.48µs | 0.5 | 5% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 8.29M | 224ns | 196.608µs | 393.216µs | 0.5 | 2% |
| mutex/rwmutex | 7.34M | 112ns | 262.144µs | 655.36µs | 0.5 | 3% |
| mutex/sharded-1024 | 62.67M | 112ns | 1.28µs | 262.144µs | 0.5 | 4% |
| chan/single-owner | 4.27M | 14.336µs | 40.96µs | 114.688µs | 0.5 | 4% |
| chan/sharded-256 | 14.19M | 640ns | 131.072µs | 655.36µs | 0.5 | 12% |
| actor/janus-today | 3.00M | 20.48µs | 98.304µs | 262.144µs | 4.5 | 2% |
| actor/single-tuned | 4.21M | 14.336µs | 57.344µs | 229.376µs | 1.5 | 9% |
| actor/sharded-12-ask | 5.60M | 2.048µs | 131.072µs | 393.216µs | 4.5 | 3% |
| actor/sharded-12-tuned | 10.31M | 1.536µs | 49.152µs | 163.84µs | 0.5 | 4% |

## write100

100% Update, 10k keys, uniform

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 17.75M | 112ns | 224ns | 448ns | 1.0 | 3% |
| mutex/rwmutex | 15.71M | 112ns | 320ns | 768ns | 1.0 | 2% |
| mutex/sharded-1024 | 13.85M | 112ns | 224ns | 768ns | 1.0 | 6% |
| chan/single-owner | 1.41M | 448ns | 4.096µs | 8.192µs | 1.0 | 3% |
| chan/sharded-256 | 1.27M | 512ns | 5.12µs | 10.24µs | 1.0 | 4% |
| actor/janus-today | 540.1k | 1.536µs | 7.168µs | 24.576µs | 5.0 | 34% |
| actor/single-tuned | 818.6k | 896ns | 5.12µs | 12.288µs | 2.0 | 2% |
| actor/sharded-12-ask | 509.8k | 1.536µs | 7.168µs | 16.384µs | 5.0 | 3% |
| actor/sharded-12-tuned | 951.4k | 768ns | 5.12µs | 10.24µs | 1.0 | 3% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 8.52M | 112ns | 40.96µs | 81.92µs | 1.0 | 1% |
| mutex/rwmutex | 8.37M | 112ns | 40.96µs | 81.92µs | 1.0 | 4% |
| mutex/sharded-1024 | 40.74M | 112ns | 512ns | 28.672µs | 1.0 | 5% |
| chan/single-owner | 3.68M | 2.048µs | 10.24µs | 24.576µs | 1.0 | 3% |
| chan/sharded-256 | 13.06M | 512ns | 1.792µs | 24.576µs | 1.0 | 3% |
| actor/janus-today | 2.64M | 2.56µs | 14.336µs | 65.536µs | 5.0 | 4% |
| actor/single-tuned | 3.23M | 2.56µs | 8.192µs | 49.152µs | 2.0 | 2% |
| actor/sharded-12-ask | 3.73M | 2.048µs | 10.24µs | 98.304µs | 5.0 | 9% |
| actor/sharded-12-tuned | 5.92M | 1.28µs | 5.12µs | 24.576µs | 1.0 | 3% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 7.74M | 224ns | 229.376µs | 458.752µs | 1.0 | 3% |
| mutex/rwmutex | 7.73M | 224ns | 229.376µs | 524.288µs | 1.0 | 1% |
| mutex/sharded-1024 | 51.87M | 224ns | 640ns | 393.216µs | 1.0 | 7% |
| chan/single-owner | 4.25M | 14.336µs | 32.768µs | 131.072µs | 1.0 | 1% |
| chan/sharded-256 | 14.93M | 640ns | 131.072µs | 524.288µs | 1.0 | 13% |
| actor/janus-today | 2.95M | 20.48µs | 98.304µs | 262.144µs | 5.0 | 7% |
| actor/single-tuned | 4.30M | 14.336µs | 57.344µs | 229.376µs | 2.0 | 15% |
| actor/sharded-12-ask | 5.48M | 2.048µs | 131.072µs | 327.68µs | 5.0 | 11% |
| actor/sharded-12-tuned | 9.45M | 1.536µs | 57.344µs | 196.608µs | 1.0 | 5% |

## hotkey

50/50, 80% of ops on one key

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 21.57M | 112ns | 224ns | 448ns | 0.5 | 7% |
| mutex/rwmutex | 21.04M | 112ns | 224ns | 448ns | 0.5 | 3% |
| mutex/sharded-1024 | 20.35M | 112ns | 224ns | 448ns | 0.5 | 3% |
| chan/single-owner | 1.49M | 448ns | 4.096µs | 7.168µs | 0.5 | 1% |
| chan/sharded-256 | 1.49M | 448ns | 4.096µs | 7.168µs | 0.5 | 2% |
| actor/janus-today | 554.9k | 1.536µs | 6.144µs | 14.336µs | 4.5 | 1% |
| actor/single-tuned | 863.8k | 768ns | 5.12µs | 10.24µs | 1.5 | 3% |
| actor/sharded-12-ask | 536.6k | 1.536µs | 7.168µs | 16.384µs | 4.5 | 1% |
| actor/sharded-12-tuned | 929.2k | 768ns | 5.12µs | 10.24µs | 0.5 | 25% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 10.88M | 112ns | 24.576µs | 57.344µs | 0.5 | 13% |
| mutex/rwmutex | 8.97M | 112ns | 28.672µs | 57.344µs | 0.5 | 2% |
| mutex/sharded-1024 | 8.89M | 112ns | 28.672µs | 57.344µs | 0.5 | 1% |
| chan/single-owner | 3.78M | 1.792µs | 14.336µs | 20.48µs | 0.5 | 5% |
| chan/sharded-256 | 4.03M | 1.536µs | 16.384µs | 28.672µs | 0.5 | 8% |
| actor/janus-today | 2.69M | 2.56µs | 12.288µs | 65.536µs | 4.5 | 2% |
| actor/single-tuned | 3.45M | 2.048µs | 8.192µs | 49.152µs | 1.5 | 2% |
| actor/sharded-12-ask | 3.25M | 2.56µs | 10.24µs | 65.536µs | 4.5 | 2% |
| actor/sharded-12-tuned | 3.99M | 1.792µs | 8.192µs | 24.576µs | 0.5 | 7% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 9.87M | 112ns | 196.608µs | 393.216µs | 0.5 | 2% |
| mutex/rwmutex | 8.47M | 112ns | 262.144µs | 655.36µs | 0.5 | 4% |
| mutex/sharded-1024 | 9.30M | 112ns | 229.376µs | 524.288µs | 0.5 | 10% |
| chan/single-owner | 4.32M | 14.336µs | 40.96µs | 114.688µs | 0.5 | 3% |
| chan/sharded-256 | 5.07M | 12.288µs | 40.96µs | 114.688µs | 0.5 | 9% |
| actor/janus-today | 3.21M | 20.48µs | 81.92µs | 229.376µs | 4.5 | 3% |
| actor/single-tuned | 4.30M | 14.336µs | 49.152µs | 196.608µs | 1.5 | 1% |
| actor/sharded-12-ask | 4.03M | 20.48µs | 81.92µs | 229.376µs | 4.5 | 3% |
| actor/sharded-12-tuned | 5.32M | 14.336µs | 32.768µs | 98.304µs | 0.5 | 1% |

## scan-blocks

95% Get, 5% Update on 100k keys, plus one goroutine scanning all 100k in a loop; latency is the foreground ops'

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 75.2k | 112ns | 2.62144ms | 3.670016ms | 0.1 | 4% |
| mutex/rwmutex | 102.2k | 112ns | 2.097152ms | 5.24288ms | 0.1 | 12% |
| mutex/sharded-1024 | 66.66M | 112ns | 224ns | 640ns | 0.0 | 14% |
| chan/single-owner | 39.6k | 640ns | 1.048576ms | 1.31072ms | 0.1 | 22% |
| chan/sharded-256 | 12.53M | 512ns | 3.584µs | 32.768µs | 0.0 | 31% |
| actor/janus-today | 24.6k | 10.24µs | 1.048576ms | 1.31072ms | 4.3 | 13% |
| actor/single-tuned | 29.8k | 4.096µs | 1.048576ms | 1.31072ms | 1.1 | 14% |
| actor/sharded-12-ask | 1.19M | 1.536µs | 98.304µs | 131.072µs | 4.1 | 7% |
| actor/sharded-12-tuned | 1.31M | 1.024µs | 114.688µs | 163.84µs | 0.0 | 15% |

Background scans per second: mutex/coarse 1495; mutex/rwmutex 1508; mutex/sharded-1024 1251; chan/single-owner 1454; chan/sharded-256 771; actor/janus-today 1433; actor/single-tuned 1468; actor/sharded-12-ask 817; actor/sharded-12-tuned 833; 

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 217.9k | 224ns | 5.24288ms | 8.388608ms | 0.1 | 8% |
| mutex/rwmutex | 190.2k | 224ns | 7.340032ms | 12.582912ms | 0.1 | 7% |
| mutex/sharded-1024 | 99.06M | 112ns | 224ns | 229.376µs | 0.1 | 11% |
| chan/single-owner | 96.5k | 786.432µs | 1.31072ms | 1.31072ms | 0.1 | 1% |
| chan/sharded-256 | 16.32M | 640ns | 131.072µs | 458.752µs | 0.0 | 3% |
| actor/janus-today | 91.2k | 786.432µs | 1.048576ms | 1.31072ms | 4.1 | 5% |
| actor/single-tuned | 93.4k | 786.432µs | 1.31072ms | 1.572864ms | 1.1 | 5% |
| actor/sharded-12-ask | 5.42M | 1.792µs | 131.072µs | 327.68µs | 4.1 | 9% |
| actor/sharded-12-tuned | 7.67M | 1.28µs | 98.304µs | 163.84µs | 0.1 | 12% |

Background scans per second: mutex/coarse 1430; mutex/rwmutex 1441; mutex/sharded-1024 818; chan/single-owner 1395; chan/sharded-256 192; actor/janus-today 1388; actor/single-tuned 1375; actor/sharded-12-ask 780; actor/sharded-12-tuned 925; 

## slow-io-25us

50/50, 10k keys, 25us of work inside the owner per op (a store bound by one database connection)

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 39.3k | 28.672µs | 28.672µs | 65.536µs | 0.5 | 0% |
| mutex/rwmutex | 39.2k | 28.672µs | 32.768µs | 114.688µs | 0.5 | 1% |
| mutex/sharded-1024 | 39.2k | 28.672µs | 28.672µs | 65.536µs | 0.5 | 1% |
| chan/single-owner | 36.3k | 28.672µs | 32.768µs | 57.344µs | 0.5 | 3% |
| chan/sharded-256 | 35.5k | 28.672µs | 32.768µs | 65.536µs | 0.5 | 4% |
| actor/janus-today | 34.4k | 28.672µs | 40.96µs | 81.92µs | 4.5 | 0% |
| actor/single-tuned | 35.2k | 28.672µs | 40.96µs | 131.072µs | 1.5 | 1% |
| actor/sharded-12-ask | 34.8k | 28.672µs | 40.96µs | 65.536µs | 4.5 | 2% |
| actor/sharded-12-tuned | 34.8k | 28.672µs | 40.96µs | 98.304µs | 0.5 | 1% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 37.0k | 28.672µs | 2.097152ms | 2.62144ms | 0.5 | 1% |
| mutex/rwmutex | 41.7k | 65.536µs | 917.504µs | 1.048576ms | 0.5 | 2% |
| mutex/sharded-1024 | 308.3k | 28.672µs | 40.96µs | 163.84µs | 0.5 | 1% |
| chan/single-owner | 36.7k | 229.376µs | 262.144µs | 327.68µs | 0.5 | 0% |
| chan/sharded-256 | 292.4k | 28.672µs | 57.344µs | 114.688µs | 0.5 | 1% |
| actor/janus-today | 36.4k | 229.376µs | 262.144µs | 393.216µs | 4.5 | 0% |
| actor/single-tuned | 36.3k | 229.376µs | 262.144µs | 393.216µs | 1.5 | 0% |
| actor/sharded-12-ask | 204.3k | 32.768µs | 114.688µs | 196.608µs | 4.5 | 5% |
| actor/sharded-12-tuned | 197.3k | 32.768µs | 114.688µs | 655.36µs | 0.5 | 6% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 36.3k | 1.835008ms | 2.62144ms | 2.62144ms | 0.5 | 0% |
| mutex/rwmutex | 40.6k | 3.145728ms | 3.670016ms | 5.24288ms | 0.5 | 0% |
| mutex/sharded-1024 | 396.9k | 28.672µs | 3.670016ms | 25.165824ms | 0.5 | 11% |
| chan/single-owner | 35.6k | 1.835008ms | 2.097152ms | 4.194304ms | 0.5 | 1% |
| chan/sharded-256 | 365.9k | 28.672µs | 5.24288ms | 12.582912ms | 0.5 | 5% |
| actor/janus-today | 36.1k | 1.835008ms | 2.097152ms | 2.097152ms | 4.5 | 2% |
| actor/single-tuned | 36.5k | 1.835008ms | 2.097152ms | 2.097152ms | 1.5 | 0% |
| actor/sharded-12-ask | 365.5k | 163.84µs | 917.504µs | 1.572864ms | 4.5 | 13% |
| actor/sharded-12-tuned | 367.2k | 163.84µs | 917.504µs | 1.835008ms | 0.5 | 6% |

