//! What the cluster's stores hold as of now, one reader per kind, each
//! a copy of the store's state that a page reads without holding a
//! lock.

use std::sync::Arc;

use k8s_openapi::api::apps::v1::{
    ControllerRevision, DaemonSet, Deployment, ReplicaSet, StatefulSet,
};
use k8s_openapi::api::batch::v1::{CronJob, Job};
use k8s_openapi::api::core::v1::{
    ConfigMap, Namespace, Node, PersistentVolume, PersistentVolumeClaim, Pod, Secret, Service,
    ServiceAccount,
};
use k8s_openapi::api::discovery::v1::EndpointSlice;
use k8s_openapi::api::networking::v1::{Ingress, IngressClass, NetworkPolicy};
use k8s_openapi::api::policy::v1::PodDisruptionBudget;
use k8s_openapi::api::resource::v1::ResourceClaim;
use k8s_openapi::api::scheduling::v1::PriorityClass;
use k8s_openapi::api::storage::v1::{CSIDriver, StorageClass, VolumeAttachment};
use kube::api::{ApiResource, PartialObjectMeta};

use super::{Cluster, Custom, RbacObjects};
use transect_graph::edges::{ClusterScoped, Plurals};

impl Cluster {
    pub fn domain(&self) -> &str {
        &self.domain
    }

    pub fn namespaces(&self) -> Vec<Arc<Namespace>> {
        self.namespaces.state()
    }

    pub fn nodes(&self) -> Vec<Arc<Node>> {
        let nodes = self.nodes.state().into_iter();
        nodes.map(|node| self.pretend.apply(node)).collect()
    }

    pub fn pods(&self) -> Vec<Arc<Pod>> {
        self.pods.state()
    }

    pub fn deployments(&self) -> Vec<Arc<Deployment>> {
        self.deployments.state()
    }

    pub fn stateful_sets(&self) -> Vec<Arc<StatefulSet>> {
        self.stateful_sets.state()
    }

    pub fn daemon_sets(&self) -> Vec<Arc<DaemonSet>> {
        self.daemon_sets.state()
    }

    pub fn cron_jobs(&self) -> Vec<Arc<CronJob>> {
        self.cron_jobs.state()
    }

    pub fn ingresses(&self) -> Vec<Arc<Ingress>> {
        self.ingresses.state()
    }

    pub fn services(&self) -> Vec<Arc<Service>> {
        self.services.state()
    }

    pub fn endpoint_slices(&self) -> Vec<Arc<EndpointSlice>> {
        self.endpoint_slices.state()
    }

    /// Every `VolumeAttachment`, when the cluster serves them.
    pub fn attachments(&self) -> Vec<Arc<VolumeAttachment>> {
        self.attachments.state()
    }

    pub fn claims(&self) -> Vec<Arc<PersistentVolumeClaim>> {
        self.claims.state()
    }

    pub fn volumes(&self) -> Vec<Arc<PersistentVolume>> {
        self.volumes.state()
    }

    /// The custom kinds watched whole, by group and kind; every other
    /// custom kind is trimmed to metadata, conditions, and printer
    /// columns.
    pub fn whole_kinds(&self) -> std::collections::HashSet<(String, String)> {
        self.custom.whole_kinds()
    }

    pub fn storage_classes(&self) -> Vec<Arc<StorageClass>> {
        self.storage_classes.state()
    }

    /// Every role and binding `transect` may list.
    pub fn rbac_objects(&self) -> RbacObjects {
        self.rbac.objects()
    }

    /// Every `ResourceClaim`, when the cluster serves them.
    pub fn resource_claims(&self) -> Vec<Arc<ResourceClaim>> {
        self.devices.claims()
    }

    pub fn service_accounts(&self) -> Vec<Arc<PartialObjectMeta<ServiceAccount>>> {
        self.service_accounts.state()
    }

    pub fn ingress_classes(&self) -> Vec<Arc<IngressClass>> {
        self.ingress_classes.state()
    }

    pub fn priority_classes(&self) -> Vec<Arc<PartialObjectMeta<PriorityClass>>> {
        self.priority_classes.state()
    }

    pub fn csi_drivers(&self) -> Vec<Arc<CSIDriver>> {
        self.csi_drivers.state()
    }

    /// How far the watch on each custom kind has come, by group and
    /// kind.
    pub fn custom_watched(&self) -> std::collections::HashMap<(String, String), super::Watch> {
        self.custom.watched_kinds()
    }

    /// Every `ResourceClaim` and `ResourceSlice`, or the state of discovery
    /// of the two kinds.
    pub fn device_objects(&self) -> super::devices::Objects {
        self.devices.objects()
    }

    /// Who RBAC lets write each custom kind's status, as of now.
    pub fn grants(&self) -> crate::ownership::Grants {
        self.rbac.grants()
    }

    pub fn budgets(&self) -> Vec<Arc<PodDisruptionBudget>> {
        self.budgets.state()
    }

    pub fn network_policies(&self) -> Vec<Arc<NetworkPolicy>> {
        self.network_policies.state()
    }

    /// Each custom kind watched, whole or trimmed, with its objects.
    pub fn custom_kinds(&self) -> Vec<(ApiResource, Vec<Arc<Custom>>)> {
        self.custom.snapshot()
    }

    /// What each custom kind's CRD declares about the kind.
    pub fn definitions(&self) -> transect_resources::knowledge::Definitions {
        let crds = self.definitions.state();
        transect_resources::knowledge::Definitions::read(crds.iter().map(AsRef::as_ref))
    }

    /// The package each custom kind's CRD names in its labels.
    pub fn packages(&self) -> crate::ownership::Packages {
        let crds = self.definitions.state();
        crate::ownership::Packages::read(crds.iter().map(AsRef::as_ref))
    }

    /// The printer columns each custom kind's CRD declares.
    pub fn columns(&self) -> transect_resources::knowledge::Columns {
        let crds = self.definitions.state();
        transect_resources::knowledge::Columns::read(crds.iter().map(AsRef::as_ref))
    }

    /// The plural of each kind discovery has resolved, by group and
    /// kind, which names the kind in a page's URL: every kind served at
    /// the start, and each custom owner kind found since.
    pub fn plurals(&self) -> Plurals {
        let mut plurals = self.plurals.plurals();
        plurals.extend(self.custom.plurals());
        plurals
    }

    /// The kinds discovery found that are not namespaced.
    pub fn cluster_scoped(&self) -> ClusterScoped {
        self.plurals.cluster_scoped()
    }

    /// Each kind discovery found at the start, at its group's preferred
    /// version.
    pub fn preferred(&self) -> super::plurals::Preferred {
        self.plurals.preferred()
    }

    pub fn replica_sets(&self) -> Vec<Arc<PartialObjectMeta<ReplicaSet>>> {
        self.replica_sets.state()
    }

    pub fn jobs(&self) -> Vec<Arc<PartialObjectMeta<Job>>> {
        self.jobs.state()
    }

    pub fn controller_revisions(&self) -> Vec<Arc<PartialObjectMeta<ControllerRevision>>> {
        self.controller_revisions.state()
    }

    pub fn config_maps(&self) -> Vec<Arc<PartialObjectMeta<ConfigMap>>> {
        self.config_maps.state()
    }

    /// Every `Secret`'s metadata, or none when `transect` does not read
    /// Secrets' annotations.
    pub fn secrets(&self) -> Option<Vec<Arc<PartialObjectMeta<Secret>>>> {
        self.secrets.state()
    }
}
