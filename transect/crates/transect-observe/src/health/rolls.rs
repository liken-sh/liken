//! Finds container restarts that follow another workload's rollout on
//! the same machine. A new `DaemonSet` pod, for example, can restart a
//! service that other workloads on the machine depend on, such as a
//! shared system bus. Their containers then restart on each machine
//! shortly after the new pod starts there. When that happens on at
//! least `MACHINES` machines, transect reports the rollout as the likely
//! cause.

use std::collections::{BTreeMap, BTreeSet};

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;

use transect_cluster::workloads::Revisions;
use transect_graph::edges::Object;

/// How many seconds after another workload's pod starts on a machine a
/// restart on that machine counts as following it.
const WINDOW_SECONDS: i64 = 120;

/// The fewest machines on which restarts must follow a rollout before
/// transect reports it.
const MACHINES: usize = 2;

/// The last stop of a restarted container of a workload's pod.
#[derive(Clone, Debug, PartialEq)]
pub struct Restart {
    pub workload: Object,
    pub container: String,
    pub machine: String,
    pub at: Timestamp,
}

/// A pod of a workload's newest revision that started on `machine` at
/// `at`.
#[derive(Clone, Debug, PartialEq)]
pub struct Rolled {
    pub workload: Object,
    pub machine: String,
    pub at: Timestamp,
}

/// The workload whose rollout the restarts followed, and the number of
/// machines where they followed it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AfterRoll {
    pub rolled: String,
    pub machines: usize,
}

/// For each workload and container, the other workload whose rollout
/// its restarts followed on the most machines, when that is at least
/// `MACHINES`.  When two workloads tie, the one that sorts first wins.
pub fn after_rolls(
    restarts: &[Restart],
    rolled: &[Rolled],
) -> BTreeMap<(Object, String), AfterRoll> {
    let mut followed: BTreeMap<(Object, String), BTreeMap<&Object, BTreeSet<&str>>> =
        BTreeMap::new();
    for restart in restarts {
        let key = (restart.workload.clone(), restart.container.clone());
        let causes = followed.entry(key).or_default();
        for step in rolled.iter().filter(|step| follows(restart, step)) {
            causes
                .entry(&step.workload)
                .or_default()
                .insert(&step.machine);
        }
    }
    followed
        .into_iter()
        .filter_map(|(key, causes)| {
            let (rolled, machines) = causes
                .into_iter()
                .map(|(workload, machines)| (workload, machines.len()))
                .max_by(|a, b| a.1.cmp(&b.1).then_with(|| b.0.cmp(a.0)))?;
            (machines >= MACHINES).then(|| {
                let rolled = rolled.name.clone();
                (key, AfterRoll { rolled, machines })
            })
        })
        .collect()
}

fn follows(restart: &Restart, step: &Rolled) -> bool {
    let seconds = restart.at.as_second() - step.at.as_second();
    step.workload != restart.workload
        && step.machine == restart.machine
        && (0..=WINDOW_SECONDS).contains(&seconds)
}

/// The last stop of each container of `pod` that has restarted, with
/// `owner`, the pod's top owner.
pub fn restarts_of(pod: &Pod, owner: &Object) -> Vec<Restart> {
    let Some(machine) = machine(pod) else {
        return Vec::new();
    };
    let status = pod.status.as_ref();
    let init = status.and_then(|s| s.init_container_statuses.as_ref());
    let main = status.and_then(|s| s.container_statuses.as_ref());
    init.into_iter()
        .chain(main)
        .flatten()
        .filter(|container| container.restart_count > 0)
        .filter_map(|container| {
            let terminated = container.last_state.as_ref()?.terminated.as_ref()?;
            Some(Restart {
                workload: owner.clone(),
                container: container.name.clone(),
                machine: machine.clone(),
                at: terminated.finished_at.as_ref()?.0,
            })
        })
        .collect()
}

/// When `pod` started on its machine, if it belongs to its controller's
/// newest revision, which is when it was created at or after that
/// revision. A pod with no start time counts from its creation.
pub fn rolled_of(pod: &Pod, owner: &Object, revisions: &Revisions) -> Option<Rolled> {
    let namespace = pod.namespace()?;
    let controller = pod
        .owner_references()
        .iter()
        .find(|r| r.controller == Some(true))?;
    let newest = match controller.kind.as_str() {
        "DaemonSet" | "StatefulSet" => {
            revisions.newest(&namespace, &controller.kind, &controller.name)
        }
        // A `Deployment` names each `ReplicaSet` `<deployment>-<hash>`,
        // where the hash is the pod's `pod-template-hash` label.
        "ReplicaSet" => {
            let hash = pod.labels().get("pod-template-hash")?;
            let deployment = controller.name.strip_suffix(&format!("-{hash}"))?;
            revisions.newest(&namespace, "Deployment", deployment)
        }
        _ => None,
    }?;
    let created = pod.metadata.creation_timestamp.as_ref()?.0;
    let started = pod.status.as_ref().and_then(|s| s.start_time.as_ref());
    let machine = machine(pod)?;
    (created >= newest).then(|| Rolled {
        workload: owner.clone(),
        machine,
        at: started.map_or(created, |time| time.0),
    })
}

fn machine(pod: &Pod) -> Option<String> {
    pod.spec.as_ref()?.node_name.clone()
}

#[cfg(test)]
mod tests;
