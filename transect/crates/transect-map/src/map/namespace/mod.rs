//! The namespace page shows one namespace opened in its band of the
//! cluster map. It draws a box for each workload, or a matrix of
//! DaemonSets by machine for a namespace of DaemonSets, and groups the
//! namespace's other custom resources by kind below them. The other
//! bands fold to one line each, and the machines appear as a row of
//! chips that count the namespace's pods.

mod boxes;
mod folded;
mod line;
mod managed;
mod matrix;
mod sentence;

use std::collections::{BTreeSet, HashMap};
use std::sync::Arc;

use askama::Template;
use k8s_openapi::api::core::v1::{Node, Pod, Service};
use k8s_openapi::jiff::Timestamp;

pub use boxes::WorkloadBox;
use boxes::boxes;
pub use folded::{Folded, MachineChip, fold, machine_chips};
pub use matrix::Matrix;
use matrix::{Sources, draws_as_matrix};

use super::ClusterMap;
use crate::cards::NamespaceCard;
use crate::objects::Group;
use transect_cluster::ownership::Ownership;
use transect_cluster::workloads::Objects;
use transect_frame::frame::crumbs::Crumb;
use transect_graph::edges::{Graph, Loop, Object, Plurals};
use transect_graph::pods::finished;
use transect_ground::machines::Machine;
use transect_ground::storage::{self, Counts, Storage};
use transect_observe::health::Health;

/// The namespace page. Its `main` block also renders alone, as the
/// fragment that a pushed update replaces.
#[derive(Template)]
#[template(path = "map/namespace/namespace.html", blocks = ["main"])]
pub struct NamespacePage {
    /// The main column's row of edge kinds, set by
    /// `with_edge_row`.
    pub edge_row: super::EdgeRow,
    pub namespace: String,
    /// The band that contains the namespace, shown in the crumb.
    pub layer: String,
    pub bands: Vec<Folded>,
    /// The counts beside the namespace's name, such as "7 workloads · 16
    /// pods".
    pub header: String,
    pub boxes: Vec<WorkloadBox>,
    /// The namespace's custom resources that are not workloads, one
    /// group per kind, drawn below the boxes.
    pub resources: Vec<Group>,
    /// The matrix that a namespace of DaemonSets draws instead of its
    /// boxes. `boxes` is empty when this is set.
    pub matrix: Option<Matrix>,
    pub machines: Vec<MachineChip>,
    /// Each backend outside the cluster that stores the namespace's
    /// volumes, with its counts, for the outside-systems band folded to one
    /// line.
    pub ground: Vec<(String, Counts)>,
    pub card: NamespaceCard,
}

/// What `NamespacePage::build` reads to draw the page.
pub struct Contents<'a> {
    /// The namespace's top owners.
    pub workloads: Vec<&'a Object>,
    /// Every pod in the cluster. `build` selects the namespace's pods.
    pub pods: &'a [Arc<Pod>],
    /// The graph. Its controlled-by edges give the controller of each
    /// custom resource.
    pub graph: &'a Graph,
    /// The ownership index, which maps a pod to its top owner.
    pub ownership: &'a Ownership,
    pub objects: &'a Objects<'a>,
    pub services: &'a [Arc<Service>],
    pub by_workload: &'a HashMap<Object, Health>,
    pub storage: &'a Storage,
    /// The plurals of the custom kinds, used in each box's link.
    pub plurals: &'a Plurals,
    /// Every loop in the cluster. The folded bands tag the other
    /// namespaces of each loop that this namespace is in.
    pub loops: &'a [Loop],
    /// Every Node. The matrix reads their labels and taints to show why
    /// a DaemonSet has no pod on a node.
    pub nodes: &'a [Arc<Node>],
    /// The custom resources that are not workloads, one group per kind.
    pub resources: Vec<Group>,
    pub now: Timestamp,
}

impl NamespacePage {
    pub fn with_edge_row(self, edge_row: super::EdgeRow) -> Self {
        Self { edge_row, ..self }
    }

    /// The page for `namespace`, or `None` when no band of `map`
    /// contains it. `machines` supplies the machine chips, each with
    /// its count of the namespace's pods.
    pub fn build(
        map: &ClusterMap,
        namespace: &str,
        contents: Contents,
        machines: &[Machine],
        card: NamespaceCard,
    ) -> Option<Self> {
        let layer = map.layer_of(namespace)?.to_owned();
        let pods: Vec<&Pod> = contents
            .pods
            .iter()
            .map(AsRef::as_ref)
            .filter(|pod| pod.metadata.namespace.as_deref() == Some(namespace))
            .collect();
        let by_owner = pods_by_owner(namespace, &pods, contents.ownership);
        let running = pods.iter().filter(|pod| !finished(pod)).count();
        let matrix = draws_as_matrix(&contents.workloads, &by_owner).then(|| {
            let sources = Sources {
                by_owner: &by_owner,
                daemon_sets: contents.objects.daemon_sets,
                nodes: contents.nodes,
                by_workload: contents.by_workload,
                plurals: contents.plurals,
                now: contents.now,
            };
            matrix::matrix(&contents.workloads, &sources)
        });
        let workloads = if matrix.is_some() {
            Vec::new()
        } else {
            contents.workloads.clone()
        };
        Some(Self {
            edge_row: super::EdgeRow::default(),
            namespace: namespace.into(),
            layer,
            bands: fold(map, namespace, &looped_with(contents.loops, namespace)),
            header: sentence::header(
                contents.workloads.len(),
                running,
                contents.storage.claims_in(namespace),
            ),
            boxes: boxes(
                &workloads,
                &by_owner,
                contents.objects,
                contents.services,
                contents.by_workload,
                contents.plurals,
                &managed::managed_by(contents.graph, namespace),
            ),
            resources: contents.resources,
            matrix,
            machines: machine_chips(machines),
            ground: storage::under(contents.storage, namespace),
            card,
        })
    }

    /// The crumbs: home, the layer, and the namespace. The layer links
    /// to the cluster map with this namespace still selected, so a
    /// reader who leaves through the layer returns to the same map.
    pub fn crumbs(&self) -> Vec<Crumb> {
        let select = format!("/?select={}", self.namespace);
        vec![
            Crumb::home(),
            Crumb::link(&self.layer, format!("{select}#{}", self.layer)),
            Crumb::current_name(&self.namespace),
        ]
    }

    pub fn events_url(&self) -> String {
        format!("/events/namespaces/{}", self.namespace)
    }

    /// Whether the card links to the namespace's access page.
    pub fn card_lens(&self) -> bool {
        true
    }

    /// Always `None`, because this page shows the card and its name
    /// does not link to itself.
    pub fn card_link(&self) -> Option<String> {
        None
    }

    /// The lens tabs: the overview, which is this page, and access.
    pub fn tabs(&self) -> transect_frame::frame::tabs::Tabs {
        transect_frame::frame::tabs::Tabs::namespace(
            &transect_graph::links::namespace(&self.namespace),
            transect_frame::frame::tabs::Lens::Overview,
        )
    }

    /// Always false, because the main column already draws each
    /// workload as a box.
    pub fn card_workloads(&self) -> bool {
        false
    }

    pub fn daemon_sets_label(&self) -> &'static str {
        matrix::DAEMON_SETS
    }

    pub fn others_label(&self) -> &'static str {
        matrix::OTHERS
    }

    pub fn legend(&self) -> &'static [(&'static str, &'static str, &'static str)] {
        &matrix::LEGEND
    }

    /// The text of the ground line when the namespace stores nothing
    /// outside the cluster.
    pub fn nothing_under(&self) -> &'static str {
        storage::NOTHING_UNDER
    }
}

/// The names of the other namespaces in each loop of `loops` that
/// contains `namespace`.
fn looped_with(loops: &[Loop], namespace: &str) -> BTreeSet<String> {
    let members = loops
        .iter()
        .filter(|found| found.holds(namespace))
        .flat_map(|found| &found.namespaces);
    members
        .filter(|&member| member != namespace)
        .cloned()
        .collect()
}

/// Groups the pods by their top owner. A pod with no owner is its own
/// top owner.
fn pods_by_owner<'a>(
    namespace: &str,
    pods: &[&'a Pod],
    ownership: &Ownership,
) -> HashMap<Object, Vec<&'a Pod>> {
    let mut by_owner: HashMap<Object, Vec<&Pod>> = HashMap::new();
    for &pod in pods {
        let name = pod.metadata.name.as_deref().unwrap_or_default();
        let owner = ownership.top_owner(&Object::new("", "Pod", Some(namespace), name));
        by_owner.entry(owner).or_default().push(pod);
    }
    by_owner
}
