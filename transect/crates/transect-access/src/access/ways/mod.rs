//! Ways in: every host or address, and port, where a request from
//! outside the cluster enters it, with the gates the cluster's objects
//! put on the way to a pod: TLS, sign-in, and a NetworkPolicy.
//!
//! The rows come from three sources.  Ingresses give a row on each port
//! their controller serves them on.  LoadBalancer and NodePort Services
//! give a row each.  Pods that hold host ports or run on their machine's
//! network give a row each.  A load balancer's listener pods are part
//! of their Service's row, and requests to the Ingress hosts enter
//! through the ingress controller's own Service, so neither of those
//! gets a row of its own.

pub mod addresses;
pub mod backends;
#[cfg(test)]
mod fixture;
mod hosts;
pub mod listeners;
pub mod page;
pub mod policy;
#[cfg(test)]
mod policy_tests;
mod routed;
#[cfg(test)]
mod tests;

use std::collections::BTreeSet;
use std::sync::Arc;

use k8s_openapi::api::apps::v1::DaemonSet;
use k8s_openapi::api::core::v1::{Node, Pod, Service};
use k8s_openapi::api::networking::v1::Ingress;

use crate::access::accounts::Named;
pub use backends::{Backend, Target};
pub use listeners::LoadBalancerController;
pub use page::WaysPage;
pub use policy::Leg;
use transect_graph::edges::{Object, Provenance};
use transect_network::entry::entry_points::EntryPoints;
use transect_network::policy::Admission;
use transect_resources::intent::Line;

/// Whether NetworkPolicy isolates a workload's pods for ingress.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Isolation {
    Isolated,
    Open,
    /// Its pods are on their machine's network, where no NetworkPolicy
    /// applies.
    MachineNetwork,
}

/// The ingress controller that serves an Ingress: its workload, and the
/// ports it declares it serves Ingresses on.
pub struct Controller<'a> {
    pub workload: Option<Object>,
    pub entry_points: Option<&'a EntryPoints>,
    /// The `IngressController` that declares the entry points.
    pub declared_by: Option<&'a str>,
}

/// The objects and lookups that [`Ways::read`] reads.
pub struct Sources<'a> {
    pub ingresses: &'a [Arc<Ingress>],
    pub services: &'a [Arc<Service>],
    pub pods: &'a [Arc<Pod>],
    pub daemon_sets: &'a [Arc<DaemonSet>],
    pub nodes: &'a [Arc<Node>],
    pub load_balancers: &'a [LoadBalancerController],
    /// The workload a pod is part of.
    pub workload_of: &'a dyn Fn(&Pod) -> Option<Object>,
    /// The workloads a Service selects.
    pub selected: &'a dyn Fn(&Object) -> Vec<Object>,
    pub isolation: &'a dyn Fn(&Object) -> Option<Isolation>,
    /// The rules that admit callers to an isolated workload.
    pub admissions: &'a dyn Fn(&Object) -> Vec<Admission>,
    /// The objects an Ingress sends each request to before its
    /// workload, which sign its callers in.
    pub sign_in: &'a dyn Fn(&Object) -> Vec<Object>,
    pub controller: &'a dyn Fn(&Ingress) -> Controller<'a>,
    /// The workloads that at least one route signs its callers in
    /// through.
    pub signs_in: &'a BTreeSet<Object>,
    /// The sentence, written on a workload or a route, that states that
    /// the workload signs its callers in itself.  The API does not show
    /// this, so a person has to write it.
    pub declares: &'a dyn Fn(&Object) -> Option<Line>,
}

/// How a way in enters the cluster.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Through {
    Host,
    LoadBalancer,
    /// A Service's `externalIPs`.
    ExternalIp,
    NodePort,
    HostPort,
    MachineNetwork,
}

/// Whether the cluster's objects put a gate on a way: on all of the
/// routes or pods in a row, on some, or on none.  When the API does not
/// show a gate, the gate is declared if the people who run the cluster
/// wrote a sentence that states it is there.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum State {
    On,
    Half,
    Declared,
    Off,
}

/// One gate on one way: its state, its title, the objects that put it
/// there, and where the sentence that declares it was written.
#[derive(Clone, Debug, PartialEq)]
pub struct Gate {
    pub state: State,
    pub title: String,
    pub names: Vec<Object>,
    pub said: Option<Provenance>,
}

impl Gate {
    fn of(state: State, title: &str) -> Self {
        Self {
            state,
            title: title.into(),
            names: Vec::new(),
            said: None,
        }
    }

    /// The gate a sentence declares.
    fn declared(line: &Line) -> Self {
        Self {
            state: State::Declared,
            title: line.text.clone(),
            names: Vec::new(),
            said: Some(line.provenance.clone()),
        }
    }
}

/// The sign-in gate of a way that reaches `workloads`, when no object
/// puts one on it.  The gate is declared when each of them has a
/// sentence that states it signs its callers in itself, half when some
/// do, and open otherwise.
fn declared_sign_in(workloads: &[Object], sources: &Sources) -> Gate {
    let declared: Vec<Line> = workloads.iter().filter_map(sources.declares).collect();
    match declared.first() {
        Some(line) if declared.len() == workloads.len() => Gate::declared(line),
        Some(line) => Gate {
            state: State::Half,
            said: Some(line.provenance.clone()),
            ..Gate::of(
                State::Half,
                &format!(
                    "Some of the workloads it reaches sign their callers in themselves: {}",
                    line.text
                ),
            )
        },
        None => Gate::of(
            State::Off,
            "No route to it signs in through anything in the cluster.",
        ),
    }
}

/// One way in.
#[derive(Clone, Debug, PartialEq)]
pub struct Way {
    pub through: Through,
    /// A host, or the addresses it answers on, in words.
    pub name: String,
    /// The objects that open it: Ingresses, or a Service.
    pub via: Vec<Object>,
    pub ports: Vec<String>,
    pub tls: Gate,
    pub sign_in: Gate,
    pub policy: Gate,
    pub reaches: Vec<Object>,
    /// The Services it passes and the ports its workloads answer on.
    pub backends: Vec<Backend>,
    /// The machine whose address this way is, when it is the address of
    /// one machine.  `name` includes the machine's name.
    pub machine: Option<String>,
}

impl Way {
    /// How `self` and `other` order in a list: by what they go through,
    /// then by name, hosts in DNS order, then by ports.
    pub fn order(&self, other: &Way) -> std::cmp::Ordering {
        self.through
            .cmp(&other.through)
            .then_with(|| transect_graph::dns::order(&self.name, &other.name))
            .then_with(|| self.ports.cmp(&other.ports))
    }

    /// Splits the name into the machine's name, with a link to the
    /// machine's page, and the words after it, so a page can link the
    /// machine on its own.  A way that is not the address of one
    /// machine returns no machine and its whole name.
    pub fn on_machine(&self) -> (Option<Named>, String) {
        let parted = self.machine.as_deref().and_then(|machine| {
            let rest = self.name.strip_prefix(machine)?;
            let named = Named {
                text: machine.to_string(),
                href: Some(transect_graph::links::machine(machine)),
            };
            Some((Some(named), rest.to_string()))
        });
        parted.unwrap_or_else(|| (None, self.name.clone()))
    }
}

/// An ingress controller the hosts enter through: its workload, how
/// many hosts it serves, and the Service it answers on, with its ports
/// and addresses.
#[derive(Clone, Debug, PartialEq)]
pub struct Entered {
    pub workload: Object,
    pub hosts: usize,
    pub service: Option<(Object, Vec<String>, String)>,
}

/// Every way in, and the objects that the page names in the lines under
/// the rows.
#[derive(Debug, Default)]
pub struct Ways {
    pub rows: Vec<Way>,
    pub entered: Vec<Entered>,
    /// The Ingresses that route only to workloads that other routes sign
    /// in through, with the number of hosts each routes.  Such an
    /// Ingress is the sign-in's own callback, so it is not a way in of
    /// its own.
    pub callbacks: Vec<(Object, usize)>,
    /// The Ingresses whose class transect finds no ingress controller
    /// workload for in the cluster, so they are not ways in.
    pub unserved: Vec<Object>,
}

impl Ways {
    pub fn read(sources: &Sources) -> Self {
        let mut ways = hosts::read(sources);
        let controllers: BTreeSet<Object> =
            ways.entered.iter().map(|e| e.workload.clone()).collect();
        addresses::read(sources, &controllers, &mut ways);
        ways
    }
}

/// "3 machines' addresses", or "1 machine's address".
fn machines(count: usize) -> String {
    match count {
        1 => "1 machine's address".into(),
        count => format!("{count} machines' addresses"),
    }
}

/// "node-a's address" for one machine, by name, and "3 machines'
/// addresses" for several, with the one machine's name.
fn machines_named(nodes: &std::collections::BTreeSet<String>) -> (String, Option<String>) {
    match nodes.iter().collect::<Vec<_>>().as_slice() {
        [node] => (format!("{node}'s address"), Some(node.to_string())),
        _ => (machines(nodes.len()), None),
    }
}
