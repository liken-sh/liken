//! Searches the logs of the whole cluster for each pattern over the
//! pattern's window, and for error lines, by the level field, over the
//! last hour.  The searches run in the background every `EVERY`, and
//! again when the source or the patterns change.
//!
//! Each search is a stats query that groups its matches by namespace
//! and pod, returns at most `ROWS` rows, and stops at `TIMEOUT`.  So a
//! search costs the store one scan of its window, however many lines
//! match.

use std::sync::Arc;
use std::time::Duration;

use k8s_openapi::jiff::Timestamp;
use tokio::sync::watch;

use super::Sourcing;
use super::machines::MachineCount;
use super::patterns::LogPattern;
use super::query::{Row, Values, fill};
use super::round;
use super::sample::Sample;
use super::source::{Fields, Reached};
use super::store::Store;

/// How often the searches run.
const EVERY: Duration = Duration::from_secs(300);

/// How long one search may take.
pub const TIMEOUT: Duration = Duration::from_secs(15);

/// The most rows one search reads.  `search_query` writes it into the
/// template's `{limit}`.
pub const ROWS: usize = 200;

/// The window that transect counts the error lines over.
pub const ERRORS_WINDOW: &str = "1h";

/// The matches of one search in one namespace, and in one pod,
/// container, or node when the search groups by it.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Count {
    pub namespace: String,
    pub pod: Option<String>,
    pub container: Option<String>,
    pub node: Option<String>,
    pub matches: usize,
    pub last: Option<Timestamp>,
}

/// The results of one round of searches.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Searched {
    /// The log source searched, or `None` when the cluster declares
    /// none.
    pub source: Option<String>,
    /// The error lines by namespace and pod, or `None` when the source
    /// maps no level field.
    pub errors: Option<Result<Vec<Count>, String>>,
    /// Each pattern, with the counts its search found or the reason it
    /// found nothing.
    pub patterns: Vec<(LogPattern, Result<Vec<Count>, String>)>,
    /// The newest matching lines of each search, when the source
    /// declares a `lines` query.
    pub samples: Vec<(SearchTarget, Vec<Sample>)>,
    /// The machines' own lines that each search matched, by machine and
    /// unit, for the searches that the store responded to.
    pub machines: Vec<(SearchTarget, Vec<MachineCount>)>,
    /// The newest of the machines' own lines that each search matched.
    pub machine_samples: Vec<(SearchTarget, Vec<Sample>)>,
}

/// Which search a result belongs to: the search for error lines, or
/// the search for one pattern, by the pattern's name.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SearchTarget {
    Errors,
    Pattern(String),
}

impl Searched {
    /// The newest lines that the search for `target` read, newest first.
    pub fn samples(&self, target: &SearchTarget) -> &[Sample] {
        found(&self.samples, target)
    }

    /// The machines' own lines that the search for `target` counted.
    pub fn machines(&self, target: &SearchTarget) -> &[MachineCount] {
        found(&self.machines, target)
    }

    /// The newest of the machines' own lines that the search for
    /// `target` read, newest first.
    pub fn machine_samples(&self, target: &SearchTarget) -> &[Sample] {
        found(&self.machine_samples, target)
    }
}

fn found<'a, T>(all: &'a [(SearchTarget, Vec<T>)], target: &SearchTarget) -> &'a [T] {
    let found = all.iter().find(|(one, _)| one == target);
    found.map_or(&[], |(_, found)| found.as_slice())
}

/// The counts in a search's rows.  This function drops a row with no
/// namespace, because it counts lines from outside any pod, such as a
/// machine's own log, and the namespace views cannot show those.
pub fn counts(rows: &[Row], fields: &Fields) -> Vec<Count> {
    let counts = rows.iter().filter_map(|row| {
        Some(Count {
            namespace: row
                .get(&fields.namespace)
                .filter(|ns| !ns.is_empty())?
                .clone(),
            pod: named(row, Some(&fields.pod)),
            container: named(row, Some(&fields.container)),
            node: named(row, fields.node.as_ref()),
            matches: row.get("matches")?.parse().ok()?,
            last: row.get("last").and_then(|last| last.parse().ok()),
        })
    });
    counts.filter(|count| count.matches > 0).collect()
}

/// The value of `field` in `row`, unless it is empty or unmapped.
pub fn named(row: &Row, field: Option<&String>) -> Option<String> {
    let value = row.get(field?)?;
    (!value.is_empty()).then(|| value.clone())
}

/// The search query for `filter` over `window`.
pub fn search_query(reached: &Reached, filter: &str, window: &str) -> String {
    let source = &reached.source;
    let values = Values {
        window: Some(window),
        pattern: Some(filter),
        limit: Some(ROWS),
        ..Values::default()
    };
    fill(&source.search, source.language, &source.fields, &values)
}

/// Runs every search now, then every `EVERY`, and again when the source
/// or the patterns change.  After each round it sends on `changed`, so
/// health and the findings read the new results even when no resource
/// changed.
pub fn watch(
    store: Store,
    sourcing: Sourcing,
    changed: watch::Sender<()>,
) -> watch::Receiver<Arc<Searched>> {
    let (sender, receiver) = watch::channel(Arc::new(Searched::default()));
    // Each load of the resources wakes the loop, but it starts a new
    // round only when the source or the patterns differ from the last
    // round's.
    let mut resources = sourcing.patterns.clone();
    tokio::spawn(async move {
        loop {
            let now = current(&sourcing);
            let searched = round::round(&store, &now, TIMEOUT).await;
            sender.send_replace(Arc::new(searched));
            changed.send_replace(());
            let due = tokio::time::sleep(EVERY);
            tokio::pin!(due);
            loop {
                tokio::select! {
                    _ = &mut due => break,
                    result = resources.changed() => {
                        if result.is_err() {
                            return;
                        }
                        if current(&sourcing) != now {
                            break;
                        }
                    }
                }
            }
        }
    });
    receiver
}

/// The source and the patterns that a round searches.
fn current(sourcing: &Sourcing) -> (Option<Reached>, Vec<LogPattern>) {
    let patterns = sourcing.patterns.read();
    (sourcing.reached(), patterns)
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

    fn count(namespace: &str, pod: Option<&str>, matches: usize) -> Count {
        Count {
            namespace: namespace.into(),
            pod: pod.map(Into::into),
            matches,
            last: "2026-01-01T12:00:00Z".parse().ok(),
            ..Count::default()
        }
    }

    const LAST: (&str, &str) = ("last", "2026-01-01T12:00:00Z");

    #[rstest]
    #[case::by_pod(
        vec![row(&[("k8s.namespace.name", "shop"), ("k8s.pod.name", "web-1"), ("matches", "14"), LAST])],
        vec![count("shop", Some("web-1"), 14)]
    )]
    #[case::by_namespace(
        vec![row(&[("k8s.namespace.name", "shop"), ("matches", "3"), LAST])],
        vec![count("shop", None, 3)]
    )]
    #[case::outside_any_namespace(vec![row(&[("matches", "12"), LAST])], vec![])]
    #[case::not_a_count(vec![row(&[("k8s.namespace.name", "shop"), ("matches", "many")])], vec![])]
    fn reads_counts_from_rows(#[case] rows: Vec<Row>, #[case] expected: Vec<Count>) {
        assert_eq!(counts(&rows, &source().fields), expected);
    }

    #[rstest]
    fn fills_a_search_with_its_window_and_the_row_limit() {
        let reached = source().reached(None, "cluster.local");
        let errors = &reached.source.levels.error;
        let filter = reached
            .source
            .language
            .at_least_error("severity_text", errors);
        assert_eq!(
            search_query(&reached, &filter, "1h"),
            r#"_time:1h (severity_text:~"(?i)^(err|fatal|panic|crit)") | stats by (k8s.namespace.name, k8s.pod.name) count() matches, max(_time) last | sort by (matches) desc | limit 200"#
        );
    }
}
