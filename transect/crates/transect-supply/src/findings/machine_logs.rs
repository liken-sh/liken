//! The finding for the machines' own error lines: what each machine's
//! kubelet, container runtime, or kernel logged at the error level over
//! the search's window.  These lines are routine on a busy machine, such as a
//! kubelet's conflicts on a status update, so transect colors nothing
//! for them.  The finding lists which machines logged the most, and each
//! machine's page shows its lines.

use k8s_openapi::jiff::Timestamp;

use super::Finding;
use transect_observe::health::{minutes_between, times};
use transect_observe::logs::machines::MachineCount;
use transect_resources::knowledge::age;

/// One machine's own error lines, across its logs.
#[derive(Clone, Debug, PartialEq)]
pub struct MachineLogged {
    pub machine: String,
    pub errors: usize,
    pub minutes_ago: i64,
}

/// A finding when any machine logged an error line of its own in
/// `window`, the machine with the most first.
pub fn of(counts: &[MachineCount], window: &str, now: Timestamp) -> Option<Finding> {
    let mut machines: Vec<MachineLogged> = Vec::new();
    for count in counts {
        let minutes_ago = count.last.map_or(0, |last| minutes_between(last, now));
        match machines.iter_mut().find(|one| one.machine == count.node) {
            Some(one) => {
                one.errors += count.matches;
                one.minutes_ago = one.minutes_ago.min(minutes_ago);
            }
            None => machines.push(MachineLogged {
                machine: count.node.clone(),
                errors: count.matches,
                minutes_ago,
            }),
        }
    }
    machines.sort_by(|a, b| {
        b.errors
            .cmp(&a.errors)
            .then_with(|| a.machine.cmp(&b.machine))
    });
    (!machines.is_empty()).then(|| Finding::MachineLogs {
        window: window.into(),
        machines,
    })
}

/// "88 error lines in the machines' own logs in the last 1h".
pub fn title(window: &str, machines: &[MachineLogged]) -> String {
    let total: usize = machines.iter().map(|one| one.errors).sum();
    format!(
        "{} in the machines' own logs in the last {window}",
        times(total as i64, "error line")
    )
}

/// "61, the latest 2m ago".
pub fn line(one: &MachineLogged) -> String {
    format!("{}, the latest {} ago", one.errors, age(one.minutes_ago))
}

pub fn body() -> &'static str {
    "A pod ships each machine's own logs, such as its kubelet's and its kernel's, to the log source, and these are their lines at the error level. They color no namespace, and each machine's page shows its lines."
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    const NOW: &str = "2026-01-01T12:10:00Z";

    fn count(node: &str, unit: &str, matches: usize, last: &str) -> MachineCount {
        MachineCount {
            node: node.into(),
            unit: Some(unit.into()),
            matches,
            last: last.parse().ok(),
        }
    }

    #[rstest]
    fn sums_each_machines_logs_the_most_first() {
        let counts = vec![
            count("machine-a", "k3s", 3, "2026-01-01T12:00:00Z"),
            count("machine-b", "kernel", 9, "2026-01-01T11:10:00Z"),
            count("machine-a", "containerd", 8, "2026-01-01T12:08:00Z"),
        ];
        let found = of(&counts, "1h", NOW.parse().unwrap()).unwrap();
        let lines: Vec<(String, Option<String>, String)> = found
            .lines()
            .into_iter()
            .map(|line| (line.label, line.href, line.text))
            .collect();
        assert_eq!(
            (found.title(), lines),
            (
                "20 error lines in the machines' own logs in the last 1h".into(),
                vec![
                    (
                        "machine-a".into(),
                        Some("/machines/machine-a".into()),
                        "11, the latest 2m ago".into()
                    ),
                    (
                        "machine-b".into(),
                        Some("/machines/machine-b".into()),
                        "9, the latest 1h ago".into()
                    ),
                ]
            )
        );
    }

    #[rstest]
    fn no_lines_no_finding() {
        assert_eq!(of(&[], "1h", NOW.parse().unwrap()), None);
    }
}
