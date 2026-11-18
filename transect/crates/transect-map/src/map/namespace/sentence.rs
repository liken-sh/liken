//! The text of the namespace page. The rest of the page is names and
//! numbers from the cluster.

use std::fmt;

use super::boxes::Title;
use super::line::{JobState, LastRun, Line, Spread};
use super::managed::ManagedBy;

/// "controlled by cache-system", or "likely controlled by cache-system
/// or other-system" when the graph could not tell which of them it is.
impl fmt::Display for ManagedBy {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        if self.guessed {
            write!(f, "likely ")?;
        }
        write!(f, "controlled by {}", self.names.join(" or "))
    }
}

impl fmt::Display for Title {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Workload { kind, name } => {
                f.write_str(&transect_graph::edges::kind_and_name(kind, name))
            }
            Self::Helpers(names) => write!(f, "{}", names.join(" · ")),
        }
    }
}

impl fmt::Display for Line {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Replicas { replicas, spread } => {
                write!(f, "{}", count(*replicas, "replica"))?;
                match spread {
                    Some(Spread::AntiAffinity) => write!(f, ", spread by anti-affinity"),
                    Some(Spread::TopologySpread) => write!(f, ", spread by topology constraints"),
                    None => Ok(()),
                }
            }
            Self::OnMachines(machines) => write!(f, "on {}", count(*machines, "machine")),
            Self::LastRun(LastRun::Running) => write!(f, "running now"),
            Self::LastRun(LastRun::Succeeded) => write!(f, "last run succeeded"),
            Self::LastRun(LastRun::Failed) => write!(f, "last run failed"),
            Self::LastRun(LastRun::Never) => write!(f, "has not run yet"),
            Self::LastRun(LastRun::Suspended) => write!(f, "suspended"),
            Self::Job(JobState::Running) => write!(f, "running now"),
            Self::Job(JobState::Succeeded) => write!(f, "succeeded"),
            Self::Job(JobState::Failed) => write!(f, "failed"),
            Self::Job(JobState::NoPods) => write!(f, "finished, its pods gone"),
            Self::Pods(0) => write!(f, "no pods running"),
            Self::Pods(pods) => write!(f, "{} running", count(*pods, "pod")),
            Self::Helpers => write!(f, "one pod each, with no volume and no Service"),
        }
    }
}

/// The header beside the open namespace's name. It leaves out the volume
/// count for a namespace with no volumes, because most have none.
pub fn header(workloads: usize, pods: usize, volumes: usize) -> String {
    let mut header = format!("{} · {}", count(workloads, "workload"), count(pods, "pod"));
    if volumes > 0 {
        header.push_str(&format!(" · {}", count(volumes, "volume")));
    }
    header
}

/// "4 more": the namespaces that a folded band leaves out after the names
/// it shows.
pub fn more(more: usize) -> String {
    format!("{more} more")
}

/// The text of a folded band that has no namespaces.
pub const EMPTY_BAND: &str = "no namespaces";

/// `number` and `noun`, with an "s" added to the noun unless the number
/// is 1.
fn count(number: impl fmt::Display, noun: &str) -> String {
    let number = number.to_string();
    format!("{number} {noun}{}", if number == "1" { "" } else { "s" })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::one(vec!["cache-system"], false, "controlled by cache-system")]
    #[case::a_guess(vec!["cache-system", "other-system"], true, "likely controlled by cache-system or other-system")]
    fn names_the_controller(#[case] names: Vec<&str>, #[case] guessed: bool, #[case] text: &str) {
        let names = names.into_iter().map(Into::into).collect();
        assert_eq!(ManagedBy { names, guessed }.to_string(), text);
    }

    #[rstest]
    #[case::one_replica(Line::Replicas { replicas: 1, spread: None }, "1 replica")]
    #[case::anti_affinity(
        Line::Replicas { replicas: 3, spread: Some(Spread::AntiAffinity) },
        "3 replicas, spread by anti-affinity"
    )]
    #[case::topology_spread(
        Line::Replicas { replicas: 2, spread: Some(Spread::TopologySpread) },
        "2 replicas, spread by topology constraints"
    )]
    #[case::scaled_to_zero(Line::Replicas { replicas: 0, spread: None }, "0 replicas")]
    #[case::daemon_set(Line::OnMachines(4), "on 4 machines")]
    #[case::daemon_set_on_one(Line::OnMachines(1), "on 1 machine")]
    #[case::last_run(Line::LastRun(LastRun::Succeeded), "last run succeeded")]
    #[case::never_run(Line::LastRun(LastRun::Never), "has not run yet")]
    #[case::job_gone(Line::Job(JobState::NoPods), "finished, its pods gone")]
    #[case::no_pods(Line::Pods(0), "no pods running")]
    #[case::one_pod(Line::Pods(1), "1 pod running")]
    #[case::helpers(Line::Helpers, "one pod each, with no volume and no Service")]
    fn line_text(#[case] line: Line, #[case] text: &str) {
        assert_eq!(line.to_string(), text);
    }

    #[rstest]
    #[case::workload(Title::Workload { kind: "Cache".into(), name: "sessions".into() }, "Cache sessions")]
    #[case::helpers(Title::Helpers(vec!["mcp".into(), "relay".into()]), "mcp · relay")]
    fn title_text(#[case] title: Title, #[case] text: &str) {
        assert_eq!(title.to_string(), text);
    }

    #[rstest]
    #[case::plural(7, 16, 12, "7 workloads · 16 pods · 12 volumes")]
    #[case::singular(1, 1, 1, "1 workload · 1 pod · 1 volume")]
    #[case::no_volumes(2, 3, 0, "2 workloads · 3 pods")]
    fn header_text(
        #[case] workloads: usize,
        #[case] pods: usize,
        #[case] volumes: usize,
        #[case] text: &str,
    ) {
        assert_eq!(header(workloads, pods, volumes), text);
    }
}
