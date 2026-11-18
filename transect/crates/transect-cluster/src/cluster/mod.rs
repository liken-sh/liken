//! The cluster's objects, in memory stores that watches on the
//! Kubernetes API keep current. Each page request reads the stores, so
//! it shows the cluster as it is at that moment.
//!
//! A watch event that changes a store also sends on a
//! `tokio::sync::watch` channel of `()`, which records only that
//! something changed since a receiver last looked. So a burst of events
//! wakes a reader once, and a slow reader cannot lag or lose events as
//! it can on a `broadcast` channel. A reader that needs the details
//! reads the stores.

mod attachments;
mod change;
mod custom;
mod definitions;
pub mod devices;
pub mod discovery;
#[cfg(test)]
mod grants;
mod owners;
mod plurals;
mod pretend;
mod rbac;
mod read;
mod reflect;
mod secrets;
mod trim;

use std::sync::Arc;

use k8s_openapi::api::apps::v1::{
    ControllerRevision, DaemonSet, Deployment, ReplicaSet, StatefulSet,
};
use k8s_openapi::api::batch::v1::{CronJob, Job};
use k8s_openapi::api::core::v1::{
    ConfigMap, Namespace, Node, PersistentVolume, PersistentVolumeClaim, Pod, Service,
    ServiceAccount,
};
use k8s_openapi::api::discovery::v1::EndpointSlice;
use k8s_openapi::api::networking::v1::IngressClass;
use k8s_openapi::api::networking::v1::{Ingress, NetworkPolicy};
use k8s_openapi::api::policy::v1::PodDisruptionBudget;
use k8s_openapi::api::scheduling::v1::PriorityClass;
use k8s_openapi::api::storage::v1::{CSIDriver, StorageClass};
use kube::api::PartialObjectMeta;
use kube::runtime::reflector::Store;
use kube::{Api, Client};
use tokio::sync::watch;

use attachments::AttachmentStore;
use change::Ignored;
use custom::CustomOwners;
pub use custom::{Custom, Watch};
use definitions::DefinitionStore;
use devices::DeviceStores;
use plurals::PluralStore;
pub use pretend::Pretend;
pub use rbac::RbacObjects;
use rbac::RbacStores;
use reflect::{Changes, reflect, reflect_cut};
use secrets::SecretStore;
use transect_resources::knowledge::KindKnowledge;
use transect_resources::resources::Field;
#[cfg(any(test, debug_assertions))]
pub use trim::conditions_and_columns as trim_custom;
pub use trim::meta as trim_meta;

#[derive(Clone)]
pub struct Cluster {
    namespaces: Store<Namespace>,
    nodes: Store<Node>,
    pods: Store<Pod>,
    deployments: Store<Deployment>,
    stateful_sets: Store<StatefulSet>,
    daemon_sets: Store<DaemonSet>,
    cron_jobs: Store<CronJob>,
    ingresses: Store<Ingress>,
    /// A `Service`'s selector gives the edges to the workloads it selects,
    /// and the workload page shows each `Service` that selects the
    /// workload.
    services: Store<Service>,
    /// The workload page counts each `Service`'s ready endpoints from
    /// these.
    endpoint_slices: Store<EndpointSlice>,
    /// Claims, the volumes bound to them, and their storage classes.
    /// The storage walk follows them to each backend.
    claims: Store<PersistentVolumeClaim>,
    volumes: Store<PersistentVolume>,
    storage_classes: Store<StorageClass>,
    /// The CSI drivers. A driver's `data-on` annotation says that its
    /// volumes are stored on the disks of the machines that mount them.
    csi_drivers: Store<CSIDriver>,
    /// The machines that CSI drivers attached volumes to, when the
    /// cluster serves VolumeAttachments.
    attachments: AttachmentStore,
    /// The workload page lists the budgets that select a workload's pods
    /// among its guards.
    budgets: Store<PodDisruptionBudget>,
    /// The allowed-to-call edges are computed from these.
    network_policies: Store<NetworkPolicy>,
    /// ReplicaSets and Jobs are watched as metadata, because `transect`
    /// reads only their owner references and creation times. A
    /// `Deployment` keeps ten old ReplicaSets by default, so after pods
    /// they are the most numerous kind here.
    replica_sets: Store<PartialObjectMeta<ReplicaSet>>,
    jobs: Store<PartialObjectMeta<Job>>,
    /// The revisions that StatefulSets and DaemonSets keep of their pod
    /// templates, as metadata. The newest revision's creation time is
    /// when the workload last rolled out, as a `ReplicaSet`'s is for a
    /// `Deployment`.
    controller_revisions: Store<PartialObjectMeta<ControllerRevision>>,
    /// ConfigMaps, as metadata, for their intent annotations.
    config_maps: Store<PartialObjectMeta<ConfigMap>>,
    /// ServiceAccounts, IngressClasses, and PriorityClasses. A name that
    /// refers to one links to its page only when the object exists.
    /// `synced` does not wait for these stores, so until their first
    /// list arrives, such a name is plain text.
    service_accounts: Store<PartialObjectMeta<ServiceAccount>>,
    ingress_classes: Store<IngressClass>,
    priority_classes: Store<PartialObjectMeta<PriorityClass>>,
    /// Secrets, as metadata, only with `--secret-annotations`.
    secrets: SecretStore,
    /// Custom kinds, each stored whole or trimmed. The `custom` module
    /// comment says which.
    custom: CustomOwners,
    /// The CRDs, each trimmed to the fields that describe its kind, when
    /// `transect` may list them.
    definitions: DefinitionStore,
    /// The plural name of every kind that API discovery listed at startup.
    plurals: PluralStore,
    /// Roles and bindings, which show who may write each custom kind's
    /// status, for each kind `transect` may list.
    rbac: RbacStores,
    /// ResourceClaims and ResourceSlices, when the cluster serves them.
    devices: DeviceStores,
    /// The cluster's DNS domain, such as `cluster.local`. The API does
    /// not report it, so it comes from a flag.
    domain: Arc<str>,
    /// The machines that a development build shows as not ready.
    pretend: Pretend,
    changed: watch::Sender<()>,
    /// Metadata keys that kind knowledge lists as rewritten by tools
    /// without a real change. Every watch ignores them when it decides
    /// whether an object changed.
    ignored: Ignored,
}

impl Cluster {
    /// Starts a watch on each built-in kind the map draws. Optional
    /// kinds are discovered in the background, and each starts its own
    /// watches once the cluster is found to serve it. Secrets are
    /// watched, as metadata, only when `secret_annotations` is true.
    pub fn watch(
        client: Client,
        domain: String,
        pretend: Pretend,
        secret_annotations: bool,
    ) -> Self {
        let (changed, _) = watch::channel(());
        let ignored = Ignored::default();
        let changes = Changes {
            sender: changed.clone(),
            ignored: ignored.clone(),
        };
        let rbac = RbacStores::discover(&client, &changes);
        let devices = DeviceStores::discover(&client, &changes);
        let attachments = AttachmentStore::discover(&client, &changes);
        let plurals = PluralStore::discover(&client, &changed);
        let definitions = DefinitionStore::discover(&client, &changes);
        let secrets = match secret_annotations {
            true => SecretStore::discover(&client, &changes),
            false => SecretStore::default(),
        };
        Self {
            namespaces: reflect(Api::all(client.clone()), changes.clone()),
            nodes: reflect_cut(Api::all(client.clone()), changes.clone(), trim::node),
            // Pods change far more often than any other kind here.
            // `trim::pod` removes the fields that no page draws, so a
            // change to only those fields sends no change signal.
            pods: reflect_cut(Api::all(client.clone()), changes.clone(), trim::pod),
            deployments: reflect_cut(Api::all(client.clone()), changes.clone(), trim::deployment),
            stateful_sets: reflect_cut(
                Api::all(client.clone()),
                changes.clone(),
                trim::stateful_set,
            ),
            daemon_sets: reflect_cut(Api::all(client.clone()), changes.clone(), trim::daemon_set),
            cron_jobs: reflect_cut(Api::all(client.clone()), changes.clone(), trim::cron_job),
            ingresses: reflect(Api::all(client.clone()), changes.clone()),
            services: reflect(Api::all(client.clone()), changes.clone()),
            endpoint_slices: reflect(Api::all(client.clone()), changes.clone()),
            claims: reflect_cut(Api::all(client.clone()), changes.clone(), trim::claim),
            volumes: reflect(Api::all(client.clone()), changes.clone()),
            storage_classes: reflect(Api::all(client.clone()), changes.clone()),
            csi_drivers: reflect(Api::all(client.clone()), changes.clone()),
            attachments,
            budgets: reflect(Api::all(client.clone()), changes.clone()),
            network_policies: reflect(Api::all(client.clone()), changes.clone()),
            replica_sets: reflect(Api::all(client.clone()), changes.clone()),
            jobs: reflect(Api::all(client.clone()), changes.clone()),
            controller_revisions: reflect(Api::all(client.clone()), changes.clone()),
            config_maps: reflect(Api::all(client.clone()), changes.clone()),
            service_accounts: reflect(Api::all(client.clone()), changes.clone()),
            ingress_classes: reflect(Api::all(client.clone()), changes.clone()),
            priority_classes: reflect(Api::all(client), changes.clone()),
            secrets,
            custom: CustomOwners::default(),
            definitions,
            plurals,
            rbac,
            devices,
            domain: domain.into(),
            pretend,
            changed,
            ignored,
        }
    }

    /// Starts the watches on custom kinds, now and whenever a new one
    /// appears. Call it after `synced` has resolved.
    /// `Store::wait_until_ready` in kube-runtime keeps the waker of only
    /// the last task that polled it, so a second task that waits on the
    /// same stores leaves the first one waiting until something else
    /// wakes it.
    pub fn watch_custom_kinds(
        &self,
        client: Client,
        knowledge: Field<Vec<KindKnowledge>>,
        device_kinds: Field<Vec<custom::GroupKind>>,
    ) {
        custom::discover_custom_kinds(self.clone(), client, knowledge, device_kinds);
    }

    /// Sets the metadata keys that watches ignore from `knowledge`, now
    /// and after each reload.
    pub fn ignore_changes_from(&self, mut knowledge: Field<Vec<KindKnowledge>>) {
        let ignored = self.ignored.clone();
        tokio::spawn(async move {
            loop {
                let named = knowledge.read_and_update();
                let named = named.iter();
                ignored.set(named.flat_map(|kind| kind.ignore_changes.clone()).collect());
                if knowledge.changed().await.is_err() {
                    return;
                }
            }
        });
    }

    /// The `Changes` that a watch uses to signal a change to its store.
    fn changes(&self) -> Changes {
        Changes {
            sender: self.changed.clone(),
            ignored: self.ignored.clone(),
        }
    }

    /// The channel that signals that any store changed. Each view's
    /// render task subscribes to it, and a source outside the Kubernetes
    /// API, such as usage from Prometheus, sends on it too.
    pub fn change_signal(&self) -> watch::Sender<()> {
        self.changed.clone()
    }

    /// Resolves when each store below has its first list, because before
    /// that a page would show namespaces with too few workloads, or none.
    /// It does not wait for the custom kinds, because the ownership walk
    /// can name a custom owner from its owner reference alone, and a kind
    /// that cannot be listed would delay every page. For each optional
    /// kind, it waits until the kind is off, the kind is on and has its
    /// first list, or the first probe has failed. It does not wait for
    /// the retries after that.
    pub async fn synced(&self) -> anyhow::Result<()> {
        let stores = async {
            tokio::try_join!(
                self.namespaces.wait_until_ready(),
                self.nodes.wait_until_ready(),
                self.pods.wait_until_ready(),
                self.deployments.wait_until_ready(),
                self.stateful_sets.wait_until_ready(),
                self.daemon_sets.wait_until_ready(),
                self.cron_jobs.wait_until_ready(),
                self.ingresses.wait_until_ready(),
                self.services.wait_until_ready(),
                self.endpoint_slices.wait_until_ready(),
                self.claims.wait_until_ready(),
                self.volumes.wait_until_ready(),
                self.storage_classes.wait_until_ready(),
                self.csi_drivers.wait_until_ready(),
                self.budgets.wait_until_ready(),
                self.network_policies.wait_until_ready(),
                self.replica_sets.wait_until_ready(),
                self.jobs.wait_until_ready(),
                self.controller_revisions.wait_until_ready(),
                self.config_maps.wait_until_ready(),
            )
        };
        let (stores, (), (), (), (), (), ()) = tokio::join!(
            stores,
            self.plurals.synced(),
            self.definitions.synced(),
            self.rbac.synced(),
            self.devices.synced(),
            self.attachments.synced(),
            self.secrets.synced(),
        );
        stores?;
        Ok(())
    }
}
