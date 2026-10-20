// Package usage holds the measurement of one WASM Run.
package usage

// Usage is what one WASM Run consumed, measured by the host.
//
// Fuel is exact and repeatable for the same module and input. PeakMemoryBytes is
// the size of the module's exported linear memory when the run ended, which is
// its peak because linear memory never shrinks. It is zero when the module
// exports no memory. DurationMS is wall time measured by the host clock.
//
// CPUTimeMicros is the active CPU time the run's thread spent, which is what the
// CPU layer bills: time spent waiting adds to DurationMS but not to it. It is
// zero when the platform cannot read thread CPU time. Windows reads it in
// scheduler ticks of about 15.6 ms, so short runs there read low.
type Usage struct {
	Fuel            uint64 `json:"fuel"`
	PeakMemoryBytes uint64 `json:"peak_memory_bytes"`
	DurationMS      int64  `json:"duration_ms"`
	CPUTimeMicros   int64  `json:"cpu_time_us"`
}
