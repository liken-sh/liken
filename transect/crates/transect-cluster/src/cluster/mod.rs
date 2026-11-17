//! The cluster's objects, held in memory and kept current by watches on
//! the Kubernetes API.  Pages read from these stores on each request, so
//! they show the cluster as of that request.
//!
//! Every watch event also marks the cluster changed, on a `watch` channel
//! of `()`.  A `watch` keeps only whether something changed since a
//! receiver last looked, so a burst of a thousand events wakes a reader
//! once, and a slow reader never lags or drops events the way it would on
//! a `broadcast` channel.  Readers that need the details read the stores.

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
    /// Services.  Their selectors separate a workload that serves
    /// something from a small helper on the namespace page, and the
    /// section view draws the Services that select a workload in its
    /// entry band.
    services: Store<Service>,
    /// The slices that list each `Service`'s endpoints and whether each
    /// is ready, which the entry counts.
    endpoint_slices: Store<EndpointSlice>,
    /// Claims, the volumes bound to them, and the classes that made
    /// them, which the storage walk follows to each backend.
    claims: Store<PersistentVolumeClaim>,
    volumes: Store<PersistentVolume>,
    storage_classes: Store<StorageClass>,
    /// The CSI drivers, whose `data-on` annotation states that a driver
    /// keeps its volumes on the disks of the machines that mount them.
    csi_drivers: Store<CSIDriver>,
    /// The machines CSI drivers attached volumes to, when the cluster
    /// serves VolumeAttachments.
    attachments: AttachmentStore,
    /// The budgets that limit how many of a workload's pods an eviction
    /// may take at once, which the workload page shows as a guard.
    budgets: Store<PodDisruptionBudget>,
    /// The policies that state which pods may call which, from which
    /// the allowed-to-call edges are evaluated.
    network_policies: Store<NetworkPolicy>,
    /// ReplicaSets and Jobs are read only for their owner references,
    /// so they are watched as metadata.  A `Deployment` keeps ten old
    /// ReplicaSets by default, which makes them the most numerous kind
    /// here after pods.
    replica_sets: Store<PartialObjectMeta<ReplicaSet>>,
    jobs: Store<PartialObjectMeta<Job>>,
    /// The revisions StatefulSets and DaemonSets keep of their pod
    /// templates, read as metadata for when each was made, which is when
    /// its workload last rolled.  A `Deployment`'s revisions are its
    /// ReplicaSets.
    controller_revisions: Store<PartialObjectMeta<ControllerRevision>>,
    /// ConfigMaps, as metadata, for the intent annotations on them.
    config_maps: Store<PartialObjectMeta<ConfigMap>>,
    /// ServiceAccounts, IngressClasses, and PriorityClasses, as
    /// metadata, so that a name that refers to one links to its page
    /// only when it exists.  Nothing waits for them, so until their
    /// first list arrives, such a name shows as plain text.
    service_accounts: Store<PartialObjectMeta<ServiceAccount>>,
    ingress_classes: Store<IngressClass>,
    priority_classes: Store<PartialObjectMeta<PriorityClass>>,
    /// Secrets, as metadata, only with `--secret-annotations`.
    secrets: SecretStore,
    /// The custom kinds found in owner references or described by kind
    /// knowledge, whole, and every other CRD's kind, trimmed to its
    /// metadata and conditions.
    custom: CustomOwners,
    /// The CRDs, each cut to the parts that describe its kind, when
    /// `transect` may list them.
    definitions: DefinitionStore,
    /// The plural of every kind the API served at the start.
    plurals: PluralStore,
    /// Roles and bindings, which name who may write each custom kind's
    /// status, for each kind `transect` may list.
    rbac: RbacStores,
    /// ResourceClaims and ResourceSlices, when the cluster serves them.
    devices: DeviceStores,
    /// The cluster's DNS domain, such as `cluster.local`.  The API does
    /// not report it, so it comes from a flag.
    domain: Arc<str>,
    /// The machines a development build pretends are not ready.
    pretend: Pretend,
    changed: watch::Sender<()>,
    /// The metadata keys that kind knowledge names as ones a tool
    /// rewrites without changing anything, which every watch leaves out
    /// when it decides whether an object changed.
    ignored: Ignored,
}

impl Cluster {
    /// Starts a watch on each kind the map draws, and discovery of the
    /// optional kinds in the background, each of which starts its own
    /// watches once it finds that the cluster serves them.  Secrets are
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
            // Pods change far more often than anything else here.  A
            // change the map does not draw renders to the same HTML,
            // and the event stream sends a rendering only when it differs.
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

    /// Starts a watch on each custom kind: whole on each that owns a pod
    /// or a workload or that the kind knowledge in `resources`
    /// describes, and trimmed on every other CRD, now and whenever a new
    /// one appears.  Call it once
    /// `synced` has resolved, because a store's readiness wakes only one
    /// waiting task, so a second task waiting on `synced` at the same time
    /// would leave the first asleep until something else woke it.
    pub fn watch_custom_kinds(
        &self,
        client: Client,
        knowledge: Field<Vec<KindKnowledge>>,
        device_kinds: Field<Vec<custom::GroupKind>>,
    ) {
        custom::discover_custom_kinds(self.clone(), client, knowledge, device_kinds);
    }

    /// Keeps the keys every watch ignores as `knowledge` names them,
    /// now and after each reload.
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

    /// The `Changes` a watch sends on when its store changes.
    fn changes(&self) -> Changes {
        Changes {
            sender: self.changed.clone(),
            ignored: self.ignored.clone(),
        }
    }

    /// The signal that any store changed.  A source outside the API whose
    /// readings the map also draws, such as usage from Prometheus, sends
    /// on it too, and each view's render task subscribes to it.
    pub fn change_signal(&self) -> watch::Sender<()> {
        self.changed.clone()
    }

    /// Resolves once every store holds its first full list.  Until then a
    /// page would show namespaces with too few workloads, or none at all.
    /// The custom kinds' stores are left out, because the walk names a
    /// custom owner from the owner reference alone, and a kind that cannot
    /// be listed would otherwise hold up every page.  Each optional kind
    /// is waited for until its discovery first answers, which has a
    /// timeout of its own.  One still retrying after that holds up
    /// nothing.
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
