//! Per-container usage from the results of the `MetricsSource`'s
//! container queries, and per-pod usage as the sum of a pod's
//! containers. transect sums the containers itself instead of running a
//! second query, so a pod's total always equals the sum of the
//! containers on its page.
//!
//! The keys hold no `Node` name, because scrape configs differ in which
//! label of a container's series holds it.

use std::collections::HashMap;

use super::queries::ContainerLabels;
use crate::prometheus::Sample;

/// A pod's namespace and name.
pub type PodKey = (String, String);

/// A container's pod and its own name.
pub type ContainerKey = (PodKey, String);

/// Maps each container to the value of its sample, using `labels` to
/// find the container's namespace, pod, and name. A sample that lacks
/// one of those labels is skipped, because it is not about a container.
pub fn by_container(samples: Vec<Sample>, labels: &ContainerLabels) -> HashMap<ContainerKey, f64> {
    samples
        .into_iter()
        .filter_map(|mut sample| {
            let namespace = sample.labels.remove(&labels.namespace)?;
            let pod = sample.labels.remove(&labels.pod)?;
            let container = sample.labels.remove(&labels.container)?;
            Some((((namespace, pod), container), sample.value))
        })
        .collect()
}

/// The sum of each pod's container values.
pub fn by_pod(containers: &HashMap<ContainerKey, f64>) -> HashMap<PodKey, f64> {
    let mut pods: HashMap<PodKey, f64> = HashMap::new();
    for ((pod, _), value) in containers {
        *pods.entry(pod.clone()).or_default() += value;
    }
    pods
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::usage::fixtures::sample;

    fn key(namespace: &str, pod: &str, container: &str) -> ContainerKey {
        ((namespace.into(), pod.into()), container.into())
    }

    #[rstest]
    fn keys_samples_by_namespace_pod_and_container() {
        let labels = crate::usage::fixtures::container_labels();
        let containers = by_container(
            vec![
                sample(
                    &[("namespace", "shop"), ("pod", "web"), ("container", "app")],
                    2e8,
                ),
                sample(&[("namespace", "shop"), ("pod", "web")], 1e8),
                sample(&[("pod", "orphan"), ("container", "app")], 1e8),
            ],
            &labels,
        );
        assert_eq!(
            containers,
            HashMap::from([(key("shop", "web", "app"), 2e8)])
        );
    }

    #[rstest]
    fn a_pod_is_the_sum_of_its_containers() {
        let containers = HashMap::from([
            (key("shop", "web", "app"), 2e8),
            (key("shop", "web", "proxy"), 1e8),
            (key("shop", "cart", "app"), 5e7),
        ]);
        assert_eq!(
            by_pod(&containers),
            HashMap::from([
                (("shop".into(), "web".into()), 3e8),
                (("shop".into(), "cart".into()), 5e7),
            ])
        );
    }
}
