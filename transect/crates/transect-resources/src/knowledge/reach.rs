//! What a relation reaches on the map, and the edges kind knowledge
//! draws from it.  `addresses::relations::reached` resolves what each
//! relation names, because resolving it reads addresses and devices.

use super::references::Relation;
use transect_graph::edges::{Edge, Endpoint, Graph, Object};

/// The ends one relation reaches, and whether they are a guess.
#[derive(Debug, Default, PartialEq)]
pub struct Reached {
    pub ends: Vec<Endpoint>,
    pub guessed: bool,
}

/// An edge of its kind from `from` to each end each of `relations`
/// reaches, with its field as its provenance.  An end that is `from`
/// or one of its own objects draws nothing, nor does an edge `graph`
/// already holds from the API, because the cluster already states it.
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
