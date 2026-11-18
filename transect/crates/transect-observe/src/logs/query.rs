//! The queries that transect fills in from a log source's templates,
//! and the language they are written in.
//!
//! A placeholder is a name in braces with nothing else inside, such as
//! `{pod}` or `{fields.pod}`.  Braces that contain anything more, such
//! as LogsQL's stream filter `{app="web"}`, are the query's own text.
//! `{fields.<name>}` becomes the store's name for a field.  The other
//! placeholders become values, quoted the way the language quotes them.
//!
//! Each language is a module of pure functions behind `Language`, so a
//! second store's language needs one more variant and one more module,
//! and no caller changes.

use std::collections::BTreeMap;

use k8s_openapi::jiff::Timestamp;

use super::logsql;
use super::source::Fields;

/// A log store's query language.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Language {
    LogsQl,
}

/// One row of a query's response, for one line or one group, with the
/// value of each field by the field's name.
pub type Row = BTreeMap<String, String>;

impl Language {
    pub fn named(name: &str) -> Result<Self, String> {
        match name {
            "logsql" => Ok(Self::LogsQl),
            other => Err(format!(
                "the language {other:?} is not supported, want logsql (LogsQL)"
            )),
        }
    }

    /// `value` escaped so that the language parses it as the same text
    /// inside double quotes.  The template supplies the quotes.
    pub fn escape(self, value: &str) -> String {
        match self {
            Self::LogsQl => logsql::escape(value),
        }
    }

    /// The store's field `name`, written the way a filter or a pipe
    /// names a field.
    pub fn field(self, name: &str) -> String {
        match self {
            Self::LogsQl => logsql::field(name),
        }
    }

    /// `query`, narrowed to the lines that also match `filter`.
    pub fn narrow(self, query: &str, filter: &str) -> String {
        match self {
            Self::LogsQl => logsql::narrow(query, filter),
        }
    }

    /// A filter for lines whose `field` is exactly `value`.
    pub fn equals(self, field: &str, value: &str) -> String {
        match self {
            Self::LogsQl => logsql::equals(field, value),
        }
    }

    /// A filter for lines whose `field` is exactly one of `values`.
    pub fn one_of(self, field: &str, values: &[String]) -> String {
        match self {
            Self::LogsQl => logsql::one_of(field, values),
        }
    }

    /// A filter for the lines that `filter` does not match.
    pub fn not(self, filter: &str) -> String {
        match self {
            Self::LogsQl => logsql::not(filter),
        }
    }

    /// A filter for lines whose level `field` starts with one of
    /// `starts`, in any case.
    pub fn at_least_error(self, field: &str, starts: &[String]) -> String {
        match self {
            Self::LogsQl => logsql::at_least_error(field, starts),
        }
    }

    /// `query`, changed to return only `fields`.
    pub fn keep(self, query: &str, fields: &[&str]) -> String {
        match self {
            Self::LogsQl => logsql::keep(query, fields),
        }
    }

    /// The rows of a response's body.
    pub fn rows(self, body: &str) -> Result<Vec<Row>, String> {
        match self {
            Self::LogsQl => logsql::rows(body),
        }
    }
}

/// The kind of query that a template declares.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Query {
    /// The newest lines of one pod.
    Tail,
    /// Every match of one pattern across the cluster, counted by
    /// namespace and pod.
    Search,
    /// The newest lines of one pattern across the cluster.
    Lines,
}

impl Query {
    fn name(self) -> &'static str {
        match self {
            Self::Tail => "tail",
            Self::Search => "search",
            Self::Lines => "lines",
        }
    }

    /// The value placeholders that the query may contain, and the ones
    /// it must contain.
    fn values(self) -> (&'static [&'static str], &'static [&'static str]) {
        match self {
            Self::Tail => (
                &["namespace", "pod", "since", "limit"],
                &["namespace", "pod"],
            ),
            Self::Search | Self::Lines => (&["window", "pattern", "limit"], &["window", "pattern"]),
        }
    }
}

/// The values that `fill` writes into one query.  `fill` writes an
/// empty string for a placeholder whose value is `None`, so a caller
/// sets each value that its kind of query uses.  `check` rejects a
/// template with a placeholder that its kind of query does not use.
#[derive(Clone, Debug, Default)]
pub struct Values<'a> {
    pub namespace: Option<&'a str>,
    pub pod: Option<&'a str>,
    /// The time the pod started, so a tail reads no lines older than
    /// the pod.
    pub since: Option<Timestamp>,
    pub limit: Option<usize>,
    /// A duration in the language's own syntax, such as `1h`.
    pub window: Option<&'a str>,
    /// A query fragment, written in the language.
    pub pattern: Option<&'a str>,
}

/// One part of a template: either the template's own text or a
/// placeholder's name.
#[derive(Debug, PartialEq)]
enum Part<'a> {
    Text(&'a str),
    Placeholder(&'a str),
}

fn parts(template: &str) -> Vec<Part<'_>> {
    let mut parts = Vec::new();
    let mut rest = template;
    while let Some(open) = rest.find('{') {
        let after = &rest[open + 1..];
        let close = after.find(['{', '}']);
        let name = close
            .filter(|&at| after[at..].starts_with('}'))
            .map(|at| &after[..at])
            .filter(|name| is_name(name));
        let Some(name) = name else {
            parts.push(Part::Text(&rest[..open + 1]));
            rest = after;
            continue;
        };
        parts.push(Part::Text(&rest[..open]));
        parts.push(Part::Placeholder(name));
        rest = &after[name.len() + 1..];
    }
    parts.push(Part::Text(rest));
    parts.retain(|part| *part != Part::Text(""));
    parts
}

fn is_name(name: &str) -> bool {
    !name.is_empty()
        && name
            .chars()
            .all(|c| c.is_ascii_lowercase() || c == '.' || c == '_')
}

/// Checks that `template` is a valid template for `query`.  It returns
/// the reason when the template has a placeholder that the query does
/// not fill, names a `{fields.<name>}` that the source does not map, or
/// lacks a placeholder that the query requires.
pub fn check(query: Query, template: &str, fields: &Fields) -> Result<(), String> {
    let (allowed, required) = query.values();
    let mut seen = Vec::new();
    for part in parts(template) {
        let Part::Placeholder(name) = part else {
            continue;
        };
        match name.strip_prefix("fields.") {
            Some(field) if fields.named(field).is_some() => {}
            Some(_) => {
                return Err(format!(
                    "the {} query uses {{{name}}}, and the source maps no such field",
                    query.name()
                ));
            }
            None if allowed.contains(&name) => seen.push(name),
            None => {
                return Err(format!(
                    "the {} query has no placeholder {{{name}}}, the placeholders are {}",
                    query.name(),
                    listed(allowed)
                ));
            }
        }
    }
    let missing: Vec<&str> = required
        .iter()
        .copied()
        .filter(|name| !seen.contains(name))
        .collect();
    match missing.as_slice() {
        [] => Ok(()),
        _ => Err(format!(
            "the {} query is missing {}",
            query.name(),
            listed(&missing)
        )),
    }
}

fn listed(names: &[&str]) -> String {
    let braced: Vec<String> = names.iter().map(|name| format!("{{{name}}}")).collect();
    braced.join(", ")
}

/// `template` with each placeholder replaced.
pub fn fill(template: &str, language: Language, fields: &Fields, values: &Values) -> String {
    let mut query = String::new();
    for part in parts(template) {
        match part {
            Part::Text(text) => query.push_str(text),
            Part::Placeholder(name) => query.push_str(&value(name, language, fields, values)),
        }
    }
    query
}

fn value(name: &str, language: Language, fields: &Fields, values: &Values) -> String {
    if let Some(field) = name.strip_prefix("fields.") {
        return fields
            .named(field)
            .map(|field| language.field(field))
            .unwrap_or_default();
    }
    match name {
        "namespace" => values.namespace.map(|v| language.escape(v)),
        "pod" => values.pod.map(|v| language.escape(v)),
        "since" => values.since.map(|at| at.to_string()),
        "limit" => values.limit.map(|limit| limit.to_string()),
        "window" => values.window.map(Into::into),
        "pattern" => values.pattern.map(|pattern| format!("({pattern})")),
        _ => None,
    }
    .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::source::fixtures::source;
    use super::*;

    #[rstest]
    #[case::text_alone("_time:5m", vec![Part::Text("_time:5m")])]
    #[case::a_placeholder(
        "a {pod} b",
        vec![Part::Text("a "), Part::Placeholder("pod"), Part::Text(" b")]
    )]
    #[case::a_field("{fields.pod}:x", vec![Part::Placeholder("fields.pod"), Part::Text(":x")])]
    #[case::a_stream_filter(r#"{app="web"} x"#, vec![Part::Text("{"), Part::Text(r#"app="web"} x"#)])]
    #[case::a_brace_left_open("{pod", vec![Part::Text("{"), Part::Text("pod")])]
    fn reads_placeholders_and_leaves_other_braces(
        #[case] template: &str,
        #[case] expected: Vec<Part>,
    ) {
        assert_eq!(parts(template), expected);
    }

    fn tail(values: Values) -> String {
        let source = source();
        fill(&source.tail, source.language, &source.fields, &values)
    }

    #[rstest]
    #[case::plain_names(
        "web-1",
        r#"_time:>=2026-01-01T00:00:00Z k8s.namespace.name:"shop" k8s.pod.name:"web-1" | sort by (_time) desc | limit 50"#
    )]
    #[case::a_quote_in_a_name(
        r#"we"b"#,
        r#"_time:>=2026-01-01T00:00:00Z k8s.namespace.name:"shop" k8s.pod.name:"we\"b" | sort by (_time) desc | limit 50"#
    )]
    fn fills_the_tail(#[case] pod: &str, #[case] expected: &str) {
        let values = Values {
            namespace: Some("shop"),
            pod: Some(pod),
            since: Some("2026-01-01T00:00:00Z".parse().unwrap()),
            limit: Some(50),
            ..Values::default()
        };
        assert_eq!(tail(values), expected);
    }

    #[rstest]
    fn fills_the_search_with_the_pattern_grouped() {
        let source = source();
        let values = Values {
            window: Some("1h"),
            pattern: Some(r#"_msg:~"a|b""#),
            limit: Some(200),
            ..Values::default()
        };
        assert_eq!(
            fill(&source.search, source.language, &source.fields, &values),
            r#"_time:1h (_msg:~"a|b") | stats by (k8s.namespace.name, k8s.pod.name) count() matches, max(_time) last | sort by (matches) desc | limit 200"#
        );
    }
}
