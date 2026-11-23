//! A part is one built-in controller under a workload's top owner, such
//! as the `StatefulSet` that a cache's operator creates, with the fields
//! that the workload's page and card show.

use std::collections::HashMap;

use k8s_openapi::api::apps::v1::{DaemonSet, Deployment, StatefulSet};
use k8s_openapi::api::batch::v1::CronJob;
use k8s_openapi::api::core::v1::PodTemplateSpec;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;

/// The update strategy that the API server sets when a `Deployment`, a
/// `StatefulSet`, or a `DaemonSet` has none.
const DEFAULT_STRATEGY: &str = "RollingUpdate";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Controller {
    Deployment,
    StatefulSet,
    DaemonSet,
    CronJob,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Part<'a> {
    pub controller: Controller,
    pub name: String,
    /// `spec.replicas`, for a `Deployment` or a `StatefulSet`.
    pub replicas: Option<i32>,
    /// How many of its pods are ready, and how many it should run. `None` for
    /// a `CronJob`, whose pods come and go.
    pub ready: Option<(i32, i32)>,
    pub template: &'a PodTemplateSpec,
    /// `RollingUpdate`, `Recreate`, or `OnDelete`, and `None` for a `CronJob`.
    pub strategy: Option<String>,
    /// When its newest revision was created: the newest `ReplicaSet` of
    /// a `Deployment`, or the newest `ControllerRevision` of a
    /// `StatefulSet` or a `DaemonSet`. A new revision is created when
    /// the pod template changes.
    pub rolled: Option<Timestamp>,
}

/// The creation time of the newest revision of each controller, by the
/// controller's namespace, kind, and name. A revision's controller is
/// its owner reference with `controller: true`.
#[derive(Default)]
pub struct Revisions {
    newest: HashMap<(String, String, String), Timestamp>,
}

impl Revisions {
    /// `revisions` is the metadata of every `ReplicaSet` and
    /// `ControllerRevision`.
    pub fn build<'a>(revisions: impl IntoIterator<Item = &'a ObjectMeta>) -> Self {
        let mut newest: HashMap<(String, String, String), Timestamp> = HashMap::new();
        for meta in revisions {
            let (Some(namespace), Some(created)) = (&meta.namespace, &meta.creation_timestamp)
            else {
                continue;
            };
            let owners = meta.owner_references.iter().flatten();
            for owner in owners.filter(|owner| owner.controller == Some(true)) {
                let key = (namespace.clone(), owner.kind.clone(), owner.name.clone());
                let entry = newest.entry(key).or_insert(created.0);
                *entry = (*entry).max(created.0);
            }
        }
        Self { newest }
    }

    fn of(&self, meta: &ObjectMeta, kind: &str) -> Option<Timestamp> {
        self.newest(meta.namespace.as_deref()?, kind, meta.name.as_deref()?)
    }

    pub fn newest(&self, namespace: &str, kind: &str, name: &str) -> Option<Timestamp> {
        let key = (namespace.to_owned(), kind.to_owned(), name.to_owned());
        self.newest.get(&key).copied()
    }
}

impl<'a> Part<'a> {
    pub fn deployment(deployment: &'a Deployment, revisions: &Revisions) -> Self {
        let spec = &deployment.spec;
        let replicas = spec.replicas.unwrap_or(1);
        let ready = deployment.status.as_ref().and_then(|s| s.ready_replicas);
        Self {
            controller: Controller::Deployment,
            name: deployment.name_any(),
            replicas: Some(replicas),
            ready: Some((ready.unwrap_or(0), replicas)),
            template: &spec.template,
            strategy: Some(strategy(
                spec.strategy.as_ref().and_then(|s| s.type_.clone()),
            )),
            rolled: revisions.of(&deployment.metadata, "Deployment"),
        }
    }

    pub fn stateful_set(stateful_set: &'a StatefulSet, revisions: &Revisions) -> Self {
        let spec = &stateful_set.spec;
        let replicas = spec.replicas.unwrap_or(1);
        let ready = stateful_set.status.as_ref().and_then(|s| s.ready_replicas);
        let strategy_type = spec.update_strategy.as_ref().and_then(|s| s.type_.clone());
        Self {
            controller: Controller::StatefulSet,
            name: stateful_set.name_any(),
            replicas: Some(replicas),
            ready: Some((ready.unwrap_or(0), replicas)),
            template: &spec.template,
            strategy: Some(strategy(strategy_type)),
            rolled: revisions.of(&stateful_set.metadata, "StatefulSet"),
        }
    }

    pub fn daemon_set(daemon_set: &'a DaemonSet, revisions: &Revisions) -> Self {
        let spec = &daemon_set.spec;
        let ready = daemon_set
            .status
            .as_ref()
            .map(|s| (s.number_ready, s.desired_number_scheduled));
        let strategy_type = spec.update_strategy.as_ref().and_then(|s| s.type_.clone());
        Self {
            controller: Controller::DaemonSet,
            name: daemon_set.name_any(),
            replicas: None,
            ready: Some(ready.unwrap_or_default()),
            template: &spec.template,
            strategy: Some(strategy(strategy_type)),
            rolled: revisions.of(&daemon_set.metadata, "DaemonSet"),
        }
    }

    /// A `CronJob`, or `None` when its job template has no spec. The API
    /// server rejects such a `CronJob`, but the type allows it.
    pub fn cron_job(cron_job: &'a CronJob) -> Option<Self> {
        let template = &cron_job.spec.job_template.spec.as_ref()?.template;
        Some(Self {
            controller: Controller::CronJob,
            name: cron_job.name_any(),
            replicas: None,
            ready: None,
            template,
            strategy: None,
            rolled: None,
        })
    }
}

fn strategy(type_: Option<String>) -> String {
    type_.unwrap_or_else(|| DEFAULT_STRATEGY.into())
}

#[cfg(test)]
mod tests {
    use rstest::rstest;
    use serde::de::DeserializeOwned;

    use super::*;

    fn yaml<K: DeserializeOwned>(text: &str) -> K {
        serde_yaml_ng::from_str(text).unwrap()
    }

    fn revision(name: &str, kind: &str, owner: &str, created: &str) -> ObjectMeta {
        yaml(&format!(
            "{{namespace: shop, name: {name}, creationTimestamp: '{created}', ownerReferences: [{{apiVersion: apps/v1, kind: {kind}, name: {owner}, uid: x, controller: true}}]}}"
        ))
    }

    #[rstest]
    fn rolled_is_the_creation_of_the_newest_revision() {
        let metas = [
            revision("web-1", "Deployment", "web", "2026-01-01T00:00:00Z"),
            revision("web-2", "Deployment", "web", "2026-01-03T00:00:00Z"),
            revision("web-0", "Deployment", "web", "2025-12-01T00:00:00Z"),
            revision("cache-1", "StatefulSet", "web", "2026-01-05T00:00:00Z"),
        ];
        let revisions = Revisions::build(&metas);
        let deployment: Deployment =
            yaml("{metadata: {namespace: shop, name: web}, spec: {selector: {}, template: {}}}");
        let part = Part::deployment(&deployment, &revisions);
        assert_eq!(part.rolled, Some("2026-01-03T00:00:00Z".parse().unwrap()));
    }

    #[rstest]
    #[case::left_out("{selector: {}, template: {}}", Some(1), "RollingUpdate")]
    #[case::recreate(
        "{replicas: 3, selector: {}, template: {}, strategy: {type: Recreate}}",
        Some(3),
        "Recreate"
    )]
    fn reads_a_deployments_replicas_and_strategy(
        #[case] spec: &str,
        #[case] replicas: Option<i32>,
        #[case] strategy: &str,
    ) {
        let deployment: Deployment = yaml(&format!("{{metadata: {{name: web}}, spec: {spec}}}"));
        let part = Part::deployment(&deployment, &Revisions::default());
        assert_eq!(
            (part.replicas, part.strategy.as_deref()),
            (replicas, Some(strategy))
        );
    }

    #[rstest]
    fn a_daemon_set_has_no_replicas_but_counts_its_ready_pods() {
        let daemon_set: DaemonSet = yaml(
            "{metadata: {name: agent}, spec: {selector: {}, template: {}, updateStrategy: {type: OnDelete}}, status: {desiredNumberScheduled: 4, numberReady: 3, currentNumberScheduled: 4, numberMisscheduled: 0}}",
        );
        let part = Part::daemon_set(&daemon_set, &Revisions::default());
        assert_eq!(
            (part.replicas, part.ready, part.strategy.as_deref()),
            (None, Some((3, 4)), Some("OnDelete"))
        );
    }
}
