//! The machines band shows the control-plane machines first and the
//! workers after them, so the reader can tell the machines that run the
//! cluster from the machines that run its workloads.

use super::Machine;

#[derive(Debug, PartialEq)]
pub struct Group<'a> {
    /// The role of the group's machines, or `None` when the band shows
    /// only one group.
    pub label: Option<&'static str>,
    pub machines: Vec<&'a Machine>,
}

/// The control-plane machines and the workers, each in the order of
/// `machines`, without an empty group.  When no node has the
/// control-plane label, as on a managed cluster, it returns one group
/// with no label, even when `machines` is empty, because the labels do
/// not show that every machine is a worker.
pub fn groups(machines: &[Machine]) -> Vec<Group<'_>> {
    let (control_plane, workers): (Vec<_>, Vec<_>) =
        machines.iter().partition(|machine| machine.control_plane);
    if control_plane.is_empty() {
        return vec![Group {
            label: None,
            machines: workers,
        }];
    }
    [("control plane", control_plane), ("workers", workers)]
        .into_iter()
        .filter(|(_, machines)| !machines.is_empty())
        .map(|(label, machines)| Group {
            label: Some(label),
            machines,
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::machines::Resources;
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

    fn labels_and_names<'a>(groups: &[Group<'a>]) -> Vec<(Option<&'static str>, Vec<&'a str>)> {
        groups
            .iter()
            .map(|group| {
                let names = group.machines.iter().map(|m| m.name.as_str()).collect();
                (group.label, names)
            })
            .collect()
    }

    #[rstest]
    #[case::mixed(
        &[("alpha", false), ("beta", true), ("gamma", false), ("delta", true)],
        vec![
            (Some("control plane"), vec!["beta", "delta"]),
            (Some("workers"), vec!["alpha", "gamma"]),
        ],
    )]
    #[case::no_control_plane(
        &[("alpha", false), ("beta", false)],
        vec![(None, vec!["alpha", "beta"])],
    )]
    #[case::only_control_plane(
        &[("alpha", true), ("beta", true)],
        vec![(Some("control plane"), vec!["alpha", "beta"])],
    )]
    #[case::no_machines(&[], vec![(None, vec![])])]
    fn groups_the_control_plane_before_the_workers(
        #[case] nodes: &[(&str, bool)],
        #[case] expected: Vec<(Option<&'static str>, Vec<&str>)>,
    ) {
        let machines: Vec<_> = nodes
            .iter()
            .map(|&(name, control_plane)| machine(name, control_plane))
            .collect();
        assert_eq!(labels_and_names(&groups(&machines)), expected);
    }
}
