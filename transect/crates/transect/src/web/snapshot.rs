//! One reading of the cluster's stores, and what every page derives
//! from it the same way: health, the map, the machines, and the edge
//! graph.

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

/// The values the cluster's signals returned in one reading, besides
/// usage, which only the machines and the pages that draw it read.
pub struct Signals {
    pub logs: Counted,
    pub alerts: alerts::Reading,
    /// The ingress controllers' metrics for each backend.
    pub entry: entry::poll::Reading,
    /// The bytes each claim's volume holds, from the metrics source.
    pub volumes_used: transect_observe::metrics::Reading,
    /// The name of the pod that runs Prometheus, once read.
    pub prometheus_pod: Option<String>,
}

/// The cluster's stores as one render reads them.
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
    /// ConfigMaps, as metadata, which intent can be written on.
    pub config_maps: Vec<Arc<PartialObjectMeta<ConfigMap>>>,
    /// Secrets, as metadata, or none when `transect` does not read
    /// their annotations.
    pub secrets: Option<Vec<Arc<PartialObjectMeta<Secret>>>>,
    pub ownership: Ownership,
    /// What applied each top owner, by the marks its tool writes.
    pub applied: Applied,
    /// The objects of the GitOps tools that kind knowledge describes.
    pub gitops: GitOps,
    pub plurals: Plurals,
    /// Every claim followed to where its volume stores its data, with
    /// the backends an `Outside` groups renamed to its box.
    pub storage: Storage,
    /// The CSI drivers, whose `data-on` intent states where their data
    /// is.
    pub csi_drivers: Vec<Arc<CSIDriver>>,
    /// The Outsides, by the backends they group.
    pub outsides: Outsides,
    pub addresses: Addresses,
    /// Who RBAC lets write each custom kind's status.
    pub grants: Grants,
    /// The managers that write each custom resource's status.
    pub status_managers: StatusManagers,
    /// The devices each allocated claim holds, or why there are none.
    pub devices: Devices,
    /// The image labels read so far.
    pub images: Arc<Known>,
    /// The contents of each pod log read that is still cached, and the
    /// matches that the log source's searches returned.
    pub logs: Counted,
    /// The alerts that Alertmanager last reported as firing.
    pub alerts: alerts::Reading,
    /// The ingress controllers' metrics for each backend.
    pub entry: entry::poll::Reading,
    /// The bytes each claim's volume holds, from the metrics source.
    pub volumes_used: transect_observe::metrics::Reading,
    /// The name of the pod that runs Prometheus, once read.
    pub prometheus_pod: Option<String>,
    /// Every custom kind watched, each with its objects: whole for the
    /// kinds that own pods or that kind knowledge describes, and trimmed
    /// to metadata and conditions for the rest.
    pub custom: Vec<(ApiResource, Vec<Arc<Custom>>)>,
    /// `transect`'s own resources, as of this reading.
    pub resources: Arc<Loaded>,
    /// The overlays merged onto the objects above.
    pub overlaid: Overlaid,
    /// The intent declared for each object, from its annotations and the
    /// Intents.
    pub intents: Intents,
    /// The kind knowledge matched to the kinds the cluster serves.
    pub kinds: Kinds,
    /// The kinds discovery found that are not namespaced.
    pub cluster_scoped: ClusterScoped,
    /// What each custom resource of a known kind names, through its
    /// kind's knowledge.
    pub relations: HashMap<Object, Vec<Relation>>,
    /// The description each custom kind's CRD gives of it.
    pub definitions: Definitions,
    /// The issuer of each `Ingress`'s certificate, as kind knowledge's
    /// `referencedBy` with role `issues-tls` names it.
    pub issuers: transect_network::entry::Issuers,
    /// The kinds held only for their own pages.
    pub paged: Paged,
    /// Every object that has a page.
    pub held: HashSet<Object>,
}

impl Snapshot {
    /// The cluster's stores as of now, with `resources`' overlays merged
    /// onto the objects they name.  Ownership and the appliers' marks are
    /// read from the stores themselves, because an overlay holds intent,
    /// not owner references.
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
        // A `ServiceAccount`'s intent states why it is bound as it is,
        // and an overlay may supply that intent in place of its
        // annotations.
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
            held: HashSet::new(),
        };
        snapshot
            .derive(|on_machines| transect_ground::storage::Storage::read(cluster, on_machines));
        snapshot
    }

    /// Fills in what the stores and the resources give together: the
    /// kind knowledge matched to the kinds served, the intent on every
    /// object, the storage that `storage` follows with the CSI drivers
    /// intent places on machines, the relations of each custom resource
    /// of a known kind, and the objects that have a page.
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
        self.held = self.held_objects();
        self.issuers = self.tls_issuers();
    }

    /// The input to the entry's traffic numbers: the answers to each
    /// controller's queries, and whether any `IngressController`
    /// declares the `entry` section that reads them.
    pub fn traffic(&self) -> transect_network::entry::Answers<'_> {
        let controllers = self.resources.ingress_controllers.iter();
        transect_network::entry::Answers {
            measured: &self.entry,
            declared: controllers.into_iter().any(|c| c.entry.is_some()),
            unloaded: self.resources.directory.unloaded("IngressController"),
        }
    }

    /// The `Service` the `MetricsSource`'s alerts section names, whose
    /// pods are the cluster's alerting path, when it names one.
    pub fn alerts_service(&self) -> Option<&transect_graph::upstream::Service> {
        let source = self.resources.metrics_source.as_ref()?;
        source.alerts.as_ref()?.service.as_ref()
    }

    /// What decides which objects have a page, as of this reading.
    pub fn pages(&self) -> Pages<'_> {
        Pages {
            held: &self.held,
            ownership: &self.ownership,
            relations: &self.relations,
            gitops: &self.gitops,
            plurals: &self.plurals,
        }
    }

    /// A snapshot of a cluster with nothing in it, which a test or the
    /// fixture fills.
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
            held: HashSet::new(),
        }
    }

    /// The health of every namespace that has a reason to report: the
    /// rules over the built-in kinds, the condition that means each
    /// custom workload works, the error lines in the logs, the error
    /// and warning patterns' matches, and the alerts firing about it.
    pub fn health(&self, now: Timestamp) -> HashMap<String, Health> {
        let health = health::assess(&self.objects(), &self.ownership, &self.revisions(), now);
        let mut reasons = self.conditions_of_custom();
        reasons.extend(self.log_reasons(now));
        reasons.extend(self.alert_reasons(now));
        health::with_reasons(health, reasons)
    }

    /// When each controller last rolled, from the revisions it keeps.
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

    /// Who the NetworkPolicies allow to call whom.  `workloads`
    /// gives the pod templates of each workload with no running pod.
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
