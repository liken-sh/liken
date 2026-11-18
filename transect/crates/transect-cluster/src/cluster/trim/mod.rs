//! Removes the fields that `transect` does not read from each object
//! before a store keeps it, because those fields would be most of the
//! memory that a store of every pod takes. Code that starts to read
//! another field must keep it here first, or the field is unset in the
//! store.

use k8s_openapi::api::apps::v1::{DaemonSet, Deployment, StatefulSet};
use k8s_openapi::api::batch::v1::CronJob;
use k8s_openapi::api::core::v1::{
    Capabilities, Container, EphemeralContainer, Node, PersistentVolumeClaim, Pod, PodSpec,
    PodTemplateSpec, SecurityContext,
};
use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::{
    CustomResourceDefinition, JSONSchemaProps,
};
use k8s_openapi::apimachinery::pkg::apis::meta::v1::{ManagedFieldsEntry, ObjectMeta};
use kube::core::DynamicObject;
use serde_json::{Map, Value};

use transect_resources::knowledge::{Column, KEPT};

mod conditions;
mod fields;

/// The annotation where `kubectl apply` stores the whole manifest that it applied.
const LAST_APPLIED: &str = "kubectl.kubernetes.io/last-applied-configuration";

/// Drops the managed fields and the last-applied annotation, which are
/// often the largest parts of an object.
pub fn meta(meta: &mut ObjectMeta) {
    meta.managed_fields = None;
    remove_last_applied(meta);
}

/// Like `meta`, but keeps the manager name of each managed fields entry
/// for the `status` subresource, because controller attribution reads
/// which managers write the status.
pub fn all_but_status_managers(meta: &mut ObjectMeta) {
    let managers: Vec<ManagedFieldsEntry> = meta
        .managed_fields
        .take()
        .into_iter()
        .flatten()
        .filter(|entry| entry.subresource.as_deref() == Some("status"))
        .map(|entry| ManagedFieldsEntry {
            manager: entry.manager,
            subresource: entry.subresource,
            ..ManagedFieldsEntry::default()
        })
        .collect();
    meta.managed_fields = (!managers.is_empty()).then_some(managers);
    remove_last_applied(meta);
}

fn remove_last_applied(meta: &mut ObjectMeta) {
    if let Some(annotations) = &mut meta.annotations {
        annotations.remove(LAST_APPLIED);
    }
}

/// Drops the parts of a pod that nothing reads. These include the
/// finalizers, the generations, the deletion grace period, and the host
/// address, which `Job` tracking and the kubelet rewrite as a pod starts
/// and stops. If the store kept them, each of those writes would
/// rebuild the model.
pub fn pod(pod: &mut Pod) {
    meta(&mut pod.metadata);
    pod.metadata.finalizers = None;
    pod.metadata.generation = None;
    pod.metadata.deletion_grace_period_seconds = None;
    conditions::pod(pod);
    if let Some(spec) = &mut pod.spec {
        pod_spec(spec);
    }
    if let Some(status) = &mut pod.status {
        status.allocated_resources = None;
        status.resources = None;
        status.observed_generation = None;
        status.host_ip = None;
        status.host_ips = None;
    }
    let statuses = pod.status.iter_mut().flat_map(|status| {
        let app = status.container_statuses.iter_mut().flatten();
        let init = status.init_container_statuses.iter_mut().flatten();
        app.chain(init)
    });
    for status in statuses {
        status.allocated_resources = None;
        status.allocated_resources_status = None;
        status.container_id = None;
        status.resources = None;
        status.stop_signal = None;
        status.user = None;
        status.volume_mounts = None;
    }
}

pub fn deployment(deployment: &mut Deployment) {
    meta(&mut deployment.metadata);
    conditions::deployment(deployment);
    template(&mut deployment.spec.template);
}

pub fn node(node: &mut Node) {
    meta(&mut node.metadata);
    conditions::node(node);
}

pub fn claim(claim: &mut PersistentVolumeClaim) {
    meta(&mut claim.metadata);
    conditions::claim(claim);
}

/// Trims a custom resource of a kind that `transect` reads whole, as
/// `all_but_status_managers` trims its metadata. It also drops the
/// heartbeat times of the conditions.
pub fn whole_custom(object: &mut DynamicObject) {
    all_but_status_managers(&mut object.metadata);
    conditions::custom(&mut object.data);
}

pub fn stateful_set(stateful_set: &mut StatefulSet) {
    meta(&mut stateful_set.metadata);
    template(&mut stateful_set.spec.template);
}

pub fn daemon_set(daemon_set: &mut DaemonSet) {
    meta(&mut daemon_set.metadata);
    template(&mut daemon_set.spec.template);
}

pub fn cron_job(cron_job: &mut CronJob) {
    meta(&mut cron_job.metadata);
    if let Some(job) = &mut cron_job.spec.job_template.spec {
        template(&mut job.template);
    }
}

/// Trims a CRD to what a page reads: its names, and each version's
/// top-level description and printer columns. The schemas are most of a
/// CRD's size.
pub fn definition(crd: &mut CustomResourceDefinition) {
    meta(&mut crd.metadata);
    // Controller attribution matches a CRD to a workload by the labels
    // and annotations that name the package that installed the CRD.
    let package = |map: &mut Option<std::collections::BTreeMap<String, String>>| {
        if let Some(map) = map {
            map.retain(|key, _| crate::ownership::package_key(key));
        }
    };
    package(&mut crd.metadata.annotations);
    package(&mut crd.metadata.labels);
    crd.spec.conversion = None;
    crd.status = None;
    for version in &mut crd.spec.versions {
        version.selectable_fields = None;
        version.subresources = None;
        if let Some(schema) = &mut version.schema {
            let top = schema.open_api_v3_schema.take();
            schema.open_api_v3_schema = top.map(|top| JSONSchemaProps {
                description: top.description,
                ..JSONSchemaProps::default()
            });
        }
    }
}

/// The fields of a condition that a trimmed object keeps.
const CONDITION_FIELDS: [&str; 5] = ["type", "status", "reason", "message", "lastTransitionTime"];

/// Trims a custom resource of a kind that `transect` does not read
/// whole. It keeps the metadata, the conditions,
/// `status.observedGeneration`, each of `paths`, which kind knowledge
/// reads, and the value of each printer column in `columns`, under
/// `KEPT`. The rest of the object can hold a credential that `transect`
/// cannot detect, so it never reaches the store.
pub fn conditions_and_columns(object: &mut DynamicObject, columns: &[Column], paths: &[String]) {
    all_but_status_managers(&mut object.metadata);
    let values =
        transect_resources::knowledge::columns::kept(columns, &object.metadata, &object.data);
    let status = object.data.get("status");
    let conditions = status
        .and_then(|status| status.get("conditions"))
        .and_then(Value::as_array)
        .map(|conditions| conditions.iter().map(condition).collect::<Vec<_>>());
    let observed = status.and_then(|status| status.get("observedGeneration").cloned());
    let mut kept = Map::new();
    if let Some(conditions) = conditions {
        kept.insert("conditions".into(), Value::Array(conditions));
    }
    if let Some(observed) = observed {
        kept.insert("observedGeneration".into(), observed);
    }
    let mut data = Map::new();
    if !kept.is_empty() {
        data.insert("status".into(), Value::Object(kept));
    }
    if !values.is_empty() {
        data.insert(KEPT.into(), Value::Object(values));
    }
    fields::copy(&object.data, &mut data, paths);
    object.data = match data.is_empty() {
        true => Value::Null,
        false => Value::Object(data),
    };
}

/// A condition with only the fields in `CONDITION_FIELDS`.
fn condition(condition: &Value) -> Value {
    let kept = CONDITION_FIELDS.iter().filter_map(|field| {
        let value = condition.get(*field)?;
        Some(((*field).to_string(), value.clone()))
    });
    Value::Object(kept.collect())
}

fn template(template: &mut PodTemplateSpec) {
    if let Some(spec) = &mut template.spec {
        pod_spec(spec);
    }
}

fn pod_spec(spec: &mut PodSpec) {
    spec.dns_config = None;
    spec.enable_service_links = None;
    // The access lens lists the `Secret`s that each container reads
    // through its environment, and that includes ephemeral containers.
    spec.ephemeral_containers = spec
        .ephemeral_containers
        .take()
        .map(|containers| {
            let containers = containers.into_iter();
            containers.map(|one| EphemeralContainer {
                name: one.name,
                env: one.env,
                env_from: one.env_from,
                ..EphemeralContainer::default()
            })
        })
        .map(Iterator::collect);
    spec.host_aliases = None;
    spec.preemption_policy = None;
    spec.readiness_gates = None;
    spec.scheduler_name = None;
    spec.security_context = None;
    spec.termination_grace_period_seconds = None;
    let containers = spec.containers.iter_mut();
    for one in containers.chain(spec.init_containers.iter_mut().flatten()) {
        container(one);
    }
}

fn container(container: &mut Container) {
    container.image_pull_policy = None;
    container.resize_policy = None;
    container.security_context = container.security_context.take().and_then(host_access);
    container.stdin = None;
    container.stdin_once = None;
    container.termination_message_path = None;
    container.termination_message_policy = None;
    container.tty = None;
    container.working_dir = None;
}

/// Keeps only whether a container is privileged and which capabilities
/// it adds, which are what the access lens draws. It returns `None` when
/// the context sets neither.
fn host_access(context: SecurityContext) -> Option<SecurityContext> {
    let privileged = context.privileged.filter(|privileged| *privileged);
    let added = context
        .capabilities
        .and_then(|capabilities| capabilities.add);
    let added = added.filter(|added| !added.is_empty());
    if privileged.is_none() && added.is_none() {
        return None;
    }
    Some(SecurityContext {
        privileged,
        capabilities: added.map(|add| Capabilities {
            add: Some(add),
            drop: None,
        }),
        ..SecurityContext::default()
    })
}

#[cfg(test)]
mod tests;
