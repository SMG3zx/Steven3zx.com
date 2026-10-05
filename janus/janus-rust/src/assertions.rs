//! Bounded JSONL assertion events for the local verification loop.

use std::fs::{create_dir_all, OpenOptions};
use std::io::{self, Write};
use std::path::Path;

/// Maximum assertion message length accepted by the local contract.
pub const MAX_ASSERTION_MESSAGE_BYTES: usize = 512;
/// Maximum source label length accepted by the local contract.
pub const MAX_ASSERTION_SOURCE_BYTES: usize = 128;
/// Maximum rendered JSONL event size accepted by the local contract.
pub const MAX_ASSERTION_EVENT_BYTES: usize = 4096;

/// Optional lifecycle context attached to an assertion event.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct AssertionDetails<'a> {
    /// Operation identifier, when the assertion concerns an operation.
    pub operation_id: Option<&'a str>,
    /// Operation kind, such as `build.enqueue`.
    pub operation_kind: Option<&'a str>,
    /// Deployment identifier, when applicable.
    pub deployment_id: Option<&'a str>,
    /// Runtime identity attached to a running deployment assertion.
    pub runtime_id: Option<&'a str>,
    /// Runtime endpoint attached to a running deployment assertion.
    pub endpoint: Option<&'a str>,
    /// State before the checked transition.
    pub previous_status: Option<&'a str>,
    /// State after the checked transition.
    pub next_status: Option<&'a str>,
}

/// Append-only writer for local Antithesis-compatible assertion events.
#[derive(Debug)]
pub struct AssertionWriter {
    path: String,
    sequence: u64,
}

impl AssertionWriter {
    /// Creates a writer for a bounded JSONL event file.
    pub fn new(path: impl Into<String>) -> Self {
        Self {
            path: path.into(),
            sequence: 0,
        }
    }

    /// Appends one assertion event and advances its sequence.
    ///
    /// # Errors
    ///
    /// Returns an I/O error when the parent directory, event file, or write
    /// operation cannot be opened or completed.
    pub fn record(
        &mut self,
        message: &str,
        condition: bool,
        source: &str,
        details: AssertionDetails<'_>,
    ) -> io::Result<()> {
        let line = render(message, condition, source, self.sequence, details)?;
        let path = Path::new(&self.path);
        if let Some(parent) = path.parent() {
            if !parent.as_os_str().is_empty() {
                create_dir_all(parent)?;
            }
        }
        let mut file = OpenOptions::new().create(true).append(true).open(path)?;
        file.write_all(line.as_bytes())?;
        file.write_all(b"\n")?;
        self.sequence = self.sequence.saturating_add(1);
        Ok(())
    }
}

/// Renders one assertion event without writing it to disk.
///
/// # Errors
///
/// Returns an I/O error when the structured assertion cannot be serialized.
pub fn render(
    message: &str,
    condition: bool,
    source: &str,
    sequence: u64,
    details: AssertionDetails<'_>,
) -> io::Result<String> {
    let message = message.trim();
    let source = source.trim();
    if message.is_empty() {
        return Err(invalid_input("message is required"));
    }
    if message.len() > MAX_ASSERTION_MESSAGE_BYTES {
        return Err(invalid_input("message is too large"));
    }
    if source.len() > MAX_ASSERTION_SOURCE_BYTES {
        return Err(invalid_input("source is too large"));
    }

    let (file, line, column) = source_location(source);
    let mut output = String::with_capacity(MAX_ASSERTION_EVENT_BYTES);
    output.push_str("{\"antithesis_assert\":{");
    output.push_str("\"location\":{");
    field(&mut output, "class", source, true);
    field(&mut output, "function", "record", false);
    field(&mut output, "file", file, false);
    number_field(&mut output, "begin_line", line);
    number_field(&mut output, "begin_column", column);
    write_details(&mut output, details);
    output.push_str("},");
    field(&mut output, "assert_type", "always", true);
    field(&mut output, "display_type", "Always", false);
    field(&mut output, "message", message, false);
    field(&mut output, "id", message, false);
    number_field(
        &mut output,
        "sequence",
        u32::try_from(sequence).unwrap_or(u32::MAX),
    );
    bool_field(&mut output, "hit", condition);
    bool_field(&mut output, "must_hit", true);
    bool_field(&mut output, "condition", condition);
    output.push_str("}}");
    if output.len() > MAX_ASSERTION_EVENT_BYTES {
        return Err(invalid_input("event is too large"));
    }
    Ok(output)
}

fn write_details(output: &mut String, details: AssertionDetails<'_>) {
    output.push_str("},\"details\":{");
    let mut first_detail = true;
    optional_field(
        output,
        "operation_id",
        details.operation_id,
        &mut first_detail,
    );
    optional_field(
        output,
        "operation_kind",
        details.operation_kind,
        &mut first_detail,
    );
    optional_field(
        output,
        "deployment_id",
        details.deployment_id,
        &mut first_detail,
    );
    optional_field(output, "runtime_id", details.runtime_id, &mut first_detail);
    optional_field(output, "endpoint", details.endpoint, &mut first_detail);
    optional_field(
        output,
        "previous_status",
        details.previous_status,
        &mut first_detail,
    );
    optional_field(
        output,
        "next_status",
        details.next_status,
        &mut first_detail,
    );
}

fn invalid_input(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}

fn source_location(source: &str) -> (&str, u32, u32) {
    let mut parts = source.split('#');
    let file = parts.next().unwrap_or(source);
    let line = parts
        .next()
        .and_then(|value| value.parse().ok())
        .unwrap_or(0);
    let column = parts
        .next()
        .and_then(|value| value.parse().ok())
        .unwrap_or(0);
    (file, line, column)
}

fn optional_field(output: &mut String, name: &str, value: Option<&str>, first: &mut bool) {
    if let Some(value) = value {
        field(output, name, value, *first);
        *first = false;
    }
}

fn field(output: &mut String, name: &str, value: &str, first: bool) {
    if !first {
        output.push(',');
    }
    output.push('"');
    escape(output, name);
    output.push_str("\":\"");
    escape(output, value);
    output.push('"');
}

fn number_field(output: &mut String, name: &str, value: u32) {
    output.push(',');
    output.push('"');
    escape(output, name);
    output.push_str("\":");
    output.push_str(&value.to_string());
}

fn bool_field(output: &mut String, name: &str, value: bool) {
    output.push(',');
    output.push('"');
    escape(output, name);
    output.push_str(if value { "\":true" } else { "\":false" });
}

fn escape(output: &mut String, value: &str) {
    for character in value.chars() {
        match character {
            '"' => output.push_str("\\\""),
            '\\' => output.push_str("\\\\"),
            '\n' => output.push_str("\\n"),
            '\r' => output.push_str("\\r"),
            '\t' => output.push_str("\\t"),
            character if character.is_control() => output.push('?'),
            character => output.push(character),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rendering_matches_the_local_assertion_shape() {
        let line = render(
            "operation status is monotonic",
            true,
            "janus-rust",
            7,
            AssertionDetails::default(),
        )
        .unwrap();
        assert!(line.contains("\"assert_type\":\"always\""));
        assert!(line.contains("\"hit\":true"));
        assert!(line.contains("\"begin_line\":0"));
    }

    #[test]
    fn lifecycle_details_and_locations_are_preserved() {
        let line = render(
            "operation status transitions never regress",
            false,
            "src/worker.rs#42#7",
            0,
            AssertionDetails {
                operation_id: Some("op_1"),
                operation_kind: Some("build.enqueue"),
                previous_status: Some("processing"),
                next_status: Some("pending"),
                ..AssertionDetails::default()
            },
        )
        .unwrap();
        assert!(line.contains("\"operation_id\":\"op_1\""));
        assert!(line.contains("\"next_status\":\"pending\""));
        assert!(line.contains("\"begin_line\":42"));
    }
}
