//! The newest lines that a search matched across the cluster, read
//! through the source's `lines` query, so a finding shows a few of the
//! lines it counts instead of a number alone.
//!
//! One query for each search reads at most `SAMPLE_ROWS` lines, newest
//! first, and each finding keeps the few that came from its own pods.
//! A pod that logs far more than the others can fill those rows, so a
//! quieter workload's finding may show its counts with no lines.  The
//! pod page of that workload's pod still reads its lines.

use k8s_openapi::jiff::Timestamp;

use super::query::{Row, Values, fill};
use super::search::named;
use super::source::{Fields, Reached};
use transect_graph::untrusted::capped;

/// The most lines one search's `lines` query reads.
const SAMPLE_ROWS: usize = 200;

/// How many characters of one line a finding shows.
const SAMPLE_CHARS: usize = 240;

/// One line that a search matched, with the place it was logged.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Sample {
    pub namespace: Option<String>,
    pub pod: Option<String>,
    pub container: Option<String>,
    pub node: Option<String>,
    pub time: Option<Timestamp>,
    pub message: String,
}

/// The `lines` query for `filter` over `window`, which requests only
/// the fields that a sample shows.  It is `None` when the source
/// declares no `lines` query.
pub fn query(reached: &Reached, filter: &str, window: &str) -> Option<String> {
    let source = &reached.source;
    let template = source.lines.as_ref()?;
    let values = Values {
        window: Some(window),
        pattern: Some(filter),
        limit: Some(SAMPLE_ROWS),
        ..Values::default()
    };
    let query = fill(template, source.language, &source.fields, &values);
    let fields = &source.fields;
    let shown = [
        Some(&fields.time),
        Some(&fields.message),
        Some(&fields.namespace),
        Some(&fields.pod),
        Some(&fields.container),
        fields.node.as_ref(),
    ];
    let shown: Vec<&str> = shown.into_iter().flatten().map(String::as_str).collect();
    Some(source.language.keep(&query, &shown))
}

/// The samples in the rows of a `lines` response.  This function leaves
/// out a row whose message is empty after the cut, because it would
/// show nothing.
pub fn read(rows: &[Row], fields: &Fields) -> Vec<Sample> {
    let samples = rows.iter().filter_map(|row| {
        let message = row.get(&fields.message)?;
        Some(Sample {
            namespace: named(row, Some(&fields.namespace)),
            pod: named(row, Some(&fields.pod)),
            container: named(row, Some(&fields.container)),
            node: named(row, fields.node.as_ref()),
            time: row.get(&fields.time).and_then(|time| time.parse().ok()),
            message: capped(message, SAMPLE_CHARS)?,
        })
    });
    samples.collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::source::fixtures::source;
    use super::*;

    fn row(pairs: &[(&str, &str)]) -> Row {
        pairs
            .iter()
            .map(|(key, value)| (key.to_string(), value.to_string()))
            .collect()
    }

    #[rstest]
    fn asks_for_the_newest_lines_with_the_fields_a_sample_shows() {
        let reached = source().reached(None, "cluster.local");
        assert_eq!(
            query(&reached, r#""refused""#, "1h").as_deref(),
            Some(
                r#"_time:1h ("refused") | sort by (_time) desc | limit 200 | fields _time, _msg, k8s.namespace.name, k8s.pod.name, k8s.container.name, k8s.node.name"#
            )
        );
    }

    #[rstest]
    fn asks_nothing_without_a_lines_query() {
        let mut source = source();
        source.lines = None;
        let reached = source.reached(None, "cluster.local");
        assert_eq!(query(&reached, "x", "1h"), None);
    }

    #[rstest]
    #[case::a_pods_line(
        row(&[("_time", "2026-01-01T12:00:00Z"), ("_msg", "dial tcp: connection\nrefused"), ("k8s.namespace.name", "shop"), ("k8s.pod.name", "web-0"), ("k8s.container.name", "web"), ("k8s.node.name", "machine-a")]),
        vec![Sample {
            namespace: Some("shop".into()),
            pod: Some("web-0".into()),
            container: Some("web".into()),
            node: Some("machine-a".into()),
            time: "2026-01-01T12:00:00Z".parse().ok(),
            message: "dial tcp: connection refused".into(),
        }]
    )]
    #[case::a_machines_line(
        row(&[("_msg", "kernel: oops"), ("k8s.node.name", "machine-a")]),
        vec![Sample { node: Some("machine-a".into()), message: "kernel: oops".into(), ..Sample::default() }]
    )]
    #[case::an_empty_message(row(&[("_msg", " \u{7} ")]), vec![])]
    #[case::no_message(row(&[("k8s.pod.name", "web-0")]), vec![])]
    fn reads_samples_from_rows(#[case] row: Row, #[case] expected: Vec<Sample>) {
        assert_eq!(read(&[row], &source().fields), expected);
    }
}
