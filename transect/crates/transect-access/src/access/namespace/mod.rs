//! A namespace's access, at its access tab: the ports its workloads
//! serve, with each way in from outside the cluster and the gates on
//! it, and the policies that limit their calls; then every account
//! that may act in the namespace, a row each; and the namespace's Pod
//! Security level in a line.

use askama::Template;

use super::accounts::{Column, Named, Row};
use super::ways::page::Dot;
use transect_frame::frame::crumbs::Crumb;
use transect_frame::frame::tabs::{Lens, Tabs};
use transect_graph::edges::Provenance;
use transect_map::cards::NamespaceCard;

/// A workload that serves a port, and each way in that reaches it.
#[derive(Debug)]
pub struct Served {
    pub name: Named,
    /// "serves 8080 · 9090".
    pub serves: String,
    /// A row per way in; none for a workload no way reaches.
    pub ways: Vec<Way>,
    /// The policies that limit its calls in, each linked, or, in
    /// `unlimited`, the words that state why none does.
    pub limited: Vec<Named>,
    pub unlimited: &'static str,
    /// Whether a policy limits its calls out.
    pub out: Dot,
    /// For a workload that no way in reaches: whether a policy limits
    /// its calls in, as a dot.
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
    /// The one machine whose address this way is, which links to the
    /// machine's page.  `name` is the rest of the way's name.
    pub machine: Option<Named>,
    pub name: String,
    /// The sentence that states why a pod is on its machine's network.
    pub said: Option<(String, Provenance)>,
    pub via: Vec<Named>,
    pub how: Option<&'static str>,
    /// Each port.  The cell does not break a port across lines, and
    /// wraps only between ports.
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
    /// Kubernetes' own components and controllers, folded.
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

    /// Whether the card links to the namespace's access.  It does not on
    /// this page, because this page is the namespace's access.
    pub fn card_lens(&self) -> bool {
        false
    }

    /// The card names the namespace with no link, because this page is
    /// the namespace's own.
    pub fn card_link(&self) -> Option<String> {
        None
    }

    /// The card lists no workloads, because the page's matrix names them.
    pub fn card_workloads(&self) -> bool {
        false
    }

    pub fn events_url(&self) -> String {
        format!("/events{}", self.path())
    }
}

/// The sentence that states what a namespace's Pod Security `enforce`
/// label, `level`, lets a pod there do.
pub fn pod_security(namespace: &str, level: Option<&str>) -> String {
    let may =
        "a pod here may be privileged, use its machine's network, and mount its machine's paths";
    let may_not =
        "a pod here may not be privileged, use its machine's network, or mount its machine's paths";
    match level {
        // A cluster-wide default in the API server's
        // AdmissionConfiguration is not in the API, so the sentence
        // states that one may apply.
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
