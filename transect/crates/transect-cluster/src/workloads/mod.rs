//! Workloads: each top owner as the built-in controllers under it and
//! its pods describe it.
//!
//! A `Deployment` is its own controller.  A cache an operator runs is
//! described by the `StatefulSet` under it, because only its operator
//! defines what the custom kind's spec means.  A custom kind that runs
//! pods with no built-in controller between, as some database operators
//! do, is described by its pods alone.  The workload page, its card,
//! and the workload findings all read a workload through this one view,
//! so they agree on its replicas and its pod template.

mod last_run;
mod part;

use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;

use k8s_openapi::api::apps::v1::{DaemonSet, Deployment, StatefulSet};
use k8s_openapi::api::batch::v1::CronJob;
use k8s_openapi::api::core::v1::{Pod, PodSpec};
use kube::ResourceExt;

pub use last_run::LastRun;
use part::Part;
pub use part::{Controller, Revisions};

use crate::ownership::{self, Ownership};
use transect_graph::edges::Object;
use transect_graph::pods::finished;

/// The workload objects and pods health is read from, as the cluster's
/// stores hold them.
pub struct Objects<'a> {
    pub deployments: &'a [Arc<Deployment>],
    pub stateful_sets: &'a [Arc<StatefulSet>],
    pub daemon_sets: &'a [Arc<DaemonSet>],
    pub cron_jobs: &'a [Arc<CronJob>],
    pub pods: &'a [Arc<Pod>],
}

/// Every top owner's controllers and pods.
#[derive(Default)]
pub struct Workloads<'a> {
    parts: HashMap<Object, Vec<Part<'a>>>,
    pods: HashMap<Object, Vec<&'a Pod>>,
}

/// One top owner, read through its controllers and its pods.
#[derive(Debug)]
pub struct Workload<'w, 'a> {
    pub object: &'w Object,
    pub parts: &'w [Part<'a>],
    /// Every pod it owns, finished ones included.
    pub pods: &'w [&'a Pod],
}

impl<'a> Workloads<'a> {
    /// Files each built-in controller and each pod in `objects` under
    /// its top owner.  `revisions` dates each controller's last rollout.
    pub fn build(objects: &Objects<'a>, revisions: &Revisions, ownership: &Ownership) -> Self {
        let mut workloads = Self::default();
        let mut file = |group: &str, kind: &str, meta: &kube::api::ObjectMeta, part: Part<'a>| {
            let object = Object::new(group, kind, meta.namespace.as_deref(), &part.name);
            let top = ownership.top_owner(&object);
            workloads.parts.entry(top).or_default().push(part);
        };
        for deployment in objects.deployments {
            let part = Part::deployment(deployment, revisions);
            file("apps", "Deployment", &deployment.metadata, part);
        }
        for stateful_set in objects.stateful_sets {
            let part = Part::stateful_set(stateful_set, revisions);
            file("apps", "StatefulSet", &stateful_set.metadata, part);
        }
        for daemon_set in objects.daemon_sets {
            let part = Part::daemon_set(daemon_set, revisions);
            file("apps", "DaemonSet", &daemon_set.metadata, part);
        }
        for cron_job in objects.cron_jobs {
            if let Some(part) = Part::cron_job(cron_job) {
                file("batch", "CronJob", &cron_job.metadata, part);
            }
        }
        for pod in objects.pods {
            let object = Object::new("", "Pod", pod.namespace().as_deref(), &pod.name_any());
            let top = ownership.top_owner(&object);
            workloads.pods.entry(top).or_default().push(pod);
        }
        workloads
    }

    /// Every top owner with a controller or a pod.
    pub fn owners(&self) -> impl Iterator<Item = &Object> {
        let pods = self
            .pods
            .keys()
            .filter(|owner| !self.parts.contains_key(*owner));
        self.parts.keys().chain(pods)
    }

    pub fn get<'w>(&'w self, object: &'w Object) -> Workload<'w, 'a> {
        Workload {
            object,
            parts: self.parts.get(object).map_or(&[], Vec::as_slice),
            pods: self.pods.get(object).map_or(&[], Vec::as_slice),
        }
    }
}

impl<'a> Workload<'_, 'a> {
    /// The pods that still run or wait to, leaving out those that
    /// finished, as a `CronJob`'s do.
    pub fn running(&self) -> Vec<&'a Pod> {
        let pods = self.pods.iter().copied();
        pods.filter(|pod| !finished(pod)).collect()
    }

    /// How many pods it asks for, when that is a count it sets: the
    /// replicas of its Deployments and StatefulSets, or, for a custom
    /// kind with no controller under it, the pods it runs.  A `DaemonSet`
    /// runs one pod per machine and a `CronJob` one per run, so neither
    /// has replicas.
    pub fn replicas(&self) -> Option<i32> {
        let counted: Vec<i32> = self.parts.iter().filter_map(|part| part.replicas).collect();
        if !counted.is_empty() {
            return Some(counted.iter().sum());
        }
        let runs_its_own_pods = self.parts.is_empty() && ownership::custom(self.object);
        runs_its_own_pods.then(|| self.running().len() as i32)
    }

    /// Whether it runs more than one pod at once, which is what makes a
    /// readiness probe and a disruption budget worth having.
    pub fn replicated(&self) -> bool {
        self.replicas().is_some_and(|replicas| replicas > 1)
    }

    /// How many of its pods are ready, of how many it asks for.  A
    /// custom kind with no controller under it asks for the pods it
    /// runs.  None for a `CronJob` or a `Job`, whose pods finish.
    pub fn ready(&self) -> Option<(i32, i32)> {
        let counted: Vec<(i32, i32)> = self.parts.iter().filter_map(|part| part.ready).collect();
        if !counted.is_empty() {
            let ready = counted.iter().map(|(ready, _)| ready).sum();
            let wanted = counted.iter().map(|(_, wanted)| wanted).sum();
            return Some((ready, wanted));
        }
        if !self.parts.is_empty() || self.object.kind == "Job" {
            return None;
        }
        let running = self.running();
        let ready = running.iter().filter(|pod| pod_ready(pod)).count();
        Some((ready as i32, running.len() as i32))
    }

    /// How its newest pod ended, when it is described by its pods alone
    /// and none of them runs, as for a custom kind that runs Jobs,
    /// between runs.  The inner `None` is for one with no finished pod.
    pub fn idle(&self) -> Option<Option<LastRun>> {
        let by_its_pods = self.parts.is_empty() && ownership::custom(self.object);
        (by_its_pods && self.running().is_empty()).then(|| last_run::last_run(self.pods))
    }

    /// The pod specs it runs: its controllers' templates, or, with no
    /// controller, the specs of its running pods, or of every pod when
    /// none runs.
    pub fn templates(&self) -> Vec<&'a PodSpec> {
        if !self.parts.is_empty() {
            let templates = self.parts.iter().map(|part| part.template.spec.as_ref());
            return templates.flatten().collect();
        }
        let running = self.running();
        let pods = if running.is_empty() {
            self.pods.to_vec()
        } else {
            running
        };
        pods.iter().filter_map(|pod| pod.spec.as_ref()).collect()
    }

    /// Each pod template with its labels: its controllers' templates, or,
    /// for a kind that runs its own pods, the pods that `templates` reads.
    pub fn labelled_templates(&self) -> Vec<(&'a BTreeMap<String, String>, &'a PodSpec)> {
        static NONE: BTreeMap<String, String> = BTreeMap::new();
        if !self.parts.is_empty() {
            let templates = self.parts.iter().filter_map(|part| {
                let meta = part.template.metadata.as_ref();
                let labels = meta.and_then(|meta| meta.labels.as_ref());
                Some((labels.unwrap_or(&NONE), part.template.spec.as_ref()?))
            });
            return templates.collect();
        }
        let running = self.running();
        let pods = if running.is_empty() {
            self.pods.to_vec()
        } else {
            running
        };
        let pods = pods.into_iter().filter_map(|pod| {
            let labels = pod.metadata.labels.as_ref().unwrap_or(&NONE);
            Some((labels, pod.spec.as_ref()?))
        });
        pods.collect()
    }

    /// The labels of its running pods, or, with none running, of its
    /// controllers' pod templates, which is what a selector such as a
    /// `PodDisruptionBudget`'s is matched against.
    pub fn pod_labels(&self) -> Vec<&'a BTreeMap<String, String>> {
        let running = self.running();
        let labels: Vec<_> = if running.is_empty() {
            let templates = self.parts.iter().map(|part| &part.template.metadata);
            templates
                .flatten()
                .filter_map(|meta| meta.labels.as_ref())
                .collect()
        } else {
            running
                .iter()
                .filter_map(|pod| pod.metadata.labels.as_ref())
                .collect()
        };
        labels
    }

    /// Every image its app containers run, each once, in the order its
    /// templates list them.
    pub fn images(&self) -> Vec<String> {
        let mut images: Vec<String> = Vec::new();
        let containers = self
            .templates()
            .into_iter()
            .flat_map(|spec| &spec.containers);
        for image in containers.filter_map(|container| container.image.clone()) {
            if !images.contains(&image) {
                images.push(image);
            }
        }
        images
    }

    /// When any of its controllers last rolled.
    pub fn rolled(&self) -> Option<k8s_openapi::jiff::Timestamp> {
        self.parts.iter().filter_map(|part| part.rolled).max()
    }

    /// Whether its pods run to completion: it is a `CronJob` or a `Job`,
    /// or a custom kind whose every part a `CronJob` runs.  A `Service`
    /// sends no requests to such pods, so the guards list no rollout
    /// strategy and no missing readiness probe for them.
    pub fn scheduled(&self) -> bool {
        if !ownership::custom(self.object) {
            return matches!(self.object.kind.as_str(), "CronJob" | "Job");
        }
        let cron_jobs = self.parts.iter().map(|part| part.controller);
        !self.parts.is_empty() && cron_jobs.into_iter().all(|c| c == Controller::CronJob)
    }
}

/// Whether the pod's `Ready` condition is true.
pub fn pod_ready(pod: &Pod) -> bool {
    let conditions = pod.status.as_ref().and_then(|s| s.conditions.as_ref());
    conditions
        .into_iter()
        .flatten()
        .any(|condition| condition.type_ == "Ready" && condition.status == "True")
}

#[cfg(test)]
mod tests;
