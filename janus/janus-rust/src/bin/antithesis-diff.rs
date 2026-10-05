//! Compare normalized Rust assertion events with one or more references.

use std::collections::{BTreeMap, BTreeSet};
use std::env;
use std::process::ExitCode;

use janus_core::read_bounded_text;
use serde_json::Value;

const MAX_EVENT_FILE_BYTES: usize = 4 * 1024 * 1024;

#[derive(Clone, Debug, Default, Eq, PartialEq)]
struct Property {
    hits: usize,
    failures: usize,
    details: Vec<BTreeMap<String, String>>,
}

fn main() -> ExitCode {
    let (references, candidate) = match arguments() {
        Ok(arguments) => arguments,
        Err(error) => {
            eprintln!("{error}");
            return ExitCode::from(2);
        }
    };
    let mut reference_properties = BTreeMap::new();
    for path in references {
        let properties = match load(&path) {
            Ok(properties) => properties,
            Err(error) => {
                eprintln!("assertion differential failed: {error}");
                return ExitCode::from(2);
            }
        };
        merge(&mut reference_properties, properties);
    }
    let candidate_properties = match load(&candidate) {
        Ok(properties) => properties,
        Err(error) => {
            eprintln!("assertion differential failed: {error}");
            return ExitCode::from(2);
        }
    };
    let errors = compare(&reference_properties, &candidate_properties);
    println!(
        "compared {} reference properties with {} candidate properties",
        reference_properties.len(),
        candidate_properties.len()
    );
    if errors.is_empty() {
        println!("Assertion differential: PASS");
        ExitCode::SUCCESS
    } else {
        eprintln!("Assertion differential failures:");
        for error in errors {
            eprintln!("- {error}");
        }
        ExitCode::from(1)
    }
}

fn arguments() -> Result<(Vec<String>, String), String> {
    let mut references = Vec::from([]);
    let mut candidate = None;
    let mut arguments = env::args().skip(1);
    while let Some(argument) = arguments.next() {
        if argument == "--reference" {
            references.push(
                arguments
                    .next()
                    .ok_or_else(|| "--reference requires a path".to_owned())?,
            );
        } else if argument == "--candidate" {
            candidate = Some(
                arguments
                    .next()
                    .ok_or_else(|| "--candidate requires a path".to_owned())?,
            );
        } else {
            return Err(format!("unknown argument: {argument}"));
        }
    }
    if references.is_empty() {
        return Err("at least one --reference path is required".to_owned());
    }
    candidate
        .map(|candidate| (references, candidate))
        .ok_or_else(|| "--candidate is required".to_owned())
}

fn load(path: &str) -> Result<BTreeMap<String, Property>, String> {
    let contents = read_bounded_text(path, MAX_EVENT_FILE_BYTES)?;
    let mut properties = BTreeMap::new();
    for (line_index, line) in contents.lines().enumerate() {
        if line.trim().is_empty() {
            continue;
        }
        let event: Value = serde_json::from_str(line)
            .map_err(|error| format!("{path}:{}: invalid JSON: {error}", line_index + 1))?;
        let Some(assertion) = event.get("antithesis_assert") else {
            continue;
        };
        let assertion = assertion.as_object().ok_or_else(|| {
            format!(
                "{path}:{}: antithesis_assert must be an object",
                line_index + 1
            )
        })?;
        let message = assertion
            .get("message")
            .and_then(Value::as_str)
            .filter(|value| !value.trim().is_empty())
            .ok_or_else(|| format!("{path}:{}: assertion message is required", line_index + 1))?;
        let key = property_key(message, assertion.get("details"));
        let property = properties.entry(key).or_insert_with(Property::default);
        property.hits += 1;
        if !assertion
            .get("condition")
            .and_then(Value::as_bool)
            .ok_or_else(|| {
                format!(
                    "{path}:{}: assertion condition must be boolean",
                    line_index + 1
                )
            })?
        {
            property.failures += 1;
        }
        if let Some(details) = comparable_details(assertion.get("details")) {
            if !property.details.contains(&details) {
                property.details.push(details);
            }
        }
    }
    if properties.is_empty() {
        return Err(format!("{path}: no assertion events found"));
    }
    Ok(properties)
}

fn property_key(message: &str, details: Option<&Value>) -> String {
    let normalized = message.trim().to_ascii_lowercase();
    if let Some(details) = details.and_then(Value::as_object) {
        if details.contains_key("operation_id") && details.contains_key("previous_status") {
            return "operation_status_monotonic".to_owned();
        }
        if details.contains_key("runtime_id") && details.contains_key("endpoint") {
            return "deployment_runtime_identity".to_owned();
        }
    }
    if normalized.contains("operation status") && normalized.contains("regress") {
        return "operation_status_monotonic".to_owned();
    }
    if normalized.contains("running deployments") && normalized.contains("runtime identity") {
        return "deployment_runtime_identity".to_owned();
    }
    normalized.replace(' ', "_")
}

fn comparable_details(details: Option<&Value>) -> Option<BTreeMap<String, String>> {
    let details = details?.as_object()?;
    let mut result = BTreeMap::new();
    for field in [
        "operation_kind",
        "previous_status",
        "next_status",
        "runtime_id",
        "endpoint",
    ] {
        if let Some(value) = details.get(field) {
            let value = value.as_str().unwrap_or("<present>").to_owned();
            result.insert(field.to_owned(), value);
        }
    }
    (!result.is_empty()).then_some(result)
}

fn merge(target: &mut BTreeMap<String, Property>, source: BTreeMap<String, Property>) {
    for (key, property) in source {
        let entry = target.entry(key).or_default();
        entry.hits += property.hits;
        entry.failures += property.failures;
        for details in property.details {
            if !entry.details.contains(&details) {
                entry.details.push(details);
            }
        }
    }
}

fn compare(
    reference: &BTreeMap<String, Property>,
    candidate: &BTreeMap<String, Property>,
) -> Vec<String> {
    let mut errors = Vec::from([]);
    for key in reference.keys() {
        let Some(candidate_property) = candidate.get(key) else {
            errors.push(format!("candidate is missing property: {key}"));
            continue;
        };
        let reference_property = &reference[key];
        if reference_property.failures != candidate_property.failures {
            errors.push(format!(
                "{key}: failure count differs ({} vs {})",
                reference_property.failures, candidate_property.failures
            ));
        }
        compare_details(
            &mut errors,
            key,
            &reference_property.details,
            &candidate_property.details,
        );
    }
    errors
}

fn compare_details(
    errors: &mut Vec<String>,
    key: &str,
    reference: &[BTreeMap<String, String>],
    candidate: &[BTreeMap<String, String>],
) {
    for field in ["previous_status", "next_status", "runtime_id", "endpoint"] {
        let reference_values: BTreeSet<_> = reference
            .iter()
            .filter_map(|details| details.get(field))
            .map(|value| {
                if field == "runtime_id" {
                    "<present>"
                } else {
                    value.as_str()
                }
            })
            .collect();
        let candidate_values: BTreeSet<_> = candidate
            .iter()
            .filter_map(|details| details.get(field))
            .map(|value| {
                if field == "runtime_id" {
                    "<present>"
                } else {
                    value.as_str()
                }
            })
            .collect();
        let missing: Vec<_> = reference_values.difference(&candidate_values).collect();
        if !missing.is_empty() {
            errors.push(format!(
                "{key}: candidate lacks {field} evidence {missing:?}"
            ));
        }
    }
}
