//! The pod rules: containers that keep restarting, and pods that never
//! start.  The rules apply to every pod, so a pod that no workload owns
//! still colors its namespace.

use k8s_openapi::api::core::v1::{ContainerStatus, Pod};
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;

use super::recent::RECENT_MINUTES;
use super::{Reason, minutes_between};
use transect_graph::pods::finished;

/// How long a pod may stay `Pending`, while the scheduler places it and
/// the kubelet pulls its images, before it counts as stuck.
const PENDING_MINUTES: i64 = 5;

/// What is wrong with `pod` now, if anything.  The function returns
/// nothing for a pod that is being deleted, because it will not restart
/// again, or for a pod that is `Succeeded` or `Failed`, because its
/// containers have stopped for good.
pub fn reasons(pod: &Pod, now: Timestamp) -> Vec<Reason> {
    if pod.metadata.deletion_timestamp.is_some() {
        return Vec::new();
    }
    let Some(status) = &pod.status else {
        return Vec::new();
    };
    if finished(pod) {
        return Vec::new();
    }
    let phase = status.phase.as_deref();
    let name = pod.name_any();
    let init = status.init_container_statuses.iter().flatten();
    let main = status.container_statuses.iter().flatten();
    let restarting: Vec<Reason> = init
        .chain(main)
        .filter_map(|container| restarts(&name, container, now))
        .collect();
    // A pod stays `Pending` while an init container crash loops, and the
    // crash loop is the more specific finding.
    if restarting.is_empty() && phase == Some("Pending") {
        return pending(pod, now).into_iter().collect();
    }
    restarting
}

fn pending(pod: &Pod, now: Timestamp) -> Option<Reason> {
    let created = pod.metadata.creation_timestamp.as_ref()?;
    let minutes = minutes_between(created.0, now);
    (minutes >= PENDING_MINUTES).then(|| Reason::PodPending {
        pod: pod.name_any(),
        minutes,
    })
}

/// A container in `CrashLoopBackOff` gets only that reason, because the
/// back-off tells the reader more than the time of its last restart.
fn restarts(pod: &str, container: &ContainerStatus, now: Timestamp) -> Option<Reason> {
    if crash_looping(container) {
        return Some(Reason::CrashLooping {
            pod: pod.into(),
            container: container.name.clone(),
            restarts: container.restart_count,
        });
    }
    let (minutes_ago, clean) = last_restart(container, now)?;
    (minutes_ago < RECENT_MINUTES).then(|| Reason::Restarting {
        pod: pod.into(),
        container: container.name.clone(),
        restarts: container.restart_count,
        minutes_ago,
        clean,
    })
}

/// How many minutes before `now` `container` last restarted, and
/// whether the run before it exited 0.  Kubernetes keeps a container's
/// restart count for the life of the pod, so the time of the last run
/// decides whether a restart still matters.  A count alone would flag a
/// pod forever for one restart last month.
pub fn last_restart(container: &ContainerStatus, now: Timestamp) -> Option<(i64, bool)> {
    if container.restart_count == 0 {
        return None;
    }
    let terminated = container.last_state.as_ref()?.terminated.as_ref()?;
    let minutes_ago = minutes_between(terminated.finished_at.as_ref()?.0, now);
    Some((minutes_ago, terminated.exit_code == 0))
}

/// Whether `container` is waiting in `CrashLoopBackOff`.
pub fn crash_looping(container: &ContainerStatus) -> bool {
    let waiting = container.state.as_ref().and_then(|s| s.waiting.as_ref());
    waiting.and_then(|w| w.reason.as_deref()) == Some("CrashLoopBackOff")
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::*;
    use super::*;

    fn restarting(container: &str, restarts: i32, minutes_ago: i64) -> Vec<Reason> {
        vec![Reason::Restarting {
            pod: "web-1".into(),
            container: container.into(),
            restarts,
            minutes_ago,
            clean: false,
        }]
    }

    fn exited_cleanly(restarts: i32, minutes_ago: i64) -> Vec<Reason> {
        vec![Reason::Restarting {
            pod: "web-1".into(),
            container: "app".into(),
            restarts,
            minutes_ago,
            clean: true,
        }]
    }

    fn crash_looping(restarts: i32) -> Vec<Reason> {
        vec![Reason::CrashLooping {
            pod: "web-1".into(),
            container: "app".into(),
            restarts,
        }]
    }

    fn pending_for(minutes: i64) -> Vec<Reason> {
        vec![Reason::PodPending {
            pod: "web-1".into(),
            minutes,
        }]
    }

    #[rstest]
    #[case::running_clean(pod("shop", "web-1").container(container("app")), vec![])]
    #[case::restarted_recently(
        pod("shop", "web-1").container(container("app").restarted(3, minutes_before(12))),
        restarting("app", 3, 12)
    )]
    #[case::exited_cleanly(
        pod("shop", "web-1").container(container("app").exited_cleanly(1, minutes_before(4))),
        exited_cleanly(1, 4)
    )]
    #[case::restarted_over_an_hour_ago(
        pod("shop", "web-1").container(container("app").restarted(3, minutes_before(60))),
        vec![]
    )]
    #[case::init_container_restarted(
        pod("shop", "web-1").init_container(container("setup").restarted(1, minutes_before(2))),
        restarting("setup", 1, 2)
    )]
    #[case::crash_looping_says_only_that(
        pod("shop", "web-1")
            .container(container("app").restarted(14, minutes_before(1)).crash_looping()),
        crash_looping(14)
    )]
    #[case::pending_past_the_limit(pod("shop", "web-1").pending_since(minutes_before(5)), pending_for(5))]
    #[case::pending_on_a_crash_looping_init_container(
        pod("shop", "web-1")
            .pending_since(minutes_before(30))
            .init_container(container("app").crash_looping()),
        crash_looping(0)
    )]
    #[case::pending_within_the_limit(pod("shop", "web-1").pending_since(minutes_before(4)), vec![])]
    #[case::being_deleted(
        pod("shop", "web-1")
            .container(container("app").crash_looping())
            .deleting(),
        vec![]
    )]
    #[case::failed(
        pod("shop", "web-1")
            .container(container("app").restarted(2, minutes_before(1)))
            .phase("Failed"),
        vec![]
    )]
    fn pod_reasons(#[case] pod: Pod, #[case] expected: Vec<Reason>) {
        assert_eq!(reasons(&pod, now()), expected);
    }
}
