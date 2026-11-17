//! What each clue names, and the guessed edges that follow.  An object
//! that a clue names counts only when transect watches its kind and
//! has exactly one object of that name, so a field that matches
//! nothing, or matches several, draws no edge.  A `Secret` is the one
//! kind matched without its object, because transect never lists
//! Secrets and a `secretRef` or `secretName` names one by the
//! API's own convention.  An address counts only when it reaches
//! something in the cluster, so that text that contains a dot does not
//! add a box outside the cluster.

use std::collections::HashSet;

use serde_json::Value;

use super::StatedReading;
use transect_graph::edges::{
    ClusterScoped, Edge, Endpoint, Graph, Kind, Object, Plurals, Provenance, TOGGLED,
};
use transect_resources::knowledge::{Clue, Hint, Kinds, clues};

/// What clues resolve against.
pub struct Lookup<'a> {
    /// Every kind the cluster serves, by group and kind.
    pub plurals: &'a Plurals,
    pub scoped: &'a ClusterScoped,
    /// Every object transect watches.
    pub watched: &'a HashSet<Object>,
    pub reading: &'a StatedReading<'a>,
}

/// The guessed edges `data`, the spec and status of `from`, draws, or
/// none when its kind has knowledge.
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

/// The guessed depends-on edges `clues` of `from` draw.  A clue whose
/// end `graph` already joins to `from` by any kind draws no edge,
/// because the API already gives the relation between the two.
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
                Some(object) => lookup.reading.stands_for(&object),
                None => Vec::new(),
            },
        };
        for to in ends {
            let joined = TOGGLED
                .into_iter()
                .chain([Kind::ManagedBy])
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

/// What an address clue reaches inside the cluster.
fn address(clue: &Clue, from: &Object, lookup: &Lookup) -> Vec<Endpoint> {
    let found = lookup
        .reading
        .address(&clue.value, from.namespace.as_deref());
    let (ends, _) = found.unwrap_or_default();
    let inside = |end: &Endpoint| !matches!(end, Endpoint::Outside(_));
    ends.into_iter().filter(inside).collect()
}

/// The one object a `*Ref` or `*Name` clue of `from` names.
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

/// The kind a field's prefix names: `Service` of `service`, and a
/// claim, as a volume's `claimName` reads, is a
/// `PersistentVolumeClaim`.
fn kind_of(prefix: &str) -> String {
    if prefix.eq_ignore_ascii_case("claim") {
        return "PersistentVolumeClaim".into();
    }
    let mut chars = prefix.chars();
    let first = chars.next().map(|c| c.to_ascii_uppercase());
    first.into_iter().chain(chars).collect()
}

/// The group and kind the cluster serves as `kind`, in `group` when it
/// is given.  Among several groups the function selects the core one,
/// because `Service` means the core `Service` to anyone who writes a
/// `serviceName`.
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

/// The one watched object named `name` in `namespace`, of any kind.
fn only_named(name: &str, namespace: Option<&str>, watched: &HashSet<Object>) -> Option<Object> {
    let mut found = watched
        .iter()
        .filter(|object| object.name == name && object.namespace.as_deref() == namespace);
    let first = found.next()?;
    found.next().is_none().then(|| first.clone())
}

#[cfg(test)]
mod tests;
