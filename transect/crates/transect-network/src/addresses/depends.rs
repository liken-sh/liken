//! Finds what a workload depends on. It reads the addresses in the
//! literal environment values and arguments of the workload's pod specs
//! and resolves each one, through the `Addresses` index, to the
//! workloads and machines it reaches.

use k8s_openapi::api::core::v1::{Container, PodSpec};

use super::index::Addresses;
use super::parse::{self, Address};
use super::reach::reach;
use transect_cluster::ownership::Ownership;
use transect_graph::edges::{Edge, Endpoint, Graph, Kind, Object, Provenance};

/// One address a workload is configured with, and what it reaches.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Dependency {
    /// The host and port, as `Address::text` shows them.
    pub address: String,
    /// The workloads that the address reaches, a `Service` that selects
    /// no workload, or a machine. It never contains the workload itself
    /// or an object whose top owner is the workload.
    pub to: Vec<Endpoint>,
    /// True when `reach` marked the match as a guess.
    pub guessed: bool,
}

/// What `workload`'s `templates` depend on, with one entry for each
/// host and port, in the order the specs first write them. Addresses
/// with the same host and port but different paths can reach different
/// `Service`s. They merge into one entry with the ends of each, and the
/// entry is a guess only when every one of them is.
///
/// A workload often has its own address in its configuration, such as
/// the external URL that a web application serves on. That is not a
/// dependency, so the function drops each end whose top owner is the
/// workload, and each address that reaches only such ends.
pub fn of_workload(
    addresses: &Addresses,
    graph: &Graph,
    ownership: &Ownership,
    workload: &Object,
    templates: &[&PodSpec],
) -> Vec<Dependency> {
    let mut found: Vec<Dependency> = Vec::new();
    for address in written(templates) {
        let reached = reach(addresses, graph, &address, workload.namespace.as_deref());
        let to: Vec<Endpoint> = reached
            .to
            .into_iter()
            .filter(|end| !owns(ownership, workload, end))
            .collect();
        if to.is_empty() {
            continue;
        }
        let text = address.text();
        match found
            .iter_mut()
            .find(|dependency| dependency.address == text)
        {
            Some(dependency) => {
                let more: Vec<Endpoint> = to
                    .into_iter()
                    .filter(|end| !dependency.to.contains(end))
                    .collect();
                dependency.to.extend(more);
                dependency.guessed &= reached.guessed;
            }
            None => found.push(Dependency {
                address: text,
                to,
                guessed: reached.guessed,
            }),
        }
    }
    found
}

/// A depends-on edge from each workload to each end that its addresses
/// reach, with one edge for each pair.
pub fn depends_on<'a>(
    addresses: &Addresses,
    graph: &Graph,
    ownership: &Ownership,
    workloads: impl IntoIterator<Item = (&'a Object, Vec<&'a PodSpec>)>,
) -> Vec<Edge> {
    let mut edges = Vec::new();
    for (workload, templates) in workloads {
        let from = Endpoint::Object(workload.clone());
        let reached = of_workload(addresses, graph, ownership, workload, &templates)
            .into_iter()
            .map(|dependency| (dependency.to, dependency.guessed));
        edges.extend(edges_to(&from, reached));
    }
    edges
}

/// A depends-on edge from `from` to each end of `reached`, with one
/// edge for each end. An edge is a guess only when every address that
/// reaches its end is.
pub(super) fn edges_to(
    from: &Endpoint,
    reached: impl IntoIterator<Item = (Vec<Endpoint>, bool)>,
) -> Vec<Edge> {
    let mut edges: Vec<Edge> = Vec::new();
    for (ends, guessed) in reached {
        for to in ends {
            match edges.iter_mut().find(|edge| edge.to == to) {
                Some(edge) => edge.guessed &= guessed,
                None => edges.push(Edge {
                    kind: Kind::DependsOn,
                    from: from.clone(),
                    to,
                    guessed,
                    provenance: Provenance::Kubernetes,
                }),
            }
        }
    }
    edges
}

/// Whether `end` is `workload` or an object it owns, by top owner. A
/// `Service` that an operator created for the workload has the
/// workload as its top owner.
fn owns(ownership: &Ownership, workload: &Object, end: &Endpoint) -> bool {
    match end {
        Endpoint::Object(object) => &ownership.top_owner(object) == workload,
        _ => false,
    }
}

/// The addresses in the environment values and arguments of every
/// container and init container of `templates`. A value that comes
/// from a `Secret`, a `ConfigMap`, or a field reference is not in the
/// spec, so it is skipped.
fn written(templates: &[&PodSpec]) -> Vec<Address> {
    let containers = templates.iter().flat_map(|spec| {
        let init = spec.init_containers.iter().flatten();
        spec.containers.iter().chain(init)
    });
    containers.flat_map(of_container).collect()
}

fn of_container(container: &Container) -> Vec<Address> {
    let variables = container.env.iter().flatten();
    let mut found: Vec<Address> = variables
        .filter_map(|variable| Some((&variable.name, variable.value.as_deref()?)))
        .flat_map(|(name, value)| parse::variable(name, value))
        .collect();
    let arguments = container.args.iter().flatten();
    found.extend(arguments.flat_map(|argument| parse::argument(argument)));
    found
}
