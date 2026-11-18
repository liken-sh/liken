//! The edges that kind knowledge draws from an object's relations.  The
//! caller resolves each relation to its ends, because that needs the
//! addresses and devices that `transect-network` reads.

use super::references::Relation;
use transect_graph::edges::{Edge, Endpoint, Graph, Object};

/// The ends that one relation resolves to, and whether they are a guess.
#[derive(Debug, Default, PartialEq)]
pub struct Reached {
    pub ends: Vec<Endpoint>,
    pub guessed: bool,
}

/// An edge from `from` to each end of each relation that has an edge
/// kind, with the relation as its provenance.  An end is skipped when
/// `owns` reports that `from` owns it, when `graph` already has the
/// edge, or when an earlier relation drew the same edge.
pub fn edges(
    from: &Object,
    relations: &[Relation],
    reached: impl Fn(&Relation) -> Reached,
    owns: impl Fn(&Endpoint) -> bool,
    graph: &Graph,
) -> Vec<Edge> {
    let start = Endpoint::Object(from.clone());
    let mut drawn: Vec<Edge> = Vec::new();
    for relation in relations {
        let Some(kind) = relation.edge else {
            continue;
        };
        let found = reached(relation);
        let known = graph.leaving(kind, &start);
        for to in found.ends {
            let seen = drawn.iter().any(|edge| edge.kind == kind && edge.to == to);
            if owns(&to) || known.contains(&&to) || seen {
                continue;
            }
            drawn.push(Edge {
                kind,
                from: start.clone(),
                to,
                guessed: found.guessed,
                provenance: relation.provenance.clone(),
            });
        }
    }
    drawn
}
