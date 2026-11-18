//! A namespace's access tab: the ports that its workloads serve, each
//! way in from outside the cluster with its gates, and the policies
//! that limit calls to the workloads.  Below them are a row for each
//! account that may act in the namespace and a line on its Pod Security
//! level.

use askama::Template;

use super::accounts::{Column, Named, Row};
use super::ways::page::Dot;
use transect_frame::frame::crumbs::Crumb;
use transect_frame::frame::tabs::{Lens, Tabs};
use transect_graph::edges::Provenance;
use transect_map::cards::NamespaceCard;

/// A workload that serves a port, with each way in that reaches it.
#[derive(Debug)]
pub struct Served {
    pub name: Named,
    /// "serves 8080 · 9090".
    pub serves: String,
    /// One row for each way in.  Empty when no way in reaches the
    /// workload.
    pub ways: Vec<Way>,
    /// The policies that limit calls in to the workload, each linked.
    /// When there are none, `unlimited` says why.
    pub limited: Vec<Named>,
    pub unlimited: &'static str,
    /// Whether a policy limits calls out from the workload.
    pub out: Dot,
    /// Whether a policy limits calls in, for a workload that no way in
    /// reaches.
    pub policy: Dot,
}

impl Served {
    /// How many table rows it spans.
    pub fn span(&self) -> usize {
        self.ways.len().max(1)
    }
}

/// One way in to a workload.
#[derive(Debug)]
pub struct Way {
    /// The machine, when this way in is a machine's address, linked to
    /// the machine's page.  `name` holds the rest of the way's name.
    pub machine: Option<Named>,
    pub name: String,
    /// The sentence that says why a pod is on its machine's network,
    /// with where the fact comes from.
    pub declared_at: Option<(String, Provenance)>,
    pub via: Vec<Named>,
    pub how: Option<&'static str>,
    /// One string for each port, so that the table cell wraps between
    /// ports and never inside one.
    pub ports: Vec<String>,
    pub gates: [Dot; 3],
}

#[derive(Template)]
#[template(path = "access/namespace/namespace.html", blocks = ["main"])]
pub struct NamespaceAccessPage {
    pub namespace: String,
    pub layer: String,
    pub served: Vec<Served>,
    pub columns: Vec<Column>,
    pub accounts: Vec<Row>,
    /// The accounts of Kubernetes' own components and controllers,
    /// which the page folds.
    pub controllers: Vec<Row>,
    /// What the namespace's Pod Security `enforce` label lets a pod do.
    pub pod_security: String,
    pub card: NamespaceCard,
}

impl NamespaceAccessPage {
    pub fn path(&self) -> String {
        format!(
            "{}/access",
            transect_graph::links::namespace(&self.namespace)
        )
    }

    /// "3 workloads serve 5 ports".
    pub fn ports_count(&self) -> String {
        let workloads = match self.served.len() {
            1 => "1 workload serves".to_string(),
            count => format!("{count} workloads serve"),
        };
        let ways: usize = self.served.iter().map(|served| served.ways.len()).sum();
        let reach = match self.served.len() {
            1 => "it",
            _ => "them",
        };
        match ways {
            0 => format!("{workloads} ports; no way in reaches {reach}"),
            1 => format!("{workloads} ports; 1 way in reaches {reach}"),
            ways => format!("{workloads} ports; {ways} ways in reach {reach}"),
        }
    }

    pub fn crumbs(&self) -> Vec<Crumb> {
        let select = format!("/?select={}", self.namespace);
        vec![
            Crumb::home(),
            Crumb::link(&self.layer, format!("{select}#{}", self.layer)),
            Crumb::link_name(
                &self.namespace,
                transect_graph::links::namespace(&self.namespace),
            ),
            Crumb::current("access"),
        ]
    }

    pub fn tabs(&self) -> Tabs {
        Tabs::namespace(
            &transect_graph::links::namespace(&self.namespace),
            Lens::Access,
        )
    }

    /// Whether the card links to the namespace's access tab.  It does
    /// not, because this page is that tab.
    pub fn card_lens(&self) -> bool {
        false
    }

    /// The card shows the namespace's name without a link, because this
    /// page is the namespace's own page.
    pub fn card_link(&self) -> Option<String> {
        None
    }

    /// The card lists no workloads, because the page's table lists
    /// them.
    pub fn card_workloads(&self) -> bool {
        false
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path())
    }
}

/// The sentence that says what a pod in `namespace` may do, from the
/// namespace's Pod Security `enforce` label, `level`.
pub fn pod_security(namespace: &str, level: Option<&str>) -> String {
    let may =
        "a pod here may be privileged, use its machine's network, and mount its machine's paths";
    let may_not =
        "a pod here may not be privileged, use its machine's network, or mount its machine's paths";
    match level {
        // The API does not show a default level that the API server's
        // AdmissionConfiguration sets.
        None => format!(
            "Namespace {namespace} has no Pod Security enforce label, so {may}, unless the API server's admission configuration sets a default level."
        ),
        Some("privileged") => {
            format!("Namespace {namespace} enforces the Pod Security level privileged, so {may}.")
        }
        Some("baseline") => {
            format!("Namespace {namespace} enforces the Pod Security level baseline, so {may_not}.")
        }
        Some("restricted") => format!(
            "Namespace {namespace} enforces the Pod Security level restricted, so {may_not}, and runs as a user other than root."
        ),
        Some(other) => format!(
            "Namespace {namespace} names the Pod Security level {other} to enforce, which is none of privileged, baseline, or restricted."
        ),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::no_label(
        None,
        "Namespace shop has no Pod Security enforce label, so a pod here may be privileged, use its machine's network, and mount its machine's paths, unless the API server's admission configuration sets a default level."
    )]
    #[case::baseline(
        Some("baseline"),
        "Namespace shop enforces the Pod Security level baseline, so a pod here may not be privileged, use its machine's network, or mount its machine's paths."
    )]
    #[case::restricted(
        Some("restricted"),
        "Namespace shop enforces the Pod Security level restricted, so a pod here may not be privileged, use its machine's network, or mount its machine's paths, and runs as a user other than root."
    )]
    fn says_what_pod_security_lets_a_pod_do(#[case] level: Option<&str>, #[case] expected: &str) {
        assert_eq!(pod_security("shop", level), expected);
    }
}
