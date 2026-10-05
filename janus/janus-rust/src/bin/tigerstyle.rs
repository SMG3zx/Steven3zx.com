//! Rust-native `TigerStyle` and Janus architecture baseline audit.
//!
//! This binary performs source-level policy checks that complement rustfmt,
//! Clippy, the Rust compiler, unit tests, scenario tests, and local
//! Antithesis-style verification.
//!
//! `TigerStyle` should enforce only properties that are meaningful to inspect at
//! source level. Behavioral invariants such as tenant authorization,
//! generation fencing, idempotency, lifecycle legality, deterministic replay,
//! and capacity exhaustion must also be verified through Rust tests and
//! assertions.
//!
//! Hard findings fail the audit. Advisories require review but do not fail it.

use std::collections::BTreeMap;
use std::env;
use std::fs;
use std::path::{Path, PathBuf};

const MAX_LINE_BYTES: usize = 120;
const MAX_FUNCTION_LINES: usize = 70;

#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
enum Severity {
    Advisory,
    Hard,
}

impl Severity {
    const fn label(self) -> &'static str {
        match self {
            Self::Advisory => "warning",
            Self::Hard => "error",
        }
    }
}

#[derive(Clone, Copy, Debug)]
struct Rule {
    id: &'static str,
    severity: Severity,
    description: &'static str,
}

const LINE_LENGTH: Rule = Rule {
    id: "TS001",
    severity: Severity::Hard,
    description: "line exceeds maximum length",
};

const UNSAFE_CODE: Rule = Rule {
    id: "TS002",
    severity: Severity::Hard,
    description: "unsafe code is forbidden",
};

const UNBOUNDED_CHANNEL: Rule = Rule {
    id: "TS010",
    severity: Severity::Hard,
    description: "unbounded channel or mailbox is forbidden",
};

const UNBOUNDED_COLLECTION: Rule = Rule {
    id: "TS011",
    severity: Severity::Advisory,
    description: "collection has no explicit initial capacity",
};

const DYNAMIC_CAPACITY: Rule = Rule {
    id: "TS012",
    severity: Severity::Advisory,
    description: "dynamic capacity requires an explicit upper-bound review",
};

const UNBOUNDED_LOOP: Rule = Rule {
    id: "TS020",
    severity: Severity::Advisory,
    description: "loop requires bounded-progress review",
};

const WHILE_TRUE: Rule = Rule {
    id: "TS021",
    severity: Severity::Hard,
    description: "while true is forbidden; use an explicit bounded condition",
};

const PANIC: Rule = Rule {
    id: "TS030",
    severity: Severity::Hard,
    description: "panic macro is forbidden in production control-plane code",
};

const TODO: Rule = Rule {
    id: "TS031",
    severity: Severity::Hard,
    description: "unfinished implementation macro is forbidden",
};

const UNWRAP: Rule = Rule {
    id: "TS032",
    severity: Severity::Advisory,
    description: "unwrap requires explicit failure-policy review",
};

const EXPECT: Rule = Rule {
    id: "TS033",
    severity: Severity::Advisory,
    description: "expect requires explicit failure-policy review",
};

const UNREACHABLE: Rule = Rule {
    id: "TS034",
    severity: Severity::Advisory,
    description: "unreachable requires invariant review",
};

const DIRECT_TIME: Rule = Rule {
    id: "TS040",
    severity: Severity::Hard,
    description: "direct wall-clock access is forbidden in deterministic core code",
};

const DIRECT_RANDOMNESS: Rule = Rule {
    id: "TS041",
    severity: Severity::Hard,
    description: "direct randomness is forbidden in deterministic core code",
};

const RANDOM_UUID: Rule = Rule {
    id: "TS042",
    severity: Severity::Hard,
    description: "random UUID generation is forbidden in deterministic core code",
};

const ENV_ACCESS: Rule = Rule {
    id: "TS043",
    severity: Severity::Advisory,
    description: "direct environment access requires adapter-boundary review",
};

const STATIC_MUT: Rule = Rule {
    id: "TS044",
    severity: Severity::Hard,
    description: "mutable static state is forbidden",
};

const INTEGER_CAST: Rule = Rule {
    id: "TS050",
    severity: Severity::Advisory,
    description: "integer cast requires narrowing and bounds review",
};

const IGNORED_RESULT: Rule = Rule {
    id: "TS060",
    severity: Severity::Advisory,
    description: "discarded result requires explicit review",
};

const BLOCKING_SLEEP: Rule = Rule {
    id: "TS070",
    severity: Severity::Advisory,
    description: "blocking sleep requires runtime-boundary review",
};

const LOCK_USAGE: Rule = Rule {
    id: "TS071",
    severity: Severity::Advisory,
    description: "lock acquisition requires coordination and await-boundary review",
};

const UNBOUNDED_READ: Rule = Rule {
    id: "TS080",
    severity: Severity::Advisory,
    description: "potentially unbounded read requires input-size review",
};

const FUNCTION_LENGTH: Rule = Rule {
    id: "TS090",
    severity: Severity::Advisory,
    description: "function exceeds recommended size",
};

#[derive(Debug)]
struct Finding {
    rule: Rule,
    path: PathBuf,
    line: usize,
    detail: Option<String>,
}

#[derive(Default)]
struct Audit {
    files: usize,
    findings: Vec<Finding>,
}

impl Audit {
    fn report(&mut self, rule: Rule, path: &Path, line: usize, detail: impl Into<Option<String>>) {
        self.findings.push(Finding {
            rule,
            path: path.to_path_buf(),
            line,
            detail: detail.into(),
        });
    }

    fn hard_findings(&self) -> usize {
        self.findings
            .iter()
            .filter(|finding| finding.rule.severity == Severity::Hard)
            .count()
    }

    fn advisories(&self) -> usize {
        self.findings
            .iter()
            .filter(|finding| finding.rule.severity == Severity::Advisory)
            .count()
    }
}

fn rust_files(root: &Path, output: &mut Vec<PathBuf>) -> std::io::Result<()> {
    if !root.is_dir() {
        return Ok(());
    }

    let mut pending = vec![root.to_path_buf()];

    while let Some(directory) = pending.pop() {
        for entry in fs::read_dir(directory)? {
            let entry = entry?;
            let path = entry.path();

            if path.is_dir() {
                pending.push(path);
            } else if path.extension().is_some_and(|extension| extension == "rs") {
                output.push(path);
            }
        }
    }

    Ok(())
}

fn is_tigerstyle_file(path: &Path) -> bool {
    path.file_name().is_some_and(|name| name == "tigerstyle.rs")
}

fn is_generated_binding_path(path: &Path) -> bool {
    path.components()
        .any(|component| component.as_os_str() == "module_bindings")
}

fn is_probable_test_path(path: &Path) -> bool {
    path.components().any(|component| {
        let value = component.as_os_str();
        value == "tests" || value == "benches"
    })
}

fn line_is_comment(line: &str) -> bool {
    let trimmed = line.trim_start();
    trimmed.starts_with("//") || trimmed.starts_with('*')
}

fn line_is_test_attribute(line: &str) -> bool {
    let trimmed = line.trim();
    trimmed == "#[test]"
        || trimmed.starts_with("#[cfg(test")
        || trimmed.starts_with("#[cfg_attr(test")
}

fn brace_delta(line: &str) -> isize {
    let mut delta = 0isize;
    let mut in_string = false;
    let mut escaped = false;

    for character in line.chars() {
        if in_string {
            if escaped {
                escaped = false;
            } else if character == '\\' {
                escaped = true;
            } else if character == '"' {
                in_string = false;
            }
            continue;
        }

        match character {
            '"' => in_string = true,
            '{' => delta += 1,
            '}' => delta -= 1,
            _ => {}
        }
    }

    delta
}

fn audit_function_lengths(path: &Path, lines: &[&str], audit: &mut Audit) {
    let mut function_start = None;
    let mut brace_depth = 0isize;
    let mut seen_open_brace = false;

    for (index, line) in lines.iter().enumerate() {
        let trimmed = line.trim_start();

        if function_start.is_none()
            && (trimmed.starts_with("fn ")
                || trimmed.starts_with("pub fn ")
                || trimmed.starts_with("pub(crate) fn ")
                || trimmed.starts_with("pub(super) fn ")
                || trimmed.starts_with("async fn ")
                || trimmed.starts_with("pub async fn ")
                || trimmed.starts_with("pub(crate) async fn "))
        {
            function_start = Some(index);
            brace_depth = 0;
            seen_open_brace = false;
        }

        if let Some(start) = function_start {
            if !seen_open_brace && line.contains(';') {
                function_start = None;
                brace_depth = 0;
                seen_open_brace = false;
                continue;
            }
            let delta = brace_delta(line);
            if line.contains('{') {
                seen_open_brace = true;
            }
            brace_depth += delta;

            if seen_open_brace && brace_depth <= 0 {
                let length = index + 1 - start;
                if length > MAX_FUNCTION_LINES {
                    audit.report(
                        FUNCTION_LENGTH,
                        path,
                        start + 1,
                        Some(format!(
                            "function-lines={length} recommended-max={MAX_FUNCTION_LINES}"
                        )),
                    );
                }

                function_start = None;
                brace_depth = 0;
                seen_open_brace = false;
            }
        }
    }
}

fn audit_line(audit: &mut Audit, path: &Path, line_number: usize, line: &str, test_context: bool) {
    if line.len() > MAX_LINE_BYTES {
        audit.report(
            LINE_LENGTH,
            path,
            line_number,
            Some(format!("line-bytes={} limit={MAX_LINE_BYTES}", line.len())),
        );
    }
    if line_is_comment(line) {
        return;
    }
    audit_safety(audit, path, line_number, line);
    if test_context {
        return;
    }
    audit_bounds(audit, path, line_number, line);
    audit_failure_policy(audit, path, line_number, line);
    audit_time_randomness(audit, path, line_number, line);
    audit_data_access(audit, path, line_number, line);
}

fn audit_safety(audit: &mut Audit, path: &Path, line_number: usize, line: &str) {
    if !is_tigerstyle_file(path)
        && [
            "unsafe {",
            "unsafe fn",
            "unsafe impl",
            "unsafe trait",
            "unsafe extern",
        ]
        .iter()
        .any(|token| line.contains(token))
    {
        audit.report(UNSAFE_CODE, path, line_number, None);
    }
    if line.contains("static mut ") {
        audit.report(STATIC_MUT, path, line_number, None);
    }
}

fn audit_bounds(audit: &mut Audit, path: &Path, line_number: usize, line: &str) {
    if [
        "unbounded_channel(",
        "mpsc::unbounded_channel(",
        "crossbeam_channel::unbounded(",
        "async_channel::unbounded(",
    ]
    .iter()
    .any(|token| line.contains(token))
    {
        audit.report(UNBOUNDED_CHANNEL, path, line_number, None);
    }
    if [
        "Vec::new()",
        "VecDeque::new()",
        "HashMap::new()",
        "HashSet::new()",
        "BinaryHeap::new()",
        "String::new()",
    ]
    .iter()
    .any(|token| line.contains(token))
    {
        audit.report(UNBOUNDED_COLLECTION, path, line_number, None);
    }
    if [
        "Vec::with_capacity(",
        "VecDeque::with_capacity(",
        "HashMap::with_capacity(",
        "HashSet::with_capacity(",
        "String::with_capacity(",
    ]
    .iter()
    .any(|token| line.contains(token) && !has_symbolic_capacity_bound(line))
    {
        audit.report(DYNAMIC_CAPACITY, path, line_number, None);
    }
    let trimmed = line.trim();
    if trimmed.starts_with("while true ") || trimmed.starts_with("while true{") {
        audit.report(WHILE_TRUE, path, line_number, None);
    } else if trimmed == "loop {" || trimmed.starts_with("loop {") {
        audit.report(UNBOUNDED_LOOP, path, line_number, None);
    }
}

fn audit_failure_policy(audit: &mut Audit, path: &Path, line_number: usize, line: &str) {
    if line.contains("panic!(") {
        audit.report(PANIC, path, line_number, None);
    }
    if line.contains("todo!(") || line.contains("unimplemented!(") {
        audit.report(TODO, path, line_number, None);
    }
    if line.contains(".unwrap()") || line.contains(".unwrap_err()") {
        audit.report(UNWRAP, path, line_number, None);
    }
    if line.contains(".expect(") || line.contains(".expect_err(") {
        audit.report(EXPECT, path, line_number, None);
    }
    if line.contains("unreachable!(") && !line.contains("tigerstyle: invariant-checked") {
        audit.report(UNREACHABLE, path, line_number, None);
    }
}

fn audit_time_randomness(audit: &mut Audit, path: &Path, line_number: usize, line: &str) {
    if !line.contains("tigerstyle: allow-direct-time")
        && ["SystemTime::now()", "Utc::now()", "Local::now()"]
            .iter()
            .any(|token| line.contains(token))
    {
        audit.report(DIRECT_TIME, path, line_number, None);
    }
    if !line.contains("tigerstyle: allow-direct-randomness")
        && [
            "thread_rng(",
            "rand::random(",
            "rand::rng(",
            "OsRng",
            "getrandom(",
        ]
        .iter()
        .any(|token| line.contains(token))
    {
        audit.report(DIRECT_RANDOMNESS, path, line_number, None);
    }
    if line.contains("Uuid::new_v4(") || line.contains("Uuid::new_v7(") {
        audit.report(RANDOM_UUID, path, line_number, None);
    }
    if !line.contains("tigerstyle: allow-direct-env")
        && !is_tigerstyle_file(path)
        && ["std::env::", "env::var(", "env::vars("]
            .iter()
            .any(|token| line.contains(token))
    {
        audit.report(ENV_ACCESS, path, line_number, None);
    }
    if [
        " as u8",
        " as u16",
        " as u32",
        " as u64",
        " as u128",
        " as usize",
        " as i8",
        " as i16",
        " as i32",
        " as i64",
        " as i128",
        " as isize",
    ]
    .iter()
    .any(|token| line.contains(token))
    {
        audit.report(INTEGER_CAST, path, line_number, None);
    }
}

fn audit_data_access(audit: &mut Audit, path: &Path, line_number: usize, line: &str) {
    if line.trim().starts_with("let _ = ") {
        audit.report(IGNORED_RESULT, path, line_number, None);
    }
    if line.contains("std::thread::sleep(") {
        audit.report(BLOCKING_SLEEP, path, line_number, None);
    }
    if [
        ".read_to_end(",
        ".read_to_string(",
        "fs::read(",
        "fs::read_to_string(",
    ]
    .iter()
    .any(|token| line.contains(token))
    {
        audit.report(UNBOUNDED_READ, path, line_number, None);
    }
}

fn has_symbolic_capacity_bound(line: &str) -> bool {
    let Some(open) = line.find("with_capacity(") else {
        return false;
    };
    let argument = line[open + "with_capacity(".len()..]
        .split(')')
        .next()
        .unwrap_or_default()
        .trim();
    let root = argument.split('.').next().unwrap_or_default().trim();
    if !root.is_empty()
        && root.chars().all(|character| {
            character.is_ascii_uppercase() || character.is_ascii_digit() || character == '_'
        })
    {
        return true;
    }
    if let Some(minimum) = argument.split_once(".min(").map(|(_, value)| value) {
        let bound = minimum.trim_end_matches(')').trim();
        let bound = bound.strip_prefix("crate::").unwrap_or(bound);
        if !bound.is_empty()
            && bound.chars().all(|character| {
                character.is_ascii_uppercase()
                    || character.is_ascii_digit()
                    || "_:".contains(character)
            })
        {
            return true;
        }
    }
    !argument.is_empty()
        && argument.chars().all(|character| {
            character.is_ascii_uppercase()
                || character.is_ascii_digit()
                || "_{}<>: ,".contains(character)
        })
}

fn audit_file(path: &Path, audit: &mut Audit) -> Result<(), Box<dyn std::error::Error>> {
    let source = fs::read_to_string(path)?;
    let lines: Vec<_> = source.lines().collect();

    audit.files += 1;

    let path_is_test = is_probable_test_path(path);
    let mut pending_test_attribute = false;
    let mut test_scope_depth: Option<isize> = None;
    let mut brace_depth = 0isize;
    let mut pending_locks: Vec<(isize, usize)> = Vec::new();

    for (index, line) in lines.iter().enumerate() {
        let line_number = index + 1;

        pending_locks.retain(|(scope_depth, _)| brace_depth >= *scope_depth);
        if line.contains(".await") && !pending_locks.is_empty() {
            for (_, lock_line) in std::mem::take(&mut pending_locks) {
                audit.report(LOCK_USAGE, path, lock_line, None);
            }
        }

        if line_is_test_attribute(line) {
            pending_test_attribute = true;
        }

        let currently_in_test_scope = path_is_test || test_scope_depth.is_some();

        audit_line(
            audit,
            path,
            line_number,
            line,
            currently_in_test_scope || pending_test_attribute,
        );

        let delta = brace_delta(line);

        if [".lock()", ".read()", ".write()"]
            .iter()
            .any(|token| line.contains(token))
        {
            pending_locks.push((brace_depth + delta, line_number));
        }

        if pending_test_attribute && line.contains('{') {
            test_scope_depth = Some(brace_depth + delta);
            pending_test_attribute = false;
        }

        brace_depth += delta;

        if let Some(scope_depth) = test_scope_depth {
            if brace_depth < scope_depth {
                test_scope_depth = None;
            }
        }

        if pending_test_attribute
            && !line.trim().starts_with('#')
            && !line.trim().is_empty()
            && !line.contains('{')
        {
            pending_test_attribute = false;
        }
    }

    if !path_is_test {
        audit_function_lengths(path, &lines, audit);
    }

    Ok(())
}

fn print_report(audit: &Audit) {
    for finding in &audit.findings {
        print!(
            "{}[{}] {}:{}: {}",
            finding.rule.severity.label(),
            finding.rule.id,
            finding.path.display(),
            finding.line,
            finding.rule.description
        );

        if let Some(detail) = &finding.detail {
            print!(" ({detail})");
        }

        println!();
    }

    let mut counts = BTreeMap::<&str, usize>::new();
    for finding in &audit.findings {
        *counts.entry(finding.rule.id).or_default() += 1;
    }

    println!();
    println!("TigerStyle Rust audit");
    println!("  files:       {}", audit.files);
    println!("  errors:      {}", audit.hard_findings());
    println!("  advisories:  {}", audit.advisories());

    if !counts.is_empty() {
        println!();
        println!("Findings by rule:");
        for (rule, count) in counts {
            println!("  {rule}: {count}");
        }
    }

    println!();

    if audit.hard_findings() == 0 {
        println!("PASS: TigerStyle hard findings remain zero.");
    } else {
        println!("FAIL: TigerStyle hard findings must be zero.");
    }
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    let mut files = Vec::new();

    rust_files(&manifest.join("src"), &mut files)?;

    let spacetimedb_root = manifest.join("../janus-spacetimedb/src");
    rust_files(&spacetimedb_root, &mut files)?;

    files.sort();
    files.dedup();
    // The auditor intentionally contains source-scanning patterns that would
    // report on its own implementation; audit the code under test instead.
    files.retain(|path| !is_tigerstyle_file(path) && !is_generated_binding_path(path));

    let mut audit = Audit::default();

    for path in &files {
        audit_file(path, &mut audit)?;
    }

    print_report(&audit);

    if audit.hard_findings() != 0 {
        return Err("TigerStyle audit failed".into());
    }

    Ok(())
}
