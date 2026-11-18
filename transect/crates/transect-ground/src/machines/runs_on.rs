//! The runs-on edges, which join each pod to the machine that its
//! `spec.nodeName` binds it to.  When a namespace is selected, the
//! machine strips count that namespace's pods from these edges.

use std::collections::HashMap;

use k8s_openapi::api::core::v1::Pod;

use super::Machine;
use transect_graph::edges::{Edge, Endpoint, Graph, Kind, Object, Provenance};
use transect_graph::pods::finished;

/// An edge for each unfinished pod that is bound to a machine, including
/// a `Pending` pod that the scheduler has bound.  A pod that `Succeeded`
/// or `Failed` gets no edge, which matches the strip's own pod count.
pub fn runs_on<'a>(pods: impl IntoIterator<Item = &'a Pod>) -> Vec<Edge> {
    pods.into_iter()
        .filter(|pod| !finished(pod))
        .filter_map(|pod| {
            let node = pod.spec.as_ref()?.node_name.clone()?;
            Some(Edge {
                kind: Kind::RunsOn,
                from: Endpoint::Object(Object::new(
                    "",
                    "Pod",
                    pod.metadata.namespace.as_deref(),
                    pod.metadata.name.as_deref()?,
                )),
                to: Endpoint::Machine(node),
                guessed: false,
                provenance: Provenance::Kubernetes,
            })
        })
        .collect()
}

/// Sets each machine's count of `namespace`'s pods, read from the
/// runs-on edges in `graph`.  A machine that runs none of them gets a
/// count of zero, so its strip can show "none here".
pub fn count_here(machines: &mut [Machine], graph: &Graph, namespace: &str) {
    let mut here: HashMap<&str, usize> = HashMap::new();
    for edge in graph.touching_namespace(namespace) {
        if let (Kind::RunsOn, Endpoint::Machine(machine)) = (edge.kind, &edge.to) {
            *here.entry(machine).or_default() += 1;
        }
    }
    for machine in machines {
        machine.here = Some(here.get(machine.name.as_str()).copied().unwrap_or(0));
    }
}

impl Machine {
    /// The number of the selected namespace's pods on this machine, as the
    /// strip shows it beside the machine's pod count, or `None` when no
    /// namespace is selected.
    pub fn here_text(&self) -> Option<String> {
        match self.here? {
            0 => Some("none here".into()),
            count => Some(format!("{count} here")),
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::machines::Resources;
    use transect_graph::edges::fixtures;
    use transect_observe::usage::Used;

    fn pod(namespace: &str, name: &str, node: Option<&str>, phase: &str) -> Pod {
        let node = node
            .map(|node| format!("nodeName: {node}, "))
            .unwrap_or_default();
        serde_yaml::from_str(&format!(
            "metadata: {{namespace: {namespace}, name: {name}}}\nspec: {{{node}containers: []}}\nstatus: {{phase: {phase}}}"
        ))
        .unwrap()
    }

    #[rstest]
    #[case::running(pod("shop", "web", Some("alpha"), "Running"), vec![fixtures::runs_on("shop", "web", "alpha")])]
    #[case::pending_on_a_machine(pod("shop", "web", Some("alpha"), "Pending"), vec![fixtures::runs_on("shop", "web", "alpha")])]
    #[case::not_scheduled(pod("shop", "web", None, "Pending"), vec![])]
    #[case::succeeded(pod("shop", "web", Some("alpha"), "Succeeded"), vec![])]
    #[case::failed(pod("shop", "web", Some("alpha"), "Failed"), vec![])]
    fn a_pod_runs_on_its_machine_until_it_finishes(#[case] pod: Pod, #[case] edges: Vec<Edge>) {
        assert_eq!(runs_on([&pod]), edges);
    }

    fn machine(name: &str) -> Machine {
        Machine {
            name: name.into(),
            control_plane: false,
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
            held: vec![],
            pod_memory: vec![],
            alerted: None,
        }
    }

    #[rstest]
    fn counts_only_the_selected_namespaces_pods_on_each_machine() {
        let graph = Graph::new([vec![
            fixtures::runs_on("shop", "web-1", "alpha"),
            fixtures::runs_on("shop", "web-2", "alpha"),
            fixtures::runs_on("shop", "cart", "beta"),
            fixtures::runs_on("blog", "web", "gamma"),
        ]]);
        let mut machines = [machine("alpha"), machine("beta"), machine("gamma")];
        count_here(&mut machines, &graph, "shop");
        let here: Vec<Option<String>> = machines.iter().map(Machine::here_text).collect();
        assert_eq!(
            here,
            vec![
                Some("2 here".into()),
                Some("1 here".into()),
                Some("none here".into()),
            ]
        );
    }

    #[rstest]
    fn no_selected_namespace_gives_no_here_text() {
        assert_eq!(machine("alpha").here_text(), None);
    }
}
