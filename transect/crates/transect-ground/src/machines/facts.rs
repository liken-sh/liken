//! The facts that a `Node` records about its machine: its role, its
//! labels, and the hardware and software that its kubelet reports.

use k8s_openapi::api::core::v1::Node;

use super::{BYTES_PER_GI, CONTROL_PLANE_LABEL, Resources, trimmed};

/// Labels whose keys start with these prefixes repeat facts that `lines`
/// already lists, under names that Kubernetes keeps for old clients.
const REPEATED: [&str; 1] = ["beta.kubernetes.io/"];

#[derive(Debug, Default, PartialEq)]
pub struct Facts {
    pub control_plane: bool,
    /// Each fact as a name and its value, in the order a reader looks
    /// for them.
    pub lines: Vec<(&'static str, String)>,
    /// The node's labels, as `key=value`, sorted.
    pub labels: Vec<String>,
}

impl Facts {
    pub fn of(node: &Node) -> Self {
        let labels = node.metadata.labels.iter().flatten();
        let control_plane = labels.clone().any(|(key, _)| key == CONTROL_PLANE_LABEL);
        let mut shown: Vec<String> = labels
            .filter(|(key, _)| !REPEATED.iter().any(|prefix| key.starts_with(prefix)))
            .map(|(key, value)| match value.is_empty() {
                true => key.clone(),
                false => format!("{key}={value}"),
            })
            .collect();
        shown.sort();
        let status = node.status.as_ref();
        let capacity = Resources::from_list(status.and_then(|status| status.capacity.as_ref()));
        let info = status.and_then(|status| status.node_info.as_ref());
        let address = status
            .and_then(|status| status.addresses.as_ref())
            .and_then(|addresses| addresses.iter().find(|a| a.type_ == "InternalIP"))
            .map(|address| address.address.clone());
        let mut lines = Vec::new();
        if capacity.cpu.units() > 0.0 {
            lines.push(("CPU", cpus(capacity.cpu.units())));
        }
        if capacity.memory.units() > 0.0 {
            let memory = capacity.memory.units() / BYTES_PER_GI;
            lines.push(("Memory", format!("{memory:.1} GiB")));
        }
        if let Some(info) = info {
            lines.push(("Architecture", info.architecture.clone()));
            lines.push(("Operating system", info.os_image.clone()));
            lines.push(("Kernel", info.kernel_version.clone()));
            lines.push(("Kubelet", info.kubelet_version.clone()));
            lines.push(("Container runtime", info.container_runtime_version.clone()));
        }
        lines.extend(address.map(|address| ("Address", address)));
        lines.retain(|(_, value)| !value.is_empty());
        Self {
            control_plane,
            lines,
            labels: shown,
        }
    }

    /// "control plane" or "worker", as the machines band groups them.
    pub fn role(&self) -> &'static str {
        match self.control_plane {
            true => "control plane",
            false => "worker",
        }
    }
}

/// A CPU count as text, such as "4 CPUs", "1 CPU", or "0.5 CPU".
fn cpus(count: f64) -> String {
    match count > 1.0 {
        true => format!("{} CPUs", trimmed(count)),
        false => format!("{} CPU", trimmed(count)),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn node(labels: &str, status: &str) -> Node {
        serde_yaml::from_str(&format!(
            "metadata: {{name: alpha, labels: {labels}}}\nstatus: {status}"
        ))
        .unwrap()
    }

    const REPORTED: &str = "{capacity: {cpu: '4', memory: 16Gi}, \
        addresses: [{type: Hostname, address: alpha}, {type: InternalIP, address: 10.1.0.5}], \
        nodeInfo: {architecture: amd64, osImage: An OS, kernelVersion: 6.1.0, \
        kubeletVersion: v1.33.0, containerRuntimeVersion: 'containerd://2.0.0', \
        operatingSystem: linux, machineID: m, systemUUID: s, bootID: b, kubeProxyVersion: ''}}";

    #[rstest]
    fn lists_what_its_kubelet_reports() {
        let facts = Facts::of(&node("{}", REPORTED));
        assert_eq!(
            facts.lines,
            vec![
                ("CPU", "4 CPUs".into()),
                ("Memory", "16.0 GiB".into()),
                ("Architecture", "amd64".into()),
                ("Operating system", "An OS".into()),
                ("Kernel", "6.1.0".into()),
                ("Kubelet", "v1.33.0".into()),
                ("Container runtime", "containerd://2.0.0".into()),
                ("Address", "10.1.0.5".into()),
            ]
        );
    }

    #[rstest]
    #[case::a_worker("{room: attic}", "worker", vec!["room=attic"])]
    #[case::the_control_plane(
        "{node-role.kubernetes.io/control-plane: '', beta.kubernetes.io/arch: amd64}",
        "control plane",
        vec!["node-role.kubernetes.io/control-plane"]
    )]
    fn says_its_role_and_labels(
        #[case] labels: &str,
        #[case] role: &str,
        #[case] shown: Vec<&str>,
    ) {
        let facts = Facts::of(&node(labels, "{}"));
        assert_eq!(
            (facts.role(), facts.labels),
            (role, shown.iter().map(|l| l.to_string()).collect())
        );
    }

    #[rstest]
    #[case::many(4.0, "4 CPUs")]
    #[case::one(1.0, "1 CPU")]
    #[case::a_part(0.5, "0.5 CPU")]
    fn counts_its_cpus(#[case] count: f64, #[case] text: &str) {
        assert_eq!(cpus(count), text);
    }
}
