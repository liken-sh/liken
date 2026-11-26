//! How the selected namespace relates to another namespace, as a short
//! phrase that replaces the workload count in that namespace's tile,
//! the rectangle that the cluster map draws for it.
//!
//! A narrow tile truncates the phrase with an ellipsis, so each phrase
//! is a few words and its first word shows the direction: "it needs"
//! when the selection depends on the tile's namespace, and "needs it"
//! when that namespace depends on the selection.

use std::collections::BTreeMap;

use transect_graph::edges::{Edge, Endpoint, Graph, Kind};

/// Which end of the edges the selected namespace is at.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Way {
    /// The selection's objects are the edges' `from`: "it needs".
    From,
    /// The selection's objects are the edges' `to`: "needs it".
    To,
}

/// One kind of edge between the selection and a tile, in one
/// direction. `guessed` is true when every such edge was inferred and
/// none was read from the API.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Relation {
    pub kind: Kind,
    pub way: Way,
    pub guessed: bool,
}

/// The order a tile lists its relations in. Dependencies come first,
/// because they show what breaks when one side fails. `AllowedToCall`
/// comes last, because a policy often allows many more callers than
/// actually call.
const PRIORITY: [Kind; 12] = [
    Kind::DependsOn,
    Kind::AuthenticatesThrough,
    Kind::ControlledBy,
    Kind::StoresOn,
    Kind::Holds,
    Kind::PartOf,
    Kind::RoutesTo,
    Kind::Selects,
    Kind::ReconcilesFrom,
    Kind::ManagedBy,
    Kind::RunsOn,
    Kind::AllowedToCall,
];

impl Relation {
    /// The relation of a namespace that a network policy allows to call
    /// the selection.
    pub fn may_call() -> Self {
        Self {
            kind: Kind::AllowedToCall,
            way: Way::To,
            guessed: false,
        }
    }

    /// The phrase, where "it" is the selected namespace.
    pub fn phrase(self) -> &'static str {
        use Way::{From, To};
        match (self.kind, self.way) {
            (Kind::DependsOn, From) => "it needs",
            (Kind::DependsOn, To) => "needs it",
            (Kind::AuthenticatesThrough, From) => "it logs in via",
            (Kind::AuthenticatesThrough, To) => "logs in via it",
            (Kind::ControlledBy, From) => "controls it",
            (Kind::ControlledBy, To) => "it controls",
            (Kind::StoresOn, From) => "it stores on",
            (Kind::StoresOn, To) => "stores on it",
            (Kind::Holds, From) => "it holds",
            (Kind::Holds, To) => "holds it",
            (Kind::PartOf, From) => "contains it",
            (Kind::PartOf, To) => "it contains",
            (Kind::RoutesTo, From) => "it routes to",
            (Kind::RoutesTo, To) => "routes to it",
            (Kind::Selects, From) => "it selects",
            (Kind::Selects, To) => "selects it",
            (Kind::ReconcilesFrom, From) => "applies it",
            (Kind::ReconcilesFrom, To) => "it applies",
            (Kind::ManagedBy, From) => "owns it",
            (Kind::ManagedBy, To) => "it owns",
            (Kind::RunsOn, From) => "it runs on",
            (Kind::RunsOn, To) => "runs on it",
            (Kind::AllowedToCall, From) => "it may call",
            (Kind::AllowedToCall, To) => "may call it",
            (Kind::Presents, From) => "it calls",
            (Kind::Presents, To) => "calls it",
        }
    }

    /// The phrase for the tile's line. A guess ends in "?".
    pub fn short(self) -> String {
        match self.guessed {
            true => format!("{}?", self.phrase()),
            false => self.phrase().into(),
        }
    }

    /// The phrase for the tile's title. A guess ends in "?" and adds
    /// "(a guess)", or "(a guess from labels)" for `ControlledBy`.
    pub fn long(self) -> String {
        match (self.guessed, self.kind) {
            (false, _) => self.phrase().into(),
            (true, Kind::ControlledBy) => format!("{}? (a guess from labels)", self.phrase()),
            (true, _) => format!("{}? (a guess)", self.phrase()),
        }
    }

    /// Relations read from the API sort before guesses, then by
    /// `PRIORITY`, then `Way::From` before `Way::To`.
    fn rank(self) -> (bool, usize, Way) {
        let at = PRIORITY.iter().position(|kind| *kind == self.kind);
        (self.guessed, at.unwrap_or(PRIORITY.len()), self.way)
    }
}

/// Sorts `relations` into the order a tile lists them.
pub fn arrange(relations: &mut [Relation]) {
    relations.sort_by_key(|relation| relation.rank());
}

/// The relations between `selected` and each other namespace that its
/// edges reach, by namespace. `AllowedToCall` edges are skipped,
/// because the page adds "may call it" from the network policies
/// instead, only for the callers that a policy names.
pub fn across(graph: &Graph, selected: &str) -> BTreeMap<String, Vec<Relation>> {
    let mut found: BTreeMap<String, Vec<Relation>> = BTreeMap::new();
    let edges = graph.touching_namespace(selected);
    for edge in edges.filter(|edge| edge.kind != Kind::AllowedToCall) {
        let Some((namespace, way)) = far(edge, selected) else {
            continue;
        };
        let relation = Relation {
            kind: edge.kind,
            way,
            guessed: edge.guessed,
        };
        add(found.entry(namespace.into()).or_default(), relation);
    }
    found
}

/// Adds `relation` to `relations`, or merges it into the relation of
/// the same kind and way. The merged relation stays a guess only when
/// both are guesses.
pub fn add(relations: &mut Vec<Relation>, relation: Relation) {
    let same = relations
        .iter_mut()
        .find(|held| held.kind == relation.kind && held.way == relation.way);
    match same {
        Some(held) => held.guessed &= relation.guessed,
        None => relations.push(relation),
    }
}

/// The other namespace of an edge between `selected` and a different
/// namespace, and which end `selected` is at.
fn far<'a>(edge: &'a Edge, selected: &str) -> Option<(&'a str, Way)> {
    let (from, to) = (namespace(&edge.from), namespace(&edge.to));
    match (from, to) {
        (Some(from), Some(to)) if from == selected && to != selected => Some((to, Way::From)),
        (Some(from), Some(to)) if to == selected && from != selected => Some((from, Way::To)),
        _ => None,
    }
}

fn namespace(end: &Endpoint) -> Option<&str> {
    match end {
        Endpoint::Object(object) => object.namespace.as_deref(),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_graph::edges::{Object, Provenance};

    fn relation(kind: Kind, way: Way) -> Relation {
        Relation {
            kind,
            way,
            guessed: false,
        }
    }

    #[rstest]
    fn every_phrase_is_short_and_says_its_direction_first(
        #[values(
            Kind::RunsOn,
            Kind::ManagedBy,
            Kind::StoresOn,
            Kind::Selects,
            Kind::RoutesTo,
            Kind::DependsOn,
            Kind::ReconcilesFrom,
            Kind::ControlledBy,
            Kind::AllowedToCall,
            Kind::Holds,
            Kind::AuthenticatesThrough,
            Kind::PartOf
        )]
        kind: Kind,
    ) {
        let from = relation(kind, Way::From).phrase();
        let to = relation(kind, Way::To).phrase();
        let first = |phrase: &'static str| phrase.split(' ').next().unwrap();
        assert!(from.len() <= 14 && to.len() <= 14, "{from} / {to}");
        assert_ne!(first(from), first(to), "{from} / {to}");
        assert!(PRIORITY.contains(&kind));
    }

    #[rstest]
    #[case::needs_before_controls(
        vec![relation(Kind::ControlledBy, Way::From), relation(Kind::DependsOn, Way::To)],
        vec!["needs it", "controls it"]
    )]
    #[case::may_call_last(
        vec![relation(Kind::AllowedToCall, Way::To), relation(Kind::ReconcilesFrom, Way::From)],
        vec!["applies it", "may call it"]
    )]
    #[case::a_guess_after_what_was_read(
        vec![
            Relation { guessed: true, ..relation(Kind::ControlledBy, Way::To) },
            relation(Kind::ReconcilesFrom, Way::To),
        ],
        vec!["it applies", "it controls"]
    )]
    #[case::what_the_selection_needs_first(
        vec![relation(Kind::DependsOn, Way::To), relation(Kind::DependsOn, Way::From)],
        vec!["it needs", "needs it"]
    )]
    fn relations_sort_by_a_fixed_priority(
        #[case] mut relations: Vec<Relation>,
        #[case] phrases: Vec<&str>,
    ) {
        arrange(&mut relations);
        let said: Vec<&str> = relations.iter().map(|relation| relation.phrase()).collect();
        assert_eq!(said, phrases);
    }

    #[rstest]
    #[case::certain(Kind::ControlledBy, false, "controls it", "controls it")]
    #[case::from_labels(
        Kind::ControlledBy,
        true,
        "controls it?",
        "controls it? (a guess from labels)"
    )]
    #[case::from_names(Kind::DependsOn, true, "it needs?", "it needs? (a guess)")]
    fn a_guess_ends_in_a_question_mark(
        #[case] kind: Kind,
        #[case] guessed: bool,
        #[case] short: &str,
        #[case] long: &str,
    ) {
        let relation = Relation {
            guessed,
            ..relation(kind, Way::From)
        };
        assert_eq!(
            (relation.short().as_str(), relation.long().as_str()),
            (short, long)
        );
    }

    fn edge(kind: Kind, from: &str, to: &str, guessed: bool) -> Edge {
        Edge {
            kind,
            from: Endpoint::Object(Object::new("apps", "Deployment", Some(from), "a")),
            to: Endpoint::Object(Object::new("apps", "Deployment", Some(to), "b")),
            guessed,
            provenance: Provenance::Kubernetes,
        }
    }

    #[rstest]
    #[case::the_selection_depends_on_another(
        vec![edge(Kind::DependsOn, "shop", "db", false)],
        vec![("db", relation(Kind::DependsOn, Way::From))]
    )]
    #[case::another_depends_on_the_selection(
        vec![edge(Kind::DependsOn, "web", "shop", false)],
        vec![("web", relation(Kind::DependsOn, Way::To))]
    )]
    #[case::inside_the_selection(vec![edge(Kind::DependsOn, "shop", "shop", false)], vec![])]
    #[case::a_guess_only_while_every_edge_is_one(
        vec![
            edge(Kind::ControlledBy, "shop", "ops", true),
            edge(Kind::ControlledBy, "shop", "ops", false),
        ],
        vec![("ops", relation(Kind::ControlledBy, Way::From))]
    )]
    #[case::allowed_to_call_left_out(vec![edge(Kind::AllowedToCall, "web", "shop", false)], vec![])]
    fn across_reads_the_edges_that_leave_the_selection(
        #[case] edges: Vec<Edge>,
        #[case] expected: Vec<(&str, Relation)>,
    ) {
        let graph = Graph::new([edges]);
        let found = across(&graph, "shop");
        let found: Vec<(&str, Relation)> = found
            .iter()
            .flat_map(|(namespace, relations)| {
                relations
                    .iter()
                    .map(move |relation| (namespace.as_str(), *relation))
            })
            .collect();
        assert_eq!(found, expected);
    }
}
