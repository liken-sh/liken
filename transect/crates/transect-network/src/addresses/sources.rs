//! Depends-on edges from GitOps sources.  When the cluster serves a
//! source's URL, for example at a forge's `Ingress` host, the source
//! depends on the workload behind that URL.  So the map shows a cycle
//! when a forge serves the manifests that a GitOps tool applies to the
//! forge itself, which a person must break by hand when the cluster
//! starts cold.

use super::depends::edges_to;
use super::index::Addresses;
use super::parse;
use super::reach::reach;
use transect_graph::edges::{Edge, Endpoint, Graph, Object};

/// One depends-on edge from each source to each end that its URL
/// reaches.  A short name in a URL resolves in the source's namespace,
/// as `source-controller`'s own lookups of it would from there.
pub fn sources_depend_on<'a>(
    addresses: &Addresses,
    graph: &Graph,
    sources: impl IntoIterator<Item = (&'a Object, &'a str)>,
) -> Vec<Edge> {
    let mut edges = Vec::new();
    for (source, url) in sources {
        let from = Endpoint::Object(source.clone());
        let reached = parse::addresses(url).into_iter().map(|address| {
            let found = reach(addresses, graph, &address, source.namespace.as_deref());
            (found.to, found.guessed)
        });
        edges.extend(edges_to(&from, reached));
    }
    edges
}
