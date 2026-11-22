//! The edges that leave an object and the edges that reach it, grouped
//! by kind, so a page lists everything the graph connects to the
//! object.

use super::super::super::snapshot::Snapshot;
use super::{refer, refer_object};
use transect_graph::edges::{Endpoint, Graph, Kind, Object};
use transect_map::objects::{Section, Side};
use transect_resources::knowledge;

/// The edge kinds that `edges` lists in both directions. `edges` lists
/// only the `ControlledBy` edges that reach the object, because `about`
/// lists the ones that leave it. `part_of` lists `PartOf`.
const KINDS: [Kind; 10] = [
    Kind::RoutesTo,
    Kind::Selects,
    Kind::DependsOn,
    Kind::AuthenticatesThrough,
    Kind::StoresOn,
    Kind::Holds,
    Kind::ReconcilesFrom,
    Kind::ManagedBy,
    Kind::AllowedToCall,
    Kind::RunsOn,
];

/// Two sections. "Refers to" lists the target of each edge that leaves
/// `object`, and each object that its annotations name, as the
/// `referencedBy` field of a `KindKnowledge` declares. "Referred to by"
/// lists the source of each edge that reaches `object`, and each object
/// whose annotations name it.
pub fn edges(snapshot: &Snapshot, object: &Object, graph: &Graph) -> Vec<Section> {
    let here = object.namespace.as_deref();
    let end = Endpoint::Object(object.clone());
    let (mut out, mut into) = (
        Section::new("Refers to", Side::Out),
        Section::new("Referred to by", Side::Into),
    );
    for kind in KINDS {
        let leaving = graph.leaving(kind, &end).into_iter();
        out = out.fact(
            kind.words(),
            leaving.map(|e| refer(snapshot, e, here)).collect(),
        );
        let reaching = graph.reaching(kind, &end).into_iter();
        into = into.fact(
            kind.toward(),
            reaching.map(|e| refer(snapshot, e, here)).collect(),
        );
    }
    let controls = graph.reaching(Kind::ControlledBy, &end).into_iter();
    into = into.fact(
        Kind::ControlledBy.toward(),
        controls.map(|e| refer(snapshot, e, here)).collect(),
    );
    let referrals = knowledge::referrals(
        &snapshot.kinds,
        &snapshot.custom_objects(),
        &snapshot.described(),
    );
    let named = referrals.iter().filter(|r| &r.referrer == object);
    let named = named.map(|r| refer_object(snapshot, &r.target, here));
    out = out.fact("in its annotations", named.collect());
    let naming = referrals.iter().filter(|r| &r.target == object);
    let naming = naming.map(|r| refer_object(snapshot, &r.referrer, here));
    into = into.fact("in their annotations", naming.collect());
    vec![out, into]
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::super::super::fixture;
    use super::*;

    /// The labels of `object`'s section `title`, each with the names
    /// listed under it.
    fn facts(object: &Object, title: &str) -> Vec<(String, Vec<String>)> {
        let model = fixture::model();
        let graph = model.graph();
        let sections = edges(&model.snapshot, object, graph);
        let section = sections.into_iter().find(|s| s.title == title).unwrap();
        let facts = section.facts.into_iter();
        facts
            .map(|fact| {
                (
                    fact.label,
                    fact.values.into_iter().map(|r| r.text).collect(),
                )
            })
            .collect()
    }

    #[rstest]
    fn names_what_reaches_an_object_from_its_own_side() {
        let service = Object::new("", "Service", Some("acme-shop"), "storefront-svc");
        let named_by = facts(&service, "Referred to by");
        let routes = named_by.iter().find(|(label, _)| label == "routes to it");
        assert_eq!(
            routes.map(|(_, names)| names.clone()),
            Some(vec![
                "Ingress storefront-route".to_string(),
                "Ingress storefront-admin".to_string()
            ])
        );
    }
}
