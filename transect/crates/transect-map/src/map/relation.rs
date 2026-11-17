//! How a selected namespace relates to a parcel its edges reach, as the
//! short line that replaces the parcel's workload count.
//!
//! A narrow parcel cuts the line with an ellipsis, so each phrase is a
//! few words whose first word shows the direction: "it needs" when the
//! selection depends on this namespace, "needs it" when this namespace
//! depends on the selection.  However much is cut, the reader still
//! knows which way the relation runs.

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

/// One kind of edge between the selection and a parcel, in one
/// direction, and whether every such edge is a guess.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Relation {
    pub kind: Kind,
    pub way: Way,
    pub guessed: bool,
}

/// The order in which a parcel shows its relations, most important first.
/// What the selection needs and what needs it come first, since those
/// are what a loss takes; "may call" comes last, since policy lets many
/// namespaces call one and few of them do.
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
    /// A caller a policy names, which "may call" the selection.
    pub fn may_call() -> Self {
        Self {
            kind: Kind::AllowedToCall,
            way: Way::To,
            guessed: false,
        }
    }

    /// The phrase, with "it" as the selected namespace.  Each phrase
    /// starts with the word that shows which way the relation runs: "it"
    /// when the selection acts on the parcel, the verb when the parcel
    /// acts on the selection.
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
        }
    }

    /// The phrase as the parcel shows it: a guess ends in "?".
    pub fn short(self) -> String {
        match self.guessed {
            true => format!("{}?", self.phrase()),
            false => self.phrase().into(),
        }
    }

    /// The phrase as the parcel's title shows it, with what a guess was
    /// read from.
    pub fn long(self) -> String {
        match (self.guessed, self.kind) {
            (false, _) => self.phrase().into(),
            (true, Kind::ControlledBy) => format!("{}? (a guess from labels)", self.phrase()),
            (true, _) => format!("{}? (a guess)", self.phrase()),
        }
    }

    /// Where the relation sorts: what transect read before what it
    /// guessed, then by `PRIORITY`, and within a kind, what the
    /// selection does to the parcel first.
    fn rank(self) -> (bool, usize, Way) {
        let at = PRIORITY.iter().position(|kind| *kind == self.kind);
        (self.guessed, at.unwrap_or(PRIORITY.len()), self.way)
    }
}

/// Puts `relations` in the order a parcel shows them.
pub fn arrange(relations: &mut [Relation]) {
    relations.sort_by_key(|relation| relation.rank());
}

/// The relations the selected namespace's edges give each other
/// namespace they reach, by namespace.  A relation is a guess only when
/// every edge that gives it is one.  "Allowed to call" is left out,
/// because the map tags only the callers a policy names.  The policy
/// summary names those callers, and these edges do not.
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

/// Adds `relation` to `relations`, once for its kind and way, keeping it
/// a guess only while every edge that gave it is one.
pub fn add(relations: &mut Vec<Relation>, relation: Relation) {
    let same = relations
        .iter_mut()
        .find(|held| held.kind == relation.kind && held.way == relation.way);
    match same {
        Some(held) => held.guessed &= relation.guessed,
        None => relations.push(relation),
    }
}

/// The namespace at the edge's far end from `selected`, and which end
/// the selection is at, for an edge that crosses between two namespaces.
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
