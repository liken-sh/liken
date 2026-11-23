//! A `LogPattern` resource declares a filter in the log source's query
//! language, a severity, and what a match means. transect tags the
//! matching lines of a pod's tail, and searches the whole cluster for
//! the pattern over its window. A match becomes a reason on each
//! namespace where it occurs, or a finding when the severity is
//! `worth-knowing`.

use serde::Deserialize;

use transect_graph::untrusted::capped;
use transect_resources::resources::DocId;

/// The most characters of a pattern's meaning that the page keeps.
const MEANING_CHARS: usize = 300;

/// The window that a pattern searches when it declares none.
const WINDOW: &str = "1h";

#[derive(Clone, Debug, PartialEq)]
pub struct LogPattern {
    pub doc: DocId,
    pub name: String,
    /// A filter in the log source's language, such as
    /// `_msg:~"Connection refused"`.
    pub matches: String,
    pub severity: Severity,
    pub meaning: String,
    /// A duration in the language's syntax, such as `1h`.
    pub window: String,
}

/// How much a match matters. The page shows an error or a warning in
/// the health colors, on the line and on its namespace. A match that is
/// only worth knowing has no color and becomes a finding.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Severity {
    WorthKnowing,
    Warning,
    Error,
}

impl Severity {
    fn named(name: &str) -> Result<Self, String> {
        match name {
            "error" => Ok(Self::Error),
            "warning" => Ok(Self::Warning),
            "worth-knowing" => Ok(Self::WorthKnowing),
            other => Err(format!(
                "severity is error, warning, or worth-knowing, not {other:?}"
            )),
        }
    }

    /// The CSS class of the tag that the page shows on a matching line.
    pub fn tag_class(self) -> &'static str {
        match self {
            Self::Error => "log-tag log-tag-error",
            Self::Warning => "log-tag log-tag-warning",
            Self::WorthKnowing => "log-tag",
        }
    }
}

#[derive(Deserialize)]
struct Document {
    metadata: Metadata,
    spec: Spec,
}

#[derive(Deserialize)]
struct Metadata {
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Spec {
    #[serde(rename = "match")]
    matches: String,
    severity: String,
    meaning: String,
    window: Option<String>,
}

/// The `LogPattern` that `value` declares, or the reason it is not
/// valid.
pub fn parse(doc: DocId, value: serde_yaml_ng::Value) -> Result<LogPattern, String> {
    let document: Document = serde_yaml_ng::from_value(value).map_err(|error| error.to_string())?;
    let spec = document.spec;
    let matches = spec.matches.trim();
    if matches.is_empty() {
        return Err("match is empty".into());
    }
    let window = spec.window.unwrap_or_else(|| WINDOW.into());
    if seconds(&window).is_none() {
        return Err(format!(
            "window is a duration such as 30m, 1h, or 1d, not {window:?}"
        ));
    }
    Ok(LogPattern {
        doc,
        name: document.metadata.name,
        matches: matches.into(),
        severity: Severity::named(&spec.severity)?,
        meaning: capped(&spec.meaning, MEANING_CHARS).ok_or("meaning is empty")?,
        window,
    })
}

/// The number of seconds in `text`, when it is a duration that both
/// LogsQL and Prometheus accept: one or more counts, each followed by a
/// unit, such as `1h30m`. Milliseconds round down to whole seconds.
pub fn seconds(text: &str) -> Option<i64> {
    let units = [
        ("ms", 0),
        ("s", 1),
        ("m", 60),
        ("h", 60 * 60),
        ("d", 24 * 60 * 60),
        ("w", 7 * 24 * 60 * 60),
        ("y", 365 * 24 * 60 * 60),
    ];
    let (mut rest, mut total, mut millis) = (text, 0i64, 0i64);
    while !rest.is_empty() {
        let digits = rest.len() - rest.trim_start_matches(|c: char| c.is_ascii_digit()).len();
        let count: i64 = rest[..digits].parse().ok()?;
        rest = &rest[digits..];
        let (unit, each) = units.iter().find(|(unit, _)| rest.starts_with(unit))?;
        rest = &rest[unit.len()..];
        match *unit {
            "ms" => millis = millis.checked_add(count)?,
            _ => total = total.checked_add(count.checked_mul(*each)?)?,
        }
    }
    (!text.is_empty()).then_some(total + millis / 1000)
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    pub fn pattern(name: &str, matches: &str, severity: Severity) -> LogPattern {
        LogPattern {
            doc: DocId::default(),
            name: name.into(),
            matches: matches.into(),
            severity,
            meaning: format!("{name} means something."),
            window: WINDOW.into(),
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn document(spec: &str) -> serde_yaml_ng::Value {
        let text = format!(
            "apiVersion: transect.liken.sh/v1alpha1\nkind: LogPattern\nmetadata: {{name: refused}}\nspec: {spec}\n"
        );
        serde_yaml_ng::from_str(&text).unwrap()
    }

    #[rstest]
    fn reads_a_pattern_with_the_default_window() {
        let spec = r#"{match: '_msg:~"refused"', severity: error, meaning: "It cannot\nconnect."}"#;
        let pattern = parse(DocId::default(), document(spec)).unwrap();
        assert_eq!(
            (
                pattern.matches.as_str(),
                pattern.severity,
                pattern.meaning.as_str(),
                pattern.window.as_str()
            ),
            (
                r#"_msg:~"refused""#,
                Severity::Error,
                "It cannot connect.",
                "1h"
            )
        );
    }

    #[rstest]
    #[case::an_empty_match("{match: ' ', severity: error, meaning: x}", "match is empty")]
    #[case::an_unknown_severity("{match: a, severity: fatal, meaning: x}", "worth-knowing")]
    #[case::an_empty_meaning("{match: a, severity: error, meaning: ' '}", "meaning is empty")]
    #[case::a_window_that_is_not_a_duration(
        "{match: a, severity: error, meaning: x, window: an hour}",
        "duration"
    )]
    #[case::an_unknown_field("{match: a, severity: error, meaning: x, query: b}", "query")]
    fn rejects_an_invalid_pattern_with_the_reason(#[case] spec: &str, #[case] reason: &str) {
        let error = parse(DocId::default(), document(spec)).unwrap_err();
        assert!(
            error.contains(reason),
            "the error is {error:?}, want it to mention {reason:?}"
        );
    }

    #[rstest]
    #[case::minutes("30m", Some(30 * 60))]
    #[case::compound("1h30m", Some(90 * 60))]
    #[case::milliseconds("1500ms", Some(1))]
    #[case::days("7d", Some(7 * 24 * 60 * 60))]
    #[case::no_unit("60", None)]
    #[case::no_count("h", None)]
    #[case::empty("", None)]
    #[case::words("1 hour", None)]
    fn reads_a_duration(#[case] text: &str, #[case] expected: Option<i64>) {
        assert_eq!(seconds(text), expected);
    }
}
