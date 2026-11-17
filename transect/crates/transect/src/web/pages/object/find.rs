//! Finding one object in the store that holds it, read the way the
//! table of Kubernetes's own kinds in `objects::kinds` gives for its
//! kind.

use std::sync::Arc;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use kube::Resource;
use serde::Serialize;
use serde_json::Value;

use super::super::super::snapshot::Snapshot;
use super::Found;
use transect_graph::edges::Object;
use transect_map::objects::kinds::{ReadFrom, built_in};

/// `object`, from the store that holds it, or none for a kind no store
/// holds.  A `ConfigMap` and the kinds held as metadata give their
/// metadata alone, so no page can show what a `ConfigMap` holds.
pub fn found(snapshot: &Snapshot, object: &Object) -> Option<Found> {
    let read = built_in(&object.group, &object.kind).and_then(|served| served.read);
    match read {
        None => custom(snapshot, object),
        Some(ReadFrom::Status) => with_status(snapshot, object)?,
        Some(ReadFrom::Metadata) => with_metadata(snapshot, object)?,
        Some(ReadFrom::OwnPage | ReadFrom::Get | ReadFrom::Never) => None,
    }
}

/// `object`, with its status, from the store of its kind, or none for
/// a kind with no such store.
fn with_status(snapshot: &Snapshot, object: &Object) -> Option<Option<Found>> {
    let paged = &snapshot.paged;
    let rbac = &paged.rbac;
    Some(match (object.group.as_str(), object.kind.as_str()) {
        ("", "Service") => status(&snapshot.services, object),
        ("", "PersistentVolume") => status(&paged.volumes, object),
        ("", "PersistentVolumeClaim") => status(&paged.claims, object),
        ("apps", "Deployment") => status(&snapshot.deployments, object),
        ("apps", "StatefulSet") => status(&snapshot.stateful_sets, object),
        ("apps", "DaemonSet") => status(&snapshot.daemon_sets, object),
        ("batch", "CronJob") => status(&snapshot.cron_jobs, object),
        ("networking.k8s.io", "Ingress") => status(&snapshot.ingresses, object),
        ("networking.k8s.io", "NetworkPolicy") => status(&snapshot.network_policies, object),
        ("policy", "PodDisruptionBudget") => status(&snapshot.budgets, object),
        ("storage.k8s.io", "CSIDriver") => status(&snapshot.csi_drivers, object),
        ("storage.k8s.io", "StorageClass") => status(&paged.storage_classes, object),
        ("networking.k8s.io", "IngressClass") => status(&paged.ingress_classes, object),
        ("rbac.authorization.k8s.io", "Role") => status(&rbac.roles, object),
        ("rbac.authorization.k8s.io", "ClusterRole") => status(&rbac.cluster_roles, object),
        ("rbac.authorization.k8s.io", "RoleBinding") => status(&rbac.role_bindings, object),
        ("rbac.authorization.k8s.io", "ClusterRoleBinding") => {
            status(&rbac.cluster_role_bindings, object)
        }
        ("resource.k8s.io", "ResourceClaim") => status(&paged.resource_claims, object),
        _ => return None,
    })
}

/// `object`, by its metadata alone, from the store of its kind, or
/// none for a kind with no such store.
fn with_metadata(snapshot: &Snapshot, object: &Object) -> Option<Option<Found>> {
    let paged = &snapshot.paged;
    Some(match (object.group.as_str(), object.kind.as_str()) {
        ("", "ConfigMap") => metadata(&snapshot.config_maps, object),
        ("", "ServiceAccount") => metadata(&paged.service_accounts, object),
        ("apps", "ReplicaSet") => metadata(&snapshot.replica_sets, object),
        ("batch", "Job") => metadata(&snapshot.jobs, object),
        ("scheduling.k8s.io", "PriorityClass") => metadata(&paged.priority_classes, object),
        _ => return None,
    })
}

/// The custom resource `object`, as its watch holds it.
fn custom(snapshot: &Snapshot, object: &Object) -> Option<Found> {
    let (_, objects) = snapshot.custom_kind(&object.group, &object.kind)?;
    let found = objects
        .iter()
        .find(|found| named(&found.metadata, object))?;
    Some(Found::of(found))
}

fn named(meta: &ObjectMeta, object: &Object) -> bool {
    meta.name.as_deref() == Some(&object.name) && meta.namespace == object.namespace
}

/// The object of `objects` `object` names, with its status alone of its
/// fields, which is where its conditions are.
fn status<K: Resource + Serialize>(objects: &[Arc<K>], object: &Object) -> Option<Found> {
    let found = objects.iter().find(|found| named(found.meta(), object))?;
    let whole = serde_json::to_value(found.as_ref()).unwrap_or_default();
    let data = match whole.get("status") {
        Some(status) => serde_json::json!({ "status": status }),
        None => Value::Null,
    };
    Some(Found {
        meta: found.meta().clone(),
        data,
        api_version: None,
    })
}

/// The object of `objects` `object` names, by its metadata alone.
fn metadata<K: Resource>(objects: &[Arc<K>], object: &Object) -> Option<Found> {
    let found = objects.iter().find(|found| named(found.meta(), object))?;
    Some(Found {
        meta: found.meta().clone(),
        data: Value::Null,
        api_version: None,
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::super::super::fixture;
    use super::*;

    #[rstest]
    #[case::a_workload("apps", "StatefulSet", Some("acme-ledger"), "ledger-db", true)]
    #[case::a_service("", "Service", Some("acme-shop"), "storefront-svc", true)]
    #[case::a_custom_resource("gizmos.example.test", "Gizmo", Some("acme-shop"), "gizmo-red", true)]
    #[case::nothing_by_that_name("apps", "StatefulSet", Some("acme-ledger"), "gone", false)]
    fn finds_an_object_in_the_store_that_holds_it(
        #[case] group: &str,
        #[case] kind: &str,
        #[case] namespace: Option<&str>,
        #[case] name: &str,
        #[case] expected: bool,
    ) {
        let object = Object::new(group, kind, namespace, name);
        assert_eq!(found(&fixture::snapshot(), &object).is_some(), expected);
    }

    #[rstest]
    fn the_table_and_the_stores_agree() {
        let snapshot = fixture::snapshot();
        let unread: Vec<String> = transect_map::objects::kinds::every_built_in()
            .filter_map(|served| {
                let object = Object::new(&served.resource.group, &served.resource.kind, None, "");
                let store = match served.read? {
                    ReadFrom::Status => with_status(&snapshot, &object),
                    ReadFrom::Metadata => with_metadata(&snapshot, &object),
                    ReadFrom::OwnPage | ReadFrom::Get | ReadFrom::Never => Some(None),
                };
                store.is_none().then_some(served.resource.kind)
            })
            .collect();
        assert_eq!(unread, Vec::<String>::new());
    }
}
