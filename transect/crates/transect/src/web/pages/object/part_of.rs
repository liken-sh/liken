//! What an object is part of and what is part of it, from the `part of`
//! edges that kind knowledge creates.

use super::super::super::snapshot::Snapshot;
use super::refer;
use transect_graph::edges::{Endpoint, Graph, Kind, Object};
use transect_map::objects::{Nesting, Part};

/// The first whole that `object` is part of is `frame`, and the other
/// wholes are `also`. `parts` has its parts, each with its own parts,
/// sorted by kind and then by name. The object and its wholes are left
/// out of both levels of parts, so a cycle of `part of` edges does not
/// nest an object inside itself.
pub fn nesting(snapshot: &Snapshot, graph: &Graph, object: &Object) -> Nesting {
    let here = object.namespace.as_deref();
    let end = Endpoint::Object(object.clone());
    let wholes: Vec<&Endpoint> = graph
        .leaving(Kind::PartOf, &end)
        .into_iter()
        .filter(|whole| **whole != end)
        .collect();
    let outside = |other: &&Endpoint| **other != end && !wholes.contains(other);
    let frame = wholes.first().map(|whole| refer(snapshot, whole, here));
    let also = wholes
        .iter()
        .skip(1)
        .map(|whole| refer(snapshot, whole, here))
        .collect();
    let parts = parts_of(graph, &end)
        .into_iter()
        .filter(outside)
        .map(|part| Part {
            reference: refer(snapshot, part, here),
            parts: parts_of(graph, part)
                .into_iter()
                .filter(outside)
                .filter(|inner| *inner != part)
                .map(|inner| refer(snapshot, inner, here))
                .collect(),
        });
    Nesting {
        frame,
        also,
        parts: parts.collect(),
    }
}

/// The objects whose first `part of` edge in the graph goes to `end`,
/// sorted by kind and then by name. So a part of two wholes is listed
/// only under the first, and the second shows on the part's own page.
fn parts_of<'a>(graph: &'a Graph, end: &Endpoint) -> Vec<&'a Endpoint> {
    let mut parts = graph.reaching(Kind::PartOf, end);
    parts.retain(|part| graph.leaving(Kind::PartOf, part).first() == Some(&end));
    parts.sort_by_key(|end| sort_key(end));
    parts.dedup();
    parts
}

fn sort_key(end: &Endpoint) -> (String, String) {
    match end {
        Endpoint::Object(object) => (object.kind.clone(), object.name.clone()),
        other => (String::new(), format!("{other:?}")),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_graph::edges::{Edge, Provenance};

    fn rig(kind: &str, name: &str) -> Endpoint {
        Endpoint::Object(Object::new("rigs.example.com", kind, Some("sky"), name))
    }

    fn part_of(part: Endpoint, whole: Endpoint) -> Edge {
        Edge {
            kind: Kind::PartOf,
            from: part,
            to: whole,
            guessed: false,
            provenance: Provenance::Kubernetes,
        }
    }

    /// A telescope that is part of each of `wholes`. Two trains are part
    /// of the telescope. A camera is part of one train, and a filter is
    /// part of the other train.
    fn graph(wholes: &[Endpoint]) -> Graph {
        let scope = rig("Telescope", "main");
        let mut edges: Vec<Edge> = wholes
            .iter()
            .map(|whole| part_of(scope.clone(), whole.clone()))
            .collect();
        edges.push(part_of(rig("Train", "wide"), scope.clone()));
        edges.push(part_of(rig("Train", "guide"), scope.clone()));
        edges.push(part_of(rig("Camera", "cam"), rig("Train", "guide")));
        edges.push(part_of(rig("Filter", "red"), rig("Train", "wide")));
        edges.push(part_of(rig("Filter", "red"), rig("Train", "guide")));
        Graph::new([edges])
    }

    /// The frame's text, the text of each other whole, and the text of
    /// each part together with the text of its own parts.
    type Read = (Option<String>, Vec<String>, Vec<(String, Vec<String>)>);

    fn read(nesting: Nesting) -> Read {
        let parts = nesting.parts.into_iter().map(|part| {
            let inner = part.parts.into_iter().map(|inner| inner.text).collect();
            (part.reference.text, inner)
        });
        (
            nesting.frame.map(|frame| frame.text),
            nesting.also.into_iter().map(|also| also.text).collect(),
            parts.collect(),
        )
    }

    #[rstest]
    #[case::one_whole(
        vec![rig("Site", "hill")],
        (Some("Site hill".into()), vec![], vec![])
    )]
    #[case::two_wholes(
        vec![rig("Site", "hill"), rig("Pier", "north")],
        (Some("Site hill".into()), vec!["Pier north".into()], vec![])
    )]
    fn frames_the_object_in_its_first_whole_and_lists_the_rest(
        #[case] wholes: Vec<Endpoint>,
        #[case] expected: Read,
    ) {
        let snapshot = crate::web::fixture::snapshot();
        let telescope = Object::new("rigs.example.com", "Telescope", Some("sky"), "main");
        let (frame, also, _) = read(nesting(&snapshot, &graph(&wholes), &telescope));
        assert_eq!((frame, also), (expected.0, expected.1));
    }

    #[rstest]
    #[case::part_of_itself(vec![part_of(rig("Telescope", "main"), rig("Telescope", "main"))])]
    #[case::part_of_its_own_part(vec![
        part_of(rig("Telescope", "main"), rig("Train", "wide")),
        part_of(rig("Train", "wide"), rig("Telescope", "main")),
    ])]
    #[case::part_of_its_parts_part(vec![
        part_of(rig("Train", "wide"), rig("Telescope", "main")),
        part_of(rig("Camera", "cam"), rig("Train", "wide")),
        part_of(rig("Telescope", "main"), rig("Camera", "cam")),
    ])]
    fn never_nests_an_object_inside_itself(#[case] edges: Vec<Edge>) {
        let snapshot = crate::web::fixture::snapshot();
        let telescope = Object::new("rigs.example.com", "Telescope", Some("sky"), "main");
        let (frame, also, parts) = read(nesting(&snapshot, &Graph::new([edges]), &telescope));
        let inside: Vec<String> = parts
            .iter()
            .flat_map(|(part, inner)| std::iter::once(part).chain(inner))
            .cloned()
            .collect();
        assert!(
            !inside.contains(&"Telescope main".to_string()),
            "the box holds {inside:?}, want no Telescope main"
        );
        assert!(
            frame.is_none_or(|frame| !inside.contains(&frame)),
            "the box holds {inside:?}, want it to leave out the frame"
        );
        assert!(also.is_empty());
    }

    #[rstest]
    fn boxes_each_part_with_its_own_parts() {
        let snapshot = crate::web::fixture::snapshot();
        let telescope = Object::new("rigs.example.com", "Telescope", Some("sky"), "main");
        let (_, _, parts) = read(nesting(&snapshot, &graph(&[]), &telescope));
        assert_eq!(
            parts,
            vec![
                ("Train guide".into(), vec!["Camera cam".into()]),
                ("Train wide".into(), vec!["Filter red".into()]),
            ]
        );
    }
}
