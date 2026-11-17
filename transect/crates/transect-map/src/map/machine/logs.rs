//! The machine page's own logs: what the machine's kubelet, container
//! runtime, or kernel logged at the error level, and which patterns
//! matched there, by unit, with the newest of those lines.  The log
//! source's searches count these lines apart from the pods' lines, so
//! the routine conflicts that a machine logs do not count against the
//! pod that ships its logs.

use k8s_openapi::jiff::Timestamp;
use transect_resources::resources::OffLine;

use transect_observe::health::{minutes_between, times};
use transect_observe::logs::evidence::Evidence;
use transect_observe::logs::machines::MachineCount;
use transect_observe::logs::search::{Asked, ERRORS_WINDOW, Searched};
use transect_resources::knowledge::age;

/// How many of the machine's lines the page shows.
const SAMPLES: usize = 5;

/// The machine's own logs as its page shows them.
#[derive(Debug, Default, PartialEq)]
pub struct OwnLogs {
    /// One sentence per unit and search, the most first.
    pub lines: Vec<String>,
    /// The newest lines, with no list of places, because the lines
    /// already name the units.
    pub evidence: Evidence,
    /// The note the section shows when it has no lines.
    pub note: Option<OffLine>,
}

/// The note of a cluster that declares no log source.
const UNDECLARED: &str = "No LogSource is declared, so transect reads no machine's own logs.";

impl OwnLogs {
    /// The own logs of the machine `node`, from the last round of
    /// searches.  `separated` is true when those searches split the
    /// machines' lines from the pods'.
    pub fn of(searched: &Searched, node: &str, separated: bool, now: Timestamp) -> Self {
        let Some(source) = &searched.source else {
            return Self::noted(UNDECLARED);
        };
        let mut lines = Vec::new();
        let errors = here(searched.machines(&Asked::Errors), node);
        lines.extend(errors.iter().map(|count| error_line(count, now)));
        for (pattern, _) in &searched.patterns {
            let asked = Asked::Pattern(pattern.name.clone());
            let counts = here(searched.machines(&asked), node);
            let window = &pattern.window;
            let said = counts
                .iter()
                .map(|c| pattern_line(&pattern.name, window, c, now));
            lines.extend(said);
        }
        let mut samples = searched.machine_samples(&Asked::Errors).to_vec();
        for (pattern, _) in &searched.patterns {
            let asked = Asked::Pattern(pattern.name.clone());
            samples.extend(searched.machine_samples(&asked).iter().cloned());
        }
        samples.retain(|sample| sample.node.as_deref() == Some(node));
        samples.sort_by_key(|sample| std::cmp::Reverse(sample.time));
        samples.dedup();
        samples.truncate(SAMPLES);
        let note = match (lines.is_empty(), separated) {
            (false, _) => None,
            (true, true) => Some(OffLine::said(format!(
                "The LogSource {source} has no error line from this machine's own logs in the last {ERRORS_WINDOW}, and no pattern matched there."
            ))),
            (true, false) => Some(OffLine::said(format!(
                "The LogSource {source} does not name which of its lines are a machine's own, so they count as the lines of the pod that ships them."
            ))),
        };
        Self {
            lines,
            evidence: Evidence {
                samples,
                ..Evidence::default()
            },
            note,
        }
    }

    fn noted(note: &str) -> Self {
        Self {
            note: Some(OffLine::said(note)),
            ..Self::default()
        }
    }

    /// The logs, with a note when no log source is declared because the
    /// one named `unloaded` did not load.
    pub fn with_unloaded(self, unloaded: Option<&str>) -> Self {
        let undeclared = self.note.as_ref().map(|note| note.text.as_str()) == Some(UNDECLARED);
        match unloaded {
            Some(name) if undeclared => Self {
                note: Some(OffLine {
                    text: format!("No machine's own logs: the LogSource {name} did not load"),
                    see_resources: true,
                }),
                ..self
            },
            _ => self,
        }
    }
}

fn here<'a>(counts: &'a [MachineCount], node: &str) -> Vec<&'a MachineCount> {
    counts.iter().filter(|count| count.node == node).collect()
}

fn unit(count: &MachineCount) -> &str {
    count.unit.as_deref().unwrap_or("its log")
}

/// "k3s: 53 error lines in the last 1h, the latest 2m ago".
fn error_line(count: &MachineCount, now: Timestamp) -> String {
    format!(
        "{}: {} in the last {ERRORS_WINDOW}, the latest {} ago",
        unit(count),
        times(count.matches as i64, "error line"),
        latest(count, now),
    )
}

/// "k3s: refused: 3 matching lines in the last 1h, the latest 5m ago".
fn pattern_line(pattern: &str, window: &str, count: &MachineCount, now: Timestamp) -> String {
    format!(
        "{}: {pattern}: {} in the last {window}, the latest {} ago",
        unit(count),
        times(count.matches as i64, "matching line"),
        latest(count, now),
    )
}

fn latest(count: &MachineCount, now: Timestamp) -> String {
    age(count.last.map_or(0, |last| minutes_between(last, now)))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_observe::logs::patterns::Severity;
    use transect_observe::logs::patterns::fixtures::pattern;
    use transect_observe::logs::sample::Sample;

    const NOW: &str = "2026-01-01T12:10:00Z";

    fn count(node: &str, unit: &str, matches: usize) -> MachineCount {
        MachineCount {
            node: node.into(),
            unit: Some(unit.into()),
            matches,
            last: "2026-01-01T12:08:00Z".parse().ok(),
        }
    }

    fn sample(node: &str, at: &str, message: &str) -> Sample {
        Sample {
            node: Some(node.into()),
            time: at.parse().ok(),
            message: message.into(),
            ..Sample::default()
        }
    }

    fn searched() -> Searched {
        let refused = pattern("refused", "refused", Severity::Error);
        Searched {
            source: Some("logs".into()),
            patterns: vec![(refused, Ok(vec![]))],
            machines: vec![
                (
                    Asked::Errors,
                    vec![
                        count("machine-a", "k3s", 53),
                        count("machine-b", "kernel", 2),
                    ],
                ),
                (
                    Asked::Pattern("refused".into()),
                    vec![count("machine-a", "containerd", 3)],
                ),
            ],
            machine_samples: vec![(
                Asked::Errors,
                vec![
                    sample("machine-a", "2026-01-01T12:01:00Z", "older"),
                    sample("machine-b", "2026-01-01T12:09:00Z", "another machine's"),
                    sample("machine-a", "2026-01-01T12:08:00Z", "newer"),
                ],
            )],
            ..Searched::default()
        }
    }

    #[rstest]
    fn lists_each_units_errors_and_matches_with_the_newest_lines() {
        let logs = OwnLogs::of(&searched(), "machine-a", true, NOW.parse().unwrap());
        let samples: Vec<&str> = logs
            .evidence
            .samples
            .iter()
            .map(|s| s.message.as_str())
            .collect();
        assert_eq!(
            (logs.lines, samples, logs.note),
            (
                vec![
                    "k3s: 53 error lines in the last 1h, the latest 2m ago".to_string(),
                    "containerd: refused: 3 matching lines in the last 1h, the latest 2m ago"
                        .into(),
                ],
                vec!["newer", "older"],
                None
            )
        );
    }

    #[rstest]
    #[case::separated(
        true,
        Some(
            "The LogSource logs has no error line from this machine's own logs in the last 1h, and no pattern matched there."
        )
    )]
    #[case::not_separated(
        false,
        Some(
            "The LogSource logs does not name which of its lines are a machine's own, so they count as the lines of the pod that ships them."
        )
    )]
    fn says_why_a_machine_shows_no_lines(#[case] separated: bool, #[case] expected: Option<&str>) {
        let logs = OwnLogs::of(&searched(), "machine-c", separated, NOW.parse().unwrap());
        assert_eq!(logs.note.map(|note| note.text).as_deref(), expected);
    }

    #[rstest]
    fn says_when_there_is_no_log_source() {
        let logs = OwnLogs::of(
            &Searched::default(),
            "machine-a",
            true,
            NOW.parse().unwrap(),
        );
        assert_eq!(
            logs.note.map(|note| note.text).as_deref(),
            Some("No LogSource is declared, so transect reads no machine's own logs.")
        );
    }
}
