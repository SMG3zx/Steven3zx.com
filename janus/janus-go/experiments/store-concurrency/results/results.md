# Results

Go go1.27.1, windows/amd64, GOMAXPROCS=12, 3 reps of 1.5s per cell, median by throughput. Spread is (max-min)/median ops/s across the reps.

## Side by side

Every variant ran in the same session with the same settings. Bold is the best in the column among the general-purpose variants: the counter-only atomic is a ceiling that cannot do a general update, so it is never marked. n/a means the variant cannot run that workload (see the report). Latencies are of foreground operations.

### 1 callers

**Throughput (ops/s, higher is better)**

| variant | family | read95 | mixed50 | write100 | hotkey | slow-io-25us |
|---|---|---:|---:|---:|---:|---:|
| mutex/coarse | mutex | 24.31M | 19.05M | 19.12M | 19.57M | **39.5k** |
| mutex/rwmutex | mutex | 30.18M | 18.85M | 15.59M | 19.51M | 39.4k |
| mutex/sharded-1024 | mutex | 26.52M | 16.20M | 14.48M | 19.21M | 39.2k |
| chan/single-owner | channel | 1.94M | 1.41M | 1.76M | 1.47M | 35.9k |
| chan/sharded-256 | channel | 1.37M | 1.36M | 1.35M | 1.48M | 35.7k |
| actor/janus-today | actor | 515.8k | 525.6k | 484.7k | 543.4k | 34.6k |
| actor/single-tuned | actor | 857.8k | 865.6k | 843.1k | 865.2k | 35.2k |
| actor/sharded-12-ask | actor | 531.4k | 512.1k | 516.9k | 536.4k | 34.9k |
| actor/sharded-12-tuned | actor | 986.5k | 960.2k | 928.8k | 957.3k | 35.2k |
| atomic/slots-cas | atomic | **44.18M** | **31.16M** | **23.24M** | **26.22M** | n/a |
| atomic/syncmap-cas | atomic | 15.07M | 9.72M | 6.07M | 11.85M | n/a |
| atomic/slots-fetch-add (counter only) | atomic | 49.97M | 45.97M | 54.05M | 40.48M | n/a |

**p99 latency (lower is better)**

| variant | family | read95 | mixed50 | write100 | hotkey | slow-io-25us |
|---|---|---:|---:|---:|---:|---:|
| mutex/coarse | mutex | 224ns | **224ns** | **224ns** | **224ns** | **28.672µs** |
| mutex/rwmutex | mutex | **112ns** | **224ns** | **224ns** | **224ns** | **28.672µs** |
| mutex/sharded-1024 | mutex | **112ns** | **224ns** | 320ns | **224ns** | **28.672µs** |
| chan/single-owner | channel | 3.584µs | 4.096µs | 3.584µs | 4.096µs | 32.768µs |
| chan/sharded-256 | channel | 4.096µs | 4.096µs | 4.096µs | 4.096µs | 32.768µs |
| actor/janus-today | actor | 8.192µs | 7.168µs | 10.24µs | 7.168µs | 40.96µs |
| actor/single-tuned | actor | 5.12µs | 5.12µs | 6.144µs | 5.12µs | 40.96µs |
| actor/sharded-12-ask | actor | 7.168µs | 7.168µs | 7.168µs | 7.168µs | 40.96µs |
| actor/sharded-12-tuned | actor | 5.12µs | 5.12µs | 5.12µs | 5.12µs | 40.96µs |
| atomic/slots-cas | atomic | **112ns** | **224ns** | **224ns** | **224ns** | n/a |
| atomic/syncmap-cas | atomic | 320ns | 320ns | 512ns | 448ns | n/a |
| atomic/slots-fetch-add (counter only) | atomic | 112ns | 112ns | 112ns | 112ns | n/a |

**p99.9 latency (lower is better)**

| variant | family | read95 | mixed50 | write100 | hotkey | slow-io-25us |
|---|---|---:|---:|---:|---:|---:|
| mutex/coarse | mutex | 448ns | **448ns** | 640ns | 640ns | **57.344µs** |
| mutex/rwmutex | mutex | **224ns** | 512ns | 640ns | 512ns | **57.344µs** |
| mutex/sharded-1024 | mutex | **224ns** | 512ns | 896ns | 512ns | **57.344µs** |
| chan/single-owner | channel | 6.144µs | 7.168µs | 10.24µs | 7.168µs | 81.92µs |
| chan/sharded-256 | channel | 8.192µs | 7.168µs | 7.168µs | 7.168µs | 81.92µs |
| actor/janus-today | actor | 24.576µs | 14.336µs | 49.152µs | 14.336µs | **57.344µs** |
| actor/single-tuned | actor | 12.288µs | 10.24µs | 14.336µs | 12.288µs | 81.92µs |
| actor/sharded-12-ask | actor | 16.384µs | 14.336µs | 16.384µs | 16.384µs | 98.304µs |
| actor/sharded-12-tuned | actor | 10.24µs | 10.24µs | 12.288µs | 10.24µs | 65.536µs |
| atomic/slots-cas | atomic | 320ns | **448ns** | **512ns** | **448ns** | n/a |
| atomic/syncmap-cas | atomic | 512ns | 640ns | 1.024µs | 640ns | n/a |
| atomic/slots-fetch-add (counter only) | atomic | 112ns | 112ns | 112ns | 224ns | n/a |

### 8 callers

**Throughput (ops/s, higher is better)**

| variant | family | read95 | mixed50 | write100 | hotkey | scan-blocks | slow-io-25us |
|---|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | mutex | 11.53M | 9.39M | 8.47M | 10.39M | 71.0k | 36.5k |
| mutex/rwmutex | mutex | 9.35M | 7.51M | 8.16M | 8.69M | 82.8k | 40.7k |
| mutex/sharded-1024 | mutex | 64.88M | 52.42M | 41.66M | 8.75M | 66.49M | **310.1k** |
| chan/single-owner | channel | 3.85M | 3.85M | 3.67M | 3.77M | 39.2k | 36.6k |
| chan/sharded-256 | channel | 15.22M | 14.12M | 12.94M | 4.15M | 14.23M | 290.4k |
| actor/janus-today | actor | 2.74M | 2.73M | 2.16M | 2.66M | 24.9k | 36.0k |
| actor/single-tuned | actor | 3.39M | 3.32M | 2.51M | 3.33M | 31.8k | 36.3k |
| actor/sharded-12-ask | actor | 4.04M | 4.06M | 3.74M | 3.25M | 1.20M | 203.1k |
| actor/sharded-12-tuned | actor | 6.72M | 6.35M | 6.02M | 4.10M | 1.35M | 207.8k |
| atomic/slots-cas | atomic | **124.36M** | **73.49M** | **50.10M** | **19.57M** | **115.33M** | n/a |
| atomic/syncmap-cas | atomic | 72.30M | 36.86M | 24.60M | 8.80M | 35.84M | n/a |
| atomic/slots-fetch-add (counter only) | atomic | 126.30M | 119.04M | 116.09M | 64.67M | 116.35M | n/a |

**p99 latency (lower is better)**

| variant | family | read95 | mixed50 | write100 | hotkey | scan-blocks | slow-io-25us |
|---|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | mutex | 24.576µs | 28.672µs | 40.96µs | 24.576µs | 3.145728ms | 3.145728ms |
| mutex/rwmutex | mutex | 20.48µs | 32.768µs | 40.96µs | 28.672µs | 3.145728ms | 917.504µs |
| mutex/sharded-1024 | mutex | 224ns | 320ns | 512ns | 28.672µs | **224ns** | **40.96µs** |
| chan/single-owner | channel | 12.288µs | 10.24µs | 12.288µs | 12.288µs | 917.504µs | 262.144µs |
| chan/sharded-256 | channel | 1.28µs | 1.28µs | 2.048µs | 14.336µs | 1.792µs | 57.344µs |
| actor/janus-today | actor | 12.288µs | 12.288µs | 20.48µs | 14.336µs | 1.048576ms | 327.68µs |
| actor/single-tuned | actor | 8.192µs | 8.192µs | 16.384µs | 8.192µs | 1.048576ms | 262.144µs |
| actor/sharded-12-ask | actor | 7.168µs | 6.144µs | 10.24µs | 10.24µs | 98.304µs | 114.688µs |
| actor/sharded-12-tuned | actor | 3.584µs | 4.096µs | 4.096µs | 8.192µs | 114.688µs | 114.688µs |
| atomic/slots-cas | atomic | **112ns** | **224ns** | **320ns** | **3.072µs** | **224ns** | n/a |
| atomic/syncmap-cas | atomic | 320ns | 448ns | 768ns | 16.384µs | 640ns | n/a |
| atomic/slots-fetch-add (counter only) | atomic | 112ns | 112ns | 112ns | 224ns | 112ns | n/a |

**p99.9 latency (lower is better)**

| variant | family | read95 | mixed50 | write100 | hotkey | scan-blocks | slow-io-25us |
|---|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | mutex | 49.152µs | 57.344µs | 81.92µs | 57.344µs | 5.24288ms | 6.291456ms |
| mutex/rwmutex | mutex | 81.92µs | 65.536µs | 81.92µs | 65.536µs | 6.291456ms | 1.31072ms |
| mutex/sharded-1024 | mutex | 1.28µs | 6.144µs | 24.576µs | 57.344µs | 768ns | **131.072µs** |
| chan/single-owner | channel | 20.48µs | 20.48µs | 28.672µs | 24.576µs | 1.048576ms | 393.216µs |
| chan/sharded-256 | channel | 5.12µs | 5.12µs | 28.672µs | 24.576µs | 14.336µs | 163.84µs |
| actor/janus-today | actor | 65.536µs | 65.536µs | 114.688µs | 81.92µs | 1.31072ms | 524.288µs |
| actor/single-tuned | actor | 49.152µs | 49.152µs | 163.84µs | 49.152µs | 1.048576ms | 327.68µs |
| actor/sharded-12-ask | actor | 65.536µs | 81.92µs | 81.92µs | 65.536µs | 131.072µs | 196.608µs |
| actor/sharded-12-tuned | actor | 10.24µs | 14.336µs | 28.672µs | **20.48µs** | 163.84µs | 163.84µs |
| atomic/slots-cas | atomic | **224ns** | **3.584µs** | **8.192µs** | 81.92µs | **448ns** | n/a |
| atomic/syncmap-cas | atomic | 640ns | 20.48µs | 98.304µs | 57.344µs | 1.024µs | n/a |
| atomic/slots-fetch-add (counter only) | atomic | 112ns | 224ns | 224ns | 320ns | 224ns | n/a |

### 64 callers

**Throughput (ops/s, higher is better)**

| variant | family | read95 | mixed50 | write100 | hotkey | scan-blocks | slow-io-25us |
|---|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | mutex | 10.37M | 8.38M | 8.01M | 8.99M | 187.1k | 36.3k |
| mutex/rwmutex | mutex | 9.16M | 7.52M | 7.80M | 8.59M | 186.3k | 39.7k |
| mutex/sharded-1024 | mutex | 121.67M | 66.36M | 50.54M | 8.60M | 88.94M | **397.8k** |
| chan/single-owner | channel | 4.51M | 4.34M | 4.17M | 4.29M | 100.6k | 36.4k |
| chan/sharded-256 | channel | 17.59M | 16.86M | 14.59M | 4.92M | 16.04M | 390.6k |
| actor/janus-today | actor | 3.24M | 3.18M | 3.01M | 3.12M | 92.6k | 36.1k |
| actor/single-tuned | actor | 4.48M | 4.44M | 4.01M | 4.31M | 96.6k | 36.3k |
| actor/sharded-12-ask | actor | 6.00M | 6.17M | 4.99M | 3.97M | 5.51M | 346.0k |
| actor/sharded-12-tuned | actor | 10.54M | 10.33M | 8.92M | 5.30M | 7.53M | 360.4k |
| atomic/slots-cas | atomic | **261.88M** | **126.20M** | **69.21M** | **20.05M** | **196.23M** | n/a |
| atomic/syncmap-cas | atomic | 103.11M | 42.62M | 25.94M | 8.59M | 48.97M | n/a |
| atomic/slots-fetch-add (counter only) | atomic | 336.05M | 275.84M | 274.56M | 111.21M | 299.57M | n/a |

**p99 latency (lower is better)**

| variant | family | read95 | mixed50 | write100 | hotkey | scan-blocks | slow-io-25us |
|---|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | mutex | 196.608µs | 196.608µs | 229.376µs | 229.376µs | 6.291456ms | 3.670016ms |
| mutex/rwmutex | mutex | 40.96µs | 262.144µs | 229.376µs | 262.144µs | 7.340032ms | 5.24288ms |
| mutex/sharded-1024 | mutex | 224ns | 1.024µs | 640ns | 229.376µs | **224ns** | 3.670016ms |
| chan/single-owner | channel | 40.96µs | 32.768µs | 40.96µs | 40.96µs | 1.048576ms | 2.097152ms |
| chan/sharded-256 | channel | 114.688µs | 114.688µs | 131.072µs | 40.96µs | 131.072µs | 5.24288ms |
| actor/janus-today | actor | 81.92µs | 81.92µs | 98.304µs | 98.304µs | 1.048576ms | 2.097152ms |
| actor/single-tuned | actor | 49.152µs | 40.96µs | 57.344µs | 57.344µs | 1.048576ms | 2.097152ms |
| actor/sharded-12-ask | actor | 114.688µs | 114.688µs | 163.84µs | 81.92µs | 131.072µs | 1.048576ms |
| actor/sharded-12-tuned | actor | 49.152µs | 49.152µs | 81.92µs | 32.768µs | 114.688µs | **917.504µs** |
| atomic/slots-cas | atomic | **112ns** | **224ns** | **448ns** | **5.12µs** | **224ns** | n/a |
| atomic/syncmap-cas | atomic | 320ns | 640ns | 1.024µs | 262.144µs | 640ns | n/a |
| atomic/slots-fetch-add (counter only) | atomic | 112ns | 112ns | 112ns | 224ns | 112ns | n/a |

**p99.9 latency (lower is better)**

| variant | family | read95 | mixed50 | write100 | hotkey | scan-blocks | slow-io-25us |
|---|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | mutex | 393.216µs | 393.216µs | 458.752µs | 458.752µs | 10.48576ms | 5.24288ms |
| mutex/rwmutex | mutex | 1.048576ms | 655.36µs | 458.752µs | 655.36µs | 14.680064ms | 6.291456ms |
| mutex/sharded-1024 | mutex | 163.84µs | 262.144µs | 393.216µs | 655.36µs | 229.376µs | 29.360128ms |
| chan/single-owner | channel | 98.304µs | 98.304µs | 163.84µs | **114.688µs** | 1.31072ms | 2.097152ms |
| chan/sharded-256 | channel | 458.752µs | 524.288µs | 524.288µs | **114.688µs** | 524.288µs | 12.582912ms |
| actor/janus-today | actor | 229.376µs | 229.376µs | 229.376µs | 229.376µs | 1.31072ms | 2.62144ms |
| actor/single-tuned | actor | 229.376µs | 196.608µs | 196.608µs | 196.608µs | 1.31072ms | 2.62144ms |
| actor/sharded-12-ask | actor | 327.68µs | 327.68µs | 458.752µs | 229.376µs | 327.68µs | 1.835008ms |
| actor/sharded-12-tuned | actor | 114.688µs | 163.84µs | 229.376µs | **114.688µs** | 196.608µs | **1.572864ms** |
| atomic/slots-cas | atomic | **320ns** | **4.096µs** | **81.92µs** | 131.072µs | **448ns** | n/a |
| atomic/syncmap-cas | atomic | 1.28µs | 98.304µs | 524.288µs | 786.432µs | 7.168µs | n/a |
| atomic/slots-fetch-add (counter only) | atomic | 112ns | 224ns | 224ns | 320ns | 224ns | n/a |

## Detail: read95

95% Get, 5% Update, 10k keys, uniform

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 24.31M | 112ns | 224ns | 448ns | 0.1 | 15% |
| mutex/rwmutex | 30.18M | 112ns | 112ns | 224ns | 0.1 | 8% |
| mutex/sharded-1024 | 26.52M | 112ns | 112ns | 224ns | 0.1 | 5% |
| chan/single-owner | 1.94M | 320ns | 3.584µs | 6.144µs | 0.1 | 13% |
| chan/sharded-256 | 1.37M | 512ns | 4.096µs | 8.192µs | 0.1 | 9% |
| actor/janus-today | 515.8k | 1.536µs | 8.192µs | 24.576µs | 4.0 | 10% |
| actor/single-tuned | 857.8k | 896ns | 5.12µs | 12.288µs | 1.1 | 2% |
| actor/sharded-12-ask | 531.4k | 1.536µs | 7.168µs | 16.384µs | 4.0 | 5% |
| actor/sharded-12-tuned | 986.5k | 768ns | 5.12µs | 10.24µs | 0.1 | 1% |
| atomic/slots-cas | 44.18M | 112ns | 112ns | 320ns | 0.1 | 5% |
| atomic/syncmap-cas | 15.07M | 112ns | 320ns | 512ns | 0.1 | 18% |
| atomic/slots-fetch-add (counter only) | 49.97M | 1ns | 112ns | 112ns | 0.0 | 6% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 11.53M | 112ns | 24.576µs | 49.152µs | 0.1 | 2% |
| mutex/rwmutex | 9.35M | 112ns | 20.48µs | 81.92µs | 0.1 | 1% |
| mutex/sharded-1024 | 64.88M | 112ns | 224ns | 1.28µs | 0.1 | 14% |
| chan/single-owner | 3.85M | 1.792µs | 12.288µs | 20.48µs | 0.1 | 3% |
| chan/sharded-256 | 15.22M | 512ns | 1.28µs | 5.12µs | 0.1 | 2% |
| actor/janus-today | 2.74M | 2.56µs | 12.288µs | 65.536µs | 4.0 | 4% |
| actor/single-tuned | 3.39M | 2.048µs | 8.192µs | 49.152µs | 1.1 | 13% |
| actor/sharded-12-ask | 4.04M | 1.792µs | 7.168µs | 65.536µs | 4.0 | 2% |
| actor/sharded-12-tuned | 6.72M | 1.28µs | 3.584µs | 10.24µs | 0.1 | 2% |
| atomic/slots-cas | 124.36M | 112ns | 112ns | 224ns | 0.1 | 7% |
| atomic/syncmap-cas | 72.30M | 112ns | 320ns | 640ns | 0.1 | 12% |
| atomic/slots-fetch-add (counter only) | 126.30M | 112ns | 112ns | 112ns | 0.0 | 2% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 10.37M | 112ns | 196.608µs | 393.216µs | 0.1 | 4% |
| mutex/rwmutex | 9.16M | 112ns | 40.96µs | 1.048576ms | 0.1 | 4% |
| mutex/sharded-1024 | 121.67M | 112ns | 224ns | 163.84µs | 0.1 | 4% |
| chan/single-owner | 4.51M | 14.336µs | 40.96µs | 98.304µs | 0.1 | 6% |
| chan/sharded-256 | 17.59M | 640ns | 114.688µs | 458.752µs | 0.1 | 4% |
| actor/janus-today | 3.24M | 20.48µs | 81.92µs | 229.376µs | 4.0 | 3% |
| actor/single-tuned | 4.48M | 14.336µs | 49.152µs | 229.376µs | 1.1 | 3% |
| actor/sharded-12-ask | 6.00M | 2.048µs | 114.688µs | 327.68µs | 4.0 | 9% |
| actor/sharded-12-tuned | 10.54M | 1.536µs | 49.152µs | 114.688µs | 0.1 | 6% |
| atomic/slots-cas | 261.88M | 112ns | 112ns | 320ns | 0.1 | 4% |
| atomic/syncmap-cas | 103.11M | 112ns | 320ns | 1.28µs | 0.1 | 1% |
| atomic/slots-fetch-add (counter only) | 336.05M | 112ns | 112ns | 112ns | 0.0 | 2% |

## Detail: mixed50

50% Get, 50% Update, 10k keys, uniform

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 19.05M | 112ns | 224ns | 448ns | 0.5 | 1% |
| mutex/rwmutex | 18.85M | 112ns | 224ns | 512ns | 0.5 | 1% |
| mutex/sharded-1024 | 16.20M | 112ns | 224ns | 512ns | 0.5 | 1% |
| chan/single-owner | 1.41M | 448ns | 4.096µs | 7.168µs | 0.5 | 0% |
| chan/sharded-256 | 1.36M | 512ns | 4.096µs | 7.168µs | 0.5 | 2% |
| actor/janus-today | 525.6k | 1.536µs | 7.168µs | 14.336µs | 4.5 | 5% |
| actor/single-tuned | 865.6k | 768ns | 5.12µs | 10.24µs | 1.5 | 5% |
| actor/sharded-12-ask | 512.1k | 1.536µs | 7.168µs | 14.336µs | 4.5 | 4% |
| actor/sharded-12-tuned | 960.2k | 768ns | 5.12µs | 10.24µs | 0.5 | 4% |
| atomic/slots-cas | 31.16M | 112ns | 224ns | 448ns | 0.5 | 4% |
| atomic/syncmap-cas | 9.72M | 112ns | 320ns | 640ns | 1.5 | 12% |
| atomic/slots-fetch-add (counter only) | 45.97M | 1ns | 112ns | 112ns | 0.0 | 3% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 9.39M | 112ns | 28.672µs | 57.344µs | 0.5 | 2% |
| mutex/rwmutex | 7.51M | 112ns | 32.768µs | 65.536µs | 0.5 | 1% |
| mutex/sharded-1024 | 52.42M | 112ns | 320ns | 6.144µs | 0.5 | 8% |
| chan/single-owner | 3.85M | 2.048µs | 10.24µs | 20.48µs | 0.5 | 6% |
| chan/sharded-256 | 14.12M | 640ns | 1.28µs | 5.12µs | 0.5 | 3% |
| actor/janus-today | 2.73M | 2.56µs | 12.288µs | 65.536µs | 4.5 | 4% |
| actor/single-tuned | 3.32M | 2.048µs | 8.192µs | 49.152µs | 1.5 | 7% |
| actor/sharded-12-ask | 4.06M | 1.792µs | 6.144µs | 81.92µs | 4.5 | 3% |
| actor/sharded-12-tuned | 6.35M | 1.28µs | 4.096µs | 14.336µs | 0.5 | 1% |
| atomic/slots-cas | 73.49M | 112ns | 224ns | 3.584µs | 0.5 | 3% |
| atomic/syncmap-cas | 36.86M | 224ns | 448ns | 20.48µs | 1.5 | 4% |
| atomic/slots-fetch-add (counter only) | 119.04M | 112ns | 112ns | 224ns | 0.0 | 1% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 8.38M | 112ns | 196.608µs | 393.216µs | 0.5 | 4% |
| mutex/rwmutex | 7.52M | 112ns | 262.144µs | 655.36µs | 0.5 | 0% |
| mutex/sharded-1024 | 66.36M | 112ns | 1.024µs | 262.144µs | 0.5 | 3% |
| chan/single-owner | 4.34M | 14.336µs | 32.768µs | 98.304µs | 0.5 | 2% |
| chan/sharded-256 | 16.86M | 640ns | 114.688µs | 524.288µs | 0.5 | 7% |
| actor/janus-today | 3.18M | 20.48µs | 81.92µs | 229.376µs | 4.5 | 2% |
| actor/single-tuned | 4.44M | 14.336µs | 40.96µs | 196.608µs | 1.5 | 5% |
| actor/sharded-12-ask | 6.17M | 2.048µs | 114.688µs | 327.68µs | 4.5 | 7% |
| actor/sharded-12-tuned | 10.33M | 1.536µs | 49.152µs | 163.84µs | 0.5 | 11% |
| atomic/slots-cas | 126.20M | 112ns | 224ns | 4.096µs | 0.5 | 7% |
| atomic/syncmap-cas | 42.62M | 224ns | 640ns | 98.304µs | 1.5 | 22% |
| atomic/slots-fetch-add (counter only) | 275.84M | 112ns | 112ns | 224ns | 0.0 | 2% |

## Detail: write100

100% Update, 10k keys, uniform

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 19.12M | 112ns | 224ns | 640ns | 1.0 | 24% |
| mutex/rwmutex | 15.59M | 112ns | 224ns | 640ns | 1.0 | 8% |
| mutex/sharded-1024 | 14.48M | 112ns | 320ns | 896ns | 1.0 | 7% |
| chan/single-owner | 1.76M | 448ns | 3.584µs | 10.24µs | 1.0 | 29% |
| chan/sharded-256 | 1.35M | 512ns | 4.096µs | 7.168µs | 1.0 | 4% |
| actor/janus-today | 484.7k | 1.536µs | 10.24µs | 49.152µs | 5.0 | 18% |
| actor/single-tuned | 843.1k | 896ns | 6.144µs | 14.336µs | 2.0 | 2% |
| actor/sharded-12-ask | 516.9k | 1.536µs | 7.168µs | 16.384µs | 5.0 | 3% |
| actor/sharded-12-tuned | 928.8k | 768ns | 5.12µs | 12.288µs | 1.0 | 3% |
| atomic/slots-cas | 23.24M | 112ns | 224ns | 512ns | 1.0 | 8% |
| atomic/syncmap-cas | 6.07M | 224ns | 512ns | 1.024µs | 3.0 | 13% |
| atomic/slots-fetch-add (counter only) | 54.05M | 1ns | 112ns | 112ns | 0.0 | 8% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 8.47M | 112ns | 40.96µs | 81.92µs | 1.0 | 4% |
| mutex/rwmutex | 8.16M | 112ns | 40.96µs | 81.92µs | 1.0 | 3% |
| mutex/sharded-1024 | 41.66M | 112ns | 512ns | 24.576µs | 1.0 | 3% |
| chan/single-owner | 3.67M | 2.048µs | 12.288µs | 28.672µs | 1.0 | 2% |
| chan/sharded-256 | 12.94M | 512ns | 2.048µs | 28.672µs | 1.0 | 4% |
| actor/janus-today | 2.16M | 3.072µs | 20.48µs | 114.688µs | 5.0 | 35% |
| actor/single-tuned | 2.51M | 2.56µs | 16.384µs | 163.84µs | 2.0 | 46% |
| actor/sharded-12-ask | 3.74M | 2.048µs | 10.24µs | 81.92µs | 5.0 | 3% |
| actor/sharded-12-tuned | 6.02M | 1.28µs | 4.096µs | 28.672µs | 1.0 | 3% |
| atomic/slots-cas | 50.10M | 112ns | 320ns | 8.192µs | 1.0 | 16% |
| atomic/syncmap-cas | 24.60M | 224ns | 768ns | 98.304µs | 3.0 | 0% |
| atomic/slots-fetch-add (counter only) | 116.09M | 112ns | 112ns | 224ns | 0.0 | 2% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 8.01M | 112ns | 229.376µs | 458.752µs | 1.0 | 3% |
| mutex/rwmutex | 7.80M | 224ns | 229.376µs | 458.752µs | 1.0 | 3% |
| mutex/sharded-1024 | 50.54M | 224ns | 640ns | 393.216µs | 1.0 | 13% |
| chan/single-owner | 4.17M | 14.336µs | 40.96µs | 163.84µs | 1.0 | 8% |
| chan/sharded-256 | 14.59M | 640ns | 131.072µs | 524.288µs | 1.0 | 3% |
| actor/janus-today | 3.01M | 20.48µs | 98.304µs | 229.376µs | 5.0 | 6% |
| actor/single-tuned | 4.01M | 16.384µs | 57.344µs | 196.608µs | 2.0 | 5% |
| actor/sharded-12-ask | 4.99M | 2.048µs | 163.84µs | 458.752µs | 5.0 | 23% |
| actor/sharded-12-tuned | 8.92M | 1.536µs | 81.92µs | 229.376µs | 1.0 | 4% |
| atomic/slots-cas | 69.21M | 112ns | 448ns | 81.92µs | 1.0 | 10% |
| atomic/syncmap-cas | 25.94M | 224ns | 1.024µs | 524.288µs | 3.0 | 13% |
| atomic/slots-fetch-add (counter only) | 274.56M | 112ns | 112ns | 224ns | 0.0 | 6% |

## Detail: hotkey

50/50, 80% of ops on one key

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 19.57M | 112ns | 224ns | 640ns | 0.5 | 2% |
| mutex/rwmutex | 19.51M | 112ns | 224ns | 512ns | 0.5 | 13% |
| mutex/sharded-1024 | 19.21M | 112ns | 224ns | 512ns | 0.5 | 9% |
| chan/single-owner | 1.47M | 448ns | 4.096µs | 7.168µs | 0.5 | 1% |
| chan/sharded-256 | 1.48M | 448ns | 4.096µs | 7.168µs | 0.5 | 3% |
| actor/janus-today | 543.4k | 1.536µs | 7.168µs | 14.336µs | 4.5 | 1% |
| actor/single-tuned | 865.2k | 768ns | 5.12µs | 12.288µs | 1.5 | 1% |
| actor/sharded-12-ask | 536.4k | 1.536µs | 7.168µs | 16.384µs | 4.5 | 8% |
| actor/sharded-12-tuned | 957.3k | 768ns | 5.12µs | 10.24µs | 0.5 | 2% |
| atomic/slots-cas | 26.22M | 112ns | 224ns | 448ns | 0.5 | 11% |
| atomic/syncmap-cas | 11.85M | 112ns | 448ns | 640ns | 1.1 | 9% |
| atomic/slots-fetch-add (counter only) | 40.48M | 112ns | 112ns | 224ns | 0.0 | 6% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 10.39M | 112ns | 24.576µs | 57.344µs | 0.5 | 6% |
| mutex/rwmutex | 8.69M | 112ns | 28.672µs | 65.536µs | 0.5 | 7% |
| mutex/sharded-1024 | 8.75M | 112ns | 28.672µs | 57.344µs | 0.5 | 5% |
| chan/single-owner | 3.77M | 2.048µs | 12.288µs | 24.576µs | 0.5 | 4% |
| chan/sharded-256 | 4.15M | 1.536µs | 14.336µs | 24.576µs | 0.5 | 2% |
| actor/janus-today | 2.66M | 2.56µs | 14.336µs | 81.92µs | 4.5 | 7% |
| actor/single-tuned | 3.33M | 2.048µs | 8.192µs | 49.152µs | 1.5 | 3% |
| actor/sharded-12-ask | 3.25M | 2.56µs | 10.24µs | 65.536µs | 4.5 | 4% |
| actor/sharded-12-tuned | 4.10M | 1.792µs | 8.192µs | 20.48µs | 0.5 | 4% |
| atomic/slots-cas | 19.57M | 112ns | 3.072µs | 81.92µs | 2.0 | 2% |
| atomic/syncmap-cas | 8.80M | 224ns | 16.384µs | 57.344µs | 1.7 | 5% |
| atomic/slots-fetch-add (counter only) | 64.67M | 112ns | 224ns | 320ns | 0.0 | 2% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 8.99M | 112ns | 229.376µs | 458.752µs | 0.5 | 11% |
| mutex/rwmutex | 8.59M | 112ns | 262.144µs | 655.36µs | 0.5 | 6% |
| mutex/sharded-1024 | 8.60M | 112ns | 229.376µs | 655.36µs | 0.5 | 5% |
| chan/single-owner | 4.29M | 14.336µs | 40.96µs | 114.688µs | 0.5 | 3% |
| chan/sharded-256 | 4.92M | 14.336µs | 40.96µs | 114.688µs | 0.5 | 5% |
| actor/janus-today | 3.12M | 20.48µs | 98.304µs | 229.376µs | 4.5 | 2% |
| actor/single-tuned | 4.31M | 14.336µs | 57.344µs | 196.608µs | 1.5 | 16% |
| actor/sharded-12-ask | 3.97M | 20.48µs | 81.92µs | 229.376µs | 4.5 | 6% |
| actor/sharded-12-tuned | 5.30M | 14.336µs | 32.768µs | 114.688µs | 0.5 | 7% |
| atomic/slots-cas | 20.05M | 112ns | 5.12µs | 131.072µs | 2.4 | 6% |
| atomic/syncmap-cas | 8.59M | 224ns | 262.144µs | 786.432µs | 1.6 | 2% |
| atomic/slots-fetch-add (counter only) | 111.21M | 112ns | 224ns | 320ns | 0.0 | 4% |

## Detail: scan-blocks

95% Get, 5% Update on 100k keys, plus one goroutine scanning all 100k in a loop; latency is the foreground ops'

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 71.0k | 112ns | 3.145728ms | 5.24288ms | 0.1 | 7% |
| mutex/rwmutex | 82.8k | 112ns | 3.145728ms | 6.291456ms | 0.1 | 23% |
| mutex/sharded-1024 | 66.49M | 112ns | 224ns | 768ns | 0.1 | 2% |
| chan/single-owner | 39.2k | 640ns | 917.504µs | 1.048576ms | 0.1 | 8% |
| chan/sharded-256 | 14.23M | 512ns | 1.792µs | 14.336µs | 0.1 | 6% |
| actor/janus-today | 24.9k | 10.24µs | 1.048576ms | 1.31072ms | 4.3 | 11% |
| actor/single-tuned | 31.8k | 3.584µs | 1.048576ms | 1.048576ms | 1.1 | 8% |
| actor/sharded-12-ask | 1.20M | 1.536µs | 98.304µs | 131.072µs | 4.1 | 8% |
| actor/sharded-12-tuned | 1.35M | 1.024µs | 114.688µs | 163.84µs | 0.1 | 6% |
| atomic/slots-cas | 115.33M | 112ns | 224ns | 448ns | 0.1 | 2% |
| atomic/syncmap-cas | 35.84M | 224ns | 640ns | 1.024µs | 0.1 | 5% |
| atomic/slots-fetch-add (counter only) | 116.35M | 112ns | 112ns | 224ns | 0.0 | 4% |

Background scans per second: mutex/coarse 1474; mutex/rwmutex 1359; mutex/sharded-1024 1251; chan/single-owner 1426; chan/sharded-256 967; actor/janus-today 1456; actor/single-tuned 1508; actor/sharded-12-ask 824; actor/sharded-12-tuned 842; atomic/slots-cas 3818; atomic/syncmap-cas 149; atomic/slots-fetch-add (counter only) 17308; 

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 187.1k | 224ns | 6.291456ms | 10.48576ms | 0.1 | 10% |
| mutex/rwmutex | 186.3k | 224ns | 7.340032ms | 14.680064ms | 0.1 | 2% |
| mutex/sharded-1024 | 88.94M | 112ns | 224ns | 229.376µs | 0.1 | 8% |
| chan/single-owner | 100.6k | 786.432µs | 1.048576ms | 1.31072ms | 0.1 | 4% |
| chan/sharded-256 | 16.04M | 640ns | 131.072µs | 524.288µs | 0.1 | 8% |
| actor/janus-today | 92.6k | 786.432µs | 1.048576ms | 1.31072ms | 4.1 | 2% |
| actor/single-tuned | 96.6k | 786.432µs | 1.048576ms | 1.31072ms | 1.1 | 9% |
| actor/sharded-12-ask | 5.51M | 1.792µs | 131.072µs | 327.68µs | 4.1 | 13% |
| actor/sharded-12-tuned | 7.53M | 1.28µs | 114.688µs | 196.608µs | 0.1 | 9% |
| atomic/slots-cas | 196.23M | 112ns | 224ns | 448ns | 0.1 | 13% |
| atomic/syncmap-cas | 48.97M | 224ns | 640ns | 7.168µs | 0.1 | 18% |
| atomic/slots-fetch-add (counter only) | 299.57M | 112ns | 112ns | 224ns | 0.0 | 19% |

Background scans per second: mutex/coarse 1445; mutex/rwmutex 1459; mutex/sharded-1024 743; chan/single-owner 1451; chan/sharded-256 192; actor/janus-today 1408; actor/single-tuned 1437; actor/sharded-12-ask 772; actor/sharded-12-tuned 904; atomic/slots-cas 2377; atomic/syncmap-cas 62; atomic/slots-fetch-add (counter only) 3148; 

## Detail: slow-io-25us

50/50, 10k keys, 25us of work inside the owner per op (a store bound by one database connection)

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 39.5k | 28.672µs | 28.672µs | 57.344µs | 0.5 | 0% |
| mutex/rwmutex | 39.4k | 28.672µs | 28.672µs | 57.344µs | 0.5 | 0% |
| mutex/sharded-1024 | 39.2k | 28.672µs | 28.672µs | 57.344µs | 0.5 | 1% |
| chan/single-owner | 35.9k | 28.672µs | 32.768µs | 81.92µs | 0.5 | 0% |
| chan/sharded-256 | 35.7k | 28.672µs | 32.768µs | 81.92µs | 0.5 | 1% |
| actor/janus-today | 34.6k | 28.672µs | 40.96µs | 57.344µs | 4.5 | 0% |
| actor/single-tuned | 35.2k | 28.672µs | 40.96µs | 81.92µs | 1.5 | 1% |
| actor/sharded-12-ask | 34.9k | 28.672µs | 40.96µs | 98.304µs | 4.5 | 1% |
| actor/sharded-12-tuned | 35.2k | 28.672µs | 40.96µs | 65.536µs | 0.5 | 1% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 36.5k | 28.672µs | 3.145728ms | 6.291456ms | 0.5 | 0% |
| mutex/rwmutex | 40.7k | 65.536µs | 917.504µs | 1.31072ms | 0.5 | 0% |
| mutex/sharded-1024 | 310.1k | 28.672µs | 40.96µs | 131.072µs | 0.5 | 1% |
| chan/single-owner | 36.6k | 229.376µs | 262.144µs | 393.216µs | 0.5 | 1% |
| chan/sharded-256 | 290.4k | 28.672µs | 57.344µs | 163.84µs | 0.5 | 4% |
| actor/janus-today | 36.0k | 229.376µs | 327.68µs | 524.288µs | 4.5 | 3% |
| actor/single-tuned | 36.3k | 229.376µs | 262.144µs | 327.68µs | 1.5 | 0% |
| actor/sharded-12-ask | 203.1k | 32.768µs | 114.688µs | 196.608µs | 4.5 | 1% |
| actor/sharded-12-tuned | 207.8k | 32.768µs | 114.688µs | 163.84µs | 0.5 | 1% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| mutex/coarse | 36.3k | 1.835008ms | 3.670016ms | 5.24288ms | 0.5 | 2% |
| mutex/rwmutex | 39.7k | 163.84µs | 5.24288ms | 6.291456ms | 0.5 | 4% |
| mutex/sharded-1024 | 397.8k | 28.672µs | 3.670016ms | 29.360128ms | 0.5 | 3% |
| chan/single-owner | 36.4k | 1.835008ms | 2.097152ms | 2.097152ms | 0.5 | 0% |
| chan/sharded-256 | 390.6k | 28.672µs | 5.24288ms | 12.582912ms | 0.5 | 2% |
| actor/janus-today | 36.1k | 1.835008ms | 2.097152ms | 2.62144ms | 4.5 | 2% |
| actor/single-tuned | 36.3k | 1.835008ms | 2.097152ms | 2.62144ms | 1.5 | 0% |
| actor/sharded-12-ask | 346.0k | 131.072µs | 1.048576ms | 1.835008ms | 4.5 | 2% |
| actor/sharded-12-tuned | 360.4k | 131.072µs | 917.504µs | 1.572864ms | 0.5 | 1% |

