//! What an object is part of and what is part of it, from the `part
//! of` edges kind knowledge draws, for the nesting on its board.

use super::super::super::snapshot::Snapshot;
use super::refer;
use transect_graph::edges::{Endpoint, Graph, Kind, Object};
use transect_map::objects::{Nesting, Part};

/// `object`'s nesting in `graph`: the first thing it is part of as its
/// frame, anything else as a line, and its parts, each with its own
/// parts, by kind and then by name.  A cycle of `part of` edges, such as
/// an object part of itself or of its own part, never draws an object
/// inside itself: the object and what it is part of are left out of its
/// parts at both depths.
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

/// What is part of `end` first, by kind and then by name.  A part of
/// two wholes nests under the first its knowledge names, and the second
/// lists it on its own page only.
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

    /// A telescope part of `wholes`, whose two trains are part of it,
    /// with a camera part of one train, and a filter that is part of the
    /// other train first.
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

    /// The frame's text, each other whole's, and each part's with its
    /// own parts'.
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
    fn frames_the_board_in_the_first_whole_and_lists_the_rest(
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
            "{inside:?}"
        );
        assert!(
            frame.is_none_or(|frame| !inside.contains(&frame)),
            "{inside:?}"
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
