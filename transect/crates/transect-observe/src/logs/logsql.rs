//! LogsQL, the query language of VictoriaLogs. This module quotes
//! values, adds filters to a query, and parses a response. A response is
//! JSON lines, with one object for each log line or stats group.

use super::query::Row;

/// `value` escaped for the inside of a double-quoted LogsQL string.
pub fn escape(value: &str) -> String {
    let mut escaped = String::with_capacity(value.len());
    for c in value.chars() {
        match c {
            '\\' | '"' => {
                escaped.push('\\');
                escaped.push(c);
            }
            '\n' => escaped.push_str("\\n"),
            c if c.is_control() => {}
            c => escaped.push(c),
        }
    }
    escaped
}

/// A field name written for LogsQL. A name of only letters, digits,
/// `_`, `.`, and `-`, such as `k8s.pod.name`, is left as it is, and any
/// other name is quoted.
pub fn field(name: &str) -> String {
    let plain = !name.is_empty()
        && name
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || "_.-".contains(c));
    match plain {
        true => name.into(),
        false => format!("\"{}\"", escape(name)),
    }
}

/// A filter for lines whose field `name` is exactly `value`.
pub fn equals(name: &str, value: &str) -> String {
    format!("{}:=\"{}\"", field(name), escape(value))
}

/// A filter for lines whose field `name` is exactly one of `values`.
pub fn one_of(name: &str, values: &[String]) -> String {
    let quoted: Vec<String> = values
        .iter()
        .map(|value| format!("\"{}\"", escape(value)))
        .collect();
    format!("{}:in({})", field(name), quoted.join(", "))
}

/// A filter for the lines that `filter` does not match.
pub fn not(filter: &str) -> String {
    format!("!({filter})")
}

/// A filter for lines whose level field `name` starts with one of
/// `starts`, in any letter case. It matches each start as literal text,
/// so a `.` or a `|` in a start is not a regular expression operator.
pub fn at_least_error(name: &str, starts: &[String]) -> String {
    let starts: Vec<String> = starts.iter().map(|start| literal(start)).collect();
    let pattern = format!("(?i)^({})", starts.join("|"));
    format!("{}:~\"{}\"", field(name), escape(&pattern))
}

/// `text` with each character that a regular expression treats
/// specially escaped, so the expression matches the text literally.
fn literal(text: &str) -> String {
    let mut escaped = String::with_capacity(text.len());
    for c in text.chars() {
        if "\\.+*?()|[]{}^$".contains(c) {
            escaped.push('\\');
        }
        escaped.push(c);
    }
    escaped
}

/// `query` with `filter` added to its filters, before its first pipe.
/// The query's own filters go in parentheses first, so that `filter`
/// applies to the whole of a query such as `a or b`.
pub fn narrow(query: &str, filter: &str) -> String {
    match first_pipe(query) {
        Some(at) => format!(
            "({}) {filter} | {}",
            query[..at].trim(),
            query[at + 1..].trim()
        ),
        None => format!("({}) {filter}", query.trim()),
    }
}

/// `query` with a `fields` pipe, so the response has only `fields`.
pub fn keep(query: &str, fields: &[&str]) -> String {
    let names: Vec<String> = fields.iter().map(|name| field(name)).collect();
    format!("{} | fields {}", query.trim(), names.join(", "))
}

/// The byte offset of the first pipe outside a quoted string.
fn first_pipe(query: &str) -> Option<usize> {
    let mut quote = None;
    let mut escaped = false;
    for (at, c) in query.char_indices() {
        match (quote, c) {
            (Some(_), _) if escaped => escaped = false,
            (Some('"'), '\\') => escaped = true,
            (Some(open), c) if c == open => quote = None,
            (Some(_), _) => {}
            (None, '"' | '\'' | '`') => quote = Some(c),
            (None, '|') => return Some(at),
            (None, _) => {}
        }
    }
    None
}

/// The text that VictoriaLogs returns as the message of a line that has
/// none, such as a blank line that a container wrote. A tail shows such
/// a line as blank, because the text reads like an error.
const NO_MESSAGE: &str = "missing _msg field; see https://docs.victoriametrics.com/";

/// Whether `message` is the placeholder for a line with no message.
pub fn no_message(message: &str) -> bool {
    message.starts_with(NO_MESSAGE)
}

/// The rows of a response. A value that is not a JSON string, such as a
/// number, becomes its JSON text.
pub fn rows(body: &str) -> Result<Vec<Row>, String> {
    let lines = body.lines().filter(|line| !line.trim().is_empty());
    lines
        .map(|line| {
            let object: serde_json::Map<String, serde_json::Value> = serde_json::from_str(line)
                .map_err(|error| format!("the response is not JSON lines: {error}"))?;
            Ok(object
                .into_iter()
                .map(|(key, value)| {
                    let value = match value {
                        serde_json::Value::String(text) => text,
                        other => other.to_string(),
                    };
                    (key, value)
                })
                .collect())
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::plain("web-1", "web-1")]
    #[case::a_quote(r#"say "hi""#, r#"say \"hi\""#)]
    #[case::a_backslash(r"a\b", r"a\\b")]
    #[case::a_newline_and_a_bell("a\nb\u{7}", r"a\nb")]
    fn escapes_a_value_for_double_quotes(#[case] value: &str, #[case] expected: &str) {
        assert_eq!(escape(value), expected);
    }

    #[rstest]
    #[case::dotted("k8s.pod.name", "k8s.pod.name")]
    #[case::underscored("_msg", "_msg")]
    #[case::a_space("pod name", r#""pod name""#)]
    fn quotes_a_field_only_when_it_needs_it(#[case] name: &str, #[case] expected: &str) {
        assert_eq!(field(name), expected);
    }

    #[rstest]
    #[case::before_the_first_pipe(
        "a:x | sort by (_time) desc | limit 5",
        "c:=\"web\"",
        "(a:x) c:=\"web\" | sort by (_time) desc | limit 5"
    )]
    #[case::an_or_keeps_its_meaning("a or b | limit 5", "c", "(a or b) c | limit 5")]
    #[case::a_pipe_in_a_string(r#"_msg:~"a|b" | limit 5"#, "c", r#"(_msg:~"a|b") c | limit 5"#)]
    #[case::an_escaped_quote(
        r#"_msg:"say \"|\"" | limit 5"#,
        "c",
        r#"(_msg:"say \"|\"") c | limit 5"#
    )]
    #[case::no_pipe("a:x", "c", "(a:x) c")]
    fn adds_a_filter_before_the_pipes(
        #[case] query: &str,
        #[case] filter: &str,
        #[case] expected: &str,
    ) {
        assert_eq!(narrow(query, filter), expected);
    }

    #[rstest]
    #[case::one(&["journal"], r#"k8s.daemonset.name:in("journal")"#)]
    #[case::two_with_a_quote(&["a", "b\"c"], r#"k8s.daemonset.name:in("a", "b\"c")"#)]
    fn matches_any_of_several_values(#[case] values: &[&str], #[case] expected: &str) {
        let values: Vec<String> = values.iter().map(|v| v.to_string()).collect();
        assert_eq!(one_of("k8s.daemonset.name", &values), expected);
    }

    #[rstest]
    #[case::words(&["err", "fatal"], r#"severity_text:~"(?i)^(err|fatal)""#)]
    #[case::a_dot_is_itself(&["e.r"], r#"severity_text:~"(?i)^(e\\.r)""#)]
    #[case::a_quote(&["e\"r"], r#"severity_text:~"(?i)^(e\"r)""#)]
    fn matches_a_level_by_its_start(#[case] starts: &[&str], #[case] expected: &str) {
        let starts: Vec<String> = starts.iter().map(|s| s.to_string()).collect();
        assert_eq!(at_least_error("severity_text", &starts), expected);
    }

    #[rstest]
    fn negates_a_filter() {
        assert_eq!(not("a or b"), "!(a or b)");
    }

    #[rstest]
    fn limits_the_response_to_the_named_fields() {
        assert_eq!(
            keep("a | limit 5", &["_time", "_msg", "pod name"]),
            r#"a | limit 5 | fields _time, _msg, "pod name""#
        );
    }

    fn row(pairs: &[(&str, &str)]) -> Row {
        pairs
            .iter()
            .map(|(key, value)| (key.to_string(), value.to_string()))
            .collect()
    }

    #[rstest]
    #[case::lines(
        "{\"_time\":\"2026-01-01T00:00:00Z\",\"_msg\":\"ready\"}\n\n{\"_msg\":\"a \\\"quote\\\"\"}\n",
        vec![row(&[("_time", "2026-01-01T00:00:00Z"), ("_msg", "ready")]), row(&[("_msg", "a \"quote\"")])]
    )]
    #[case::a_number(r#"{"matches":14}"#, vec![row(&[("matches", "14")])])]
    #[case::nothing("", vec![])]
    fn reads_json_lines(#[case] body: &str, #[case] expected: Vec<Row>) {
        assert_eq!(rows(body), Ok(expected));
    }

    #[rstest]
    fn rejects_a_response_that_is_not_json_lines() {
        assert!(rows("bad query").unwrap_err().contains("JSON lines"));
    }
}
