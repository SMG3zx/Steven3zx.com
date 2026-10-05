//! Bounded telemetry samples and provider-neutral export boundary.

/// Maximum metric name length.
pub const MAX_METRIC_NAME_BYTES: usize = 128;
/// Maximum metric label count in one sample.
pub const MAX_METRIC_LABELS: usize = 8;

/// Metric value kind.
#[derive(Clone, Copy, Debug, Eq, PartialEq, serde::Deserialize, serde::Serialize)]
pub enum MetricKind {
    /// A value that may increase or decrease.
    Gauge,
    /// A monotonically increasing count.
    Counter,
    /// A single observed duration or size.
    HistogramObservation,
}

/// One bounded metric label.
#[derive(Clone, Debug, Eq, PartialEq, serde::Deserialize, serde::Serialize)]
pub struct MetricLabel {
    /// Label key.
    pub key: String,
    /// Label value.
    pub value: String,
}

/// One telemetry sample ready for export.
#[derive(Clone, Debug, PartialEq, serde::Deserialize, serde::Serialize)]
pub struct MetricSample {
    /// Metric name.
    pub name: String,
    /// Metric kind.
    pub kind: MetricKind,
    /// Numeric sample value.
    pub value: f64,
    /// Unix timestamp supplied by the caller.
    pub timestamp: u64,
    /// Bounded label set.
    pub labels: Vec<MetricLabel>,
}

/// Telemetry recording failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TelemetryError {
    /// Name or label is empty or too long.
    InvalidName,
    /// Too many labels were attached.
    TooManyLabels,
    /// Sample value is not finite.
    InvalidValue,
    /// Buffer reached capacity.
    Capacity,
}

/// Sink for exporting drained telemetry samples.
pub trait TelemetryExporter {
    /// Exports one sample to the configured provider.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    fn export(&mut self, sample: MetricSample) -> Result<(), TelemetryError>;
}

/// Local Prometheus text-format exporter.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct PrometheusTextExporter {
    output: String,
}

impl PrometheusTextExporter {
    /// Creates an empty exposition buffer.
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Returns the rendered exposition text.
    #[must_use]
    pub fn output(&self) -> &str {
        &self.output
    }
}

impl TelemetryExporter for PrometheusTextExporter {
    fn export(&mut self, sample: MetricSample) -> Result<(), TelemetryError> {
        validate_sample(&sample)?;
        self.output.push_str(&sample.name);
        if !sample.labels.is_empty() {
            self.output.push('{');
            for (index, label) in sample.labels.iter().enumerate() {
                if index > 0 {
                    self.output.push(',');
                }
                self.output.push_str(&label.key);
                self.output.push_str("=\"");
                self.output.push_str(&escape_label(&label.value));
                self.output.push('"');
            }
            self.output.push('}');
        }
        self.output.push(' ');
        self.output.push_str(&sample.value.to_string());
        self.output.push(' ');
        self.output
            .push_str(&sample.timestamp.saturating_mul(1000).to_string());
        self.output.push('\n');
        Ok(())
    }
}

/// Bounded in-memory telemetry buffer.
pub struct TelemetryBuffer<const MAX_SAMPLES: usize> {
    samples: Vec<MetricSample>,
}

impl<const MAX_SAMPLES: usize> TelemetryBuffer<MAX_SAMPLES> {
    /// Creates an empty sample buffer.
    #[must_use]
    pub fn new() -> Self {
        Self {
            samples: Vec::with_capacity(MAX_SAMPLES),
        }
    }

    /// Records one validated sample.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn record(&mut self, sample: MetricSample) -> Result<(), TelemetryError> {
        validate_sample(&sample)?;
        if self.samples.len() >= MAX_SAMPLES {
            return Err(TelemetryError::Capacity);
        }
        self.samples.push(sample);
        Ok(())
    }

    /// Exports and removes all buffered samples.
    ///
    /// # Errors
    ///
    /// Returns an error when the operation cannot satisfy its input or
    /// bounded-state contract.
    pub fn drain<E: TelemetryExporter>(
        &mut self,
        exporter: &mut E,
    ) -> Result<usize, TelemetryError> {
        let mut exported_count = 0;
        while let Some(sample) = self.samples.pop() {
            exporter.export(sample)?;
            exported_count += 1;
        }
        Ok(exported_count)
    }

    /// Returns buffered samples without transferring ownership.
    #[must_use]
    pub fn samples(&self) -> &[MetricSample] {
        &self.samples
    }
}

impl<const MAX_SAMPLES: usize> Default for TelemetryBuffer<MAX_SAMPLES> {
    fn default() -> Self {
        Self::new()
    }
}

fn validate_sample(sample: &MetricSample) -> Result<(), TelemetryError> {
    if sample.name.trim().is_empty() || sample.name.len() > MAX_METRIC_NAME_BYTES {
        return Err(TelemetryError::InvalidName);
    }
    if !sample.value.is_finite() {
        return Err(TelemetryError::InvalidValue);
    }
    if sample.labels.len() > MAX_METRIC_LABELS {
        return Err(TelemetryError::TooManyLabels);
    }
    if sample.labels.iter().any(|label| {
        label.key.trim().is_empty()
            || label.value.trim().is_empty()
            || label.key.len() > MAX_METRIC_NAME_BYTES
            || label.value.len() > MAX_METRIC_NAME_BYTES
    }) {
        return Err(TelemetryError::InvalidName);
    }
    Ok(())
}

fn escape_label(value: &str) -> String {
    value
        .replace('\\', "\\\\")
        .replace('"', "\\\"")
        .replace('\n', "\\n")
}

#[cfg(test)]
mod tests {
    use super::*;

    struct Collector(Vec<MetricSample>);

    impl TelemetryExporter for Collector {
        fn export(&mut self, sample: MetricSample) -> Result<(), TelemetryError> {
            self.0.push(sample);
            Ok(())
        }
    }

    fn sample(value: f64) -> MetricSample {
        MetricSample {
            name: "janus_builds_total".to_owned(),
            kind: MetricKind::Counter,
            value,
            timestamp: 10,
            labels: vec![MetricLabel {
                key: "tenant".to_owned(),
                value: "7".to_owned(),
            }],
        }
    }

    #[test]
    fn telemetry_buffer_validates_and_exports_samples() {
        let mut buffer = TelemetryBuffer::<2>::new();
        buffer.record(sample(1.0)).unwrap();
        buffer.record(sample(2.0)).unwrap();
        let mut collector = Collector(Vec::new());
        assert_eq!(buffer.drain(&mut collector), Ok(2));
        assert!(buffer.samples().is_empty());
        assert_eq!(collector.0.len(), 2);
    }

    #[test]
    fn telemetry_rejects_invalid_values_and_capacity_overflow() {
        let mut buffer = TelemetryBuffer::<1>::new();
        assert_eq!(
            buffer.record(sample(f64::NAN)),
            Err(TelemetryError::InvalidValue)
        );
        buffer.record(sample(1.0)).unwrap();
        assert_eq!(buffer.record(sample(2.0)), Err(TelemetryError::Capacity));
    }

    #[test]
    fn prometheus_exporter_renders_labels_and_timestamps() {
        let mut exporter = PrometheusTextExporter::new();
        exporter.export(sample(2.5)).unwrap();
        assert_eq!(
            exporter.output(),
            "janus_builds_total{tenant=\"7\"} 2.5 10000\n"
        );
    }
}
