//! Health: whether each workload's pods are ready and staying up, rolled
//! up per namespace.
//!
//! Each rule returns a `Reason` that records what it found as data: the
//! workload or pod, and the counts or times that broke the rule.  The map
//! colors a tile by the worst level among its reasons, and the reasons
//! render as template sentences in `sentence.rs`, so every screen that
//! repeats a finding shows it in the same words.
//!
//! Every rule is a pure function of the objects and `now`.  A rule that
//! measures how long something has lasted can change its result with no
//! watch event, so `recheck` marks the cluster changed every `RECHECK`.

mod attribute;
mod cron_jobs;
mod earlier;
#[cfg(test)]
mod fixtures;
mod grouped;
mod known;
mod logged;
mod note;
mod pods;
mod recent;
mod rolls;
mod searched;
mod sentence;
mod workloads;

use std::collections::HashMap;
use std::time::Duration;

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;
use tokio::sync::watch;

use crate::logs::evidence::Evidence;
use grouped::Restarted;
pub use rolls::AfterRoll;
use transect_cluster::ownership::Ownership;
use transect_cluster::workloads::{Objects, Revisions};
use transect_graph::edges::Object;
use transect_graph::pods::finished;

pub use attribute::by_workload;
pub use cron_jobs::settled as cron_job_settled;
pub use earlier::{EARLIER_MINUTES, Earlier, restarts as earlier_restarts};
pub use known::{Deciding, at_rest, condition, reported, resting};
pub use logged::reasons as logged_reasons;
pub use pods::reasons as pod_reasons;
pub use recent::{RECENT_MINUTES, is_recent};
pub use searched::{
    Origin, Running, anchor as matching_anchor, by_workload as tally_by_workload,
    errors as searched_errors, evidence as tally_evidence, is_settled, matched as matched_reasons,
};
pub use sentence::{FROM_NO_POD, GONE_POD, SETTLED, times};

/// How often the rules that measure a duration run again.  A tile
/// changes color at most this long after a duration passes its threshold.
pub const RECHECK: Duration = Duration::from_secs(30);

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord)]
pub enum Level {
    #[default]
    Healthy,
    /// Listed in "Now" and drawn in gray, because it does not need
    /// attention on its own.
    Notice,
    Warning,
    Failing,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Kind {
    Deployment,
    StatefulSet,
    DaemonSet,
    CronJob,
}

/// A workload by kind and name, within the namespace a `Health` belongs to.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Workload {
    pub kind: Kind,
    pub name: String,
}

/// What a rule found.  Durations are whole minutes, so a reason renders to
/// the same sentence for a minute at a time.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Reason {
    NoReadyReplicas {
        workload: Workload,
        desired: i32,
    },
    /// No replicas ready yet, while a new pod of the workload starts.
    Starting {
        workload: Workload,
        desired: i32,
    },
    NotReady {
        workload: Workload,
        ready: i32,
        desired: i32,
    },
    CrashLooping {
        pod: String,
        container: String,
        restarts: i32,
    },
    Restarting {
        pod: String,
        container: String,
        restarts: i32,
        minutes_ago: i64,
        /// The last run exited 0, as a process that stops on purpose
        /// does, rather than crashing.
        clean: bool,
    },
    /// One container restarted in `pods` of the `of` pods that its
    /// workload runs, and the latest restart was `minutes_ago`.  `clean`
    /// is true when every one of those restarts exited 0.
    RestartedAcross {
        workload: Object,
        container: String,
        pods: usize,
        of: usize,
        minutes_ago: i64,
        clean: bool,
        /// The other workload whose rollout these restarts followed.
        after: Option<AfterRoll>,
    },
    PodPending {
        pod: String,
        minutes: i64,
    },
    LastRunFailed {
        workload: Workload,
    },
    /// A custom resource's health condition is not `True`.  The
    /// condition is the one that its kind knowledge names, or `Ready`.
    /// `status` is `None` when the resource has no such condition.
    Condition {
        workload: Object,
        condition: String,
        status: Option<String>,
        /// The condition's `reason` and `message`, as its operator
        /// wrote them.
        reason: Option<String>,
        message: Option<String>,
    },
    /// The workload's pods logged `errors` error lines, and the latest
    /// was `minutes_ago`.  `seen` records how transect counted them.
    LoggedErrors {
        workload: Object,
        errors: usize,
        minutes_ago: i64,
        seen: Seen,
        evidence: Evidence,
    },
    /// A log pattern of severity error or warning matched `matches`
    /// lines from `origin` over its `window`, and the latest match was
    /// `minutes_ago`.
    Matched {
        origin: Origin,
        pattern: String,
        meaning: String,
        matches: usize,
        window: String,
        minutes_ago: i64,
        evidence: Evidence,
        /// The workload is a `CronJob` whose last run succeeded, so its
        /// matches are worth knowing rather than a reason to look now.
        settled: bool,
    },
    /// Alertmanager reports that the alert `name` fires about `subject`.
    /// `subject` is the namespace itself when the alert's labels name no
    /// object in it.  `minutes_ago` is how long the alert has fired, when
    /// the alert records when it started.  Its level comes from its
    /// severity.
    Alert {
        subject: Object,
        container: Option<String>,
        name: String,
        summary: Option<String>,
        level: Level,
        minutes_ago: Option<i64>,
    },
}

/// How a workload's error lines were counted.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Seen {
    /// transect counted them in the log tails that it read when the pods'
    /// pages opened: `read` of the workload's `of` pods, and `lines`
    /// lines in all.
    Read {
        lines: usize,
        read: usize,
        of: usize,
    },
    /// The log source counted them from each line's level, across every
    /// pod over `window`.
    Searched { source: String, window: String },
}

impl Reason {
    pub fn level(&self) -> Level {
        match self {
            Self::NoReadyReplicas { .. } => Level::Failing,
            Self::Condition { status, .. } if status.as_deref() == Some("False") => Level::Failing,
            // One clean exit is a process that stopped on purpose, such
            // as one that exits to pick up new configuration.  A second
            // makes it a pattern worth attention.
            Self::Restarting {
                clean: true,
                restarts: ..2,
                ..
            } => Level::Notice,
            // An error line is often routine, so a count of them stays
            // gray.  A `Matched` pattern is a Warning, because a person
            // wrote it to say which lines matter.  A `Seen::Read` count
            // also covers only the pods whose pages someone opened, so a
            // higher level would color a tile by who opened which pages.
            Self::LoggedErrors { .. } => Level::Notice,
            Self::Alert { level, .. } => *level,
            _ => Level::Warning,
        }
    }
}

/// One namespace's health: the worst level among its reasons, and the
/// reasons, worst first.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Health {
    pub level: Level,
    pub reasons: Vec<Reason>,
}

impl Health {
    pub fn from_reasons(mut reasons: Vec<Reason>) -> Self {
        reasons.sort_by_key(|reason| std::cmp::Reverse(reason.level()));
        Self {
            level: reasons.iter().map(Reason::level).max().unwrap_or_default(),
            reasons,
        }
    }

    /// Every reason as a sentence, one per line, for a tile's title.
    pub fn title(&self) -> Option<String> {
        if self.reasons.is_empty() {
            return None;
        }
        let lines: Vec<String> = self.reasons.iter().map(ToString::to_string).collect();
        Some(lines.join("\n"))
    }
}

/// The health of every namespace that has a reason to report.  A
/// namespace missing from the map is healthy.  `ownership` gives the
/// workload whose replicas each pod counts toward.  `revisions` gives
/// each controller's newest revision, so that restarts can be tied to
/// another workload's rollout on the same machine.
pub fn assess(
    objects: &Objects,
    ownership: &Ownership,
    revisions: &Revisions,
    now: Timestamp,
) -> HashMap<String, Health> {
    let mut pods_in: HashMap<String, Vec<&Pod>> = HashMap::new();
    for pod in objects.pods {
        if let Some(namespace) = pod.namespace() {
            pods_in.entry(namespace).or_default().push(pod);
        }
    }
    let no_pods = Vec::new();
    let pods_of = |namespace: &str| workloads::Pods {
        pods: pods_in.get(namespace).unwrap_or(&no_pods),
        ownership,
    };

    let mut reasons: HashMap<String, Vec<Reason>> = HashMap::new();
    let mut report = |namespace: Option<String>, reason: Option<Reason>| {
        if let (Some(namespace), Some(reason)) = (namespace, reason) {
            reasons.entry(namespace).or_default().push(reason);
        }
    };
    for deployment in objects.deployments {
        let namespace = deployment.namespace();
        let pods = pods_of(namespace.as_deref().unwrap_or_default());
        report(namespace, workloads::deployment(deployment, pods, now));
    }
    for stateful_set in objects.stateful_sets {
        let namespace = stateful_set.namespace();
        let pods = pods_of(namespace.as_deref().unwrap_or_default());
        report(namespace, workloads::stateful_set(stateful_set, pods, now));
    }
    for daemon_set in objects.daemon_sets {
        let namespace = daemon_set.namespace();
        let pods = pods_of(namespace.as_deref().unwrap_or_default());
        report(namespace, workloads::daemon_set(daemon_set, pods, now));
    }
    for cron_job in objects.cron_jobs {
        report(cron_job.namespace(), cron_jobs::last_run(cron_job));
    }
    let mut restarts = Vec::new();
    let mut running: HashMap<Object, usize> = HashMap::new();
    let (mut stops, mut rolled) = (Vec::new(), Vec::new());
    for pod in objects.pods {
        let Some(namespace) = pod.namespace() else {
            continue;
        };
        let object = Object::new("", "Pod", Some(&namespace), &pod.name_any());
        let owner = ownership.top_owner(&object);
        if !finished(pod) && pod.metadata.deletion_timestamp.is_none() {
            *running.entry(owner.clone()).or_default() += 1;
        }
        stops.extend(rolls::restarts_of(pod, &owner));
        rolled.extend(rolls::rolled_of(pod, &owner, revisions));
        for reason in pods::reasons(pod, now) {
            if matches!(reason, Reason::Restarting { .. }) {
                restarts.push(Restarted {
                    namespace: namespace.clone(),
                    owner: owner.clone(),
                    reason,
                });
            } else {
                report(Some(namespace.clone()), Some(reason));
            }
        }
    }
    let after = rolls::after_rolls(&stops, &rolled);
    for (namespace, reason) in grouped::group(restarts, &running, &after) {
        report(Some(namespace), Some(reason));
    }

    reasons
        .into_iter()
        .map(|(namespace, reasons)| (namespace, Health::from_reasons(reasons)))
        .collect()
}

/// `health` with `reasons` added to their namespaces, for findings that
/// `assess` does not make, such as the conditions of custom resources.
pub fn with_reasons(
    mut health: HashMap<String, Health>,
    reasons: Vec<(String, Reason)>,
) -> HashMap<String, Health> {
    let mut added: HashMap<String, Vec<Reason>> = HashMap::new();
    for (namespace, reason) in reasons {
        added.entry(namespace).or_default().push(reason);
    }
    for (namespace, reasons) in added {
        let had = health.remove(&namespace).unwrap_or_default().reasons;
        let all = had.into_iter().chain(reasons).collect();
        health.insert(namespace, Health::from_reasons(all));
    }
    health
}

/// Marks the cluster changed every `RECHECK`, so a pod that stays
/// pending turns its tile amber even when no watch event arrives.  A
/// render that produces the same page sends nothing to the browser.
pub fn recheck(changed: watch::Sender<()>) {
    tokio::spawn(async move {
        let mut ticks = tokio::time::interval(RECHECK);
        ticks.tick().await;
        loop {
            ticks.tick().await;
            changed.send_replace(());
        }
    });
}

/// The number of minute boundaries between `then` and `now`.  Counting
/// boundaries makes every age on a page change at the same second, so an
/// open page updates once a minute and not once for each age.  The result
/// is zero when `then` is later than `now`, which happens when the API
/// server's clock is ahead of this machine's.
pub fn minutes_between(then: Timestamp, now: Timestamp) -> i64 {
    (now.as_second().div_euclid(60) - then.as_second().div_euclid(60)).max(0)
}

#[cfg(test)]
mod tests;

#[cfg(test)]
mod minutes {
    use rstest::rstest;

    use super::minutes_between;

    #[rstest]
    #[case::within_a_minute("12:00:10", "12:00:50", 0)]
    #[case::across_the_top_of_a_minute("12:00:59", "12:01:00", 1)]
    #[case::ten_minutes("12:00:30", "12:10:29", 10)]
    #[case::a_skewed_clock("12:05:00", "12:00:00", 0)]
    fn counts_the_minutes_the_clock_turns(
        #[case] then: &str,
        #[case] now: &str,
        #[case] minutes: i64,
    ) {
        let at = |time: &str| format!("2026-01-01T{time}Z").parse().unwrap();
        assert_eq!(minutes_between(at(then), at(now)), minutes);
    }
}
