//! The NetworkPolicy gate of a way in, evaluated on each of its ports.
//!
//! A policy that selects the pods limits who may call them only when
//! its rules do: a rule with no `from`, or an address block of every
//! address with no exceptions, admits any caller on the ports it names.
//! So the gate is on where a policy selects every pod the way reaches
//! and no rule admits any caller on the way's ports, open where no
//! policy selects them or a rule admits any caller on every port, and
//! half otherwise.  For a way in through an ingress controller, the
//! gate's title states whether the policy admits the controller's pods.

use super::{Backend, Gate, Isolation, Sources, State, Target};
use transect_graph::edges::Object;
use transect_network::policy::{Admission, Ports};

/// One port of a way in, and the ports of the pods behind it.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Leg {
    pub port: String,
    pub targets: Vec<Target>,
}

impl Leg {
    /// A way's single port, which forwards to each of `backends`.
    pub fn of(port: impl ToString, backends: &[Backend]) -> Self {
        let mut targets: Vec<Target> = backends.iter().map(|b| b.port.clone()).collect();
        targets.sort();
        targets.dedup();
        Self {
            port: port.to_string(),
            targets,
        }
    }
}

/// The policy gate of a way that reaches `workloads` on `legs`, through
/// the ingress controller `controller` when it goes through one.
pub fn gate(
    workloads: &[Object],
    legs: &[Leg],
    controller: Option<&Object>,
    sources: &Sources,
) -> Gate {
    let found: Vec<(&Object, Isolation)> = workloads
        .iter()
        .filter_map(|w| Some((w, (sources.isolation)(w)?)))
        .collect();
    if !found.is_empty() && found.iter().all(|(_, i)| *i == Isolation::MachineNetwork) {
        return Gate::of(
            State::Off,
            "No NetworkPolicy applies to a pod on its machine's network.",
        );
    }
    let isolated: Vec<&Object> = found
        .iter()
        .filter(|(_, i)| *i == Isolation::Isolated)
        .map(|(w, _)| *w)
        .collect();
    if isolated.is_empty() {
        return Gate::of(
            State::Off,
            "No NetworkPolicy selects its pods, so any pod may call them.",
        );
    }
    let every_port = [Leg {
        port: "every port".into(),
        targets: Vec::new(),
    }];
    let legs = match legs.is_empty() {
        true => &every_port[..],
        false => legs,
    };
    // Each leg's port, and the policy that admits any caller on it.
    let mut open: Vec<(&str, String)> = Vec::new();
    let mut limited: Vec<&str> = Vec::new();
    for leg in legs {
        let by = isolated.iter().find_map(|workload| {
            let numbers = numbers(workload, &leg.targets, sources);
            let admissions = (sources.admissions)(workload);
            let found = admissions
                .iter()
                .find(|a| numbers.iter().any(|n| a.admits_anyone_on(*n)));
            found.map(|a| a.policy.clone())
        });
        match by {
            Some(policy) => open.push((&leg.port, policy)),
            None => limited.push(&leg.port),
        }
    }
    let every = isolated.len() == found.len();
    match (open.is_empty(), limited.is_empty()) {
        (true, _) if every => limits(&isolated, legs, controller, sources),
        (true, _) => Gate::of(
            State::Half,
            "A NetworkPolicy selects the pods of some of the workloads it reaches.",
        ),
        (false, true) => Gate::of(State::Off, &format!("{}.", admitting(&open))),
        (false, false) => Gate::of(
            State::Half,
            &format!(
                "{}; a NetworkPolicy limits who may call on {}.",
                admitting(&open),
                and(&limited)
            ),
        ),
    }
}

/// A workload's own policy gate, on any of its ports, from its
/// isolation and the rules that admit callers to it.  The gate is on
/// where a policy limits who may call every port, half where a rule
/// admits any caller on some ports, and open where one admits any
/// caller on every port or no policy selects its pods.
pub fn of_workload(isolation: Option<Isolation>, admissions: &[Admission]) -> Gate {
    match isolation {
        Some(Isolation::MachineNetwork) => {
            return Gate::of(
                State::Off,
                "No NetworkPolicy applies to a pod on its machine's network.",
            );
        }
        Some(Isolation::Isolated) => {}
        _ => {
            return Gate::of(
                State::Off,
                "No NetworkPolicy selects its pods, so any pod may call them.",
            );
        }
    }
    let anyone = admissions.iter().filter(|a| {
        a.callers
            .iter()
            .any(transect_network::policy::Caller::is_anyone)
    });
    let anyone: Vec<&Admission> = anyone.collect();
    let said: Vec<String> = anyone
        .iter()
        .map(|a| {
            format!(
                "NetworkPolicy {} admits any caller on {}",
                a.policy, a.ports
            )
        })
        .collect();
    match anyone.as_slice() {
        [] => Gate::of(
            State::On,
            "A NetworkPolicy selects its pods and names who may call them.",
        ),
        found if found.iter().any(|a| a.ports == Ports::Any) => {
            Gate::of(State::Off, &format!("{}.", said.join("; ")))
        }
        _ => Gate::of(
            State::Half,
            &format!(
                "{}, and a NetworkPolicy names who may call on its other ports.",
                said.join("; ")
            ),
        ),
    }
}

/// The on gate of a way where a policy limits every pod.  Its title
/// states whether the policy admits the ingress controller that the
/// way goes through.
fn limits(
    isolated: &[&Object],
    legs: &[Leg],
    controller: Option<&Object>,
    sources: &Sources,
) -> Gate {
    let Some(controller) = controller else {
        return Gate::of(
            State::On,
            "A NetworkPolicy selects its pods and names who may call them.",
        );
    };
    let namespace = controller.namespace.as_deref().unwrap_or_default();
    let admitted = isolated.iter().all(|workload| {
        let admissions = (sources.admissions)(workload);
        legs.iter().any(|leg| {
            let numbers = numbers(workload, &leg.targets, sources);
            admissions.iter().any(|a| {
                let mut numbers = numbers.iter();
                numbers.any(|n| a.admits_workload(controller, namespace, *n))
            })
        })
    });
    let named = transect_graph::links::kind_and_name_in(controller, None);
    let title = match admitted {
        true => format!(
            "A NetworkPolicy selects its pods, admits the ingress controller {named}, and names who else may call them."
        ),
        false => format!(
            "A NetworkPolicy selects its pods and does not admit the ingress controller {named}."
        ),
    };
    Gate::of(State::On, &title)
}

/// "NetworkPolicy a admits any caller on 80 and 443; NetworkPolicy b
/// admits any caller on 8080".
fn admitting(open: &[(&str, String)]) -> String {
    let mut policies: Vec<(&str, Vec<&str>)> = Vec::new();
    for (port, policy) in open {
        match policies.iter_mut().find(|(p, _)| p == policy) {
            Some((_, ports)) => ports.push(port),
            None => policies.push((policy, vec![port])),
        }
    }
    let said: Vec<String> = policies
        .iter()
        .map(|(policy, ports)| {
            format!("NetworkPolicy {policy} admits any caller on {}", and(ports))
        })
        .collect();
    said.join("; ")
}

fn and(items: &[&str]) -> String {
    match items {
        [] => String::new(),
        [one] => one.to_string(),
        [a, b] => format!("{a} and {b}"),
        [rest @ .., last] => format!("{}, and {last}", rest.join(", ")),
    }
}

/// The port numbers `targets` name on `workload`'s pods: a number as
/// it is, and a name as the pods' containers give it.  `None` means
/// a target that names no number here, or for every port when there are
/// no targets, and a rule covers it only when it admits every port.
fn numbers(workload: &Object, targets: &[Target], sources: &Sources) -> Vec<Option<u16>> {
    if targets.is_empty() {
        return vec![None];
    }
    let mut found: Vec<Option<u16>> = Vec::new();
    for target in targets {
        let number = match target {
            Target::Number(number) => u16::try_from(*number).ok(),
            Target::Name(name) => named(workload, name, sources),
        };
        if !found.contains(&number) {
            found.push(number);
        }
    }
    found
}

/// The number `workload`'s pods give the port `name`.
fn named(workload: &Object, name: &str, sources: &Sources) -> Option<u16> {
    let pods = sources.pods.iter();
    let mut pods = pods.filter(|pod| (sources.workload_of)(pod).as_ref() == Some(workload));
    pods.find_map(|pod| {
        let containers = pod.spec.iter().flat_map(|spec| spec.containers.iter());
        let mut ports = containers.flat_map(|c| c.ports.iter().flatten());
        let port = ports.find(|p| p.name.as_deref() == Some(name))?;
        u16::try_from(port.container_port).ok()
    })
}
