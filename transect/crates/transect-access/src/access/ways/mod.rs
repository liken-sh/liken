//! The ways in: each host, or address and port, where a request from
//! outside the cluster enters it.  Each way has three gates, the checks
//! that the cluster's objects put on the path to a pod: TLS, sign-in,
//! and a NetworkPolicy.
//!
//! An Ingress gets a row for each port that its controller serves it
//! on.  A LoadBalancer or NodePort Service gets a row.  A pod that uses
//! host ports or the host network gets a row.  A load balancer's
//! listener pods are part of their Service's row.  The address of the
//! ingress controller's Service, where requests to the Ingress hosts
//! enter, gets no row, and the page states it under the table instead.

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
    /// Its pods use the host network, where NetworkPolicy does not
    /// apply.
    MachineNetwork,
}

/// The ingress controller that serves an Ingress: its workload, and the
/// entry points where it serves Ingresses.
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
    /// The NetworkPolicy rules that let callers reach an isolated
    /// workload.
    pub admissions: &'a dyn Fn(&Object) -> Vec<Admission>,
    /// The objects that sign in an Ingress's callers, which receive
    /// each request before the Ingress's workload does.
    pub sign_in: &'a dyn Fn(&Object) -> Vec<Object>,
    pub controller: &'a dyn Fn(&Ingress) -> Controller<'a>,
    /// The workloads that at least one route uses to sign in its
    /// callers.
    pub signs_in: &'a BTreeSet<Object>,
    /// The line of intent on a workload or a route that says the
    /// workload signs in its callers itself.  No Kubernetes object
    /// shows this, so a person declares it.
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

/// How much of a row a gate covers: `On` when every route or pod in
/// the row has it, `Half` when some do, and `Off` when none do.  A gate
/// is `Declared` when no object shows it but a line of intent says it
/// is there.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum State {
    On,
    Half,
    Declared,
    Off,
}

/// One gate on one way.  `names` lists the objects that put it there,
/// and `declared_at` is where the line of intent that declares it was written.
#[derive(Clone, Debug, PartialEq)]
pub struct Gate {
    pub state: State,
    pub title: String,
    pub names: Vec<Object>,
    pub declared_at: Option<Provenance>,
}

impl Gate {
    fn of(state: State, title: &str) -> Self {
        Self {
            state,
            title: title.into(),
            names: Vec::new(),
            declared_at: None,
        }
    }

    /// The gate that a line of intent declares.
    fn declared(line: &Line) -> Self {
        Self {
            state: State::Declared,
            title: line.text.clone(),
            names: Vec::new(),
            declared_at: Some(line.provenance.clone()),
        }
    }
}

/// The sign-in gate of a way that reaches `workloads`, when no object
/// puts one on it.  The gate is `Declared` when every workload has a
/// line of intent that says it signs in its callers itself, `Half` when
/// some do, and `Off` when none do.
fn declared_sign_in(workloads: &[Object], sources: &Sources) -> Gate {
    let declared: Vec<Line> = workloads.iter().filter_map(sources.declares).collect();
    match declared.first() {
        Some(line) if declared.len() == workloads.len() => Gate::declared(line),
        Some(line) => Gate {
            state: State::Half,
            declared_at: Some(line.provenance.clone()),
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
    /// A host, or a description of the addresses that accept requests.
    pub name: String,
    /// The objects that create it: Ingresses, or a Service.
    pub via: Vec<Object>,
    pub ports: Vec<String>,
    pub tls: Gate,
    pub sign_in: Gate,
    pub policy: Gate,
    pub reaches: Vec<Object>,
    /// The Services it passes through and the ports its workloads
    /// serve.
    pub backends: Vec<Backend>,
    /// The machine, when this way is the address of one machine.
    /// `name` starts with the machine's name.
    pub machine: Option<String>,
}

impl Way {
    /// Orders ways by what they go through, then by name (hosts in DNS
    /// order), then by ports.
    pub fn order(&self, other: &Way) -> std::cmp::Ordering {
        self.through
            .cmp(&other.through)
            .then_with(|| transect_graph::dns::order(&self.name, &other.name))
            .then_with(|| self.ports.cmp(&other.ports))
    }

    /// Splits `name` into the machine's name, linked to the machine's
    /// page, and the rest of the name.  A way that is not one machine's
    /// address returns `None` and the whole name.
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

/// An ingress controller that receives the requests to Ingress hosts:
/// its workload, how many hosts it serves, and its Service with that
/// Service's ports and addresses.
#[derive(Clone, Debug, PartialEq)]
pub struct Entered {
    pub workload: Object,
    pub hosts: usize,
    pub service: Option<(Object, Vec<String>, String)>,
}

/// Every way in, and the objects that the page lists in the lines under
/// the rows.
#[derive(Debug, Default)]
pub struct Ways {
    pub rows: Vec<Way>,
    pub entered: Vec<Entered>,
    /// The Ingresses that route only to sign-in workloads of other
    /// routes, with the number of hosts each routes.  Such an Ingress
    /// serves the sign-in's callback, so it is not a way in.
    pub callbacks: Vec<(Object, usize)>,
    /// The Ingresses whose class has no ingress controller workload in
    /// the cluster, so they are not ways in.
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

fn machines(count: usize) -> String {
    match count {
        1 => "1 machine's address".into(),
        count => format!("{count} machines' addresses"),
    }
}

/// A description of the addresses of `nodes`, such as "node-a's
/// address" or "3 machines' addresses", and the machine's name when
/// there is exactly one.
fn machines_named(nodes: &std::collections::BTreeSet<String>) -> (String, Option<String>) {
    match nodes.iter().collect::<Vec<_>>().as_slice() {
        [node] => (format!("{node}'s address"), Some(node.to_string())),
        _ => (machines(nodes.len()), None),
    }
}
