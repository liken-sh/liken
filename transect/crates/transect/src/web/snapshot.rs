//! A copy of the cluster's stores and loaded resources, taken once per
//! render, and the values that every page derives from it in the same
//! way: health, the map, the machines, and the graph of edges.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use k8s_openapi::api::apps::v1::{
    ControllerRevision, DaemonSet, Deployment, ReplicaSet, StatefulSet,
};
use k8s_openapi::api::batch::v1::{CronJob, Job};
use k8s_openapi::api::core::v1::{ConfigMap, Namespace, Node, Pod, Secret, Service};
use k8s_openapi::api::discovery::v1::EndpointSlice;
use k8s_openapi::api::networking::v1::{Ingress, NetworkPolicy};
use k8s_openapi::api::policy::v1::PodDisruptionBudget;
use k8s_openapi::api::storage::v1::CSIDriver;
use k8s_openapi::jiff::Timestamp;
use kube::api::{ApiResource, PartialObjectMeta};

use crate::web::loaded::Loaded;
use transect_cluster::cluster::Watch;
use transect_cluster::cluster::{Cluster, Custom};
use transect_cluster::ownership::{Grants, Ownership, StatusManagers};
use transect_cluster::workloads::{Revisions, Workloads};
use transect_graph::edges::ClusterScoped;
use transect_graph::edges::{Object, Plurals};
use transect_ground::devices::Devices;
use transect_ground::guards::Guards;
use transect_ground::machines::{self, Machine};
use transect_ground::outside::Outsides;
use transect_ground::storage::{OnMachines, Storage};
use transect_map::map::ClusterMap;
use transect_map::map::pages::Pages;
use transect_network::addresses::Addresses;
use transect_network::entry;
use transect_network::policy::{Allowed, Idle};
use transect_observe::alerts;
use transect_observe::health::{self, Health};
use transect_observe::logs::Counted;
use transect_observe::usage;
use transect_resources::intent::Intents;
use transect_resources::knowledge::{self, Definitions, Kinds, Named, Relation};
use transect_resources::layers::Layers;
use transect_resources::resources::Overlaid;
use transect_supply::findings::{self, Linked, WorkloadFinding};
use transect_supply::gitops::{Applied, GitOps};
use transect_supply::images::Known;

mod access;
mod alerted;
mod card;
mod changed;
mod described;
mod graph;
mod issuers;
mod known;
mod layer;
mod logged;
mod matched;
mod paged;
mod serving;
mod underneath;

pub use access::fingerprint;
pub use paged::Paged;

/// The latest reading of each poller that `Snapshot::take` copies.
/// Usage is not here, because only `Snapshot::machines` reads it, and
/// its callers pass it in.
pub struct Signals {
    pub logs: Counted,
    pub alerts: alerts::Reading,
    pub entry: entry::poll::Reading,
    pub volumes_used: transect_observe::metrics::Reading,
    pub prometheus_pod: Option<String>,
}

/// The stores and inputs of one render, copied together so that every
/// value derived from them describes the same moment.
pub struct Snapshot {
    pub namespaces: Vec<Arc<Namespace>>,
    pub nodes: Vec<Arc<Node>>,
    pub pods: Vec<Arc<Pod>>,
    pub deployments: Vec<Arc<Deployment>>,
    pub stateful_sets: Vec<Arc<StatefulSet>>,
    pub daemon_sets: Vec<Arc<DaemonSet>>,
    pub cron_jobs: Vec<Arc<CronJob>>,
    pub budgets: Vec<Arc<PodDisruptionBudget>>,
    pub network_policies: Vec<Arc<NetworkPolicy>>,
    pub services: Vec<Arc<Service>>,
    pub ingresses: Vec<Arc<Ingress>>,
    pub endpoint_slices: Vec<Arc<EndpointSlice>>,
    pub replica_sets: Vec<Arc<PartialObjectMeta<ReplicaSet>>>,
    pub controller_revisions: Vec<Arc<PartialObjectMeta<ControllerRevision>>>,
    pub jobs: Vec<Arc<PartialObjectMeta<Job>>>,
    /// Only the metadata, because `transect` reads `ConfigMap`s only for
    /// their intent annotations.
    pub config_maps: Vec<Arc<PartialObjectMeta<ConfigMap>>>,
    /// Secret metadata, or `None` when `transect` does not read Secret
    /// annotations. `transect` never reads Secret data.
    pub secrets: Option<Vec<Arc<PartialObjectMeta<Secret>>>>,
    pub ownership: Ownership,
    /// The tool that applied each top-level owner, found from the labels
    /// and annotations that the tool writes on it.
    pub applied: Applied,
    /// The objects of the GitOps tools that kind knowledge describes.
    pub gitops: GitOps,
    pub plurals: Plurals,
    /// Where each claim's volume stores its data. Backends that an
    /// `Outside` groups appear as that `Outside`.
    pub storage: Storage,
    /// The CSI drivers. A driver's `data-on` intent says which machines
    /// store its data.
    pub csi_drivers: Vec<Arc<CSIDriver>>,
    /// The `Outside`s and the backends that each one groups.
    pub outsides: Outsides,
    pub addresses: Addresses,
    /// The subjects that RBAC allows to write each custom kind's status.
    pub grants: Grants,
    /// The managers that write each custom resource's status.
    pub status_managers: StatusManagers,
    /// The devices each allocated claim holds, or why there are none.
    pub devices: Devices,
    /// The image labels read from registries so far.
    pub images: Arc<Known>,
    /// The pod logs read so far and still cached, and the matches that
    /// the log source's searches returned.
    pub logs: Counted,
    /// The alerts that Alertmanager last reported as firing.
    pub alerts: alerts::Reading,
    /// The results of the ingress controllers' traffic queries for each
    /// backend.
    pub entry: entry::poll::Reading,
    /// The bytes used on each claim's volume, from Prometheus.
    pub volumes_used: transect_observe::metrics::Reading,
    /// The name of the pod that runs Prometheus, once read.
    pub prometheus_pod: Option<String>,
    /// Every custom kind watched, each with its objects: whole for the
    /// kinds that own pods or that kind knowledge describes, and trimmed
    /// to metadata and conditions for the rest.
    pub custom: Vec<(ApiResource, Vec<Arc<Custom>>)>,
    /// `transect`'s own resources, as loaded when the snapshot was
    /// taken.
    pub resources: Arc<Loaded>,
    /// The overlays merged onto the objects above.
    pub overlaid: Overlaid,
    /// The intent declared for each object, from its annotations and
    /// from the loaded `Intent` resources.
    pub intents: Intents,
    /// The kind knowledge matched to the kinds the cluster serves.
    pub kinds: Kinds,
    /// The kinds discovery found that are not namespaced.
    pub cluster_scoped: ClusterScoped,
    /// The objects that each custom resource of a known kind refers to,
    /// as its kind knowledge describes.
    pub relations: HashMap<Object, Vec<Relation>>,
    /// The description in each custom kind's CRD.
    pub definitions: Definitions,
    /// The issuer of each `Ingress`'s certificate, found through the
    /// `referencedBy` entry with role `issues-tls` in kind knowledge.
    pub issuers: transect_network::entry::Issuers,
    /// The stores that are read only for the pages of their own
    /// objects.
    pub paged: Paged,
    /// Every object that has a page, except `Secret`s.
    pub with_pages: HashSet<Object>,
}

impl Snapshot {
    /// Copies the cluster's stores, with the overlays of `resources`
    /// merged onto the objects they name. Ownership, and the labels and
    /// annotations that show which tool applied an object, are read from
    /// the stores without overlays, because an overlay adds intent and
    /// never owner references.
    pub fn take(
        cluster: &Cluster,
        images: Arc<Known>,
        resources: Arc<Loaded>,
        signals: Signals,
    ) -> Self {
        let Signals {
            logs,
            alerts,
            entry,
            volumes_used,
            prometheus_pod,
        } = signals;
        let mut overlaid = Overlaid::new(resources.directory.overlays.clone());
        let services = overlaid.apply(cluster.services());
        let ingresses = overlaid.apply(cluster.ingresses());
        let nodes = overlaid.apply(cluster.nodes());
        let addresses = Addresses::build(&services, &ingresses, &nodes, cluster.domain());
        let custom: Vec<(ApiResource, Vec<Arc<Custom>>)> = cluster
            .custom_kinds()
            .into_iter()
            .map(|(resource, metas)| {
                let metas = overlaid.apply_as(&resource.group, &resource.kind, metas);
                (resource, metas)
            })
            .collect();
        let plurals = cluster.plurals();
        let gitops = {
            let kinds = Kinds::resolve(&resources.kinds, &plurals, &[]);
            let watched = cluster.custom_watched();
            GitOps::build(&kinds, &custom, |group, kind| {
                let key = (group.to_string(), kind.to_string());
                watched.get(&key).copied().unwrap_or(Watch::Listing)
            })
            .with_unloaded(resources.directory.unloaded("KindKnowledge"))
        };
        let appliers = gitops.appliers();
        let (ownership, applied) =
            cluster.ownership(|marked, ownership| Applied::build(marked, ownership, &appliers));
        // An overlay can give a `ServiceAccount` the intent annotation that
        // explains its bindings.
        let mut paged = Paged::take(cluster);
        paged.service_accounts = overlaid.apply(std::mem::take(&mut paged.service_accounts));
        let mut snapshot = Self {
            namespaces: overlaid.apply(cluster.namespaces()),
            nodes,
            pods: overlaid.apply(cluster.pods()),
            deployments: overlaid.apply(cluster.deployments()),
            stateful_sets: overlaid.apply(cluster.stateful_sets()),
            daemon_sets: overlaid.apply(cluster.daemon_sets()),
            cron_jobs: overlaid.apply(cluster.cron_jobs()),
            budgets: overlaid.apply(cluster.budgets()),
            network_policies: overlaid.apply(cluster.network_policies()),
            services,
            ingresses,
            endpoint_slices: cluster.endpoint_slices(),
            replica_sets: cluster.replica_sets(),
            controller_revisions: cluster.controller_revisions(),
            jobs: cluster.jobs(),
            config_maps: overlaid.apply(cluster.config_maps()),
            secrets: cluster.secrets().map(|secrets| overlaid.apply(secrets)),
            ownership,
            applied,
            gitops,
            plurals,
            storage: Storage::default(),
            csi_drivers: overlaid.apply(cluster.csi_drivers()),
            outsides: Outsides::new(&resources.outsides),
            addresses,
            grants: cluster.grants(),
            status_managers: cluster.status_managers(),
            devices: transect_ground::devices::Devices::read(cluster),
            images,
            logs,
            alerts,
            entry,
            volumes_used,
            prometheus_pod,
            custom,
            resources,
            overlaid,
            intents: Intents::default(),
            kinds: Kinds::default(),
            cluster_scoped: cluster.cluster_scoped(),
            relations: HashMap::new(),
            definitions: cluster.definitions(),
            issuers: Default::default(),
            paged,
            with_pages: HashSet::new(),
        };
        snapshot
            .derive(|on_machines| transect_ground::storage::Storage::read(cluster, on_machines));
        snapshot
    }

    /// Fills in the fields that depend on both the copied stores and the
    /// loaded resources. `storage` reads the storage backends, given the
    /// machines where intent places each CSI driver's data.
    pub fn derive(&mut self, storage: impl FnOnce(&OnMachines) -> Storage) {
        self.kinds = Kinds::resolve(
            &self.resources.kinds,
            &self.plurals,
            &self.resources.layers.names().collect::<Vec<_>>(),
        );
        self.intents = Intents::read(
            &self.described(),
            &self.resources.intents,
            &self.overlaid,
            &self.plurals,
        );
        let consoles = self.consoles();
        self.outsides = std::mem::take(&mut self.outsides).with_consoles(consoles);
        let storage = storage(&self.on_machines());
        self.storage = self.outsides.fold_storage(storage);
        self.relations = self.read_relations();
        self.with_pages = self.objects_with_pages();
        self.issuers = self.tls_issuers();
    }

    /// The inputs for the traffic numbers on each `Ingress`: the results
    /// of the controllers' queries, whether any `IngressController`
    /// declares the `entry` section that makes `transect` run them, and
    /// the name of an `IngressController` that failed to load.
    pub fn traffic(&self) -> transect_network::entry::TrafficResults<'_> {
        let controllers = self.resources.ingress_controllers.iter();
        transect_network::entry::TrafficResults {
            measured: &self.entry,
            declared: controllers.into_iter().any(|c| c.entry.is_some()),
            unloaded: self.resources.directory.unloaded("IngressController"),
        }
    }

    /// The `Service` in front of Alertmanager, from the `MetricsSource`'s
    /// `alerts.service`. The loss view reads it to tell whether a loss
    /// stops alerts.
    pub fn alerts_service(&self) -> Option<&transect_graph::upstream::Service> {
        let source = self.resources.metrics_source.as_ref()?;
        source.alerts.as_ref()?.service.as_ref()
    }

    /// The inputs that decide which objects have a page.
    pub fn pages(&self) -> Pages<'_> {
        Pages {
            with_pages: &self.with_pages,
            ownership: &self.ownership,
            relations: &self.relations,
            gitops: &self.gitops,
            plurals: &self.plurals,
        }
    }

    /// A snapshot of a cluster with nothing in it, for a test or the
    /// fixture to fill.
    #[cfg(any(test, debug_assertions))]
    pub fn empty() -> Self {
        Self {
            namespaces: Vec::new(),
            nodes: Vec::new(),
            pods: Vec::new(),
            deployments: Vec::new(),
            stateful_sets: Vec::new(),
            daemon_sets: Vec::new(),
            cron_jobs: Vec::new(),
            budgets: Vec::new(),
            network_policies: Vec::new(),
            services: Vec::new(),
            ingresses: Vec::new(),
            endpoint_slices: Vec::new(),
            replica_sets: Vec::new(),
            controller_revisions: Vec::new(),
            jobs: Vec::new(),
            config_maps: Vec::new(),
            secrets: None,
            ownership: Ownership::default(),
            applied: Applied::default(),
            gitops: GitOps::off(transect_supply::gitops::Off::NoKnowledge),
            plurals: Plurals::default(),
            storage: Storage::default(),
            csi_drivers: Vec::new(),
            outsides: Outsides::default(),
            addresses: Addresses::default(),
            grants: Grants::default(),
            status_managers: StatusManagers::default(),
            devices: Devices::default(),
            images: Arc::default(),
            logs: Counted::default(),
            alerts: alerts::Reading::Off,
            entry: Default::default(),
            volumes_used: Default::default(),
            prometheus_pod: None,
            custom: Vec::new(),
            resources: Arc::default(),
            overlaid: Overlaid::default(),
            intents: Intents::default(),
            kinds: Kinds::default(),
            cluster_scoped: ClusterScoped::default(),
            relations: HashMap::new(),
            definitions: Definitions::default(),
            issuers: Default::default(),
            paged: Paged::default(),
            with_pages: HashSet::new(),
        }
    }

    /// The health of every namespace that has something to report, from
    /// the rules over the built-in kinds, the conditions of custom
    /// workloads, error lines in the logs, matches of the log patterns,
    /// and firing alerts.
    pub fn health(&self, now: Timestamp) -> HashMap<String, Health> {
        let health = health::assess(&self.objects(), &self.ownership, &self.revisions(), now);
        let mut reasons = self.conditions_of_custom();
        reasons.extend(self.log_reasons(now));
        reasons.extend(self.alert_reasons(now));
        health::with_reasons(health, reasons)
    }

    /// When each controller last rolled out, from the revisions it
    /// keeps.
    pub fn revisions(&self) -> Revisions {
        let replica_sets = self.replica_sets.iter().map(|meta| &meta.metadata);
        let controller_revisions = self.controller_revisions.iter().map(|meta| &meta.metadata);
        Revisions::build(replica_sets.chain(controller_revisions))
    }

    /// The findings about every workload in the cluster, each with the
    /// workload it is about.
    pub fn workload_findings(&self, workloads: &Workloads) -> Vec<(WorkloadFinding, Linked)> {
        self.ownership
            .all_workloads()
            .flat_map(|object| {
                let workload = workloads.get(object);
                let guards = Guards::read(&workload, &self.budgets, &self.services);
                let found = findings::of_workload(&guards, workload.replicas().unwrap_or(0));
                let linked = Linked {
                    object: object.clone(),
                    href: transect_graph::links::link(object, &self.plurals),
                };
                found.into_iter().map(move |one| (one, linked.clone()))
            })
            .collect()
    }

    pub fn objects(&self) -> transect_cluster::workloads::Objects<'_> {
        transect_cluster::workloads::Objects {
            deployments: &self.deployments,
            stateful_sets: &self.stateful_sets,
            daemon_sets: &self.daemon_sets,
            cron_jobs: &self.cron_jobs,
            pods: &self.pods,
        }
    }

    pub fn map(&self, layers: &Layers, health: HashMap<String, Health>) -> ClusterMap {
        let named = knowledge::named(&self.kinds, &self.custom_objects(), layers, &self.plurals);
        ClusterMap::build(
            layers,
            self.namespaces.iter().map(|namespace| &namespace.metadata),
            &self.ownership.workloads_per_namespace(),
            health,
            &Named::layers(&named),
        )
    }

    /// Every machine, with what it uses and the alerts firing about it.
    pub fn machines(&self, usage: &usage::Reading) -> Vec<Machine> {
        let mut machines = machines::machines(
            self.nodes.iter().map(AsRef::as_ref),
            self.pods.iter().map(AsRef::as_ref),
            usage.usage(),
        );
        let mut alerted = self.machine_alerts();
        for machine in &mut machines {
            machine.alerted = alerted.remove(&machine.name);
        }
        machines
    }

    /// Which workloads the NetworkPolicies allow to call which.
    /// `workloads` supplies the pod templates of each workload that has no
    /// running pod.
    pub fn allowed(&self, workloads: &Workloads) -> Allowed {
        let idle = self.ownership.all_workloads().map(|owner| {
            let workload = workloads.get(owner);
            Idle {
                owner,
                labels: workload.pod_labels(),
                specs: workload.templates(),
            }
        });
        Allowed::evaluate(
            self.network_policies.iter().map(AsRef::as_ref),
            self.pods.iter().map(AsRef::as_ref),
            idle,
            self.namespaces.iter().map(|namespace| &namespace.metadata),
            &self.ownership,
        )
    }

    pub fn workloads(&self) -> Workloads<'_> {
        Workloads::build(&self.objects(), &self.revisions(), &self.ownership)
    }
}
