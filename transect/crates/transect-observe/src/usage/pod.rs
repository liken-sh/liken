//! What each container uses right now, from the results of the
//! `MetricsSource`'s container queries, and what each pod uses as the
//! sum of its containers.  transect sums here instead of in a second
//! query, so a pod's total equals the sum of the containers that its
//! page lists.
//!
//! The series have the pod's namespace and name, but different scrape
//! configs put the `Node`'s name in different labels.  So code that
//! needs a pod's machine reads it from the `Pod`'s `spec.nodeName`
//! instead.

use std::collections::HashMap;

use super::queries::ContainerLabels;
use crate::prometheus::Sample;

/// A pod's namespace and name.
pub type PodKey = (String, String);

/// A container's pod and its own name.
pub type ContainerKey = (PodKey, String);

/// Stores each sample under its container, by the `labels` that name
/// the container.  The function leaves out a sample that lacks one of
/// the labels, because that sample does not belong to a container.
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

/// Each pod's containers added up.
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
    fn files_samples_by_namespace_pod_and_container() {
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
