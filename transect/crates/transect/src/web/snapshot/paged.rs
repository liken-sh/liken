//! The kinds a snapshot holds only for their own pages:
//! ServiceAccounts, IngressClasses, PriorityClasses,
//! StorageClasses, roles and bindings, ResourceClaims, and the
//! volumes and their claims as the API gives them; which custom kinds
//! are watched whole; and the set of every object that has a page.

use std::collections::HashSet;
use std::sync::Arc;

use k8s_openapi::api::core::v1::{PersistentVolume, PersistentVolumeClaim, ServiceAccount};
use k8s_openapi::api::networking::v1::IngressClass;
use k8s_openapi::api::resource::v1::ResourceClaim;
use k8s_openapi::api::scheduling::v1::PriorityClass;
use k8s_openapi::api::storage::v1::StorageClass;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use kube::Resource;
use kube::api::PartialObjectMeta;

use super::Snapshot;
use transect_cluster::cluster::{Cluster, RbacObjects};
use transect_cluster::ownership::Packages;
use transect_graph::edges::Object;
use transect_resources::knowledge::Columns;

/// The stores read for the pages of their own objects, and what those
/// pages read of the custom kinds besides their objects.
#[derive(Default)]
pub struct Paged {
    pub service_accounts: Vec<Arc<PartialObjectMeta<ServiceAccount>>>,
    pub ingress_classes: Vec<Arc<IngressClass>>,
    pub priority_classes: Vec<Arc<PartialObjectMeta<PriorityClass>>>,
    pub storage_classes: Vec<Arc<StorageClass>>,
    pub rbac: RbacObjects,
    pub resource_claims: Vec<Arc<ResourceClaim>>,
    pub volumes: Vec<Arc<PersistentVolume>>,
    pub claims: Vec<Arc<PersistentVolumeClaim>>,
    /// The custom kinds watched whole; the rest are trimmed.
    pub whole: HashSet<(String, String)>,
    /// The printer columns each custom kind's CRD declares.
    pub columns: Columns,
    /// The package each custom kind's CRD names in its labels, which
    /// leads to its controller and so to a workload's Controls table.
    pub packages: Packages,
}

impl Paged {
    pub fn take(cluster: &Cluster) -> Self {
        Self {
            service_accounts: cluster.service_accounts(),
            ingress_classes: cluster.ingress_classes(),
            priority_classes: cluster.priority_classes(),
            storage_classes: cluster.storage_classes(),
            rbac: cluster.rbac_objects(),
            resource_claims: cluster.resource_claims(),
            volumes: cluster.volumes(),
            claims: cluster.claims(),
            whole: cluster.whole_kinds(),
            columns: cluster.columns(),
            packages: cluster.packages(),
        }
    }

    /// Every object here, with its metadata.
    pub fn objects(&self) -> Vec<(Object, &ObjectMeta)> {
        let rbac = &self.rbac;
        [
            of(&self.service_accounts),
            of(&self.ingress_classes),
            of(&self.priority_classes),
            of(&self.storage_classes),
            of(&rbac.cluster_roles),
            of(&rbac.roles),
            of(&rbac.cluster_role_bindings),
            of(&rbac.role_bindings),
            of(&self.resource_claims),
            of(&self.volumes),
            of(&self.claims),
        ]
        .into_iter()
        .flatten()
        .collect()
    }
}

/// Each of `objects` as an `Object`, with its metadata.
pub fn of<K: Resource<DynamicType = ()>>(objects: &[Arc<K>]) -> Vec<(Object, &ObjectMeta)> {
    let (group, kind) = (K::group(&()), K::kind(&()));
    objects
        .iter()
        .filter_map(|object| {
            let meta = object.meta();
            let name = meta.name.as_deref()?;
            let object = Object::new(&group, &kind, meta.namespace.as_deref(), name);
            Some((object, meta))
        })
        .collect()
}

impl Snapshot {
    /// Every object that has a page: what intent can be written on,
    /// except the Secrets, whose page is built from what names them,
    /// and every object of the kinds held for their pages.
    pub fn held_objects(&self) -> HashSet<Object> {
        let described = self.described().into_iter();
        let paged = self.paged.objects().into_iter();
        described
            .chain(paged)
            .map(|(object, _)| object)
            .filter(|object| !(object.group.is_empty() && object.kind == "Secret"))
            .collect()
    }
}
