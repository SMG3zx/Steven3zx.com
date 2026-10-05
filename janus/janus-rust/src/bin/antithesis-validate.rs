//! Validate Rust-local Antithesis-style assertion event JSONL.

use std::env;
use std::process::ExitCode;

use janus_core::read_bounded_text;
use serde_json::Value;

const MAX_EVENT_FILE_BYTES: usize = 4 * 1024 * 1024;

fn main() -> ExitCode {
    let Some(path) = argument_value("--input") else {
        eprintln!("usage: antithesis-validate --input <events.jsonl>");
        return ExitCode::from(2);
    };
    match validate(&path) {
        Ok(summary) => {
            println!(
                "validated {} events ({} assertions, {} failures)",
                summary.events, summary.assertions, summary.failures
            );
            if summary.failures == 0 {
                ExitCode::SUCCESS
            } else {
                ExitCode::from(1)
            }
        }
        Err(error) => {
            eprintln!("assertion event validation failed: {error}");
            ExitCode::from(2)
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct Summary {
    events: usize,
    assertions: usize,
    failures: usize,
}

fn validate(path: &str) -> Result<Summary, String> {
    let contents = read_bounded_text(path, MAX_EVENT_FILE_BYTES)?;
    let mut summary = Summary {
        events: 0,
        assertions: 0,
        failures: 0,
    };
    for (line_index, line) in contents.lines().enumerate() {
        if line.trim().is_empty() {
            continue;
        }
        summary.events += 1;
        let event: Value = serde_json::from_str(line)
            .map_err(|error| format!("{path}:{}: invalid JSON: {error}", line_index + 1))?;
        let object = event
            .as_object()
            .ok_or_else(|| format!("{path}:{}: event must be an object", line_index + 1))?;
        let Some(assertion) = object.get("antithesis_assert") else {
            continue;
        };
        summary.assertions += 1;
        let assertion = assertion.as_object().ok_or_else(|| {
            format!(
                "{path}:{}: antithesis_assert must be an object",
                line_index + 1
            )
        })?;
        assertion
            .get("message")
            .and_then(Value::as_str)
            .filter(|value| !value.trim().is_empty())
            .ok_or_else(|| format!("{path}:{}: assertion message is required", line_index + 1))?;
        let condition = assertion
            .get("condition")
            .and_then(Value::as_bool)
            .ok_or_else(|| {
                format!(
                    "{path}:{}: assertion condition must be boolean",
                    line_index + 1
                )
            })?;
        if !condition {
            summary.failures += 1;
        }
    }
    if summary.events == 0 {
        return Err(format!("{path}: event file is empty"));
    }
    if summary.assertions == 0 {
        return Err(format!("{path}: no assertion events found"));
    }
    Ok(summary)
}

fn argument_value(name: &str) -> Option<String> {
    let mut arguments = env::args().skip(1);
    while let Some(argument) = arguments.next() {
        if argument == name {
            return arguments.next();
        }
    }
    None
}
