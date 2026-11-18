//! What every view reads from one state of the cluster: the snapshot,
//! the `NetworkPolicy` evaluation, the edge graph with every kind on,
//! and the findings about each workload and image. These are costly and
//! do not depend on the view, so `latest` builds them once per change,
//! and each view turns edge kinds off in its own copy of the graph.
//! Values that depend on the shown edge kinds or on the minute are
//! computed on first request and cached.

mod edge_set;
mod index;
mod memo;
mod minute;

use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::Instant;

use k8s_openapi::jiff::Timestamp;

use super::snapshot::{Signals, Snapshot, fingerprint};
use crate::web::loaded::Loaded;
pub use edge_set::EdgeSet;
use memo::Memo;
pub use minute::Minute;
use transect_access::access::Access;
use transect_access::access::ways::Ways;
use transect_cluster::cluster::Cluster;
use transect_cluster::ownership::{Before, Departed};
use transect_graph::edges::{Graph, Kind, Object, Shown};
use transect_map::search::Index;
use transect_network::addresses::Unread;
use transect_network::policy::Allowed;
use transect_supply::findings::{Linked, WorkloadFinding};
use transect_supply::images::{self, Census, Known};
use transect_supply::vulnerabilities::{Stores, Vulnerabilities};

/// How many sets of shown edge kinds a model caches derived values for.
const EDGE_SETS: usize = 8;

/// How many minutes a model caches values for: the current minute and
/// the one before it, because a render that began just before the
/// minute changed may still request the earlier one.
const MINUTES: usize = 2;

pub struct Model {
    pub snapshot: Snapshot,
    /// The owners of deleted pods, kept across models so that a deleted
    /// pod's page can link to its owner.
    pub departed: Departed,
    allowed: Allowed,
    graph: Graph,
    /// The intent entries that refer to something `transect` cannot
    /// read, by the workload whose card shows them.
    pub unread: HashMap<Object, Vec<Unread>>,
    /// The empty `Allowed` that views read when allowed-to-call is off.
    no_calls: Allowed,
    pub workload_findings: Vec<(WorkloadFinding, Linked)>,
    /// The images that the running pods use, and which of them lack
    /// labels.
    pub census: Census,
    edge_sets: Memo<Shown, EdgeSet>,
    minutes: Memo<i64, Minute>,
    /// Every way in, computed when the model is built.
    ways: Memo<(), Ways>,
    /// The workloads that serve a port, which have an access tab.
    serving: HashSet<Object>,
    /// The overview URL of each namespace and of each workload in
    /// `serving`, the objects that have an access page.
    access_paths: HashSet<String>,
    /// The quick-search index.
    pub search: Index,
    /// What each account may do, and where, computed from RBAC.
    pub access: Arc<Access>,
    /// A fingerprint of the RBAC objects that `access` was computed
    /// from. The next model reuses `access` when the fingerprint is the
    /// same, because computing it is costly.
    access_key: u64,
    /// The findings in the vulnerability scanners' reports.
    pub vulnerabilities: Arc<transect_supply::vulnerabilities::Vulnerabilities>,
}

impl Model {
    /// Builds the model of the cluster's current state. Its deleted pods
    /// are those of `previous` plus those deleted since.
    pub fn take(
        cluster: &Cluster,
        images: Arc<Known>,
        resources: Arc<Loaded>,
        signals: Signals,
        previous: Option<&Model>,
    ) -> Self {
        let snapshot = Snapshot::take(cluster, images, resources, signals);
        let departed = match previous {
            Some(previous) => Departed::after(
                Before {
                    departed: &previous.departed,
                    pods: &previous.snapshot.pods,
                    ownership: &previous.snapshot.ownership,
                },
                &snapshot.pods,
                Instant::now(),
            ),
            None => Departed::default(),
        };
        let reduced = previous.map(|model| (model.access_key, model.access.clone()));
        Self {
            departed,
            ..Self::build_on(snapshot, reduced)
        }
    }

    /// Builds the model of `snapshot` with no earlier model, for the
    /// fixture.
    #[cfg(any(test, debug_assertions))]
    pub fn build(snapshot: Snapshot) -> Self {
        Self::build_on(snapshot, None)
    }

    /// Builds the model of `snapshot`. `reduced` is an earlier model's
    /// `access` with its fingerprint.
    fn build_on(snapshot: Snapshot, reduced: Option<(u64, Arc<Access>)>) -> Self {
        let (access_key, access) = {
            let inputs = snapshot.access_inputs();
            let key = fingerprint(&inputs);
            match reduced {
                Some((earlier, access)) if earlier == key => (key, access),
                _ => {
                    let started = Instant::now();
                    let access = Access::build(&inputs);
                    tracing::debug!(elapsed = ?started.elapsed(), "reduced access from RBAC");
                    (key, Arc::new(access))
                }
            }
        };
        let workloads = snapshot.workloads();
        let allowed = snapshot.allowed(&workloads);
        let (graph, unread) = snapshot.graph(&workloads, &allowed);
        let workload_findings = snapshot.workload_findings(&workloads);
        let serving = workloads
            .owners()
            .filter(|owner| {
                let namespace = owner.namespace.as_deref().unwrap_or_default();
                let resource = transect_graph::links::kinds::workload(owner, &snapshot.plurals);
                resource.is_some() && snapshot.ownership.workloads(namespace).any(|w| w == *owner)
            })
            .filter(|owner| {
                let end = transect_graph::edges::Endpoint::Object((*owner).clone());
                let selected = !graph
                    .reaching(transect_graph::edges::Kind::Selects, &end)
                    .is_empty();
                transect_access::access::serves(&workloads.get(owner), selected)
            })
            .cloned()
            .collect();
        drop(workloads);
        let census = images::census(&snapshot.pods, &snapshot.images);
        let vulnerabilities = Arc::new(Vulnerabilities::read(Stores {
            knowledge: &snapshot.resources.kinds,
            kinds: &snapshot.kinds,
            custom: &snapshot.custom,
            ownership: &snapshot.ownership,
            pods: &snapshot.pods,
        }));
        let search = index::index(&snapshot, &graph, &access, &serving).with(
            transect_map::search::vulnerabilities::entries(&vulnerabilities),
        );
        let namespaces = snapshot.namespaces.iter();
        let namespaces = namespaces.filter_map(|namespace| namespace.metadata.name.as_deref());
        let access_paths =
            namespaces
                .map(transect_graph::links::namespace)
                .chain(serving.iter().filter_map(|workload| {
                    transect_graph::links::page(workload, &snapshot.plurals)
                }))
                .collect();
        let model = Self {
            snapshot,
            departed: Departed::default(),
            allowed,
            graph,
            unread,
            no_calls: Allowed::default(),
            workload_findings,
            census,
            edge_sets: Memo::new(EDGE_SETS),
            minutes: Memo::new(MINUTES),
            ways: Memo::new(1),
            serving,
            access_paths,
            search,
            access,
            access_key,
            vulnerabilities,
        };
        // Compute the values that most first renders after a change
        // read: the edge set with every kind shown, the current minute,
        // and the ways in.
        model.edge_set(&Shown::default());
        model.minute(Timestamp::now());
        let started = Instant::now();
        model.ways();
        tracing::debug!(elapsed = ?started.elapsed(), "read the ways in");
        model
    }

    /// The graph with only the edge kinds that `shown` turns on.
    pub fn graph(&self, shown: &Shown) -> Graph {
        self.graph.only(shown)
    }

    /// The `EdgeSet` of the graph with only the kinds that `shown` turns
    /// on, cached for each `Shown`.
    pub fn edge_set(&self, shown: &Shown) -> Arc<EdgeSet> {
        let make = || EdgeSet::read(&self.snapshot, &self.graph(shown));
        self.edge_sets.get(shown.clone(), make)
    }

    /// Whether `workload` serves a port, and so has an access tab.
    pub fn serves(&self, workload: &Object) -> bool {
        self.serving.contains(workload)
    }

    /// The overview URL of each object that has an access page. On a
    /// page of the access lens, a link to one of these goes to the
    /// object's access page instead.
    pub fn access_paths(&self) -> &HashSet<String> {
        &self.access_paths
    }

    /// How many ways in reach each workload that serves a port, for the
    /// count beside the link to its access tab.
    pub fn ways_in(&self) -> HashMap<Object, usize> {
        let ways = self.ways();
        let reaching = |workload: &Object| {
            let rows = ways.rows.iter();
            rows.filter(|way| way.reaches.contains(workload)).count()
        };
        let served = self.serving.iter();
        served
            .map(|workload| (workload.clone(), reaching(workload)))
            .collect()
    }

    /// Every way in, with the gates along it, cached for the model's
    /// life.
    pub fn ways(&self) -> Arc<Ways> {
        self.ways.get((), || super::pages::read_ways(self))
    }

    /// The values read from the snapshot for the minute of `now`,
    /// cached for each minute.
    pub fn minute(&self, now: Timestamp) -> Arc<Minute> {
        let make = || Minute::read(&self.snapshot, now);
        self.minutes.get(Minute::of(now), make)
    }

    /// Which workloads the `NetworkPolicy` objects allow to call which,
    /// or an empty `Allowed` when `shown` turns allowed-to-call off, so
    /// that no tag or card line shows a policy.
    pub fn allowed(&self, shown: &Shown) -> &Allowed {
        if shown.is_on(Kind::AllowedToCall) {
            &self.allowed
        } else {
            &self.no_calls
        }
    }
}
