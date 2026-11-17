//! The words that show a loop, on a card and in the cluster sidebar.
//! These templates are the only words; everything else is a name from
//! the loop.

use super::{Cycle, Endpoint, Kind, Loop, Step};

/// The most members a title names before it counts them instead.
const NAMED: usize = 4;

/// What a loop means, under its title.
const MEANING: &str = "Each of these needs another of them to start. \
    While all of them run, the loop has no effect; a cold start must start one without the others.";

impl Loop {
    /// "A loop: forge and deploy", or "A loop of 6 namespaces" when it has
    /// more than `NAMED` members.
    pub fn title(&self) -> String {
        let names = &self.namespaces;
        match names.as_slice() {
            [one] => format!("A loop: {one}"),
            [one, two] => format!("A loop: {one} and {two}"),
            _ if names.len() > NAMED => format!("A loop of {} namespaces", names.len()),
            [rest @ .., last] => format!("A loop: {}, and {last}", rest.join(", ")),
            [] => "A loop".into(),
        }
    }

    /// What any loop means, the same for each.
    pub fn meaning(&self) -> &'static str {
        MEANING
    }
}

impl Step {
    /// The edges by which this member needs the next, as "reconciles
    /// from", or "reconciles from and depends on" when edges of more
    /// than one kind join the two.
    pub fn verb(&self) -> String {
        let mut verbs: Vec<&str> = Vec::new();
        for verb in self.kinds.iter().map(|&kind| verb(kind)) {
            if !verbs.contains(&verb) {
                verbs.push(verb);
            }
        }
        verbs.join(" and ")
    }
}

impl Cycle {
    /// The member the cycle starts from and returns to.
    pub fn first(&self) -> &str {
        self.steps.first().map_or("", |step| step.member.as_str())
    }
}

/// How a link names one member: an object by its kind, and its name
/// after its namespace, because a loop's members are in more than one and
/// an applier often shares its workload's name; a machine as a
/// machine; a box outside the cluster by the address that names it.
pub fn name(end: &Endpoint) -> String {
    match end {
        Endpoint::Object(object) => crate::links::kind_and_name_in(object, None),
        Endpoint::Machine(name) => format!("machine {name}"),
        Endpoint::Outside(name) => name.clone(),
        Endpoint::Device(device) => match &device.machine {
            Some(machine) => format!("device {} on {machine}", device.short()),
            None => format!("device {}", device.short()),
        },
    }
}

/// The verb phrase for one kind of edge.  Owner references and a custom
/// resource's controller mean the same to a reader: something controls it.
fn verb(kind: Kind) -> &'static str {
    match kind {
        Kind::ReconcilesFrom => "reconciles from",
        Kind::DependsOn => "depends on",
        Kind::ManagedBy | Kind::ControlledBy => "is controlled by",
        Kind::StoresOn => "stores on",
        Kind::RunsOn => "runs on",
        Kind::Selects => "selects",
        Kind::RoutesTo => "routes to",
        Kind::AllowedToCall => "may call",
        Kind::Holds => "holds",
        Kind::AuthenticatesThrough => "authenticates through",
        Kind::PartOf => "is part of",
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::edges::Object;

    fn named(namespaces: &[&str]) -> Loop {
        Loop {
            namespaces: namespaces.iter().map(|&name| name.into()).collect(),
            links: Vec::new(),
        }
    }

    #[rstest]
    #[case::two(&["deploy", "forge"], "A loop: deploy and forge")]
    #[case::three(&["a", "b", "c"], "A loop: a, b, and c")]
    #[case::four(&["a", "b", "c", "d"], "A loop: a, b, c, and d")]
    #[case::many(&["a", "b", "c", "d", "e"], "A loop of 5 namespaces")]
    fn titles_a_loop_by_its_members(#[case] namespaces: &[&str], #[case] title: &str) {
        assert_eq!(named(namespaces).title(), title);
    }

    fn step(kinds: &[Kind]) -> Step {
        Step {
            member: "forge".into(),
            kinds: kinds.to_vec(),
        }
    }

    #[rstest]
    #[case::one_kind(&[Kind::ReconcilesFrom], "reconciles from")]
    #[case::two_kinds(&[Kind::ReconcilesFrom, Kind::DependsOn], "reconciles from and depends on")]
    #[case::two_kinds_that_read_the_same(&[Kind::ManagedBy, Kind::ControlledBy], "is controlled by")]
    #[case::three_kinds(
        &[Kind::DependsOn, Kind::ControlledBy, Kind::StoresOn],
        "depends on and is controlled by and stores on"
    )]
    fn names_the_edges_of_an_arrow(#[case] kinds: &[Kind], #[case] verb: &str) {
        assert_eq!(step(kinds).verb(), verb);
    }

    #[rstest]
    #[case::a_namespaced_object(
        Endpoint::Object(Object::new("apps", "Deployment", Some("forge"), "web")),
        "Deployment forge/web"
    )]
    #[case::a_cluster_scoped_object(
        Endpoint::Object(Object::new("example.io", "Fleet", None, "all")),
        "Fleet all"
    )]
    #[case::a_machine(Endpoint::Machine("alpha".into()), "machine alpha")]
    #[case::outside_the_cluster(Endpoint::Outside("192.0.2.5".into()), "192.0.2.5")]
    #[case::a_device(
        Endpoint::Device(crate::edges::fixtures::device("radio.example.com", "alpha", "usb-1")),
        "device radio usb-1 on alpha"
    )]
    fn names_each_member(#[case] end: Endpoint, #[case] expected: &str) {
        assert_eq!(name(&end), expected);
    }
}
