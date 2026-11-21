//! Workloads: each top owner, described by the built-in controllers and
//! pods under it.
//!
//! A custom kind is described by the `StatefulSet` or other controller
//! that its operator creates, because only the operator's code gives the
//! custom kind's spec a meaning. A custom kind whose operator creates pods
//! directly, as some database operators do, is described by its pods.
//! The workload page, its card, and the workload findings all read a
//! workload through this module, so they agree on its replicas and pod
//! templates.

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

/// The controllers and pods that workloads are built from.
pub struct Objects<'a> {
    pub deployments: &'a [Arc<Deployment>],
    pub stateful_sets: &'a [Arc<StatefulSet>],
    pub daemon_sets: &'a [Arc<DaemonSet>],
    pub cron_jobs: &'a [Arc<CronJob>],
    pub pods: &'a [Arc<Pod>],
}

/// The controllers and pods under each top owner.
#[derive(Default)]
pub struct Workloads<'a> {
    parts: HashMap<Object, Vec<Part<'a>>>,
    pods: HashMap<Object, Vec<&'a Pod>>,
}

/// One top owner with the controllers and pods under it.
#[derive(Debug)]
pub struct Workload<'w, 'a> {
    pub object: &'w Object,
    pub parts: &'w [Part<'a>],
    /// Every pod under it, including finished ones.
    pub pods: &'w [&'a Pod],
}

impl<'a> Workloads<'a> {
    /// Groups each built-in controller and each pod in `objects` by its
    /// top owner. `revisions` gives the time of each controller's last
    /// rollout.
    pub fn build(objects: &Objects<'a>, revisions: &Revisions, ownership: &Ownership) -> Self {
        let mut workloads = Self::default();
        let mut add_part =
            |group: &str, kind: &str, meta: &kube::api::ObjectMeta, part: Part<'a>| {
                let object = Object::new(group, kind, meta.namespace.as_deref(), &part.name);
                let top = ownership.top_owner(&object);
                workloads.parts.entry(top).or_default().push(part);
            };
        for deployment in objects.deployments {
            let part = Part::deployment(deployment, revisions);
            add_part("apps", "Deployment", &deployment.metadata, part);
        }
        for stateful_set in objects.stateful_sets {
            let part = Part::stateful_set(stateful_set, revisions);
            add_part("apps", "StatefulSet", &stateful_set.metadata, part);
        }
        for daemon_set in objects.daemon_sets {
            let part = Part::daemon_set(daemon_set, revisions);
            add_part("apps", "DaemonSet", &daemon_set.metadata, part);
        }
        for cron_job in objects.cron_jobs {
            if let Some(part) = Part::cron_job(cron_job) {
                add_part("batch", "CronJob", &cron_job.metadata, part);
            }
        }
        for pod in objects.pods {
            let object = Object::new("", "Pod", pod.namespace().as_deref(), &pod.name_any());
            let top = ownership.top_owner(&object);
            workloads.pods.entry(top).or_default().push(pod);
        }
        workloads
    }

    /// Every top owner that has a controller or a pod.
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
    /// The pods that have not finished.
    pub fn running(&self) -> Vec<&'a Pod> {
        let pods = self.pods.iter().copied();
        pods.filter(|pod| !finished(pod)).collect()
    }

    /// The number of pods it should run: the sum of the replicas of its
    /// `Deployment`s and `StatefulSet`s, or, for a custom kind with no
    /// controller under it, the number of its running pods. It is `None`
    /// for a `DaemonSet` or a `CronJob`, which set no replica count.
    pub fn replicas(&self) -> Option<i32> {
        let counted: Vec<i32> = self.parts.iter().filter_map(|part| part.replicas).collect();
        if !counted.is_empty() {
            return Some(counted.iter().sum());
        }
        let runs_its_own_pods = self.parts.is_empty() && ownership::custom(self.object);
        runs_its_own_pods.then(|| self.running().len() as i32)
    }

    /// Whether it should run more than one pod. Only then does a readiness
    /// probe or a `PodDisruptionBudget` keep it available.
    pub fn replicated(&self) -> bool {
        self.replicas().is_some_and(|replicas| replicas > 1)
    }

    /// How many of its pods are ready, and how many it should run. With no
    /// controller under it, it should run as many as it has running pods. It
    /// is `None` for a `CronJob` or a `Job`, whose pods finish.
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
        let ready = running
            .iter()
            .filter(|pod| transect_graph::pods::ready(pod))
            .count();
        Some((ready as i32, running.len() as i32))
    }

    /// How its newest pod ended, for a custom kind with no controller
    /// and no running pod, such as one that runs `Job`s, between runs.
    /// The outer `None` means it is not such a workload, and the inner
    /// `None` means it has no finished pod.
    pub fn idle(&self) -> Option<Option<LastRun>> {
        let by_its_pods = self.parts.is_empty() && ownership::custom(self.object);
        (by_its_pods && self.running().is_empty()).then(|| last_run::last_run(self.pods))
    }

    /// Its controllers' pod templates. With no controller, it returns
    /// the specs of its running pods, or of every pod when none is
    /// running.
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

    /// The specs of `templates`, each with its labels.
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
    /// controllers' pod templates, for matching a selector such as a
    /// `PodDisruptionBudget`'s.
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

    /// The images of its app containers, without duplicates, in the order
    /// `templates` lists them.
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

    /// The time of the latest rollout of any of its controllers.
    pub fn rolled(&self) -> Option<k8s_openapi::jiff::Timestamp> {
        self.parts.iter().filter_map(|part| part.rolled).max()
    }

    /// Whether its pods run to completion: it is a `CronJob` or a `Job`,
    /// or a custom kind whose controllers are all `CronJob`s. Such pods
    /// serve no requests, so a readiness probe or a rollout strategy does
    /// not apply to them.
    pub fn scheduled(&self) -> bool {
        if !ownership::custom(self.object) {
            return matches!(self.object.kind.as_str(), "CronJob" | "Job");
        }
        let cron_jobs = self.parts.iter().map(|part| part.controller);
        !self.parts.is_empty() && cron_jobs.into_iter().all(|c| c == Controller::CronJob)
    }
}

#[cfg(test)]
mod tests;
