//! The NetworkPolicy gate of a way in, worked out for each of its
//! ports.
//!
//! A policy that selects a pod limits its callers only through its
//! rules, and a rule with no `from`, or with an `ipBlock` of every
//! address and no exceptions, admits any caller on its ports. So the
//! gate is on when policies select every pod that the way reaches and
//! no rule admits any caller on the way's ports. It is off when no
//! policy selects the pods, when the pods are on their machine's
//! network, or when rules admit any caller on every port of the way.
//! It is half in every other case. For a way in through an ingress
//! controller, the gate's title also says whether the policy admits the
//! controller's pods.

use super::{Backend, Gate, Isolation, Sources, State, Target};
use transect_graph::edges::Object;
use transect_network::policy::{Admission, Ports};

/// One port of a way in, with the pod ports that it forwards to.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Leg {
    pub port: String,
    pub targets: Vec<Target>,
}

impl Leg {
    /// The leg for a way's only port, which forwards to the distinct
    /// ports of `backends`.
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
    // `open` pairs a port with a policy that admits any caller on it.
    // `limited` has the ports where no policy does.
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
                join_with_and(&limited)
            ),
        ),
    }
}

/// The policy gate of a workload itself, for all its ports.  The gate
/// is on when a policy limits who may call every port, half when a rule
/// admits any caller on some ports, and off when a rule admits any
/// caller on every port, when no policy selects its pods, or when its
/// pods are on their machine's network.
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
    let sentences: Vec<String> = anyone
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
            Gate::of(State::Off, &format!("{}.", sentences.join("; ")))
        }
        _ => Gate::of(
            State::Half,
            &format!(
                "{}, and a NetworkPolicy names who may call on its other ports.",
                sentences.join("; ")
            ),
        ),
    }
}

/// The on gate of a way whose every pod a policy selects and limits.
/// Its title says whether the policy admits the ingress controller that
/// the way goes through.
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

/// The sentence for the `open` ports, with each policy's ports grouped:
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
    let sentences: Vec<String> = policies
        .iter()
        .map(|(policy, ports)| {
            format!(
                "NetworkPolicy {policy} admits any caller on {}",
                join_with_and(ports)
            )
        })
        .collect();
    sentences.join("; ")
}

fn join_with_and(items: &[&str]) -> String {
    match items {
        [] => String::new(),
        [one] => one.to_string(),
        [a, b] => format!("{a} and {b}"),
        [rest @ .., last] => format!("{}, and {last}", rest.join(", ")),
    }
}

/// The port numbers that `targets` mean on `workload`'s pods.  A number
/// stays as it is, and a name is looked up in the pods' container
/// ports. `None` stands for a port with no known number: a name that no
/// pod declares, or every port when `targets` is empty.  A rule covers
/// `None` only when it admits every port.
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

/// The container port number that `workload`'s pods declare under the
/// name `name`.
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
