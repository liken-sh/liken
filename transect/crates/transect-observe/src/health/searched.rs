//! Reasons from the log source's searches across the cluster: the
//! error lines of each workload, by the level field, and each error or
//! warning pattern's matches by namespace.  These searches cover every
//! pod, so when they exist, transect uses them in place of the counts
//! from the tails of the pods whose pages were opened.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;

use super::recent::is_recent;
use super::{Reason, Seen, minutes_between};
use crate::logs::evidence::Evidence;
use crate::logs::patterns::Severity;
use crate::logs::sample::Sample;
use crate::logs::search::{Asked, Count, Searched};
use transect_cluster::ownership::Ownership;
use transect_graph::edges::Object;

/// Where a count's lines came from.  A reader's first question is which
/// workload logged them, so the origin of a running pod is its
/// workload.  The origin of a gone pod is also a workload when the
/// pod's name is that of a pod of a `Job` or a `CronJob` still in the
/// cluster.  The origin of any other gone pod is the pod itself.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Origin {
    Workload(Object),
    /// A pod no longer in the cluster, by name.
    GonePod(String),
    /// The log source named no pod for the lines.
    NoPod,
}

impl Origin {
    /// The origin's name, which is the workload's name or the pod's, or
    /// empty for `NoPod`.
    pub fn name(&self) -> &str {
        match self {
            Self::Workload(workload) => &workload.name,
            Self::GonePod(pod) => pod,
            Self::NoPod => "",
        }
    }
}

/// One count of a search's matches in one namespace, from one origin,
/// with the counts it sums.
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

/// `counts` summed into tallies, with the most matches first, then by
/// name.  The function sums by the top owner for each pod that is still
/// in `pods` or is named for a `Job` or a `CronJob`, by pod for the
/// other pods that are gone, and by namespace for lines that the source
/// names no pod for.
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

/// One reason, by namespace, for each workload whose pods the search
/// counted error lines for.  The function leaves out a count for a pod
/// that is gone, unless the pod's name matches it to a workload.
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

/// One reason, with its namespace, for each workload whose pods an
/// error or warning pattern matched recently.  The function also
/// returns one for each pod that is gone, and one for each namespace's
/// lines that the source names no pod for.  Older matches, a pattern of
/// severity worth-knowing, and the matches of a `CronJob` in `settled`,
/// whose last run succeeded, are a finding instead, because a run that
/// logs an error and then retries to success needs no attention.
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
        let samples = searched.samples(&Asked::Pattern(pattern.name.clone()));
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

/// Whether `origin` is a workload in `settled`.
pub fn is_settled(origin: &Origin, settled: &HashSet<Object>) -> bool {
    matches!(origin, Origin::Workload(workload) if settled.contains(workload))
}

#[cfg(test)]
mod tests;
