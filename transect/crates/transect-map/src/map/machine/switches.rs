//! The row of machines to switch between, in the machines band's
//! groups: the control plane first and the workers after it, each group
//! after the band's gray label.  The groups take no border of their
//! own, because a ring marks the machine shown.

use transect_ground::machines::{Machine, groups};

/// One machine in the row to switch between.
#[derive(Debug, PartialEq)]
pub struct Switch {
    pub name: String,
    pub current: bool,
    pub control_plane: bool,
    /// The band's label for the group it starts, "control plane" or
    /// "workers", or none inside a group or when the band draws none.
    pub label: Option<&'static str>,
}

impl Switch {
    pub fn href(&self) -> String {
        format!("/machines/{}", self.name)
    }

    pub fn class(&self) -> String {
        let mut class = String::from("switch");
        if self.current {
            class.push_str(" switch-current");
        }
        class
    }
}

/// A switch for each of `machines`, in the band's groups, with the one
/// named `current` marked.
pub fn switches(machines: &[Machine], current: &str) -> Vec<Switch> {
    let groups = groups(machines);
    let mut switches = Vec::new();
    for group in &groups {
        for (at, machine) in group.machines.iter().enumerate() {
            switches.push(Switch {
                name: machine.name.clone(),
                current: machine.name == current,
                control_plane: machine.control_plane,
                label: group.label.filter(|_| at == 0),
            });
        }
    }
    switches
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_ground::machines::Resources;
    use transect_observe::usage::Used;

    fn machine(name: &str, control_plane: bool) -> Machine {
        Machine {
            name: name.into(),
            control_plane,
            etcd: false,
            ready: true,
            allocatable: Resources::default(),
            requested: Resources::default(),
            used: Used::default(),
            pods: 0,
            here: None,
            lost: false,
            really_lost: false,
            lost_minutes: None,
            held: Vec::new(),
            pod_memory: vec![],
            alerted: None,
        }
    }

    #[rstest]
    #[case::control_plane_first_then_the_workers(
        &[("alpha", false), ("beta", true), ("gamma", false)],
        "gamma",
        &["control plane beta", "workers alpha", "gamma current"],
    )]
    #[case::no_control_plane_no_labels(
        &[("alpha", false), ("beta", false)],
        "alpha",
        &["alpha current", "beta"],
    )]
    #[case::current_control_plane(
        &[("alpha", true), ("beta", false)],
        "alpha",
        &["control plane alpha current", "workers beta"],
    )]
    fn orders_the_control_plane_first(
        #[case] nodes: &[(&str, bool)],
        #[case] current: &str,
        #[case] shown: &[&str],
    ) {
        let machines: Vec<Machine> = nodes.iter().map(|&(n, cp)| machine(n, cp)).collect();
        let found = switches(&machines, current);
        let said: Vec<String> = found
            .iter()
            .map(|s| {
                let label = s.label.map(|l| format!("{l} ")).unwrap_or_default();
                let current = if s.current { " current" } else { "" };
                format!("{label}{}{current}", s.name)
            })
            .collect();
        assert_eq!(said, shown);
    }
}
