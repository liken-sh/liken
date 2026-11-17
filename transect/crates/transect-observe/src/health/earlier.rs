//! Restarts that are too old to count toward health, but recent enough
//! to list.  A container that restarts every few hours never shows in
//! "Now", because each restart is out of the recent span before the
//! next one happens.

use std::collections::BTreeMap;
use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;

use super::pods::{crash_looping, last_restart};
use super::recent::RECENT_MINUTES;
use transect_cluster::ownership::Ownership;
use transect_graph::edges::Object;
use transect_graph::pods::finished;

/// How far back an earlier restart is listed.  A pod's status records
/// its last restart's time for as long as the pod runs, so without a
/// limit, one restart last month would be listed until the pod is
/// replaced.
pub const EARLIER_MINUTES: i64 = 24 * 60;

/// One container of a workload that restarted earlier in `pods` of the
/// workload's pods, and the latest restart was `minutes_ago`.  `clean`
/// is true when each of those last runs exited 0.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Earlier {
    pub workload: Object,
    pub container: String,
    pub pods: usize,
    pub minutes_ago: i64,
    pub clean: bool,
}

/// Each container of `pods` whose last restart was at least
/// `RECENT_MINUTES` and less than `EARLIER_MINUTES` ago, by the top
/// owner of its pod, with the latest first.  The function lists no
/// crash looping container, because "Now" shows it however long ago it
/// last ran.  It lists no pod that has finished or is being deleted,
/// because that pod will not restart again.
pub fn restarts(pods: &[Arc<Pod>], ownership: &Ownership, now: Timestamp) -> Vec<Earlier> {
    let mut found: BTreeMap<(Object, String), Earlier> = BTreeMap::new();
    for pod in pods {
        if finished(pod) || pod.metadata.deletion_timestamp.is_some() {
            continue;
        }
        let Some(status) = &pod.status else {
            continue;
        };
        let object = Object::new("", "Pod", pod.namespace().as_deref(), &pod.name_any());
        let owner = ownership.top_owner(&object);
        let init = status.init_container_statuses.iter().flatten();
        let main = status.container_statuses.iter().flatten();
        for container in init.chain(main).filter(|c| !crash_looping(c)) {
            let Some((minutes_ago, clean)) = last_restart(container, now) else {
                continue;
            };
            if !(RECENT_MINUTES..EARLIER_MINUTES).contains(&minutes_ago) {
                continue;
            }
            let key = (owner.clone(), container.name.clone());
            let earlier = found.entry(key).or_insert_with(|| Earlier {
                workload: owner.clone(),
                container: container.name.clone(),
                pods: 0,
                minutes_ago,
                clean: true,
            });
            earlier.pods += 1;
            earlier.minutes_ago = earlier.minutes_ago.min(minutes_ago);
            earlier.clean &= clean;
        }
    }
    let mut found: Vec<Earlier> = found.into_values().collect();
    found.sort_by_key(|earlier| earlier.minutes_ago);
    found
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::*;
    use super::*;

    fn web(name: &str, status: k8s_openapi::api::core::v1::ContainerStatus) -> Pod {
        pod("shop", name)
            .owned_by("StatefulSet", "web")
            .container(status)
    }

    fn earlier(pods: usize, minutes_ago: i64, clean: bool) -> Earlier {
        Earlier {
            workload: Object::new("apps", "StatefulSet", Some("shop"), "web"),
            container: "app".into(),
            pods,
            minutes_ago,
            clean,
        }
    }

    fn found(pods: Vec<Pod>) -> Vec<Earlier> {
        let ownership = ownership(&pods.iter().collect::<Vec<_>>(), &[]);
        let pods: Vec<Arc<Pod>> = pods.into_iter().map(Arc::new).collect();
        restarts(&pods, &ownership, now())
    }

    #[rstest]
    #[case::never_restarted(vec![web("web-0", container("app"))], vec![])]
    #[case::still_recent(
        vec![web("web-0", container("app").restarted(2, minutes_before(59)))],
        vec![]
    )]
    #[case::at_the_recent_spans_edge(
        vec![web("web-0", container("app").restarted(2, minutes_before(60)))],
        vec![earlier(1, 60, false)]
    )]
    #[case::hours_ago(
        vec![web("web-0", container("app").exited_cleanly(1, minutes_before(300)))],
        vec![earlier(1, 300, true)]
    )]
    #[case::two_pods_the_latest_counts(
        vec![
            web("web-0", container("app").restarted(1, minutes_before(300))),
            web("web-1", container("app").exited_cleanly(1, minutes_before(120))),
        ],
        vec![earlier(2, 120, false)]
    )]
    #[case::a_day_ago(
        vec![web("web-0", container("app").restarted(1, minutes_before(24 * 60)))],
        vec![]
    )]
    #[case::crash_looping(
        vec![web("web-0", container("app").restarted(9, minutes_before(90)).crash_looping())],
        vec![]
    )]
    #[case::being_deleted(
        vec![web("web-0", container("app").restarted(1, minutes_before(90))).deleting()],
        vec![]
    )]
    fn lists_restarts_past_the_recent_span(#[case] pods: Vec<Pod>, #[case] expected: Vec<Earlier>) {
        assert_eq!(found(pods), expected);
    }
}
