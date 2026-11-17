//! A card's "Controlled by" and "Controls" sections.  "Controlled by"
//! lists the controllers that reconcile the card's custom resources.
//! "Controls" lists what the card's object reconciles: for a workload, a
//! count of each kind, and for a namespace, the namespaces whose custom
//! resources its workloads reconcile.  Both are read from the graph's
//! controlled-by edges.

use std::collections::BTreeMap;

use transect_graph::edges::{Edge, Endpoint, Graph, Kind, Object, Plurals, Provenance};
use transect_graph::links::{Named, Reference};

#[derive(Debug, Default, PartialEq)]
pub struct Managed {
    pub by: Vec<Controller>,
    pub manages: Vec<Reference>,
    /// The RBAC kinds `transect` may not list.  The card then states
    /// that controllers may be missing.
    pub missing: Vec<&'static str>,
}

/// One controller a card names, with the mark of the kind knowledge
/// that named it, when one did.
#[derive(Debug, PartialEq)]
pub struct Controller {
    pub reference: Reference,
    pub mark: Option<Provenance>,
}

impl Managed {
    /// The mark the "Controlled by" heading shows for every controller,
    /// when two or more share it.
    pub fn shared_by(&self) -> Option<Provenance> {
        transect_graph::edges::provenance::shared(
            self.by.iter().map(|controller| controller.mark.as_ref()),
        )
    }

    /// The workloads that control `workload`, and how many objects of
    /// each kind it controls.
    pub fn of_workload(
        graph: &Graph,
        workload: &Object,
        plurals: &Plurals,
        missing: &[&'static str],
    ) -> Self {
        let end = Endpoint::Object(workload.clone());
        let here = workload.namespace.as_deref();
        let by = graph
            .of_kind(Kind::ControlledBy)
            .filter(|edge| edge.from == end)
            .filter_map(|edge| {
                let Endpoint::Object(controller) = &edge.to else {
                    return None;
                };
                let text = Named::Object {
                    object: controller,
                    here: controller.namespace.as_deref() == here,
                }
                .to_string();
                let mark = match &edge.provenance {
                    Provenance::Kubernetes => None,
                    provenance => Some(provenance.clone()),
                };
                Some(Controller {
                    reference: Reference {
                        text: guess(text, edge.guessed),
                        href: transect_graph::links::page(controller, plurals),
                    },
                    mark,
                })
            })
            .collect();
        let manages = by_kind(
            graph.of_kind(Kind::ControlledBy).filter(|e| e.to == end),
            plurals,
        );
        Self {
            by,
            manages,
            missing: missing.to_vec(),
        }
    }

    /// The namespaces whose workloads control `namespace`'s custom
    /// resources, and the namespaces whose custom resources its
    /// workloads control.  A namespace that manages its own is left
    /// out, because its boxes already show that.
    pub fn of_namespace(graph: &Graph, namespace: &str, missing: &[&'static str]) -> Self {
        let in_here = |end: &Endpoint| in_namespace(end) == Some(namespace);
        let edges: Vec<&Edge> = graph
            .of_kind(Kind::ControlledBy)
            .filter(|edge| in_here(&edge.from) != in_here(&edge.to))
            .collect();
        let by = edges.iter().copied().filter(|edge| in_here(&edge.from));
        let manages = edges.iter().copied().filter(|edge| in_here(&edge.to));
        Self {
            by: namespaces(by, |edge| &edge.to)
                .into_iter()
                .map(|reference| Controller {
                    reference,
                    mark: None,
                })
                .collect(),
            manages: namespaces(manages, |edge| &edge.from),
            missing: missing.to_vec(),
        }
    }

    pub fn is_empty(&self) -> bool {
        self.by.is_empty() && self.manages.is_empty()
    }

    /// Where each marked controller came from, for the card's legend.
    pub fn provenances(&self) -> impl Iterator<Item = &Provenance> {
        self.by
            .iter()
            .filter_map(|controller| controller.mark.as_ref())
    }
}

/// How many objects of each kind the edges leave from, "16 Clusters",
/// each a reference to the kind's page, so an operator's card counts
/// what it manages rather than naming every namespace it manages
/// objects in.
fn by_kind<'a>(edges: impl Iterator<Item = &'a Edge>, plurals: &Plurals) -> Vec<Reference> {
    let mut kinds: BTreeMap<(&str, &str), (usize, bool)> = BTreeMap::new();
    for edge in edges {
        if let Endpoint::Object(object) = &edge.from {
            let entry = kinds
                .entry((&object.group, &object.kind))
                .or_insert((0, true));
            entry.0 += 1;
            entry.1 &= edge.guessed;
        }
    }
    kinds
        .into_iter()
        .map(|((group, kind), (count, guessed))| {
            let plural = plurals.get(&(group.to_string(), kind.to_string()));
            let label = match count {
                1 => kind.to_string(),
                _ => transect_resources::knowledge::plural_label(kind, plural.map(String::as_str)),
            };
            Reference {
                text: guess(format!("{count} {label}"), guessed),
                href: plural.map(|plural| transect_graph::links::kind(group, plural)),
            }
        })
        .collect()
}

/// The namespace of each edge's `end`, each once and sorted, as a
/// reference to its page.
fn namespaces<'a>(
    edges: impl Iterator<Item = &'a Edge>,
    end: impl Fn(&'a Edge) -> &'a Endpoint,
) -> Vec<Reference> {
    guessed_by_namespace(edges, end)
        .into_iter()
        .map(|(namespace, guessed)| {
            let mut reference = Reference::to_namespace(namespace);
            reference.text = guess(reference.text, guessed);
            reference
        })
        .collect()
}

/// The namespace of each edge's `end`, and whether it is a guess, which
/// it is only when every edge that reaches it is one.
fn guessed_by_namespace<'a>(
    edges: impl Iterator<Item = &'a Edge>,
    end: impl Fn(&'a Edge) -> &'a Endpoint,
) -> BTreeMap<&'a str, bool> {
    let mut guessed: BTreeMap<&str, bool> = BTreeMap::new();
    for edge in edges {
        if let Some(namespace) = in_namespace(end(edge)) {
            let entry = guessed.entry(namespace).or_insert(true);
            *entry &= edge.guessed;
        }
    }
    guessed
}

fn in_namespace(end: &Endpoint) -> Option<&str> {
    match end {
        Endpoint::Object(object) => object.namespace.as_deref(),
        _ => None,
    }
}

/// `text` as is, or for an end that controller attribution could not
/// tell apart from another, marked as a guess, as "Deployment operator
/// in cache-system (a guess)".
fn guess(text: String, guessed: bool) -> String {
    if guessed {
        format!("{text} (a guess)")
    } else {
        text
    }
}

/// Why a card's "Controlled by" may be short, as "Controllers may be
/// missing: transect may not list Roles and RoleBindings."
fn rbac_missing(kinds: &[&str]) -> Option<String> {
    let listed = match kinds {
        [] => return None,
        [one] => one.to_string(),
        [rest @ .., last] => format!("{} and {last}", rest.join(", ")),
    };
    Some(format!(
        "Controllers may be missing: transect may not list {listed}."
    ))
}

impl Managed {
    /// Why the section may be short, or none when `transect` read every
    /// RBAC kind.
    pub fn missing_text(&self) -> Option<String> {
        rbac_missing(&self.missing)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn object(group: &str, kind: &str, namespace: &str, name: &str) -> Object {
        Object::new(group, kind, Some(namespace), name)
    }

    fn cache(namespace: &str) -> Object {
        object("cache.example.io", "Cache", namespace, "sessions")
    }

    fn operator(namespace: &str, name: &str) -> Object {
        object("apps", "Deployment", namespace, name)
    }

    fn controlled(from: Object, to: Object, guessed: bool) -> Edge {
        Edge {
            kind: Kind::ControlledBy,
            from: Endpoint::Object(from),
            to: Endpoint::Object(to),
            guessed,
            provenance: transect_graph::edges::Provenance::Kubernetes,
        }
    }

    fn texts(references: &[Reference]) -> Vec<&str> {
        references.iter().map(|r| r.text.as_str()).collect()
    }

    fn controllers(controllers: &[Controller]) -> Vec<&str> {
        controllers
            .iter()
            .map(|c| c.reference.text.as_str())
            .collect()
    }

    fn graph() -> Graph {
        Graph::new([vec![
            controlled(cache("shop"), operator("cache-system", "operator"), false),
            controlled(cache("blog"), operator("cache-system", "operator"), true),
            controlled(cache("blog"), operator("other-system", "operator"), true),
        ]])
    }

    #[rstest]
    #[case::sure("shop", vec!["Deployment cache-system/operator"])]
    #[case::guessed(
        "blog",
        vec!["Deployment cache-system/operator (a guess)", "Deployment other-system/operator (a guess)"]
    )]
    fn a_custom_resource_names_its_controllers(#[case] namespace: &str, #[case] by: Vec<&str>) {
        let managed = Managed::of_workload(&graph(), &cache(namespace), &Plurals::new(), &[]);
        assert_eq!(controllers(&managed.by), by);
        assert!(managed.manages.is_empty());
    }

    #[rstest]
    fn marks_a_controller_its_kind_knowledge_named() {
        let provenance = Provenance::Kinds {
            knowledge: "caches.cache.example.io".into(),
            field: "controller.image".into(),
        };
        let edge = Edge {
            provenance: provenance.clone(),
            ..controlled(cache("shop"), operator("cache-system", "operator"), false)
        };
        let graph = Graph::new([vec![edge]]);
        let managed = Managed::of_workload(&graph, &cache("shop"), &Plurals::new(), &[]);
        assert_eq!(managed.provenances().collect::<Vec<_>>(), vec![&provenance]);
    }

    #[rstest]
    fn an_operator_counts_what_it_manages_by_kind() {
        let operator = operator("cache-system", "operator");
        let plurals = Plurals::from([(
            ("cache.example.io".to_string(), "Cache".to_string()),
            "caches".to_string(),
        )]);
        let managed = Managed::of_workload(&graph(), &operator, &plurals, &[]);
        let manages: Vec<(&str, Option<&str>)> = managed
            .manages
            .iter()
            .map(|r| (r.text.as_str(), r.href.as_deref()))
            .collect();
        assert_eq!(
            manages,
            [("2 Caches", Some("/kinds/caches.cache.example.io"))]
        );
        assert!(managed.by.is_empty());
    }

    #[rstest]
    #[case::managed("shop", vec!["cache-system"], vec![])]
    #[case::managed_by_a_guess("blog", vec!["cache-system (a guess)", "other-system (a guess)"], vec![])]
    #[case::managing("cache-system", vec![], vec!["blog (a guess)", "shop"])]
    fn a_namespace_names_both_directions(
        #[case] namespace: &str,
        #[case] by: Vec<&str>,
        #[case] manages: Vec<&str>,
    ) {
        let managed = Managed::of_namespace(&graph(), namespace, &[]);
        let by_namespace: Vec<&str> = managed
            .by
            .iter()
            .map(|c| c.reference.text.as_str())
            .collect();
        assert_eq!((by_namespace, texts(&managed.manages)), (by, manages));
    }

    #[rstest]
    fn a_namespace_managing_its_own_says_nothing() {
        let graph = Graph::new([vec![controlled(
            cache("shop"),
            operator("shop", "operator"),
            false,
        )]]);
        assert!(Managed::of_namespace(&graph, "shop", &[]).is_empty());
    }

    #[rstest]
    #[case::none(&[], None)]
    #[case::one(&["Roles"], Some("Controllers may be missing: transect may not list Roles."))]
    #[case::two(
        &["Roles", "RoleBindings"],
        Some("Controllers may be missing: transect may not list Roles and RoleBindings.")
    )]
    #[case::three(
        &["ClusterRoles", "Roles", "RoleBindings"],
        Some("Controllers may be missing: transect may not list ClusterRoles, Roles and RoleBindings.")
    )]
    fn says_which_rbac_kinds_are_missing(#[case] kinds: &[&str], #[case] text: Option<&str>) {
        assert_eq!(rbac_missing(kinds).as_deref(), text);
    }
}
