//! The workload boxes of the open namespace. Small helper workloads,
//! such as relays and exporters, share one box, so the main workloads
//! are easy to find in a namespace that has many helpers.

use std::collections::{BTreeSet, HashMap};
use std::sync::Arc;

use k8s_openapi::api::core::v1::{Pod, PodSpec, Service};
use kube::ResourceExt;

use super::line::{self, Line};
use super::managed::ManagedBy;
use transect_cluster::ownership::{self};
use transect_cluster::workloads::Objects;
use transect_graph::edges::{Object, Plurals};
use transect_graph::pods::finished;
use transect_observe::health::{Health, Level};

#[derive(Debug, PartialEq)]
pub struct WorkloadBox {
    pub title: Title,
    pub line: Line,
    /// Where a custom resource's controller runs. The box shows it
    /// before `line`.
    pub managed_by: Option<ManagedBy>,
    /// The machines its pods run on. A workload with no running pod,
    /// such as a CronJob between runs, lists the machines that its
    /// finished pods ran on.
    pub machines: Vec<String>,
    pub health: Health,
    /// A built-in CronJob or Job.
    pub scheduled: bool,
    /// The workload's page. The helpers' box and a bare pod have no
    /// link.
    pub href: Option<String>,
}

impl WorkloadBox {
    pub fn class(&self) -> String {
        let mut class = String::from("workload");
        if self.scheduled {
            class.push_str(" workload-scheduled");
        }
        match self.health.level {
            Level::Healthy | Level::Notice => {}
            Level::Warning => class.push_str(" workload-warning"),
            Level::Failing => class.push_str(" workload-failing"),
        }
        class
    }
}

#[derive(Debug, PartialEq)]
pub enum Title {
    Workload { kind: String, name: String },
    Helpers(Vec<String>),
}

/// A workload, its pods, and how many of those pods have not finished.
struct Facts<'a> {
    workload: &'a Object,
    pods: &'a [&'a Pod],
    running: usize,
}

/// The boxes for `workloads`, the namespace's top owners. Workloads with
/// the most running pods come first, then the helpers' box, then the
/// CronJobs and Jobs.
pub fn boxes(
    workloads: &[&Object],
    pods: &HashMap<Object, Vec<&Pod>>,
    objects: &Objects,
    services: &[Arc<Service>],
    by_workload: &HashMap<Object, Health>,
    plurals: &Plurals,
    managers: &HashMap<Object, ManagedBy>,
) -> Vec<WorkloadBox> {
    let no_pods = Vec::new();
    let mut regular = Vec::new();
    let mut scheduled = Vec::new();
    let mut helpers: Vec<(Facts, WorkloadBox)> = Vec::new();
    for &workload in workloads {
        let pods = pods.get(workload).unwrap_or(&no_pods);
        let facts = Facts {
            workload,
            pods,
            running: pods.iter().filter(|pod| !finished(pod)).count(),
        };
        let one = WorkloadBox {
            title: Title::Workload {
                kind: workload.kind.clone(),
                name: workload.name.clone(),
            },
            line: line_of(&facts, objects),
            managed_by: managers.get(workload).cloned(),
            machines: machines(pods),
            health: by_workload.get(workload).cloned().unwrap_or_default(),
            scheduled: is_scheduled(workload),
            href: transect_graph::links::page(workload, plurals),
        };
        if one.scheduled {
            scheduled.push(one);
        } else if is_helper(&facts, services) {
            helpers.push((facts, one));
        } else {
            regular.push((facts.running, one));
        }
    }
    // A single helper gets its own box, because a group of one saves no
    // room.
    let helpers_box = if helpers.len() > 1 {
        Some(group(helpers))
    } else {
        regular.extend(helpers.into_iter().map(|(facts, one)| (facts.running, one)));
        None
    };
    regular.sort_by(|(a_running, a), (b_running, b)| {
        b_running
            .cmp(a_running)
            .then_with(|| a.title.cmp_key().cmp(&b.title.cmp_key()))
    });
    scheduled.sort_by(|a, b| a.title.cmp_key().cmp(&b.title.cmp_key()));
    regular
        .into_iter()
        .map(|(_, one)| one)
        .chain(helpers_box)
        .chain(scheduled)
        .collect()
}

impl Title {
    fn cmp_key(&self) -> (&str, &str) {
        match self {
            Self::Workload { kind, name } => (name, kind),
            Self::Helpers(_) => ("", ""),
        }
    }
}

fn line_of(facts: &Facts, objects: &Objects) -> Line {
    let workload = facts.workload;
    let pods = Line::Pods(facts.running);
    if ownership::custom(workload) {
        return pods;
    }
    let named = |namespace: Option<String>, name: String| {
        namespace == workload.namespace && name == workload.name
    };
    match workload.kind.as_str() {
        "Deployment" => objects
            .deployments
            .iter()
            .find(|o| named(o.namespace(), o.name_any()))
            .map_or(pods, |deployment| {
                let spec = &deployment.spec;
                replicas(spec.replicas, spec.template.spec.as_ref())
            }),
        "StatefulSet" => objects
            .stateful_sets
            .iter()
            .find(|o| named(o.namespace(), o.name_any()))
            .map_or(pods, |stateful_set| {
                let spec = &stateful_set.spec;
                replicas(spec.replicas, spec.template.spec.as_ref())
            }),
        "DaemonSet" => objects
            .daemon_sets
            .iter()
            .find(|o| named(o.namespace(), o.name_any()))
            .and_then(|daemon_set| daemon_set.status.as_ref())
            .map_or(pods, |status| {
                Line::OnMachines(status.desired_number_scheduled)
            }),
        "CronJob" => objects
            .cron_jobs
            .iter()
            .find(|o| named(o.namespace(), o.name_any()))
            .map_or(pods, |cron_job| Line::LastRun(line::last_run(cron_job))),
        "Job" => Line::Job(line::job_state(facts.pods)),
        _ => pods,
    }
}

/// Kubernetes defaults an unset `spec.replicas` to 1. A single replica
/// has no spread across machines to show.
fn replicas(replicas: Option<i32>, template: Option<&PodSpec>) -> Line {
    let replicas = replicas.unwrap_or(1);
    let spread = if replicas > 1 {
        transect_ground::guards::spread(template)
    } else {
        None
    };
    Line::Replicas { replicas, spread }
}

fn is_scheduled(workload: &Object) -> bool {
    !ownership::custom(workload) && matches!(workload.kind.as_str(), "CronJob" | "Job")
}

/// A helper is a built-in Deployment, StatefulSet, or DaemonSet that
/// runs one pod, mounts no storage, and that no Service selects. A
/// custom kind is never a helper, because an operator runs each one for
/// its own purpose.
fn is_helper(facts: &Facts, services: &[Arc<Service>]) -> bool {
    let built_in = matches!(
        facts.workload.kind.as_str(),
        "Deployment" | "StatefulSet" | "DaemonSet"
    );
    let running: Vec<&Pod> = facts
        .pods
        .iter()
        .copied()
        .filter(|pod| !finished(pod))
        .collect();
    built_in
        && !ownership::custom(facts.workload)
        && running.len() == 1
        && !running.iter().any(|pod| has_volume(pod))
        && !running.iter().any(|pod| selected_by_service(pod, services))
}

/// Storage is a PersistentVolumeClaim or an ephemeral volume, which
/// creates one. A ConfigMap, a Secret, or the service account token
/// does not count.
fn has_volume(pod: &Pod) -> bool {
    let volumes = pod.spec.as_ref().and_then(|spec| spec.volumes.as_ref());
    volumes
        .into_iter()
        .flatten()
        .any(|volume| volume.persistent_volume_claim.is_some() || volume.ephemeral.is_some())
}

/// A Service with no selector selects no pods, because its endpoints
/// are set by hand.
fn selected_by_service(pod: &Pod, services: &[Arc<Service>]) -> bool {
    let labels = pod.labels();
    services
        .iter()
        .filter(|service| service.namespace() == pod.namespace())
        .filter_map(|service| service.spec.as_ref()?.selector.as_ref())
        .filter(|selector| !selector.is_empty())
        .any(|selector| {
            selector
                .iter()
                .all(|(key, value)| labels.get(key) == Some(value))
        })
}

fn machines(pods: &[&Pod]) -> Vec<String> {
    let node = |pod: &&&Pod| pod.spec.as_ref().and_then(|spec| spec.node_name.clone());
    let running: BTreeSet<String> = pods
        .iter()
        .filter(|pod| !finished(pod))
        .filter_map(|pod| node(&pod))
        .collect();
    let machines = if running.is_empty() {
        pods.iter().filter_map(|pod| node(&pod)).collect()
    } else {
        running
    };
    machines.into_iter().collect()
}

/// The helpers' box lists every helper's reasons, so its level is the
/// worst level of any helper.
fn group(mut helpers: Vec<(Facts, WorkloadBox)>) -> WorkloadBox {
    helpers.sort_by(|(a, _), (b, _)| a.workload.name.cmp(&b.workload.name));
    let names = helpers
        .iter()
        .map(|(facts, _)| facts.workload.name.clone())
        .collect();
    let machines: BTreeSet<String> = helpers
        .iter()
        .flat_map(|(_, one)| one.machines.iter().cloned())
        .collect();
    let reasons = helpers
        .into_iter()
        .flat_map(|(_, one)| one.health.reasons)
        .collect();
    WorkloadBox {
        title: Title::Helpers(names),
        line: Line::Helpers,
        managed_by: None,
        machines: machines.into_iter().collect(),
        health: Health::from_reasons(reasons),
        scheduled: false,
        href: None,
    }
}

#[cfg(test)]
mod tests;
