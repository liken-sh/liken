//! The replica rules: a workload whose pods are not all ready.
//!
//! A workload with no ready replicas is failing, because nothing that it
//! serves answers.  One with some replicas not ready is a warning.  Both
//! rules allow a grace period while the workload has a pod younger than
//! `GRACE_MINUTES`.  A rollout, a scale up, and a pod rescheduled off a
//! drained machine all start new pods that take a while to pass their
//! readiness checks.  The pod's age covers all three cases the same way
//! for every kind, but rollout status differs by kind, and only a
//! `Deployment`'s rollout status includes a timestamp.
//!
//! The grace period lowers the level of a workload with none ready, but
//! does not hide it.  A one-replica workload has none ready for a few
//! seconds on every rollout, so it shows as a warning then instead of
//! showing as failed for a moment.  It still shows, because it serves
//! nothing during that time.

use k8s_openapi::api::apps::v1::{DaemonSet, Deployment, StatefulSet};
use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;

use super::{Kind, Reason, Workload, minutes_between};
use transect_cluster::ownership::Ownership;
use transect_graph::edges::Object;

/// How long a new pod may take to become ready before its workload's
/// missing replicas count as a warning.
const GRACE_MINUTES: i64 = 5;

/// The pods that the health rules read for a workload: a namespace's
/// pods, and the owner references that link each pod to its workload.
#[derive(Clone, Copy)]
pub struct Pods<'a> {
    pub pods: &'a [&'a Pod],
    pub ownership: &'a Ownership,
}

pub fn deployment(deployment: &Deployment, pods: Pods, now: Timestamp) -> Option<Reason> {
    let name = deployment.name_any();
    // The API server defaults `spec.replicas` to 1 when it is left out.
    let desired = deployment.spec.replicas.unwrap_or(1);
    let ready = deployment.status.as_ref()?.ready_replicas.unwrap_or(0);
    let settling = || pods.has_new(&owner("apps", "Deployment", deployment), now);
    replicas(workload(Kind::Deployment, &name), desired, ready, settling)
}

pub fn stateful_set(stateful_set: &StatefulSet, pods: Pods, now: Timestamp) -> Option<Reason> {
    let name = stateful_set.name_any();
    let desired = stateful_set.spec.replicas.unwrap_or(1);
    let ready = stateful_set.status.as_ref()?.ready_replicas.unwrap_or(0);
    let settling = || pods.has_new(&owner("apps", "StatefulSet", stateful_set), now);
    replicas(workload(Kind::StatefulSet, &name), desired, ready, settling)
}

pub fn daemon_set(daemon_set: &DaemonSet, pods: Pods, now: Timestamp) -> Option<Reason> {
    let name = daemon_set.name_any();
    let status = daemon_set.status.as_ref()?;
    let settling = || pods.has_new(&owner("apps", "DaemonSet", daemon_set), now);
    replicas(
        workload(Kind::DaemonSet, &name),
        status.desired_number_scheduled,
        status.number_ready,
        settling,
    )
}

fn owner(group: &str, kind: &str, object: &impl ResourceExt) -> Object {
    let namespace = object.namespace();
    Object::new(group, kind, namespace.as_deref(), &object.name_any())
}

fn workload(kind: Kind, name: &str) -> Workload {
    Workload {
        kind,
        name: name.into(),
    }
}

/// Scaled to zero is healthy: nothing was asked for, and nothing is
/// missing.
fn replicas(
    workload: Workload,
    desired: i32,
    ready: i32,
    settling: impl FnOnce() -> bool,
) -> Option<Reason> {
    if desired <= 0 || ready >= desired {
        None
    } else if ready <= 0 {
        if settling() {
            Some(Reason::Starting { workload, desired })
        } else {
            Some(Reason::NoReadyReplicas { workload, desired })
        }
    } else if settling() {
        None
    } else {
        Some(Reason::NotReady {
            workload,
            ready,
            desired,
        })
    }
}

impl Pods<'_> {
    /// Whether any pod that `owner` runs, through any number of owners
    /// between them, started less than `GRACE_MINUTES` ago.  A
    /// `Deployment`'s pods belong to its ReplicaSets, so the function
    /// follows the owner references instead of matching by name.
    fn has_new(&self, owner: &Object, now: Timestamp) -> bool {
        self.pods.iter().any(|pod| {
            let created = pod.metadata.creation_timestamp.as_ref();
            let new =
                created.is_some_and(|created| minutes_between(created.0, now) < GRACE_MINUTES);
            let namespace = pod.namespace();
            let object = Object::new("", "Pod", namespace.as_deref(), &pod.name_any());
            new && self.ownership.is_owned_by(&object, owner)
        })
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::{
        self, PodBuilder, deployment_pod, minutes_before, now, pod, replica_set,
    };
    use super::*;

    type Rule<T> = fn(&T, Pods, Timestamp) -> Option<Reason>;

    /// `rule` applied to `workload` with `pods` in its namespace, whose
    /// owner references are indexed with the ReplicaSets of the
    /// Deployments that `deployment_pod` names.
    fn assessed<T>(rule: Rule<T>, workload: &T, pods: &[&Pod]) -> Option<Reason> {
        let replica_sets = [
            replica_set("shop", "web-5d8f7c6b9", "web"),
            replica_set("shop", "web-admin-5d8f7c6b9", "web-admin"),
        ];
        let ownership = fixtures::ownership(pods, &replica_sets);
        let pods = Pods {
            pods,
            ownership: &ownership,
        };
        rule(workload, pods, now())
    }

    fn not_ready(kind: Kind, ready: i32, desired: i32) -> Option<Reason> {
        Some(Reason::NotReady {
            workload: workload(kind, "web"),
            ready,
            desired,
        })
    }

    fn none_ready(kind: Kind, desired: i32) -> Option<Reason> {
        Some(Reason::NoReadyReplicas {
            workload: workload(kind, "web"),
            desired,
        })
    }

    #[rstest]
    #[case::all_ready(2, 2, None)]
    #[case::scaled_to_zero(0, 0, None)]
    #[case::surging_past_desired(2, 3, None)]
    #[case::some_ready(3, 1, not_ready(Kind::Deployment, 1, 3))]
    #[case::none_ready(2, 0, none_ready(Kind::Deployment, 2))]
    fn deployment_replicas(
        #[case] desired: i32,
        #[case] ready: i32,
        #[case] reason: Option<Reason>,
    ) {
        let web = fixtures::deployment("shop", "web", desired, ready);
        assert_eq!(assessed(super::deployment, &web, &[]), reason);
    }

    #[rstest]
    #[case::all_ready(1, 1, None)]
    #[case::scaled_to_zero(0, 0, None)]
    #[case::some_ready(3, 2, not_ready(Kind::StatefulSet, 2, 3))]
    #[case::none_ready(1, 0, none_ready(Kind::StatefulSet, 1))]
    fn stateful_set_replicas(
        #[case] desired: i32,
        #[case] ready: i32,
        #[case] reason: Option<Reason>,
    ) {
        let web = fixtures::stateful_set("shop", "web", desired, ready);
        assert_eq!(assessed(super::stateful_set, &web, &[]), reason);
    }

    #[rstest]
    #[case::all_ready(4, 4, None)]
    #[case::on_no_machines(0, 0, None)]
    #[case::some_ready(4, 3, not_ready(Kind::DaemonSet, 3, 4))]
    #[case::none_ready(4, 0, none_ready(Kind::DaemonSet, 4))]
    fn daemon_set_replicas(
        #[case] desired: i32,
        #[case] ready: i32,
        #[case] reason: Option<Reason>,
    ) {
        let web = fixtures::daemon_set("shop", "web", desired, ready);
        assert_eq!(assessed(super::daemon_set, &web, &[]), reason);
    }

    #[rstest]
    #[case::new_pod_of_ours(deployment_pod("shop", "web", minutes_before(2)), None)]
    #[case::old_pod_of_ours(
        deployment_pod("shop", "web", minutes_before(6)),
        not_ready(Kind::Deployment, 1, 2)
    )]
    #[case::new_pod_of_a_longer_name(
        deployment_pod("shop", "web-admin", minutes_before(2)),
        not_ready(Kind::Deployment, 1, 2)
    )]
    fn a_deployment_with_a_new_pod_is_settling(#[case] pod: Pod, #[case] reason: Option<Reason>) {
        let web = fixtures::deployment("shop", "web", 2, 1);
        assert_eq!(assessed(super::deployment, &web, &[&pod]), reason);
    }

    fn starting(kind: Kind, desired: i32) -> Option<Reason> {
        Some(Reason::Starting {
            workload: workload(kind, "web"),
            desired,
        })
    }

    #[rstest]
    #[case::new_pod_of_ours(
        deployment_pod("shop", "web", minutes_before(1)),
        starting(Kind::Deployment, 1)
    )]
    #[case::grace_ends_at_five_minutes(
        deployment_pod("shop", "web", minutes_before(5)),
        none_ready(Kind::Deployment, 1)
    )]
    #[case::old_pod_of_ours(
        deployment_pod("shop", "web", minutes_before(30)),
        none_ready(Kind::Deployment, 1)
    )]
    #[case::new_pod_of_a_longer_name(
        deployment_pod("shop", "web-admin", minutes_before(1)),
        none_ready(Kind::Deployment, 1)
    )]
    fn a_deployment_with_none_ready_and_a_new_pod_is_starting(
        #[case] pod: Pod,
        #[case] reason: Option<Reason>,
    ) {
        let web = fixtures::deployment("shop", "web", 1, 0);
        assert_eq!(assessed(super::deployment, &web, &[&pod]), reason);
    }

    #[rstest]
    #[case::new(minutes_before(2), starting(Kind::StatefulSet, 1))]
    #[case::old(minutes_before(6), none_ready(Kind::StatefulSet, 1))]
    fn a_stateful_set_with_none_ready_and_a_new_pod_is_starting(
        #[case] created: Timestamp,
        #[case] reason: Option<Reason>,
    ) {
        let web = fixtures::stateful_set("shop", "web", 1, 0);
        let pod = pod("shop", "web-0")
            .created(created)
            .owned_by("StatefulSet", "web");
        assert_eq!(assessed(super::stateful_set, &web, &[&pod]), reason);
    }

    #[rstest]
    #[case::new(minutes_before(2), starting(Kind::DaemonSet, 1))]
    #[case::old(minutes_before(6), none_ready(Kind::DaemonSet, 1))]
    fn a_daemon_set_with_none_ready_and_a_new_pod_is_starting(
        #[case] created: Timestamp,
        #[case] reason: Option<Reason>,
    ) {
        let web = fixtures::daemon_set("shop", "web", 1, 0);
        let pod = pod("shop", "web-abcde")
            .created(created)
            .owned_by("DaemonSet", "web");
        assert_eq!(assessed(super::daemon_set, &web, &[&pod]), reason);
    }

    #[rstest]
    #[case::new(minutes_before(4), None)]
    #[case::old(minutes_before(5), not_ready(Kind::StatefulSet, 1, 2))]
    fn a_stateful_set_with_a_new_pod_is_settling(
        #[case] created: Timestamp,
        #[case] reason: Option<Reason>,
    ) {
        let web = fixtures::stateful_set("shop", "web", 2, 1);
        let pod = pod("shop", "web-1")
            .created(created)
            .owned_by("StatefulSet", "web");
        assert_eq!(assessed(super::stateful_set, &web, &[&pod]), reason);
    }

    #[rstest]
    #[case::ours("DaemonSet", "web", None)]
    #[case::another_kind("StatefulSet", "web", not_ready(Kind::DaemonSet, 3, 4))]
    #[case::another_name("DaemonSet", "db", not_ready(Kind::DaemonSet, 3, 4))]
    fn a_daemon_set_settles_on_its_own_new_pods(
        #[case] kind: &str,
        #[case] name: &str,
        #[case] reason: Option<Reason>,
    ) {
        let web = fixtures::daemon_set("shop", "web", 4, 3);
        let pod = pod("shop", "web-abcde")
            .created(minutes_before(1))
            .owned_by(kind, name);
        assert_eq!(assessed(super::daemon_set, &web, &[&pod]), reason);
    }
}
