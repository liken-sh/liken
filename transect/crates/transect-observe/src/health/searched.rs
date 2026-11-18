//! Health reasons from the log source's searches across the cluster:
//! the count of each workload's error lines, and the matches of each
//! `Error` or `Warning` pattern.  A search covers every pod, so when the
//! error search succeeds, its counts replace the counts from the logs
//! that transect tails for the pod pages that someone opened.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;

use super::recent::is_recent;
use super::{Reason, Seen, minutes_between};
use crate::logs::evidence::Evidence;
use crate::logs::patterns::Severity;
use crate::logs::sample::Sample;
use crate::logs::search::{Count, SearchTarget, Searched};
use transect_cluster::ownership::Ownership;
use transect_graph::edges::Object;

/// Which workload or pod logged a count's lines.  For a running pod it
/// is the pod's top owner.  For a pod that is gone it is the `Job` or
/// `CronJob` that the pod's name matches, if that object is still in
/// the cluster, and otherwise the pod itself.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Origin {
    Workload(Object),
    /// A pod no longer in the cluster, by name.
    GonePod(String),
    /// The log source named no pod for the lines.
    NoPod,
}

impl Origin {
    pub fn name(&self) -> &str {
        match self {
            Self::Workload(workload) => &workload.name,
            Self::GonePod(pod) => pod,
            Self::NoPod => "",
        }
    }
}

/// The sum of a search's counts for one namespace and one origin, with
/// the counts that it sums.
#[derive(Clone, Debug, PartialEq)]
pub struct Tally {
    pub namespace: String,
    pub origin: Origin,
    pub matches: usize,
    pub last: Option<Timestamp>,
    pub counts: Vec<Count>,
}

/// The running pods, by namespace and name.
pub struct Running<'a>(HashSet<(&'a str, &'a str)>);

impl<'a> Running<'a> {
    pub fn of(pods: &'a [Arc<Pod>]) -> Self {
        let named = pods.iter().filter_map(|pod| {
            Some((
                pod.metadata.namespace.as_deref()?,
                pod.metadata.name.as_deref()?,
            ))
        });
        Self(named.collect())
    }

    pub fn runs(&self, namespace: &str, pod: &str) -> bool {
        self.0.contains(&(namespace, pod))
    }
}

/// `counts` summed by namespace and `Origin`, with the most matches
/// first, then by namespace and origin.
pub fn by_workload(counts: &[Count], pods: &[Arc<Pod>], ownership: &Ownership) -> Vec<Tally> {
    let running = Running::of(pods);
    let mut tallies: HashMap<(String, Origin), Vec<Count>> = HashMap::new();
    for count in counts {
        let origin = match count.pod.as_deref() {
            Some(pod) if running.runs(&count.namespace, pod) => {
                let pod = Object::new("", "Pod", Some(&count.namespace), pod);
                Origin::Workload(ownership.top_owner(&pod))
            }
            Some(pod) => match ownership.owner_by_name(&count.namespace, pod) {
                Some(workload) => Origin::Workload(workload),
                None => Origin::GonePod(pod.into()),
            },
            None => Origin::NoPod,
        };
        let tally = tallies
            .entry((count.namespace.clone(), origin))
            .or_default();
        tally.push(count.clone());
    }
    let mut tallies: Vec<Tally> = tallies
        .into_iter()
        .map(|((namespace, origin), counts)| Tally {
            namespace,
            origin,
            matches: counts.iter().map(|count| count.matches).sum(),
            last: counts.iter().filter_map(|count| count.last).max(),
            counts,
        })
        .collect();
    tallies.sort_by(|a, b| {
        b.matches
            .cmp(&a.matches)
            .then_with(|| a.namespace.cmp(&b.namespace))
            .then_with(|| a.origin.cmp(&b.origin))
    });
    tallies
}

/// The evidence for one tally: its counts, and the samples from its
/// pods, each pod linked at `anchor`.
pub fn evidence(
    tally: &Tally,
    samples: &[Sample],
    running: &Running,
    anchor: Option<&str>,
) -> Evidence {
    let runs = |namespace: &str, pod: &str| running.runs(namespace, pod);
    Evidence::of(&tally.counts, samples, runs, anchor)
}

/// The pod page's anchor at a pattern's matching lines.
pub fn anchor(pattern: &str) -> String {
    format!("matching-{pattern}")
}

/// A reason for each workload whose pods logged error lines, paired with
/// its namespace.  It leaves out the lines of a pod that is gone and
/// whose name matches no `Job` or `CronJob`, the lines that the log
/// source names no pod for, and a tally with no time for its last match.
pub fn errors(
    counts: &[Count],
    seen: &Seen,
    samples: &[Sample],
    pods: &[Arc<Pod>],
    ownership: &Ownership,
    now: Timestamp,
) -> Vec<(String, Reason)> {
    let running = Running::of(pods);
    let tallies = by_workload(counts, pods, ownership).into_iter();
    let mut reasons: Vec<(String, Reason)> = tallies
        .filter_map(|tally| {
            let evidence = evidence(&tally, samples, &running, None);
            let Origin::Workload(workload) = tally.origin else {
                return None;
            };
            let reason = Reason::LoggedErrors {
                workload,
                errors: tally.matches,
                minutes_ago: minutes_between(tally.last?, now),
                seen: seen.clone(),
                evidence,
            };
            Some((tally.namespace, reason))
        })
        .collect();
    reasons.sort_by(|a, b| {
        a.0.cmp(&b.0)
            .then_with(|| a.1.to_string().cmp(&b.1.to_string()))
    });
    reasons
}

/// A reason for each `Origin` whose lines an `Error` or `Warning` pattern
/// matched within the pattern's window, paired with its namespace.  The
/// other matches are findings, which `worth_knowing` in
/// `transect-supply` lists: older matches, the matches of a
/// `WorthKnowing` pattern, and the matches of a `CronJob` in `settled`,
/// whose last run succeeded.  A `CronJob` that logs an error and then
/// succeeds on a retry needs no attention.
pub fn matched(
    searched: &Searched,
    settled: &HashSet<Object>,
    pods: &[Arc<Pod>],
    ownership: &Ownership,
    now: Timestamp,
) -> Vec<(String, Reason)> {
    let running = Running::of(pods);
    let mut reasons = Vec::new();
    for (pattern, found) in &searched.patterns {
        let (Severity::Error | Severity::Warning, Ok(counts)) = (pattern.severity, found) else {
            continue;
        };
        let samples = searched.samples(&SearchTarget::Pattern(pattern.name.clone()));
        let anchor = anchor(&pattern.name);
        for tally in by_workload(counts, pods, ownership) {
            let minutes_ago = tally.last.map_or(0, |last| minutes_between(last, now));
            if !is_recent(minutes_ago, &pattern.window) || is_settled(&tally.origin, settled) {
                continue;
            }
            let reason = Reason::Matched {
                evidence: evidence(&tally, samples, &running, Some(&anchor)),
                origin: tally.origin,
                pattern: pattern.name.clone(),
                meaning: pattern.meaning.clone(),
                matches: tally.matches,
                window: pattern.window.clone(),
                minutes_ago,
                settled: false,
            };
            reasons.push((tally.namespace, reason));
        }
    }
    reasons
}

pub fn is_settled(origin: &Origin, settled: &HashSet<Object>) -> bool {
    matches!(origin, Origin::Workload(workload) if settled.contains(workload))
}

#[cfg(test)]
mod tests;
