//! The edge graph of one snapshot: every source's edges, those intent
//! states among them, and the kinds a reader can turn on and off.

use std::collections::{HashMap, HashSet};

use k8s_openapi::api::core::v1::Pod;

use super::Snapshot;
use transect_cluster::ownership;
use transect_cluster::workloads::Workloads;
use transect_graph::edges::{Graph, Kind, Object, TOGGLED};
use transect_ground::devices;
use transect_ground::machines;
use transect_ground::storage;
use transect_network::addresses::{self, Readers, Stated, StatedReading, Unread};
use transect_network::entry;
use transect_network::policy::Allowed;
use transect_resources::intent::{Listed, Said};

/// One list field of what intent states about an object.
type Field = for<'a> fn(&'a Said) -> Option<&'a Listed>;

impl Snapshot {
    /// Every edge the stores and intent give, and the entries of intent
    /// that name nothing `transect` can read, by the workload whose card
    /// shows them.  `workloads` gives each workload's pod labels, which
    /// a `Service`'s selector is matched against, and its pod specs, whose
    /// addresses resolve through the selects and routes-to edges to the
    /// workloads behind them.
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
        // The policy edges are added last, because they are the most
        // numerous kind after pods and every query the sources above
        // make would scan them.
        graph.add(allowed.edges());
        (graph, stated.unread)
    }

    /// The controlled-by edges of the custom resources `by_object` gave
    /// no controller, attributed by kind.
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
    /// package its labels and annotations name.
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

    /// The edges intent's `depends-on` and `authenticates-through` lists
    /// draw, resolved against `graph` as the configured addresses are.
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
            (Kind::DependsOn, (|said| said.depends_on.as_ref()) as Field),
            (Kind::AuthenticatesThrough, |said| {
                said.authenticates_through.as_ref()
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

    /// Every object `transect` watches that an entry or a reference can
    /// name: each object intent can be written on, and every workload.
    pub(super) fn known_objects(&self) -> HashSet<Object> {
        let mut known: HashSet<Object> = self.described().into_iter().map(|(o, _)| o).collect();
        known.extend(self.ownership.all_workloads().cloned());
        known
    }

    /// What entries and references resolve against.
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

    /// The edge kinds a reader can turn on and off here: every kind
    /// whose source this cluster has.  A kind with an edge always has
    /// its source, so these are also every kind with an edge.
    pub fn offered(&self) -> Vec<Kind> {
        TOGGLED
            .into_iter()
            .filter(|kind| match kind {
                Kind::ReconcilesFrom => self.gitops.off.is_none(),
                Kind::Holds => self.devices.off.is_none(),
                _ => true,
            })
            .collect()
    }
}
