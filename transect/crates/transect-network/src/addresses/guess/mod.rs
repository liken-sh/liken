//! Draws guessed depends-on edges for objects whose kinds have no
//! declared relations, from clues in their spec and status. A clue is
//! a field named like `secretRef` or `claimName` whose value names an
//! object, or a value that looks like an address.
//!
//! A named object counts only when transect watches it, so a name that
//! matches no object draws no edge. When the cluster serves no single
//! kind for the field name, the name counts only when exactly one
//! watched object in the namespace has it. A clue that names a `Secret`
//! counts without the object when transect watches no `Secret`s. An
//! address counts only when it reaches something inside the cluster, so
//! text that merely contains a dot does not add a box outside the
//! cluster.

use std::collections::HashSet;

use serde_json::Value;

use super::StatedReading;
use transect_graph::edges::{
    ClusterScoped, Edge, Endpoint, Graph, Kind, Object, Plurals, Provenance,
};
use transect_resources::knowledge::{Clue, Hint, Kinds, clues};

/// The kinds of an edge from an object that already show its relation
/// to an end, so a clue to that end draws no guessed edge.
const RELATED_BY: [Kind; 9] = [
    Kind::RunsOn,
    Kind::DependsOn,
    Kind::AuthenticatesThrough,
    Kind::StoresOn,
    Kind::Holds,
    Kind::ReconcilesFrom,
    Kind::ControlledBy,
    Kind::AllowedToCall,
    Kind::ManagedBy,
];

/// The cluster state that clues resolve against.
pub struct Lookup<'a> {
    /// Every kind the cluster serves, by group and kind.
    pub plurals: &'a Plurals,
    /// Which kinds are cluster-scoped.
    pub scoped: &'a ClusterScoped,
    /// Every object transect watches.
    pub watched: &'a HashSet<Object>,
    /// Resolves an address or an object to the ends it reaches.
    pub reading: &'a StatedReading<'a>,
}

/// The guessed edges for the clues in `data`, the spec and status of
/// `from`. There are none when `kinds` declares relations for the kind
/// of `from`.
pub fn guesses(
    kinds: &Kinds,
    from: &Object,
    data: &Value,
    lookup: &Lookup,
    graph: &Graph,
) -> Vec<Edge> {
    if kinds.of_object(from).is_some() {
        return Vec::new();
    }
    guessed(from, &clues(data), lookup, graph)
}

/// The guessed depends-on edges that the `clues` of `from` draw. A clue
/// draws no edge when `graph` already joins `from` to its end by a kind
/// in `RELATED_BY`, because the edge already shows that relation. It
/// also draws none when an earlier clue drew an edge to the same end,
/// or when the end is an object that `from` owns.
pub fn guessed(from: &Object, clues: &[Clue], lookup: &Lookup, graph: &Graph) -> Vec<Edge> {
    let start = Endpoint::Object(from.clone());
    let watched_kinds: HashSet<(&str, &str)> = lookup
        .watched
        .iter()
        .map(|object| (object.group.as_str(), object.kind.as_str()))
        .collect();
    let mut drawn: Vec<Edge> = Vec::new();
    for clue in clues {
        let ends = match &clue.hint {
            Hint::Address => address(clue, from, lookup),
            hint => match object(clue, hint, from, lookup, &watched_kinds) {
                Some(object) => lookup.reading.endpoints_of(&object),
                None => Vec::new(),
            },
        };
        for to in ends {
            let joined = RELATED_BY
                .into_iter()
                .any(|kind| graph.leaving(kind, &start).contains(&&to));
            let seen = drawn.iter().any(|edge| edge.to == to);
            if joined || seen || lookup.reading.owns(from, &to) {
                continue;
            }
            drawn.push(Edge {
                kind: Kind::DependsOn,
                from: start.clone(),
                to,
                guessed: true,
                provenance: Provenance::Guess {
                    field: clue.path.clone(),
                },
            });
        }
    }
    drawn
}

/// The ends inside the cluster that an address clue reaches.
fn address(clue: &Clue, from: &Object, lookup: &Lookup) -> Vec<Endpoint> {
    let found = lookup
        .reading
        .address(&clue.value, from.namespace.as_deref());
    let (ends, _) = found.unwrap_or_default();
    let inside = |end: &Endpoint| !matches!(end, Endpoint::Outside(_));
    ends.into_iter().filter(inside).collect()
}

/// The object that a `*Ref` or `*Name` clue of `from` names, or none
/// when no watched object matches.
fn object(
    clue: &Clue,
    hint: &Hint,
    from: &Object,
    lookup: &Lookup,
    watched_kinds: &HashSet<(&str, &str)>,
) -> Option<Object> {
    let (kind, namespace) = match hint {
        Hint::Kind {
            group,
            kind,
            namespace,
        } => (served(lookup.plurals, group.as_deref(), kind), namespace),
        Hint::Prefix { prefix, namespace } => {
            (served(lookup.plurals, None, &kind_of(prefix)), namespace)
        }
        Hint::Address => return None,
    };
    let namespace = namespace.as_deref().or(from.namespace.as_deref());
    let Some((group, kind)) = kind else {
        return only_named(&clue.value, namespace, lookup.watched);
    };
    let cluster_wide = lookup.scoped.contains(&(group.clone(), kind.clone()));
    let namespace = namespace.filter(|_| !cluster_wide);
    let object = Object::new(&group, &kind, namespace, &clue.value);
    let secret = group.is_empty() && kind == "Secret";
    let watched = watched_kinds.contains(&(group.as_str(), kind.as_str()));
    let found = lookup.watched.contains(&object) || (secret && !watched);
    found.then_some(object)
}

/// The kind that a field name's prefix implies: `service` gives
/// `Service`, and `claim`, as in a volume's `claimName`, gives
/// `PersistentVolumeClaim`. Any other prefix gives its capitalized
/// form.
fn kind_of(prefix: &str) -> String {
    if prefix.eq_ignore_ascii_case("claim") {
        return "PersistentVolumeClaim".into();
    }
    let mut chars = prefix.chars();
    let first = chars.next().map(|c| c.to_ascii_uppercase());
    first.into_iter().chain(chars).collect()
}

/// The group and kind that the cluster serves for `kind`, limited to
/// `group` when it is given. When several groups serve the kind, the
/// result is the core group's, because a field such as `serviceName`
/// refers to the core `Service`.
fn served(plurals: &Plurals, group: Option<&str>, kind: &str) -> Option<(String, String)> {
    let mut found: Vec<&(String, String)> = plurals
        .keys()
        .filter(|(g, k)| k == kind && group.is_none_or(|group| group == g))
        .collect();
    found.sort();
    match found.as_slice() {
        [one] => Some((*one).clone()),
        [core, ..] if core.0.is_empty() => Some((*core).clone()),
        _ => None,
    }
}

/// The watched object named `name` in `namespace`, of any kind, when
/// exactly one has that name.
fn only_named(name: &str, namespace: Option<&str>, watched: &HashSet<Object>) -> Option<Object> {
    let mut found = watched
        .iter()
        .filter(|object| object.name == name && object.namespace.as_deref() == namespace);
    let first = found.next()?;
    found.next().is_none().then(|| first.clone())
}

#[cfg(test)]
mod tests;
