//! A reason for each workload whose pods logged error lines in the
//! logs that transect has read. transect reads a pod's logs only when
//! someone opens its page, so a reason covers only the pods that were
//! read, and records how many of the workload's pods that is.

use std::collections::HashMap;
use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;

use super::{Reason, Seen, minutes_between};
use crate::logs::Logged;
use crate::logs::evidence::Evidence;
use transect_cluster::ownership::Ownership;
use transect_graph::edges::Object;

/// The totals over the log reads of one workload's pods.
#[derive(Default)]
struct Tally {
    errors: usize,
    lines: usize,
    read: usize,
    latest: Option<Timestamp>,
}

/// A reason, with its namespace, for each workload whose pods in `pods`
/// have a cached log read with an error line. Skips the read of a pod
/// that is not in `pods`, because the pod no longer exists.
pub fn reasons(
    logged: &[Logged],
    pods: &[Arc<Pod>],
    ownership: &Ownership,
    now: Timestamp,
) -> Vec<(String, Reason)> {
    let mut running: HashMap<Object, Vec<String>> = HashMap::new();
    for pod in pods {
        let meta = &pod.metadata;
        let (Some(namespace), Some(name)) = (&meta.namespace, &meta.name) else {
            continue;
        };
        let object = Object::new("", "Pod", Some(namespace), name);
        running
            .entry(ownership.top_owner(&object))
            .or_default()
            .push(name.clone());
    }
    let mut tallies: HashMap<Object, Tally> = HashMap::new();
    for read in logged {
        let object = Object::new("", "Pod", Some(&read.namespace), &read.pod);
        let owner = ownership.top_owner(&object);
        let runs = running
            .get(&owner)
            .is_some_and(|pods| pods.contains(&read.pod));
        if !runs {
            continue;
        }
        let tally = tallies.entry(owner).or_default();
        tally.errors += read.errors;
        tally.lines += read.lines;
        tally.read += 1;
        tally.latest = tally.latest.max(Some(read.read_at));
    }
    let mut reasons: Vec<(String, Reason)> = tallies
        .into_iter()
        .filter(|(_, tally)| tally.errors > 0)
        .filter_map(|(workload, tally)| {
            let namespace = workload.namespace.clone()?;
            let of = running.get(&workload).map_or(0, Vec::len);
            let reason = Reason::LoggedErrors {
                errors: tally.errors,
                minutes_ago: minutes_between(tally.latest?, now),
                seen: Seen::Read {
                    lines: tally.lines,
                    read: tally.read,
                    of,
                },
                evidence: Evidence::default(),
                workload,
            };
            Some((namespace, reason))
        })
        .collect();
    reasons.sort_by(|a, b| {
        a.0.cmp(&b.0)
            .then_with(|| a.1.to_string().cmp(&b.1.to_string()))
    });
    reasons
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_cluster::ownership::fixtures::build;

    const NOW: &str = "2026-01-01T12:10:00Z";

    fn pod(name: &str) -> Arc<Pod> {
        let text = format!("metadata: {{namespace: shop, name: {name}}}");
        Arc::new(serde_yaml_ng::from_str(&text).unwrap())
    }

    fn pods() -> Vec<Arc<Pod>> {
        ["web-0", "web-1", "web-2", "db-0"].map(pod).to_vec()
    }

    fn ownership() -> Ownership {
        let web = || vec![("apps/v1", "StatefulSet", "web")];
        build(&[
            ("", "Pod", Some("shop"), "web-0", web()),
            ("", "Pod", Some("shop"), "web-1", web()),
            ("", "Pod", Some("shop"), "web-2", web()),
            (
                "",
                "Pod",
                Some("shop"),
                "db-0",
                vec![("apps/v1", "StatefulSet", "db")],
            ),
            ("apps", "StatefulSet", Some("shop"), "web", vec![]),
            ("apps", "StatefulSet", Some("shop"), "db", vec![]),
        ])
    }

    fn logged(pod: &str, errors: usize, read_at: &str) -> Logged {
        Logged {
            namespace: "shop".into(),
            pod: pod.into(),
            read_at: read_at.parse().unwrap(),
            lines: 50,
            errors,
        }
    }

    fn web(errors: usize, lines: usize, read: usize, minutes_ago: i64) -> (String, Reason) {
        let workload = Object::new("apps", "StatefulSet", Some("shop"), "web");
        let reason = Reason::LoggedErrors {
            workload,
            errors,
            minutes_ago,
            seen: Seen::Read { lines, read, of: 3 },
            evidence: Evidence::default(),
        };
        ("shop".into(), reason)
    }

    #[rstest]
    #[case::nothing_read(vec![], vec![])]
    #[case::no_errors(vec![logged("web-0", 0, "2026-01-01T12:05:00Z")], vec![])]
    #[case::one_pod(vec![logged("web-0", 2, "2026-01-01T12:05:00Z")], vec![web(2, 50, 1, 5)])]
    #[case::two_pods_one_clean(
        vec![logged("web-0", 2, "2026-01-01T12:01:00Z"), logged("web-1", 0, "2026-01-01T12:08:00Z")],
        vec![web(2, 100, 2, 2)]
    )]
    #[case::a_pod_gone(vec![logged("web-9", 4, "2026-01-01T12:05:00Z")], vec![])]
    fn counts_errors_by_workload(
        #[case] logged: Vec<Logged>,
        #[case] expected: Vec<(String, Reason)>,
    ) {
        let now = NOW.parse().unwrap();
        assert_eq!(reasons(&logged, &pods(), &ownership(), now), expected);
    }
}
