//! What a pattern's module reads of the cluster for the page of a store:
//! the references of the store, which the snapshot follows as it does
//! for every page, the controller that the graph finds, and the
//! readiness and the endpoints that the stores of `Pod`s and
//! `EndpointSlice`s give.

use super::super::super::snapshot::Snapshot;
use transect_graph::edges::{Graph, Object};
use transect_map::map::controller;
use transect_network::entry::Endpoints;
use transect_resources::knowledge::catalog::Cluster;
use transect_resources::knowledge::{Followed, ReferenceEnds};

/// The snapshot and the graph of one page, as a pattern's module reads
/// them.
pub(super) struct Facts<'a> {
    pub snapshot: &'a Snapshot,
    pub graph: &'a Graph,
    /// The page's endpoints, built once for every part and line.
    pub endpoints: &'a Endpoints,
}

impl ReferenceEnds for Facts<'_> {
    fn ends(&self, object: &Object, path: &str) -> Vec<Followed<'_>> {
        self.snapshot.ends(object, path)
    }
}

impl Cluster for Facts<'_> {
    fn controller(&self, object: &Object) -> Option<Object> {
        controller(self.graph, object)
    }

    fn ready(&self, pod: &Object) -> Option<bool> {
        super::core::ready(self.snapshot, pod)
    }

    fn endpoints(&self, service: &Object) -> Option<Vec<Object>> {
        super::core::endpoints(self.snapshot, self.endpoints, service)
    }
}
