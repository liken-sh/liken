//! The line a card shows in place of its callers: that nothing isolates
//! its thing, that its thing is on its machine's network, or that
//! policy admits no caller.  A namespace's line names the workloads it
//! leaves open, each linked to its page.

use transect_graph::edges::{Object, kind_and_name};
use transect_graph::links::{Part, Piece};

/// A card's line when no policy isolates its thing's pods.  The map
/// tags nothing then, because every namespace may call it.
pub fn open() -> Vec<Part> {
    plain("No NetworkPolicy selects its pods, so any pod may call them.")
}

/// A workload card's line when its pods are on their machine's
/// network.  A policy that selects them by their labels has no effect
/// there, so the card names none.
pub fn on_machine_network() -> Vec<Part> {
    plain(
        "It is on its machine's network, where no NetworkPolicy applies, so any pod may call it at its machine's address.",
    )
}

/// A workload card's line when policy isolates it and its rules admit
/// no caller.
pub fn no_caller() -> Vec<Part> {
    plain("A NetworkPolicy selects its pods and admits no caller.")
}

/// A namespace card's line when policy isolates it and admits no other
/// namespace.
pub fn no_other_namespace() -> Vec<Part> {
    plain("A NetworkPolicy selects its pods and admits no other namespace.")
}

/// A namespace card's line when a rule admits every namespace but
/// `but`.
pub fn any_namespace(but: &[String]) -> Vec<Part> {
    let named = transect_graph::links::Named::AnyNamespace { pods: false, but }.to_string();
    let mut chars = named.chars();
    let first = chars.next().map(|c| c.to_uppercase().collect::<String>());
    plain(&format!(
        "{}{} may call it.",
        first.unwrap_or_default(),
        chars.as_str()
    ))
}

/// A namespace card's line when policy isolates some of its workloads
/// but not `open`, which no policy selects, or `machine_network`, whose
/// pods are on their machine's network.  Any pod may call each of
/// them, so the map tags nothing.  `href` gives each workload's page.
pub fn partly_open(
    open: &[Object],
    machine_network: &[Object],
    href: impl Fn(&Object) -> Option<String>,
) -> Vec<Part> {
    let mut pieces = Vec::new();
    if !open.is_empty() {
        let pronoun = if open.len() == 1 { "it" } else { "them" };
        pieces.push(Piece::words("No NetworkPolicy selects the pods of "));
        pieces.extend(either(open));
        pieces.push(Piece::words(format!(", so any pod may call {pronoun}.")));
    }
    if !machine_network.is_empty() {
        if !pieces.is_empty() {
            pieces.push(Piece::words(" "));
        }
        pieces.extend(transect_graph::links::listed(
            machine_network.iter().map(|o| vec![name(o)]).collect(),
        ));
        pieces.push(Piece::words(if machine_network.len() == 1 {
            " is on its machine's network, where no NetworkPolicy applies, so any pod may call it at its machine's address."
        } else {
            " are on their machines' networks, where no NetworkPolicy applies, so any pod may call them at their machines' addresses."
        }));
    }
    let named = open.iter().chain(machine_network).cloned();
    transect_graph::links::Linker::new(named, href).link(&pieces)
}

fn name(object: &Object) -> Piece {
    Piece::Name(object.clone(), kind_and_name(&object.kind, &object.name))
}

/// `objects` named as a list joined by "or": "a or b", "a, b, or c".
fn either(objects: &[Object]) -> Vec<Piece> {
    let mut pieces = Vec::new();
    for (at, object) in objects.iter().enumerate() {
        let joint = match (objects.len(), at) {
            (_, 0) => None,
            (2, _) => Some(" or "),
            (count, at) if at + 1 == count => Some(", or "),
            _ => Some(", "),
        };
        pieces.extend(joint.map(Piece::words));
        pieces.push(name(object));
    }
    pieces
}

fn plain(text: &str) -> Vec<Part> {
    vec![Part::plain(text)]
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn deployments(names: &[&str]) -> Vec<Object> {
        let made = names.iter();
        made.map(|name| Object::new("apps", "Deployment", Some("shop"), name))
            .collect()
    }

    fn text(parts: &[Part]) -> String {
        parts.iter().map(|part| part.text.as_str()).collect()
    }

    #[rstest]
    #[case::one(&["web"], &[], "No NetworkPolicy selects the pods of Deployment web, so any pod may call it.")]
    #[case::two(
        &["web", "api"],
        &[],
        "No NetworkPolicy selects the pods of Deployment web or Deployment api, so any pod may call them."
    )]
    #[case::three(
        &["web", "api", "feed"],
        &[],
        "No NetworkPolicy selects the pods of Deployment web, Deployment api, or Deployment feed, so any pod may call them."
    )]
    #[case::one_on_its_machines_network(
        &[],
        &["hub"],
        "Deployment hub is on its machine's network, where no NetworkPolicy applies, so any pod may call it at its machine's address."
    )]
    #[case::both(
        &["web"],
        &["hub", "probe"],
        "No NetworkPolicy selects the pods of Deployment web, so any pod may call it. Deployment hub and Deployment probe are on their machines' networks, where no NetworkPolicy applies, so any pod may call them at their machines' addresses."
    )]
    fn names_the_workloads_left_open(
        #[case] open: &[&str],
        #[case] machine_network: &[&str],
        #[case] expected: &str,
    ) {
        let parts = partly_open(&deployments(open), &deployments(machine_network), |_| None);
        assert_eq!(text(&parts), expected);
    }

    #[rstest]
    fn links_each_workload_it_names() {
        let parts = partly_open(&[], &deployments(&["hub"]), |object| {
            Some(format!("/{}", object.name))
        });
        let linked: Vec<(&str, Option<&str>)> = parts
            .iter()
            .map(|part| (part.text.as_str(), part.href.as_deref()))
            .take(1)
            .collect();
        assert_eq!(linked, vec![("Deployment hub", Some("/hub"))]);
    }
}
