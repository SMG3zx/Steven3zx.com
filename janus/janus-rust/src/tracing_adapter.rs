//! Production diagnostics adapter for `tokio-rs/tracing`.

use crate::{PerformanceSample, TraceRecord, TraceSink};

/// Forwards deterministic core diagnostics to the process subscriber.
#[derive(Clone, Copy, Debug, Default)]
pub struct TracingSink;

impl TraceSink for TracingSink {
    fn record(&mut self, sample: TraceRecord) {
        tracing::debug!(
            target: "janus",
            operation = sample.operation,
            tick = sample.tick.0,
            entity = sample.entity.map(|entity| entity.0),
            "janus core trace"
        );
    }
}

/// Emits deterministic work samples as structured tracing events.
pub fn record_performance(sample: PerformanceSample) {
    tracing::trace!(
        target: "janus",
        tick = sample.tick.0,
        commands_processed = sample.commands_processed,
        buffered_events = sample.buffered_events,
        buffered_effects = sample.buffered_effects,
        "janus actor tick performance"
    );
}
