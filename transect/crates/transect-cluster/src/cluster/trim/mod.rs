//! What each store drops from the objects it holds.  Every field kept
//! is one that some part of `transect` reads, and the rest would be
//! most of the memory a store of every pod takes.  Code that starts to
//! read another field has to keep it here first, or the field is unset
//! in the store.

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

/// The annotation `kubectl apply` writes with the whole manifest it applied.
const LAST_APPLIED: &str = "kubectl.kubernetes.io/last-applied-configuration";

/// Managed fields and kubectl's copy of the last applied manifest are
/// often the largest parts of an object, and nothing on the map reads
/// them.
pub fn meta(meta: &mut ObjectMeta) {
    meta.managed_fields = None;
    remove_last_applied(meta);
}

/// Keeps one entry, with only its manager's name, for each manager of
/// the `status` subresource, which controller attribution reads.
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

/// A pod without the parts of its spec and status nothing reads: how
/// its containers run as a user, how it resolves names, and what the
/// kubelet allocated and resized, to the pod and to each container.
/// Its finalizers, its generations, its grace period, and its
/// machine's address are dropped too, because `Job` tracking and the
/// kubelet rewrite them as a pod starts and stops, and an event that
/// changes only those would otherwise rebuild the model.
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

/// A custom resource of a kind `transect` reads whole: all of it but its
/// managed fields, beyond the managers of its status, and its
/// conditions' heartbeat times.
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

/// A CRD with its name, group, and kind, and each version's top
/// description and printer columns, which is all a page reads.  The
/// schemas are most of a CRD's size, and a cluster runs a hundred or
/// more CRDs.
pub fn definition(crd: &mut CustomResourceDefinition) {
    meta(&mut crd.metadata);
    // Only the labels and annotations that name the package that
    // installed it, which controller attribution matches to a workload.
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

/// The fields of a condition a trimmed object keeps.
const CONDITION_FIELDS: [&str; 5] = ["type", "status", "reason", "message", "lastTransitionTime"];

/// A custom resource of a kind `transect` has no reason to read whole,
/// cut to its metadata, its conditions, the generation its status was
/// written for, and the value of each of `columns`, its CRD's printer
/// columns, read before the cut, and each of `paths`, the paths its
/// kind knowledge reads.  That is what its `Ready` dot, its
/// controller, and its pages read.  The rest of its spec may hold a
/// credential, and no page can detect one, so the rest never reaches
/// the store.  It is also most of the memory a store of every custom
/// resource would take.
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

/// One condition with only the fields a page reads.
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
    // An ephemeral container is read only for the Secrets its
    // environment names, which the access lens follows from exec.
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

/// What of a container's security context the access lens draws: that
/// it is privileged, and the capabilities it adds.  Every other field,
/// and a context that sets neither, is dropped.
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
