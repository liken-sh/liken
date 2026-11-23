//! The "Also needs" row of the workload page: what the workload depends
//! on besides its entry and its volumes, which the row above draws, and
//! how many Secrets it reads.  The row reads the same depends-on edges
//! as the card and the section view.  It lists each address that the
//! workload is configured with, then the target of each depends-on edge
//! that no address resolves to, such as one that an annotation declares.

use std::collections::BTreeSet;

use k8s_openapi::api::core::v1::PodSpec;

use transect_graph::edges::{Edge, Object, Plurals, Provenance};
use transect_graph::environment::{Environment, Kind};
use transect_graph::links::Reference;
use transect_network::addresses::Dependency;

/// One item of the row: an address, or the target of an edge that no
/// address resolves to, with the objects it resolves to.
#[derive(Debug, PartialEq)]
pub struct Need {
    /// For an address, only its host and port, never the rest of the
    /// configured value, which can hold credentials.  For an edge, the
    /// target's name.
    pub text: String,
    pub ends: Vec<Reference>,
    /// The edge's provenance, when it has a mark to show.  Always `None`
    /// for an address.
    pub mark: Option<Provenance>,
}

impl Need {
    /// The page of the object, when the item resolves to exactly one
    /// object and that object has a page.
    pub fn href(&self) -> Option<&str> {
        match self.ends.as_slice() {
            [end] => end.href.as_deref(),
            _ => None,
        }
    }

    /// What it reaches, on hover: "Cluster postgres".
    pub fn title(&self) -> String {
        let ends = self.ends.iter().map(|end| end.text.as_str());
        ends.collect::<Vec<_>>().join(" · ")
    }
}

/// The row's items: the addresses, in the order the specs write them,
/// then the target of each of `edges` that no address resolves to, once
/// each.  `edges` are the workload's depends-on edges.
pub fn needs(
    dependencies: Vec<Dependency>,
    edges: &[&Edge],
    workload: &Object,
    plurals: &Plurals,
) -> Vec<Need> {
    let namespace = workload.namespace.as_deref();
    let reached: Vec<_> = dependencies.iter().flat_map(|d| d.to.clone()).collect();
    let mut found: Vec<Need> = dependencies
        .into_iter()
        .map(|dependency| Need {
            text: dependency.address,
            ends: dependency
                .to
                .iter()
                .map(|end| Reference::to_end(end, namespace, plurals))
                .collect(),
            mark: None,
        })
        .collect();
    let mut named = Vec::new();
    for edge in edges {
        if reached.contains(&edge.to) || named.contains(&&edge.to) {
            continue;
        }
        named.push(&edge.to);
        let end = Reference::to_end(&edge.to, namespace, plurals);
        found.push(Need {
            text: end.text.clone(),
            ends: vec![end],
            mark: edge.provenance.mark().map(|_| edge.provenance.clone()),
        });
    }
    found
}

/// How many Secrets `templates` read, each once, by variable or volume.
pub fn secrets(templates: &[&PodSpec]) -> usize {
    let names: BTreeSet<String> = templates
        .iter()
        .flat_map(|spec| Environment::read(spec).sources)
        .filter(|source| source.kind == Kind::Secret)
        .map(|source| source.name)
        .collect();
    names.len()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_graph::edges::Endpoint;

    fn web() -> Object {
        Object::new("apps", "Deployment", Some("shop"), "web")
    }

    fn need(ends: Vec<Endpoint>) -> Need {
        let dependency = Dependency {
            address: "db:5432".into(),
            to: ends,
            guessed: false,
        };
        needs(vec![dependency], &[], &web(), &Plurals::default()).remove(0)
    }

    /// A depends-on edge from web to `to`, written as `provenance`.
    fn edge(to: Endpoint, provenance: Provenance) -> Edge {
        Edge {
            kind: transect_graph::edges::Kind::DependsOn,
            from: Endpoint::Object(web()),
            to,
            guessed: false,
            provenance,
        }
    }

    fn stated() -> Provenance {
        Provenance::Intent(transect_graph::edges::Stated::Annotation {
            field: "depends-on".into(),
            on: "Deployment web".into(),
        })
    }

    #[rstest]
    #[case::an_address_and_what_only_intent_names(
        vec![edge(deployment("shop", "db"), Provenance::Kubernetes), edge(deployment("data", "pg"), stated())],
        vec![("db:5432", false), ("Deployment data/pg", true)]
    )]
    #[case::an_end_named_twice_once(
        vec![edge(deployment("data", "pg"), stated()), edge(deployment("data", "pg"), Provenance::Kubernetes)],
        vec![("db:5432", false), ("Deployment data/pg", true)]
    )]
    fn lists_what_the_edges_reach_that_no_address_does(
        #[case] edges: Vec<Edge>,
        #[case] expected: Vec<(&str, bool)>,
    ) {
        let dependency = Dependency {
            address: "db:5432".into(),
            to: vec![deployment("shop", "db")],
            guessed: false,
        };
        let edges: Vec<&Edge> = edges.iter().collect();
        let found = needs(vec![dependency], &edges, &web(), &Plurals::default());
        let found: Vec<(&str, bool)> = found
            .iter()
            .map(|need| (need.text.as_str(), need.mark.is_some()))
            .collect();
        assert_eq!(found, expected);
    }

    fn deployment(namespace: &str, name: &str) -> Endpoint {
        Endpoint::Object(Object::new("apps", "Deployment", Some(namespace), name))
    }

    #[rstest]
    #[case::one_workload(vec![deployment("shop", "db")], Some("/namespaces/shop/deployments/db"), "Deployment db")]
    #[case::a_machine(vec![Endpoint::Machine("alpha".into())], Some("/machines/alpha"), "machine alpha")]
    #[case::two_workloads(
        vec![deployment("shop", "db"), deployment("data", "db")],
        None,
        "Deployment db · Deployment data/db"
    )]
    fn leads_to_what_it_reaches(
        #[case] ends: Vec<Endpoint>,
        #[case] href: Option<&str>,
        #[case] title: &str,
    ) {
        let need = need(ends);
        assert_eq!((need.href(), need.title().as_str()), (href, title));
    }

    #[rstest]
    fn counts_each_secret_once() {
        let spec: PodSpec = serde_yaml_ng::from_str(
            "containers: [{name: app, env: [\
             {name: A, valueFrom: {secretKeyRef: {name: db, key: password}}},\
             {name: B, valueFrom: {secretKeyRef: {name: db, key: user}}},\
             {name: C, valueFrom: {configMapKeyRef: {name: settings, key: mode}}}],\
             envFrom: [{secretRef: {name: mail}}]}]",
        )
        .unwrap();
        assert_eq!(secrets(&[&spec, &spec]), 2);
    }
}
