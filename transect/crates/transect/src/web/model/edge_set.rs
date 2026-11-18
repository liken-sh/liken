//! The results that a model computes from its graph for one set of
//! enabled edge kinds. Each result reads the whole graph, and every view
//! with the same edge kinds needs the same results, so a model computes
//! them once for each edge set that its views use.

use crate::web::snapshot::Snapshot;
use transect_graph::edges::{Graph, Loop};
use transect_supply::changed::Recorded;
use transect_supply::forge::Forges;

pub struct EdgeSet {
    pub loops: Vec<Loop>,
    /// The forges that the resources declare, and the forges that run in
    /// the cluster behind an `Ingress` host. A forge in the cluster is
    /// found only when this edge set has the `Selects` edges from the
    /// `Ingress`'s `Service` to the workload.
    pub forges: Forges,
    pub recorded: Recorded,
}

impl EdgeSet {
    /// `graph` has only the edges of this edge set's kinds.
    pub fn read(snapshot: &Snapshot, graph: &Graph) -> Self {
        let forges = snapshot.forges(graph, &snapshot.workloads());
        Self {
            loops: graph.loops(),
            recorded: snapshot.recorded(&forges),
            forges,
        }
    }
}
