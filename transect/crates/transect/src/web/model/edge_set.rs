//! What a model derives from its graph with one set of edge kinds on:
//! the loops, the forges, and the changes the objects record.  Each
//! reads the whole graph, and every view of one edge set reads them
//! alike, so a model makes them once per edge set its views show.

use crate::web::snapshot::Snapshot;
use transect_graph::edges::{Graph, Loop};
use transect_supply::changed::Recorded;
use transect_supply::forge::Forges;

pub struct EdgeSet {
    /// The loops among the kinds on, ordered by their namespaces.
    pub loops: Vec<Loop>,
    /// The forges the cluster serves, through the Services the kinds
    /// on select.
    pub forges: Forges,
    /// Every change the objects record, before a render arranges them
    /// as of its time.
    pub recorded: Recorded,
}

impl EdgeSet {
    /// What `snapshot` gives through `graph`, which has only the kinds
    /// of one edge set on.
    pub fn read(snapshot: &Snapshot, graph: &Graph) -> Self {
        let forges = snapshot.forges(graph, &snapshot.workloads());
        Self {
            loops: graph.loops(),
            recorded: snapshot.recorded(&forges),
            forges,
        }
    }
}
