//! Whether a log line looks like an error or a warning.  The rule is
//! deliberately small, and this list is all of it:
//!
//! 1. A level field, `level`, `lvl`, or `severity`, written as
//!    `level=error` or `"level":"error"`, whose value starts with
//!    `err`, `fatal`, `panic`, or `crit` is an error, and one that
//!    starts with `warn` is a warning, in any case.
//! 2. A panic, Rust's `panicked at` anywhere in the line or Go's
//!    `panic:` at its start, is an error.
//! 3. A klog header, `E1108` or `W1108` with the month and day, at the
//!    start of the line, is an error or a warning.
//! 4. Otherwise a whole word in capitals, `ERROR`, `ERR`, `FATAL`,
//!    `PANIC`, or `CRITICAL`, is an error, and `WARN` or `WARNING` a
//!    warning.  Lowercase words do not count, because an ordinary line
//!    such as "retrying after error" is not an error.
//!
//! When transect reads a line from a log store, and the line's level
//! field holds a value that the `LogSource`'s `levels` name, the line
//! gets that level instead, because the store read that value from the
//! line's own structure or from the runtime that wrote the line.

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Level {
    Plain,
    Warning,
    Error,
}

const FIELDS: [&str; 3] = ["level", "lvl", "severity"];
const ERROR_VALUES: [&str; 4] = ["err", "fatal", "panic", "crit"];
const ERROR_WORDS: [&str; 5] = ["ERROR", "ERR", "FATAL", "PANIC", "CRITICAL"];
const WARNING_WORDS: [&str; 2] = ["WARN", "WARNING"];

pub fn level(line: &str) -> Level {
    field(line)
        .or_else(|| panic(line))
        .or_else(|| klog(line))
        .unwrap_or_else(|| words(line))
}

/// The level that the value of the first level field names, matched by
/// the start of the value.
fn field(line: &str) -> Option<Level> {
    let lower = line.to_ascii_lowercase();
    for name in FIELDS {
        for (at, _) in lower.match_indices(name) {
            let before = lower[..at].chars().next_back();
            if before.is_some_and(|c| c.is_ascii_alphanumeric() || c == '_') {
                continue;
            }
            let rest = lower[at + name.len()..].trim_start_matches('"');
            let Some(value) = rest.strip_prefix('=').or_else(|| rest.strip_prefix(':')) else {
                continue;
            };
            let value = value.trim_start().trim_start_matches('"');
            if ERROR_VALUES.iter().any(|start| value.starts_with(start)) {
                return Some(Level::Error);
            }
            if value.starts_with("warn") {
                return Some(Level::Warning);
            }
            return Some(Level::Plain);
        }
    }
    None
}

fn panic(line: &str) -> Option<Level> {
    (line.contains("panicked at") || line.starts_with("panic:")).then_some(Level::Error)
}

fn klog(line: &str) -> Option<Level> {
    let mut chars = line.chars();
    let level = match chars.next()? {
        'E' => Level::Error,
        'W' => Level::Warning,
        _ => return None,
    };
    let digits: String = chars.take(4).collect();
    (digits.len() == 4 && digits.chars().all(|c| c.is_ascii_digit())).then_some(level)
}

fn words(line: &str) -> Level {
    let mut found = Level::Plain;
    for word in line.split(|c: char| !c.is_ascii_alphabetic()) {
        if ERROR_WORDS.contains(&word) {
            return Level::Error;
        }
        if WARNING_WORDS.contains(&word) {
            found = Level::Warning;
        }
    }
    found
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::logfmt_error("time=12:00 level=error msg=\"dial failed\"", Level::Error)]
    #[case::logfmt_info("time=12:00 level=info msg=\"error budget fine\"", Level::Plain)]
    #[case::json_warning(r#"{"level":"warn","msg":"slow"}"#, Level::Warning)]
    #[case::json_spaced(r#"{"severity": "CRITICAL", "msg": "down"}"#, Level::Error)]
    #[case::short_field("lvl=ERR msg=refused", Level::Error)]
    #[case::field_inside_a_word("sublevel=error", Level::Plain)]
    #[case::klog_error("E1108 12:00:00.000 1 main.go:10] failed", Level::Error)]
    #[case::klog_warning("W1108 12:00:00.000 1 main.go:10] slow", Level::Warning)]
    #[case::klog_lookalike("Everything is fine", Level::Plain)]
    #[case::capital_marker("2026-01-01 12:00 ERROR could not connect", Level::Error)]
    #[case::bracketed_marker("[WARN] cache cold", Level::Warning)]
    #[case::short_marker("ERR connection refused", Level::Error)]
    #[case::error_beats_warning("WARN then FATAL", Level::Error)]
    #[case::rust_panic("thread 'main' panicked at src/main.rs:4:5:", Level::Error)]
    #[case::go_panic("panic: runtime error: index out of range", Level::Error)]
    #[case::the_word_panic_in_prose("no reason to panic: all is well", Level::Plain)]
    #[case::lowercase_prose("retrying after error", Level::Plain)]
    #[case::plain("GET /healthz 200", Level::Plain)]
    fn a_line_takes_its_level(#[case] line: &str, #[case] expected: Level) {
        assert_eq!(level(line), expected);
    }
}
