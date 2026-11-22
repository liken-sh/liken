//! Builds the edge graph of one snapshot from every source of edges,
//! including the edges that intent declares.

use std::collections::{HashMap, HashSet};

use k8s_openapi::api::core::v1::Pod;

use super::Snapshot;
use transect_cluster::ownership;
use transect_cluster::workloads::Workloads;
use transect_graph::edges::{Graph, Kind, Object};
use transect_ground::devices;
use transect_ground::machines;
use transect_ground::storage;
use transect_network::addresses::{self, Readers, Stated, StatedReading, Unread};
use transect_network::entry;
use transect_network::policy::Allowed;
use transect_resources::intent::{Listed, ObjectIntent};

/// Picks one list out of the intent written on an object.
type Field = for<'a> fn(&'a ObjectIntent) -> Option<&'a Listed>;

impl Snapshot {
    /// The graph of every edge that the stores and intent give, and the
    /// intent entries that match nothing `transect` can read, grouped by
    /// the workload whose card shows them. `workloads` supplies the pod
    /// labels that a `Service`'s selector matches, and the pod templates
    /// whose addresses resolve, through the selects and routes-to edges,
    /// to the workloads behind them.
    pub fn graph(
        &self,
        workloads: &Workloads,
        allowed: &Allowed,
    ) -> (Graph, HashMap<Object, Vec<Unread>>) {
        let labelled = self
            .ownership
            .all_workloads()
            .map(|object| (object, workloads.get(object).pod_labels()));
        let mut graph = Graph::new([
            machines::runs_on(self.pods.iter().map(AsRef::as_ref)),
            self.ownership.edges(),
            storage::stores_on(
                &self.storage,
                self.pods.iter().map(AsRef::as_ref),
                &self.ownership,
            ),
            entry::selects(&self.services, labelled),
            entry::routes_to(&self.ingresses),
        ]);
        let templates = self
            .ownership
            .all_workloads()
            .map(|object| (object, workloads.get(object).templates()));
        let depends = addresses::depends_on(&self.addresses, &graph, &self.ownership, templates);
        graph.add(depends);
        let stated = self.stated(&graph, workloads);
        graph.add(self.outsides.fold(stated.edges));
        if self.gitops.off.is_none() {
            graph.add(self.gitops.edges(&self.applied));
            let sources =
                addresses::sources_depend_on(&self.addresses, &graph, self.gitops.sources());
            graph.add(sources);
        }
        let pods: Vec<&Pod> = self.pods.iter().map(AsRef::as_ref).collect();
        let cluster_scoped: Vec<Object> = self
            .custom_objects()
            .into_iter()
            .filter(|object| object.namespace.is_none())
            .collect();
        let by_object = ownership::controlled_by(
            &self.grants,
            &self.status_managers,
            &self.plurals,
            &pods,
            &self.ownership,
            &self.kinds,
            &cluster_scoped,
        );
        let by_kind = self.controlled_by_kind(&pods, &by_object);
        graph.add(by_object);
        graph.add(by_kind);
        graph.add(devices::holds(
            &self.devices,
            pods.iter().copied(),
            &self.ownership,
        ));
        let known = self.known_objects();
        let readers = Readers::default();
        let reading = self.reading(&graph, &known, &readers);
        let edges = self.known_edges(&graph, &reading);
        let guessed = self.guessed_edges(&graph, &reading, &known);
        let referred = self.referred_edges(&graph, &reading);
        let between = self.device_edges(&reading);
        graph.add(self.outsides.fold(edges));
        graph.add(guessed);
        graph.add(self.outsides.fold(referred));
        graph.add(between);
        // The policy edges are numerous. Adding them last means the
        // queries that the sources above make do not scan them.
        graph.add(allowed.edges());
        (graph, stated.unread)
    }

    /// The controlled-by edges for custom resources that `by_object` has
    /// no edge for, found from the kind of the resource.
    fn controlled_by_kind(
        &self,
        pods: &[&Pod],
        by_object: &[transect_graph::edges::Edge],
    ) -> Vec<transect_graph::edges::Edge> {
        let attributed: HashSet<&Object> = by_object
            .iter()
            .filter_map(|edge| match &edge.from {
                transect_graph::edges::Endpoint::Object(object) => Some(object),
                _ => None,
            })
            .collect();
        let custom: Vec<(String, String, Vec<Object>)> = self
            .custom
            .iter()
            .map(|(api, found)| {
                let objects = found.iter().filter_map(|found| {
                    let meta = &found.metadata;
                    let name = meta.name.as_deref()?;
                    Some(Object::new(
                        &api.group,
                        &api.kind,
                        meta.namespace.as_deref(),
                        name,
                    ))
                });
                (api.group.clone(), api.kind.clone(), objects.collect())
            })
            .collect();
        let workloads = self.packaged_workloads();
        let sources = ownership::KindSources {
            custom: &custom,
            packages: &self.paged.packages,
            workloads: &workloads,
            pods,
            ownership: &self.ownership,
            kinds: &self.kinds,
        };
        ownership::controlled_by_kind(&sources, &attributed)
    }

    /// Each `Deployment`, `StatefulSet`, and `DaemonSet`, with the
    /// package that its labels and annotations identify.
    fn packaged_workloads(&self) -> Vec<(Object, ownership::Package)> {
        let of = |kind: &str, meta: &k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta| {
            let name = meta.name.as_deref()?;
            let object = Object::new("apps", kind, meta.namespace.as_deref(), name);
            Some((object, ownership::package(meta)))
        };
        let deployments = self
            .deployments
            .iter()
            .filter_map(|d| of("Deployment", &d.metadata));
        let sets = self
            .stateful_sets
            .iter()
            .filter_map(|s| of("StatefulSet", &s.metadata));
        let daemons = self
            .daemon_sets
            .iter()
            .filter_map(|d| of("DaemonSet", &d.metadata));
        deployments.chain(sets).chain(daemons).collect()
    }

    /// The edges from the `depends-on` and `authenticates-through` lists
    /// of intent, resolved against `graph` the same way as the addresses
    /// in configuration.
    fn stated(&self, graph: &Graph, workloads: &Workloads) -> Stated {
        let described = self.described();
        let known = self.known_objects();
        let templates = self
            .ownership
            .all_workloads()
            .map(|object| (object, workloads.get(object).templates()));
        let readers = addresses::readers(templates).with_written(self.secrets_written());
        let reading = self.reading(graph, &known, &readers);
        let mut stated = Stated::default();
        let lists = [
            (
                Kind::DependsOn,
                (|intent| intent.depends_on.as_ref()) as Field,
            ),
            (Kind::AuthenticatesThrough, |intent| {
                intent.authenticates_through.as_ref()
            }),
        ];
        for (kind, field) in lists {
            let lists = described.iter().filter_map(|(object, _)| {
                let listed = field(self.intents.of(object)?)?;
                Some((object, listed))
            });
            stated.extend(Stated::read(kind, &reading, lists));
        }
        stated
    }

    /// Every object that an intent entry or a reference can point to:
    /// each object that intent can describe, and every workload.
    pub(super) fn known_objects(&self) -> HashSet<Object> {
        let mut known: HashSet<Object> = self.described().into_iter().map(|(o, _)| o).collect();
        known.extend(self.ownership.all_workloads().cloned());
        known
    }

    /// The context for resolving intent entries and references to objects.
    pub(super) fn reading<'a>(
        &'a self,
        graph: &'a Graph,
        known: &'a HashSet<Object>,
        readers: &'a Readers,
    ) -> StatedReading<'a> {
        StatedReading {
            addresses: &self.addresses,
            graph,
            ownership: &self.ownership,
            plurals: &self.plurals,
            known,
            readers,
        }
    }
}
