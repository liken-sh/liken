//! What every view reads from one change of the cluster: the snapshot,
//! the `NetworkPolicy` evaluation, the edge graph with every kind on, and
//! the findings about each workload and image.  These are the costly
//! steps of a render, and none depends on the view, so `latest` builds
//! them once per change and each view turns kinds off in its own copy
//! of the graph.  What depends on the edge kinds a view shows, or on
//! the minute it renders in, the model makes on the first request and
//! keeps for every render after it.

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

/// How many edge sets a model keeps derived values for.
const EDGE_SETS: usize = 8;

/// How many minutes a model keeps derived values for: the one a render
/// is in, and the one before it, which a render begun just before the
/// minute turned may still request.
const MINUTES: usize = 2;

pub struct Model {
    pub snapshot: Snapshot,
    /// The owners of pods gone since earlier models, which a gone pod's
    /// page links to.
    pub departed: Departed,
    allowed: Allowed,
    graph: Graph,
    /// The entries of intent that name nothing `transect` can read, by
    /// the workload whose card shows them.
    pub unread: HashMap<Object, Vec<Unread>>,
    /// The evaluation a view with allowed-to-call turned off reads.
    no_calls: Allowed,
    /// The findings about each workload, with the workload each is
    /// about.
    pub workload_findings: Vec<(WorkloadFinding, Linked)>,
    /// The images the running pods run, and which lack labels.
    pub census: Census,
    edge_sets: Memo<Shown, EdgeSet>,
    minutes: Memo<i64, Minute>,
    /// Every way in, read once per model as it is built.
    ways: Memo<(), Ways>,
    /// The workloads that serve a port, which have an access tab.
    serving: HashSet<Object>,
    /// The usual page of each object with an access page: every
    /// namespace and every workload in `serving`.
    access_paths: HashSet<String>,
    /// What quick search finds, built once per change.
    pub search: Index,
    /// What every account may do, and where, from RBAC.
    pub access: Arc<Access>,
    /// The fingerprint of what `access` was reduced from, so the next
    /// model reduces again only when that changed.
    access_key: u64,
    /// What the scanners' reports hold, read once per model.
    pub vulnerabilities: Arc<transect_supply::vulnerabilities::Vulnerabilities>,
}

impl Model {
    /// The model of `cluster` as of now, keeping the gone pods that
    /// `previous` held and adding those that left since it.
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

    /// The model of `snapshot` alone, as the fixture builds it.
    #[cfg(any(test, debug_assertions))]
    pub fn build(snapshot: Snapshot) -> Self {
        Self::build_on(snapshot, None)
    }

    /// The model of `snapshot`, keeping `reduced`, the access an
    /// earlier model reduced with its fingerprint, when nothing it was
    /// reduced from changed.
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
        // Most views show every kind, so their first render after a
        // change finds this edge set and this minute made already, and
        // an access page finds the ways in read.
        model.edge_set(&Shown::default());
        model.minute(Timestamp::now());
        let started = Instant::now();
        model.ways();
        tracing::debug!(elapsed = ?started.elapsed(), "read the ways in");
        model
    }

    /// The graph with only the kinds `shown` turns on.
    pub fn graph(&self, shown: &Shown) -> Graph {
        self.graph.only(shown)
    }

    /// What the graph with only the kinds `shown` turns on gives.
    pub fn edge_set(&self, shown: &Shown) -> Arc<EdgeSet> {
        let make = || EdgeSet::read(&self.snapshot, &self.graph(shown));
        self.edge_sets.get(shown.clone(), make)
    }

    /// Whether `workload` serves a port, and so has an access tab.
    pub fn serves(&self, workload: &Object) -> bool {
        self.serving.contains(workload)
    }

    /// The usual page of each object with an access page.  On a page of
    /// the access lens, a link to one of these pages goes to the object's
    /// access page instead.
    pub fn access_paths(&self) -> &HashSet<String> {
        &self.access_paths
    }

    /// How many ways in reach each workload that serves a port, which
    /// its card counts beside the link to its access tab.
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

    /// Every way in, with the gates on the way to it, read as the model
    /// is built and kept for its life.
    pub fn ways(&self) -> Arc<Ways> {
        self.ways.get((), || super::pages::read_ways(self))
    }

    /// What the snapshot gives in the minute `now` is in.
    pub fn minute(&self, now: Timestamp) -> Arc<Minute> {
        let make = || Minute::read(&self.snapshot, now);
        self.minutes.get(Minute::of(now), make)
    }

    /// Who the NetworkPolicies let call whom.  With allowed-to-call
    /// turned off, the summary holds no namespace, so no tag or card line
    /// reads one.
    pub fn allowed(&self, shown: &Shown) -> &Allowed {
        if shown.is_on(Kind::AllowedToCall) {
            &self.allowed
        } else {
            &self.no_calls
        }
    }
}
