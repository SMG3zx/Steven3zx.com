# Raw results

Go go1.27.1, windows/amd64, GOMAXPROCS=12, 3 reps of 1.5s per cell, median by throughput. Spread is (max-min)/median ops/s across the reps.

## read95

95% Get, 5% Update, 10k keys, uniform

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 57.89M | 1ns | 112ns | 224ns | 0.1 | 24% |
| atomic/syncmap-cas | 16.22M | 112ns | 320ns | 512ns | 0.1 | 15% |
| atomic/slots-fetch-add (counter only) | 53.14M | 1ns | 112ns | 112ns | 0.0 | 8% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 115.34M | 112ns | 112ns | 448ns | 0.1 | 4% |
| atomic/syncmap-cas | 70.20M | 112ns | 320ns | 640ns | 0.1 | 6% |
| atomic/slots-fetch-add (counter only) | 124.74M | 112ns | 112ns | 224ns | 0.0 | 3% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 253.67M | 112ns | 112ns | 320ns | 0.1 | 4% |
| atomic/syncmap-cas | 95.92M | 112ns | 320ns | 1.024µs | 0.1 | 8% |
| atomic/slots-fetch-add (counter only) | 336.11M | 112ns | 112ns | 112ns | 0.0 | 6% |

## mixed50

50% Get, 50% Update, 10k keys, uniform

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 29.66M | 112ns | 224ns | 448ns | 0.5 | 7% |
| atomic/syncmap-cas | 8.80M | 112ns | 448ns | 768ns | 1.5 | 6% |
| atomic/slots-fetch-add (counter only) | 48.92M | 1ns | 112ns | 112ns | 0.0 | 17% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 65.23M | 112ns | 224ns | 8.192µs | 0.5 | 4% |
| atomic/syncmap-cas | 31.93M | 224ns | 512ns | 81.92µs | 1.5 | 20% |
| atomic/slots-fetch-add (counter only) | 116.43M | 112ns | 112ns | 224ns | 0.0 | 7% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 99.18M | 112ns | 224ns | 28.672µs | 0.5 | 7% |
| atomic/syncmap-cas | 38.52M | 224ns | 768ns | 229.376µs | 1.5 | 7% |
| atomic/slots-fetch-add (counter only) | 283.29M | 112ns | 112ns | 224ns | 0.0 | 2% |

## write100

100% Update, 10k keys, uniform

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 23.12M | 112ns | 224ns | 512ns | 1.0 | 12% |
| atomic/syncmap-cas | 6.73M | 224ns | 448ns | 896ns | 3.0 | 5% |
| atomic/slots-fetch-add (counter only) | 56.72M | 1ns | 112ns | 112ns | 0.0 | 4% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 45.50M | 112ns | 320ns | 57.344µs | 1.0 | 1% |
| atomic/syncmap-cas | 21.89M | 224ns | 1.024µs | 114.688µs | 3.0 | 3% |
| atomic/slots-fetch-add (counter only) | 111.19M | 112ns | 112ns | 224ns | 0.0 | 5% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 65.51M | 112ns | 448ns | 163.84µs | 1.0 | 9% |
| atomic/syncmap-cas | 25.50M | 224ns | 1.28µs | 786.432µs | 3.0 | 5% |
| atomic/slots-fetch-add (counter only) | 289.66M | 112ns | 112ns | 112ns | 0.0 | 6% |

## hotkey

50/50, 80% of ops on one key

### 1 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 34.07M | 1ns | 112ns | 320ns | 0.5 | 6% |
| atomic/syncmap-cas | 11.40M | 112ns | 448ns | 640ns | 1.1 | 14% |
| atomic/slots-fetch-add (counter only) | 41.68M | 112ns | 112ns | 112ns | 0.0 | 9% |

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 18.19M | 112ns | 3.072µs | 114.688µs | 1.9 | 4% |
| atomic/syncmap-cas | 8.56M | 224ns | 20.48µs | 65.536µs | 1.7 | 4% |
| atomic/slots-fetch-add (counter only) | 64.60M | 112ns | 224ns | 320ns | 0.0 | 1% |

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 18.74M | 112ns | 5.12µs | 229.376µs | 2.3 | 3% |
| atomic/syncmap-cas | 8.18M | 224ns | 327.68µs | 786.432µs | 1.6 | 3% |
| atomic/slots-fetch-add (counter only) | 112.62M | 112ns | 224ns | 320ns | 0.0 | 3% |

## scan-blocks

95% Get, 5% Update on 100k keys, plus one goroutine scanning all 100k in a loop; latency is the foreground ops'

### 8 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 111.62M | 112ns | 224ns | 512ns | 0.0 | 7% |
| atomic/syncmap-cas | 41.68M | 224ns | 512ns | 896ns | 0.1 | 24% |
| atomic/slots-fetch-add (counter only) | 116.36M | 112ns | 112ns | 224ns | 0.0 | 3% |

Background scans per second: atomic/slots-cas 3907; atomic/syncmap-cas 187; atomic/slots-fetch-add (counter only) 17438; 

### 64 workers

| variant | ops/s | p50 | p99 | p99.9 | allocs/op | spread |
|---|---:|---:|---:|---:|---:|---:|
| atomic/slots-cas | 186.95M | 112ns | 224ns | 512ns | 0.0 | 4% |
| atomic/syncmap-cas | 44.04M | 224ns | 640ns | 1.28µs | 0.1 | 12% |
| atomic/slots-fetch-add (counter only) | 303.17M | 112ns | 112ns | 224ns | 0.0 | 2% |

Background scans per second: atomic/slots-cas 2167; atomic/syncmap-cas 91; atomic/slots-fetch-add (counter only) 4348; 

