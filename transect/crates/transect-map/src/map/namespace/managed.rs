//! The "managed by" text on the box of a custom resource, which names
//! where its controller runs. `managed_by` reads it from the graph's
//! controlled-by edges.

use std::collections::{BTreeSet, HashMap};

use transect_graph::edges::{Endpoint, Graph, Kind, Object};

/// The controllers of one custom resource. Each is named by its
/// namespace, or by its workload's name when the workload runs in the
/// resource's own namespace.
#[derive(Clone, Debug, PartialEq)]
pub struct ManagedBy {
    pub names: Vec<String>,
    /// True when a controlled-by edge was guessed and `names` has more
    /// than one entry, so the box cannot say which one is the controller.
    pub guessed: bool,
}

/// The controllers of each custom resource in `namespace`, by object.
pub fn managed_by(graph: &Graph, namespace: &str) -> HashMap<Object, ManagedBy> {
    let mut found: HashMap<Object, (BTreeSet<String>, bool)> = HashMap::new();
    for edge in graph.of_kind(Kind::ControlledBy) {
        let (Endpoint::Object(from), Endpoint::Object(to)) = (&edge.from, &edge.to) else {
            continue;
        };
        if from.namespace.as_deref() != Some(namespace) {
            continue;
        }
        let name = match &to.namespace {
            Some(there) if there != namespace => there.clone(),
            _ => to.name.clone(),
        };
        let (names, guessed) = found.entry(from.clone()).or_default();
        names.insert(name);
        *guessed |= edge.guessed;
    }
    found
        .into_iter()
        .map(|(object, (names, guessed))| {
            let guessed = guessed && names.len() > 1;
            let names = names.into_iter().collect();
            (object, ManagedBy { names, guessed })
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_graph::edges::Edge;

    fn cache() -> Object {
        Object::new("cache.example.io", "Cache", Some("shop"), "sessions")
    }

    fn to(namespace: &str, name: &str, guessed: bool) -> Edge {
        Edge {
            kind: Kind::ControlledBy,
            from: Endpoint::Object(cache()),
            to: Endpoint::Object(Object::new("apps", "Deployment", Some(namespace), name)),
            guessed,
            provenance: transect_graph::edges::Provenance::Kubernetes,
        }
    }

    #[rstest]
    #[case::another_namespace(vec![to("cache-system", "operator", false)], vec!["cache-system"], false)]
    #[case::its_own_namespace(vec![to("shop", "operator", false)], vec!["operator"], false)]
    #[case::two_in_one_namespace(
        vec![to("cache-system", "operator", true), to("cache-system", "webhook", true)],
        vec!["cache-system"],
        false
    )]
    #[case::two_namespaces(
        vec![to("cache-system", "operator", true), to("other-system", "operator", true)],
        vec!["cache-system", "other-system"],
        true
    )]
    fn names_where_each_controller_runs(
        #[case] edges: Vec<Edge>,
        #[case] names: Vec<&str>,
        #[case] guessed: bool,
    ) {
        let found = managed_by(&Graph::new([edges]), "shop");
        let expected = ManagedBy {
            names: names.into_iter().map(Into::into).collect(),
            guessed,
        };
        assert_eq!(found.get(&cache()), Some(&expected));
    }
}
