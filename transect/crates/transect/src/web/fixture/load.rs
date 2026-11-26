//! Sorts the fixture's YAML documents into the stores of a snapshot,
//! the way the cluster's watches fill those stores.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use k8s_openapi::api::resource::v1::ResourceSlice;
use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use kube::api::{PartialObjectMeta, PartialObjectMetaExt};
use kube::core::{ApiResource, DynamicObject};
use serde::Deserialize;
use serde::de::DeserializeOwned;

use super::super::snapshot::Snapshot;
use transect_cluster::cluster::RbacObjects;
use transect_cluster::ownership::Packages;
use transect_resources::knowledge::{Column, Columns, Definitions};

/// Sorts every document in `yaml` into the stores of `snapshot`, and
/// returns the `ResourceSlice` objects. Any kind not listed here is
/// custom, and it panics unless its CRD comes earlier in `yaml`.
///
/// A custom kind's objects are kept whole when other objects have owner
/// references to the kind, when a kind or device reference matches the
/// kind's objects by a field value, or when the kind has knowledge. A
/// kind whose knowledge has `gitops` or `findings` is kept whole only
/// when `reads_fields` is true. `trim_custom` trims the objects of every
/// other custom kind, keeping the paths of `kept_paths`, as the watch
/// does.
pub fn sort(snapshot: &mut Snapshot, yaml: &str) -> Vec<ResourceSlice> {
    let documents: Vec<DynamicObject> = serde_yaml_ng::Deserializer::from_str(yaml)
        .map(|document| DynamicObject::deserialize(document).unwrap())
        .collect();
    let kinds = snapshot.resources.kinds.iter();
    let known: HashSet<String> = kinds
        .filter(|knowledge| {
            let kept = knowledge.gitops.is_some() || knowledge.findings.is_some();
            !kept || knowledge.reads_fields()
        })
        .map(|knowledge| knowledge.name.clone())
        .collect();
    let kinds = snapshot.resources.kinds.iter();
    let kept: HashMap<String, Vec<String>> = kinds
        .map(|knowledge| (knowledge.name.clone(), knowledge.kept_paths()))
        .filter(|(_, paths)| !paths.is_empty())
        .collect();
    let matched: HashSet<(String, String)> = snapshot
        .resources
        .kinds
        .iter()
        .flat_map(|knowledge| knowledge.matched())
        .chain(transect_ground::devices::device_matched(
            &snapshot.resources.devices,
        ))
        .map(|(group, kind, _)| (group, kind))
        .collect();
    let mut owners: HashSet<(String, String)> = HashSet::new();
    for document in &documents {
        for owner in document.metadata.owner_references.iter().flatten() {
            let group = owner.api_version.split_once('/').map(|(group, _)| group);
            owners.insert((group.unwrap_or_default().into(), owner.kind.clone()));
        }
    }
    let mut crds: Vec<CustomResourceDefinition> = Vec::new();
    let mut slices: Vec<ResourceSlice> = Vec::new();
    let mut rbac = RbacObjects::default();
    for document in documents {
        let types = document.types.clone().unwrap_or_default();
        let paged = &mut snapshot.paged;
        match (types.api_version.as_str(), types.kind.as_str()) {
            ("v1", "Namespace") => snapshot.namespaces.push(typed(document)),
            ("v1", "Node") => snapshot.nodes.push(typed(document)),
            ("v1", "Pod") => snapshot.pods.push(typed(document)),
            ("v1", "Service") => snapshot.services.push(typed(document)),
            ("discovery.k8s.io/v1", "EndpointSlice") => {
                snapshot.endpoint_slices.push(typed(document))
            }
            ("v1", "PersistentVolumeClaim") => paged.claims.push(typed(document)),
            ("v1", "PersistentVolume") => paged.volumes.push(typed(document)),
            ("v1", "ServiceAccount") => paged.service_accounts.push(meta(document)),
            ("networking.k8s.io/v1", "IngressClass") => paged.ingress_classes.push(typed(document)),
            ("apps/v1", "Deployment") => snapshot.deployments.push(typed(document)),
            ("apps/v1", "StatefulSet") => snapshot.stateful_sets.push(typed(document)),
            ("apps/v1", "DaemonSet") => snapshot.daemon_sets.push(typed(document)),
            ("batch/v1", "CronJob") => snapshot.cron_jobs.push(typed(document)),
            ("apps/v1", "ReplicaSet") => snapshot.replica_sets.push(meta(document)),
            ("batch/v1", "Job") => snapshot.jobs.push(meta(document)),
            ("networking.k8s.io/v1", "Ingress") => snapshot.ingresses.push(typed(document)),
            ("networking.k8s.io/v1", "NetworkPolicy") => {
                snapshot.network_policies.push(typed(document))
            }
            ("resource.k8s.io/v1", "ResourceClaim") => paged.resource_claims.push(typed(document)),
            ("resource.k8s.io/v1", "ResourceSlice") => {
                slices.push(Arc::unwrap_or_clone(typed(document)))
            }
            ("storage.k8s.io/v1", "StorageClass") => paged.storage_classes.push(typed(document)),
            ("storage.k8s.io/v1", "CSIDriver") => snapshot.csi_drivers.push(typed(document)),
            ("rbac.authorization.k8s.io/v1", "ClusterRole") => {
                rbac.cluster_roles.push(typed(document))
            }
            ("rbac.authorization.k8s.io/v1", "ClusterRoleBinding") => {
                rbac.cluster_role_bindings.push(typed(document))
            }
            ("rbac.authorization.k8s.io/v1", "Role") => rbac.roles.push(typed(document)),
            ("rbac.authorization.k8s.io/v1", "RoleBinding") => {
                rbac.role_bindings.push(typed(document))
            }
            ("apiextensions.k8s.io/v1", "CustomResourceDefinition") => {
                crds.push(Arc::unwrap_or_clone(typed(document)))
            }
            (_, kind) => {
                let crd = crds.iter().find(|crd| {
                    crd.spec.names.kind == kind
                        && types
                            .api_version
                            .starts_with(&format!("{}/", crd.spec.group))
                });
                let crd = crd.unwrap_or_else(|| {
                    panic!("the fixture has no CRD for {kind} before its first {kind} object")
                });
                let resource = resource(crd);
                let key = (resource.group.clone(), resource.kind.clone());
                let name = format!("{}.{}", resource.plural, resource.group);
                let whole =
                    owners.contains(&key) || known.contains(&name) || matched.contains(&key);
                let mut object = document;
                if whole {
                    snapshot.paged.whole.insert(key);
                } else {
                    let columns = Column::declared(&crd.spec.versions[0]);
                    let fields = kept.get(&name).map(Vec::as_slice).unwrap_or_default();
                    transect_cluster::cluster::trim_custom(&mut object, &columns, fields);
                }
                custom(snapshot, resource, object);
            }
        }
    }
    snapshot.grants =
        transect_cluster::ownership::Grants::build(transect_cluster::ownership::Rbac {
            cluster_roles: rbac.cluster_roles.iter().map(AsRef::as_ref).collect(),
            roles: rbac.roles.iter().map(AsRef::as_ref).collect(),
            cluster_role_bindings: rbac
                .cluster_role_bindings
                .iter()
                .map(AsRef::as_ref)
                .collect(),
            role_bindings: rbac.role_bindings.iter().map(AsRef::as_ref).collect(),
            missing: Vec::new(),
        });
    snapshot.paged.rbac = rbac;
    snapshot.paged.columns = Columns::read(&crds);
    snapshot.paged.packages = Packages::read(&crds);
    snapshot.definitions = Definitions::read(&crds);
    // API discovery returns the built-in kinds as well as the CRDs'
    // kinds, so the plural of each built-in kind is added too.
    for served in transect_map::objects::kinds::every_built_in() {
        let resource = served.resource;
        let key = (resource.group.clone(), resource.kind.clone());
        snapshot.plurals.insert(key, resource.plural);
    }
    for crd in &crds {
        let resource = resource(crd);
        let key = (resource.group.clone(), resource.kind.clone());
        if crd.spec.scope == "Cluster" {
            snapshot.cluster_scoped.insert(key.clone());
        }
        snapshot.plurals.insert(key, resource.plural);
    }
    slices
}

fn typed<K: DeserializeOwned>(document: DynamicObject) -> Arc<K> {
    Arc::new(serde_json::from_value(serde_json::to_value(document).unwrap()).unwrap())
}

/// `document` reduced to its metadata, as a metadata-only watch keeps
/// it.
fn meta<K>(document: DynamicObject) -> Arc<PartialObjectMeta<K>> {
    let meta: ObjectMeta = document.metadata;
    Arc::new(meta.into_response_partial())
}

/// The resource that `crd` serves, at its first listed version.
fn resource(crd: &CustomResourceDefinition) -> ApiResource {
    let (group, version) = (&crd.spec.group, &crd.spec.versions[0].name);
    ApiResource {
        group: group.clone(),
        version: version.clone(),
        api_version: format!("{group}/{version}"),
        kind: crd.spec.names.kind.clone(),
        plural: crd.spec.names.plural.clone(),
    }
}

fn custom(snapshot: &mut Snapshot, resource: ApiResource, object: DynamicObject) {
    let object = Arc::new(object);
    match snapshot
        .custom
        .iter_mut()
        .find(|(held, _)| *held == resource)
    {
        Some((_, objects)) => objects.push(object),
        None => snapshot.custom.push((resource, vec![object])),
    }
}
