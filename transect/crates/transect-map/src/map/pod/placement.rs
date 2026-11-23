//! Where a pod runs: its machine with the machine's role, size, and pod
//! count, when the pod started, and the devices that its claims hold.

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;

use transect_graph::edges::Device;
use transect_ground::machines::Machine;
use transect_observe::health::minutes_between;

#[derive(Debug, PartialEq)]
pub struct Placement {
    /// `None` while the pod is not scheduled.
    pub machine: Option<OnMachine>,
    /// When the pod started, and how many minutes ago.
    pub started: Option<(Timestamp, i64)>,
    /// The devices its claims hold.
    pub held: Vec<Device>,
}

#[derive(Debug, PartialEq)]
pub struct OnMachine {
    pub name: String,
    pub control_plane: bool,
    /// The machine's allocatable CPU cores, or `None` when transect has
    /// no `Node` for it.
    pub cores: Option<f64>,
    /// How many pods run on the machine, this one included, or `None`
    /// when transect has no `Node` for it.
    pub pods: Option<usize>,
}

pub fn placement(pod: &Pod, machines: &[Machine], held: Vec<Device>, now: Timestamp) -> Placement {
    let node = pod.spec.as_ref().and_then(|spec| spec.node_name.as_deref());
    let machine = node.map(|name| {
        let found = machines.iter().find(|machine| machine.name == name);
        OnMachine {
            name: name.into(),
            control_plane: found.is_some_and(|machine| machine.control_plane),
            cores: found.map(|machine| machine.allocatable.cpu.units()),
            pods: found.map(|machine| machine.pods),
        }
    });
    let started = pod.status.as_ref().and_then(|s| s.start_time.as_ref());
    Placement {
        machine,
        started: started.map(|time| (time.0, minutes_between(time.0, now))),
        held,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::containers::fixtures::pod;
    use super::*;
    use transect_ground::machines::machines;

    fn cluster() -> Vec<Machine> {
        let node = |name: &str, labels: &str| {
            serde_yaml_ng::from_str(&format!(
                "metadata: {{name: {name}, labels: {{{labels}}}}}\nstatus: {{allocatable: {{cpu: '4', memory: 8Gi}}}}"
            ))
            .unwrap()
        };
        let nodes = [
            node("alpha", "node-role.kubernetes.io/control-plane: ''"),
            node("beta", ""),
        ];
        let pods = [
            pod("web-1", "{nodeName: alpha, containers: []}", "{}"),
            pod("web-2", "{nodeName: alpha, containers: []}", "{}"),
        ];
        machines(&nodes, &pods, None)
    }

    #[rstest]
    #[case::on_a_machine(
        pod("web-1", "{nodeName: alpha, containers: []}", "{startTime: '2026-01-01T08:00:00Z'}"),
        Placement {
            machine: Some(OnMachine { name: "alpha".into(), control_plane: true, cores: Some(4.0), pods: Some(2) }),
            started: Some(("2026-01-01T08:00:00Z".parse().unwrap(), 240)),
            held: vec![],
        }
    )]
    #[case::on_a_node_missing_from_the_machines(
        pod("web-1", "{nodeName: gone, containers: []}", "{}"),
        Placement {
            machine: Some(OnMachine { name: "gone".into(), control_plane: false, cores: None, pods: None }),
            started: None,
            held: vec![],
        }
    )]
    #[case::not_scheduled(
        pod("web-1", "{containers: []}", "{}"),
        Placement { machine: None, started: None, held: vec![] }
    )]
    fn reads_where_a_pod_runs(#[case] pod: Pod, #[case] expected: Placement) {
        let now = "2026-01-01T12:00:00Z".parse().unwrap();
        assert_eq!(placement(&pod, &cluster(), vec![], now), expected);
    }
}
