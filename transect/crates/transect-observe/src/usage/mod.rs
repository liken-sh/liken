//! Usage: what each machine, pod, and container uses right now.
//! transect reads it from Prometheus on the scrape interval with the
//! queries that the cluster's `MetricsSource` declares.
//!
//! The machines band shows usage by node, the workload page by pod, and
//! the pod page by container.  One poller runs every query in the same
//! round, so all the views drawn from one reading show the same figures.

mod node;
mod pod;
mod poll;
mod queries;

use std::collections::HashMap;

use crate::prometheus::Sample;
use pod::{ContainerKey, PodKey, by_container, by_pod};
pub use poll::{Reading, poll};
pub use queries::{ContainerQuery, MachineQuery, Queries};

/// Used CPU and memory for one machine, pod, or container.  Either can be
/// missing on its own, when one query has a series for it and the other
/// not.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Used {
    /// Cores.
    pub cpu: Option<f64>,
    /// Bytes.
    pub memory: Option<f64>,
}

/// The samples that each usage query returned.  A query that the
/// cluster does not declare has no samples.
#[derive(Default)]
pub struct Answers {
    pub machine_cpu: Vec<Sample>,
    pub machine_memory: Vec<Sample>,
    pub container_cpu: Vec<Sample>,
    pub container_memory: Vec<Sample>,
}

/// Used CPU and memory, looked up by `Node` name, by a pod's namespace and
/// name, and by a container's pod and name.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Usage {
    cpu: HashMap<String, f64>,
    memory: HashMap<String, f64>,
    pod_cpu: HashMap<PodKey, f64>,
    pod_memory: HashMap<PodKey, f64>,
    container_cpu: HashMap<ContainerKey, f64>,
    container_memory: HashMap<ContainerKey, f64>,
}

impl Usage {
    /// The usage from samples of the machine queries, each stored under
    /// the `Node` that one of `node_labels` names.
    pub fn from_samples(cpu: Vec<Sample>, memory: Vec<Sample>, node_labels: &[String]) -> Self {
        Self {
            cpu: node::by_name(cpu, node_labels),
            memory: node::by_name(memory, node_labels),
            ..Self::default()
        }
    }

    /// This usage with each container's memory and CPU taken from
    /// samples of container queries whose series `labels` name, and
    /// each pod's usage as the sum of its containers' usage.
    #[cfg(any(test, feature = "fixtures"))]
    pub fn with_containers(
        self,
        memory: Vec<Sample>,
        cpu: Vec<Sample>,
        labels: &queries::ContainerLabels,
    ) -> Self {
        self.with_container_figures(by_container(memory, labels), by_container(cpu, labels))
    }

    fn with_container_figures(
        self,
        container_memory: HashMap<ContainerKey, f64>,
        container_cpu: HashMap<ContainerKey, f64>,
    ) -> Self {
        Self {
            pod_memory: by_pod(&container_memory),
            pod_cpu: by_pod(&container_cpu),
            container_memory,
            container_cpu,
            ..self
        }
    }

    /// The usage from `answers`, the samples that `queries` returned.
    pub fn answered(queries: &Queries, answers: Answers) -> Self {
        let usage = Self::from_samples(
            answers.machine_cpu,
            answers.machine_memory,
            &queries.node_labels,
        );
        let containers = |query: Option<&ContainerQuery>, samples| {
            query.map_or_else(HashMap::new, |query| by_container(samples, &query.labels))
        };
        usage.with_container_figures(
            containers(queries.container_memory.as_ref(), answers.container_memory),
            containers(queries.container_cpu.as_ref(), answers.container_cpu),
        )
    }

    /// The usage from the samples that `answer` returns for each of
    /// `queries`.  It is for a cluster whose query results are written
    /// down in advance, such as the fixture.
    #[cfg(any(test, debug_assertions))]
    pub fn from_answers(queries: &Queries, answer: impl Fn(&str) -> Vec<Sample>) -> Self {
        let asked = |query: Option<&str>| query.map(&answer).unwrap_or_default();
        let answers = Answers {
            machine_cpu: asked(queries.machine_cpu.as_deref()),
            machine_memory: asked(queries.machine_memory.as_deref()),
            container_cpu: asked(queries.container_cpu.as_ref().map(|q| q.query.as_str())),
            container_memory: asked(queries.container_memory.as_ref().map(|q| q.query.as_str())),
        };
        Self::answered(queries, answers)
    }

    pub fn is_empty(&self) -> bool {
        self.cpu.is_empty() && self.memory.is_empty()
    }

    /// What the machine named `node` uses.
    pub fn of(&self, node: &str) -> Used {
        Used {
            cpu: self.cpu.get(node).copied(),
            memory: self.memory.get(node).copied(),
        }
    }

    /// What the pod `namespace/name` uses.
    pub fn of_pod(&self, namespace: &str, name: &str) -> Used {
        let key = (namespace.to_owned(), name.to_owned());
        Used {
            cpu: self.pod_cpu.get(&key).copied(),
            memory: self.pod_memory.get(&key).copied(),
        }
    }

    /// What the container named `container` in the pod `namespace/pod`
    /// uses.
    pub fn of_container(&self, namespace: &str, pod: &str, container: &str) -> Used {
        let key = ((namespace.to_owned(), pod.to_owned()), container.to_owned());
        Used {
            cpu: self.container_cpu.get(&key).copied(),
            memory: self.container_memory.get(&key).copied(),
        }
    }
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::queries::ContainerLabels;
    use crate::prometheus::Sample;

    /// The labels that name a container's series in the tests.
    pub fn container_labels() -> ContainerLabels {
        ContainerLabels {
            namespace: "namespace".into(),
            pod: "pod".into(),
            container: "container".into(),
        }
    }

    /// The labels that may name the `Node` of a machine's series in the
    /// tests.
    pub fn node_labels() -> Vec<String> {
        vec!["node".into()]
    }

    pub fn sample(labels: &[(&str, &str)], value: f64) -> Sample {
        Sample {
            labels: labels
                .iter()
                .map(|(k, v)| (k.to_string(), v.to_string()))
                .collect(),
            value,
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::{container_labels, node_labels, sample};
    use super::*;

    #[rstest]
    fn a_node_without_a_series_has_no_reading() {
        let usage = Usage::from_samples(
            vec![sample(&[("node", "alpha")], 1.0)],
            vec![],
            &node_labels(),
        );
        assert_eq!(
            usage.of("alpha"),
            Used {
                cpu: Some(1.0),
                memory: None
            }
        );
        assert_eq!(usage.of("beta"), Used::default());
    }

    #[rstest]
    fn answers_by_pod_and_container_from_both_container_queries() {
        let app = [("namespace", "shop"), ("pod", "web"), ("container", "app")];
        let proxy = [
            ("namespace", "shop"),
            ("pod", "web"),
            ("container", "proxy"),
        ];
        let usage = Usage::default().with_containers(
            vec![sample(&app, 2e8), sample(&proxy, 1e8)],
            vec![sample(&app, 0.25)],
            &container_labels(),
        );
        assert_eq!(
            (
                usage.of_pod("shop", "web"),
                usage.of_container("shop", "web", "proxy")
            ),
            (
                Used {
                    cpu: Some(0.25),
                    memory: Some(3e8)
                },
                Used {
                    cpu: None,
                    memory: Some(1e8)
                },
            )
        );
        assert_eq!(usage.of_pod("blog", "web"), Used::default());
    }
}
