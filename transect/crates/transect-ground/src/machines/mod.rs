//! The machines band shows one strip for each `Node`.  A strip compares
//! the CPU and memory that the node's pods request and use with what the
//! node can allocate, and counts the pods that run on the node.

mod amount;
mod facts;
mod group;
mod limits;
mod pod_memory;
mod requests;
mod runs_on;

use std::collections::HashMap;

use k8s_openapi::api::core::v1::{Node, Pod};

pub use amount::Amount;
pub use facts::Facts;
pub use group::{Group, groups};
pub use limits::pod_limits;
pub use pod_memory::PodMemory;
pub use requests::Resources;
pub use requests::pod_requests;
pub use runs_on::{count_here, runs_on};
use transect_graph::edges::Device;
use transect_graph::pods::finished;
use transect_observe::alerts::Alerted;
use transect_observe::usage::{Usage, Used};

const CONTROL_PLANE_LABEL: &str = "node-role.kubernetes.io/control-plane";

/// The role label that a distribution such as k3s sets on the `Node` of
/// each machine that runs an etcd member.
pub const ETCD_LABEL: &str = "node-role.kubernetes.io/etcd";
pub const BYTES_PER_GI: f64 = (1u64 << 30) as f64;
pub const BYTES_PER_MI: f64 = (1u64 << 20) as f64;

#[derive(Debug, PartialEq)]
pub struct Machine {
    pub name: String,
    /// Whether the node has the control-plane role label.
    pub control_plane: bool,
    /// Whether the node has the etcd role label.
    pub etcd: bool,
    pub ready: bool,
    pub allocatable: Resources,
    pub requested: Resources,
    pub used: Used,
    pub pods: usize,
    /// How many of the selected namespace's pods run here, or none when
    /// nothing is selected.
    pub here: Option<usize>,
    /// Whether the loss that the page shows includes this machine.  When
    /// it does, the strip shows what runs there in place of its bars.
    pub lost: bool,
    /// Whether the machine is really lost, not only supposed lost, and
    /// how many minutes ago it was lost, when its `Node` gives the time.
    pub really_lost: bool,
    pub lost_minutes: Option<i64>,
    /// The devices that the selected namespace holds on this machine.
    /// The `devices` module fills the list, and it is empty when nothing
    /// is selected.
    pub held: Vec<Device>,
    /// The memory that each pod on this machine uses, beside its request.
    /// The list includes every pod, with or without a reading, so a
    /// finding can count the pods that request no memory even when
    /// Prometheus has no reading for some of them.
    pub pod_memory: Vec<PodMemory>,
    /// The alerts that are firing about this machine.  The page marks
    /// the machine's strip with them.
    pub alerted: Option<Alerted>,
}

/// The totals of the pods that are bound to one node.
#[derive(Default)]
struct OnNode {
    requested: Resources,
    pods: usize,
    pod_memory: Vec<PodMemory>,
}

/// Every machine, sorted by name, with what it uses when `usage` has a
/// reading for it.
pub fn machines<'a>(
    nodes: impl IntoIterator<Item = &'a Node>,
    pods: impl IntoIterator<Item = &'a Pod>,
    usage: Option<&Usage>,
) -> Vec<Machine> {
    let mut on_node: HashMap<&str, OnNode> = HashMap::new();
    for pod in pods {
        let Some(spec) = &pod.spec else { continue };
        let Some(node) = spec.node_name.as_deref() else {
            continue;
        };
        if finished(pod) {
            continue;
        }
        let requests = pod_requests(spec);
        let on_node = on_node.entry(node).or_default();
        on_node.requested = [on_node.requested, requests].into_iter().sum();
        on_node.pods += 1;
        let (namespace, name) = (
            pod.metadata.namespace.as_deref(),
            pod.metadata.name.as_deref(),
        );
        if let (Some(namespace), Some(name)) = (namespace, name) {
            on_node.pod_memory.push(PodMemory {
                namespace: namespace.into(),
                name: name.into(),
                used: usage.and_then(|usage| usage.of_pod(namespace, name).memory),
                requested: requests.memory,
            });
        }
    }

    let mut machines: Vec<Machine> = nodes
        .into_iter()
        .filter_map(|node| {
            let name = node.metadata.name.clone()?;
            let OnNode {
                requested,
                pods,
                pod_memory,
            } = on_node.remove(name.as_str()).unwrap_or_default();
            let status = node.status.as_ref();
            Some(Machine {
                control_plane: control_plane(node),
                etcd: labelled(node, ETCD_LABEL),
                ready: ready(node),
                allocatable: Resources::from_list(status.and_then(|s| s.allocatable.as_ref())),
                requested,
                used: usage.map(|usage| usage.of(&name)).unwrap_or_default(),
                pods,
                here: None,
                lost: false,
                really_lost: false,
                lost_minutes: None,
                held: Vec::new(),
                pod_memory,
                alerted: None,
                name,
            })
        })
        .collect();
    machines.sort_by(|a, b| a.name.cmp(&b.name));
    machines
}

/// Whether `node`'s `Ready` condition is `True`.
pub fn ready(node: &Node) -> bool {
    node.status
        .as_ref()
        .and_then(|s| s.conditions.as_ref())
        .into_iter()
        .flatten()
        .any(|c| c.type_ == "Ready" && c.status == "True")
}

/// Whether `node` has the control-plane role label.
pub fn control_plane(node: &Node) -> bool {
    labelled(node, CONTROL_PLANE_LABEL)
}

fn labelled(node: &Node, label: &str) -> bool {
    let labels = node.metadata.labels.as_ref();
    labels.is_some_and(|labels| labels.contains_key(label))
}

impl Machine {
    pub fn cpu_percent(&self) -> u32 {
        percent(self.requested.cpu.units(), self.allocatable.cpu.units())
    }

    pub fn memory_percent(&self) -> u32 {
        percent(
            self.requested.memory.units(),
            self.allocatable.memory.units(),
        )
    }

    /// How much of the CPU bar the used bar fills, when there is a
    /// reading.
    pub fn cpu_used_percent(&self) -> Option<u32> {
        let used = self.used.cpu?;
        Some(percent(used, self.allocatable.cpu.units()))
    }

    pub fn memory_used_percent(&self) -> Option<u32> {
        let used = self.used.memory?;
        Some(percent(used, self.allocatable.memory.units()))
    }

    /// The CPU bar's title, such as "CPU 1.6 used · 5.0 requested of 8".
    pub fn cpu_title(&self) -> String {
        format!(
            "CPU {}{:.1} requested of {}",
            used_text(self.used.cpu),
            self.requested.cpu.units(),
            trimmed(self.allocatable.cpu.units())
        )
    }

    /// The memory bar's title, such as
    /// "mem 8.0 used · 10.7 requested of 15.5 Gi".
    pub fn memory_title(&self) -> String {
        format!(
            "mem {}{:.1} requested of {:.1} Gi",
            used_text(self.used.memory.map(|bytes| bytes / BYTES_PER_GI)),
            self.requested.memory.units() / BYTES_PER_GI,
            self.allocatable.memory.units() / BYTES_PER_GI
        )
    }
}

/// The used part of a title, or an empty string when there is no
/// reading.
fn used_text(used: Option<f64>) -> String {
    used.map(|used| format!("{used:.1} used · "))
        .unwrap_or_default()
}

/// The percentage of a bar that `part` fills, capped at a full bar.  The
/// cap is necessary because a pod bound to its node by name skips the
/// scheduler, so the requests on a node can exceed what it allocates.
fn percent(part: f64, whole: f64) -> u32 {
    if whole <= 0.0 {
        return 0;
    }
    (part / whole * 100.0).round().clamp(0.0, 100.0) as u32
}

/// A number with one decimal place, or with none when it is whole, such
/// as "8" or "7.5".
pub fn trimmed(value: f64) -> String {
    let text = format!("{value:.1}");
    text.strip_suffix(".0").map(str::to_owned).unwrap_or(text)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn node(yaml: &str) -> Node {
        serde_yaml::from_str(yaml).unwrap()
    }

    fn pod(node: &str, phase: &str, cpu: &str, memory: &str) -> Pod {
        serde_yaml::from_str(&format!(
            r#"
metadata: {{name: app}}
spec:
  nodeName: {node}
  containers: [{{name: app, resources: {{requests: {{cpu: '{cpu}', memory: '{memory}'}}}}}}]
status: {{phase: {phase}}}
"#
        ))
        .unwrap()
    }

    fn worker(name: &str) -> Node {
        node(&format!(
            r#"
metadata: {{name: {name}}}
status:
  allocatable: {{cpu: '4', memory: 8Gi}}
  conditions: [{{type: Ready, status: 'True'}}]
"#
        ))
    }

    #[rstest]
    fn sums_the_requests_of_running_pods_on_each_node() {
        let nodes = [worker("beta"), worker("alpha")];
        let pods = [
            pod("alpha", "Running", "500m", "1Gi"),
            pod("alpha", "Pending", "250m", "512Mi"),
            pod("alpha", "Succeeded", "2", "4Gi"),
            pod("alpha", "Failed", "2", "4Gi"),
            pod("beta", "Running", "1", "2Gi"),
        ];
        let machines = machines(&nodes, &pods, None);
        let summary: Vec<_> = machines
            .iter()
            .map(|m| (m.name.as_str(), m.cpu_title(), m.memory_title(), m.pods))
            .collect();
        assert_eq!(
            summary,
            vec![
                (
                    "alpha",
                    "CPU 0.8 requested of 4".into(),
                    "mem 1.5 requested of 8.0 Gi".into(),
                    2
                ),
                (
                    "beta",
                    "CPU 1.0 requested of 4".into(),
                    "mem 2.0 requested of 8.0 Gi".into(),
                    1
                ),
            ]
        );
    }

    #[rstest]
    fn a_node_with_no_pods_requests_nothing() {
        let machines = machines(&[worker("alpha")], &[], None);
        assert_eq!(machines[0].requested, Resources::default());
        assert_eq!(machines[0].pods, 0);
    }

    fn usage(cpu: f64, memory: f64) -> Usage {
        let sample = |value| transect_observe::prometheus::Sample {
            labels: [("node".into(), "alpha".into())].into(),
            value,
        };
        let labels = transect_observe::usage::fixtures::node_labels();
        Usage::from_samples(vec![sample(cpu)], vec![sample(memory)], &labels)
    }

    #[rstest]
    fn draws_what_a_machine_uses_beside_what_it_requests() {
        let nodes = [worker("alpha")];
        let pods = [pod("alpha", "Running", "2", "2Gi")];
        let usage = usage(1.0, 6.0 * BYTES_PER_GI);
        let machine = &machines(&nodes, &pods, Some(&usage))[0];
        assert_eq!(
            (
                machine.cpu_title(),
                machine.cpu_used_percent(),
                machine.memory_title(),
                machine.memory_used_percent()
            ),
            (
                "CPU 1.0 used · 2.0 requested of 4".into(),
                Some(25),
                "mem 6.0 used · 2.0 requested of 8.0 Gi".into(),
                Some(75)
            )
        );
    }

    fn pod_in(namespace: &str, name: &str, node: &str, phase: &str, memory: &str) -> Pod {
        let mut pod = pod(node, phase, "100m", memory);
        pod.metadata.namespace = Some(namespace.into());
        pod.metadata.name = Some(name.into());
        pod
    }

    #[rstest]
    fn joins_each_pods_memory_to_its_node_through_the_pod() {
        let nodes = [worker("alpha"), worker("beta")];
        let pods = [
            pod_in("shop", "web", "alpha", "Running", "1Gi"),
            pod_in("shop", "cart", "beta", "Running", "512Mi"),
            pod_in("shop", "done", "alpha", "Succeeded", "1Gi"),
            pod_in("shop", "unread", "beta", "Running", "1Gi"),
        ];
        let sample = |pod: &str, value| transect_observe::prometheus::Sample {
            labels: [
                ("namespace".into(), "shop".into()),
                ("pod".into(), pod.into()),
                ("container".into(), "app".into()),
            ]
            .into(),
            value,
        };
        let usage = Usage::default().with_containers(
            vec![sample("web", 2e9), sample("cart", 1e8), sample("done", 5e9)],
            vec![],
            &transect_observe::usage::fixtures::container_labels(),
        );
        let machines = machines(&nodes, &pods, Some(&usage));
        let read: Vec<Vec<(&str, Option<f64>, Amount)>> = machines
            .iter()
            .map(|machine| {
                machine
                    .pod_memory
                    .iter()
                    .map(|pod| (pod.name.as_str(), pod.used, pod.requested))
                    .collect()
            })
            .collect();
        assert_eq!(
            read,
            vec![
                vec![("web", Some(2e9), Amount::parse("1Gi").unwrap())],
                vec![
                    ("cart", Some(1e8), Amount::parse("512Mi").unwrap()),
                    ("unread", None, Amount::parse("1Gi").unwrap()),
                ],
            ]
        );
    }

    #[rstest]
    fn a_machine_without_a_reading_has_no_used_bar() {
        let usage = usage(1.0, 1.0);
        let machine = &machines(&[worker("beta")], &[], Some(&usage))[0];
        assert_eq!(
            (machine.cpu_used_percent(), machine.memory_used_percent()),
            (None, None)
        );
        assert_eq!(machine.cpu_title(), "CPU 0.0 requested of 4");
    }

    #[rstest]
    #[case::ready("{conditions: [{type: Ready, status: 'True'}]}", true)]
    #[case::not_ready("{conditions: [{type: Ready, status: 'False'}]}", false)]
    #[case::unknown("{conditions: [{type: Ready, status: Unknown}]}", false)]
    #[case::no_conditions("{}", false)]
    fn readiness(#[case] status: &str, #[case] expected: bool) {
        let node = node(&format!("metadata: {{name: alpha}}\nstatus: {status}"));
        assert_eq!(ready(&node), expected);
    }

    #[rstest]
    #[case::control_plane("{node-role.kubernetes.io/control-plane: ''}", true)]
    #[case::worker("{kubernetes.io/os: linux}", false)]
    fn control_plane_label(#[case] labels: &str, #[case] expected: bool) {
        let node = node(&format!("metadata: {{name: alpha, labels: {labels}}}"));
        assert_eq!(control_plane(&node), expected);
    }

    #[rstest]
    #[case::empty(0.0, 4.0, 0)]
    #[case::part(1.0, 4.0, 25)]
    #[case::full(4.0, 4.0, 100)]
    #[case::over_is_capped(6.0, 4.0, 100)]
    #[case::nothing_allocatable(1.0, 0.0, 0)]
    fn bar_percent(#[case] part: f64, #[case] whole: f64, #[case] expected: u32) {
        assert_eq!(percent(part, whole), expected);
    }

    #[rstest]
    #[case::whole(8.0, "8")]
    #[case::fraction(7.5, "7.5")]
    #[case::rounds(3.96, "4")]
    fn trimmed_numbers(#[case] value: f64, #[case] expected: &str) {
        assert_eq!(trimmed(value), expected);
    }
}
