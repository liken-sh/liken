//! Builders for the objects that the health rules read.  Each builder
//! sets only the fields that the rules read, the way the API server
//! fills them.

use std::collections::BTreeMap;

use k8s_openapi::api::apps::v1::{
    DaemonSet, DaemonSetStatus, Deployment, DeploymentSpec, DeploymentStatus, StatefulSet,
    StatefulSetSpec, StatefulSetStatus,
};
use k8s_openapi::api::batch::v1::{CronJob, CronJobStatus};
use k8s_openapi::api::core::v1::{
    ContainerState, ContainerStateTerminated, ContainerStateWaiting, ContainerStatus,
    ObjectReference, Pod, PodStatus,
};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::{ObjectMeta, OwnerReference, Time};
use k8s_openapi::jiff::{SignedDuration, Timestamp};

use transect_cluster::ownership::{Entry, Ownership};

/// The instant every test treats as now.
pub fn now() -> Timestamp {
    "2026-01-01T12:00:00Z".parse().unwrap()
}

pub fn seconds_before(seconds: i64) -> Timestamp {
    now() - SignedDuration::from_secs(seconds)
}

pub fn minutes_before(minutes: i64) -> Timestamp {
    seconds_before(minutes * 60)
}

fn meta(namespace: &str, name: &str) -> ObjectMeta {
    ObjectMeta {
        namespace: Some(namespace.into()),
        name: Some(name.into()),
        creation_timestamp: Some(Time(minutes_before(24 * 60))),
        ..Default::default()
    }
}

pub fn deployment(namespace: &str, name: &str, desired: i32, ready: i32) -> Deployment {
    Deployment {
        metadata: meta(namespace, name),
        spec: DeploymentSpec {
            replicas: Some(desired),
            ..Default::default()
        },
        status: Some(DeploymentStatus {
            ready_replicas: Some(ready),
            ..Default::default()
        }),
    }
}

pub fn stateful_set(namespace: &str, name: &str, desired: i32, ready: i32) -> StatefulSet {
    StatefulSet {
        metadata: meta(namespace, name),
        spec: StatefulSetSpec {
            replicas: Some(desired),
            ..Default::default()
        },
        status: Some(StatefulSetStatus {
            ready_replicas: Some(ready),
            ..Default::default()
        }),
    }
}

pub fn daemon_set(namespace: &str, name: &str, desired: i32, ready: i32) -> DaemonSet {
    DaemonSet {
        metadata: meta(namespace, name),
        spec: Default::default(),
        status: Some(DaemonSetStatus {
            desired_number_scheduled: desired,
            number_ready: ready,
            ..Default::default()
        }),
    }
}

/// A `CronJob` that last ran at `scheduled` and last succeeded at
/// `succeeded`, with `active` Jobs running now.
pub fn cron_job(
    scheduled: Option<Timestamp>,
    succeeded: Option<Timestamp>,
    active: usize,
) -> CronJob {
    CronJob {
        metadata: meta("shop", "report"),
        spec: Default::default(),
        status: Some(CronJobStatus {
            last_schedule_time: scheduled.map(Time),
            last_successful_time: succeeded.map(Time),
            active: Some(vec![ObjectReference::default(); active]),
        }),
    }
}

/// A `Running` pod created a day ago, with no container statuses.
pub fn pod(namespace: &str, name: &str) -> Pod {
    Pod {
        metadata: meta(namespace, name),
        spec: None,
        status: Some(PodStatus {
            phase: Some("Running".into()),
            ..Default::default()
        }),
    }
}

/// A pod of a `Deployment`, owned by the `ReplicaSet` that the
/// `Deployment` names after itself and the pod template's hash.
pub fn deployment_pod(namespace: &str, deployment: &str, created: Timestamp) -> Pod {
    let hash = "5d8f7c6b9";
    let mut pod = pod(namespace, &format!("{deployment}-{hash}-x2x7q")).created(created);
    pod.metadata.labels = Some(BTreeMap::from([("pod-template-hash".into(), hash.into())]));
    pod.owned_by("ReplicaSet", &format!("{deployment}-{hash}"))
}

/// A `ReplicaSet` named `name`, owned by the `Deployment` `deployment`.
pub fn replica_set(namespace: &str, name: &str, deployment: &str) -> ObjectMeta {
    let mut meta = meta(namespace, name);
    meta.owner_references = Some(vec![OwnerReference {
        api_version: "apps/v1".into(),
        kind: "Deployment".into(),
        name: deployment.into(),
        controller: Some(true),
        ..Default::default()
    }]);
    meta
}

/// The metadata of a `ReplicaSet` or `ControllerRevision` named `name`
/// in the namespace `audio`, owned by the `kind` named `owner` and
/// created at `created`.
pub fn revision(name: &str, kind: &str, owner: &str, created: Timestamp) -> ObjectMeta {
    let mut meta = replica_set("audio", name, owner);
    meta.owner_references.get_or_insert_default()[0].kind = kind.into();
    meta.creation_timestamp = Some(Time(created));
    meta
}

/// The ownership index of `pods` and `replica_sets`.
pub fn ownership(pods: &[&Pod], replica_sets: &[ObjectMeta]) -> Ownership {
    let pods = pods.iter().map(|pod| Entry {
        group: "",
        kind: "Pod",
        meta: &pod.metadata,
    });
    let replica_sets = replica_sets.iter().map(|meta| Entry {
        group: "apps",
        kind: "ReplicaSet",
        meta,
    });
    Ownership::build(pods.chain(replica_sets))
}

pub trait PodBuilder {
    fn created(self, at: Timestamp) -> Self;
    fn pending_since(self, at: Timestamp) -> Self;
    fn phase(self, phase: &str) -> Self;
    fn owned_by(self, kind: &str, name: &str) -> Self;
    fn on(self, machine: &str) -> Self;
    fn deleting(self) -> Self;
    fn container(self, status: ContainerStatus) -> Self;
    fn init_container(self, status: ContainerStatus) -> Self;
}

impl PodBuilder for Pod {
    fn created(mut self, at: Timestamp) -> Self {
        self.metadata.creation_timestamp = Some(Time(at));
        self
    }

    fn pending_since(self, at: Timestamp) -> Self {
        self.created(at).phase("Pending")
    }

    fn phase(mut self, phase: &str) -> Self {
        self.status.get_or_insert_default().phase = Some(phase.into());
        self
    }

    fn owned_by(mut self, kind: &str, name: &str) -> Self {
        self.metadata
            .owner_references
            .get_or_insert_default()
            .push(OwnerReference {
                api_version: "apps/v1".into(),
                kind: kind.into(),
                name: name.into(),
                controller: Some(true),
                ..Default::default()
            });
        self
    }

    fn on(mut self, machine: &str) -> Self {
        self.spec.get_or_insert_default().node_name = Some(machine.into());
        self
    }

    fn deleting(mut self) -> Self {
        self.metadata.deletion_timestamp = Some(Time(now()));
        self
    }

    fn container(mut self, status: ContainerStatus) -> Self {
        let status_list = &mut self.status.get_or_insert_default().container_statuses;
        status_list.get_or_insert_default().push(status);
        self
    }

    fn init_container(mut self, status: ContainerStatus) -> Self {
        let status_list = &mut self.status.get_or_insert_default().init_container_statuses;
        status_list.get_or_insert_default().push(status);
        self
    }
}

/// A ready container with no restarts.
pub fn container(name: &str) -> ContainerStatus {
    ContainerStatus {
        name: name.into(),
        ready: true,
        ..Default::default()
    }
}

pub trait ContainerBuilder {
    /// Restarted `restarts` times, the last run ending at `finished`.
    fn restarted(self, restarts: i32, finished: Timestamp) -> Self;
    /// Restarted `restarts` times, the last run exiting 0 at `finished`.
    fn exited_cleanly(self, restarts: i32, finished: Timestamp) -> Self;
    fn crash_looping(self) -> Self;
}

impl ContainerBuilder for ContainerStatus {
    fn restarted(mut self, restarts: i32, finished: Timestamp) -> Self {
        self.restart_count = restarts;
        self.last_state = Some(ContainerState {
            terminated: Some(ContainerStateTerminated {
                exit_code: 1,
                finished_at: Some(Time(finished)),
                ..Default::default()
            }),
            ..Default::default()
        });
        self
    }

    fn exited_cleanly(self, restarts: i32, finished: Timestamp) -> Self {
        let mut status = self.restarted(restarts, finished);
        let last = status
            .last_state
            .as_mut()
            .and_then(|s| s.terminated.as_mut());
        if let Some(terminated) = last {
            terminated.exit_code = 0;
            terminated.reason = Some("Completed".into());
        }
        status
    }

    fn crash_looping(mut self) -> Self {
        self.ready = false;
        self.state = Some(ContainerState {
            waiting: Some(ContainerStateWaiting {
                reason: Some("CrashLoopBackOff".into()),
                ..Default::default()
            }),
            ..Default::default()
        });
        self
    }
}
