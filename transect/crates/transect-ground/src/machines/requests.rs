//! The CPU and memory that the scheduler reserves for a pod on its node,
//! computed the same way that Kubernetes computes them.
//!
//! The rule, from `PodRequests` in Kubernetes' component-helpers:
//!
//! - App containers run together, so their requests add up.
//! - Ordinary init containers run one at a time before the app
//!   containers, so the pod needs room for the largest of them, not
//!   their sum.
//! - A sidecar, an init container with `restartPolicy: Always`, keeps
//!   running while every container that starts after it runs.  Its
//!   request adds to the app containers' total and to every init
//!   container that follows it.
//! - The pod needs the larger of the app containers' total and its
//!   largest init container step.
//! - Requests set on the pod as a whole, in `spec.resources`, replace
//!   the containers' total for that resource.
//! - The runtime's `spec.overhead` is added to the result.

use std::collections::BTreeMap;

use k8s_openapi::api::core::v1::{Container, PodSpec, ResourceRequirements};
use k8s_openapi::apimachinery::pkg::api::resource::Quantity;

use super::amount::Amount;

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Resources {
    pub cpu: Amount,
    pub memory: Amount,
}

impl Resources {
    /// The `cpu` and `memory` entries of a resource list.  The API
    /// server rejects a quantity that does not parse, so a quantity that
    /// fails to parse here is counted as zero.
    pub fn from_list(list: Option<&BTreeMap<String, Quantity>>) -> Self {
        let amount = |name: &str| {
            list.and_then(|list| list.get(name))
                .and_then(|quantity| Amount::parse(&quantity.0))
                .unwrap_or_default()
        };
        Self {
            cpu: amount("cpu"),
            memory: amount("memory"),
        }
    }

    fn add(self, other: Self) -> Self {
        Self {
            cpu: self.cpu + other.cpu,
            memory: self.memory + other.memory,
        }
    }

    fn max(self, other: Self) -> Self {
        Self {
            cpu: self.cpu.max(other.cpu),
            memory: self.memory.max(other.memory),
        }
    }
}

impl std::iter::Sum for Resources {
    fn sum<I: Iterator<Item = Self>>(all: I) -> Self {
        all.fold(Self::default(), Self::add)
    }
}

/// The CPU and memory the scheduler reserves for a pod with `spec`.
pub fn pod_requests(spec: &PodSpec) -> Resources {
    let mut total: Resources = spec.containers.iter().map(requests).sum();

    let mut sidecars = Resources::default();
    let mut largest_init_step = Resources::default();
    for init in spec.init_containers.iter().flatten() {
        let step = if init.restart_policy.as_deref() == Some("Always") {
            total = total.add(requests(init));
            sidecars = sidecars.add(requests(init));
            sidecars
        } else {
            requests(init).add(sidecars)
        };
        largest_init_step = largest_init_step.max(step);
    }
    total = total.max(largest_init_step);

    if let Some(pod) = spec.resources.as_ref().and_then(|r| r.requests.as_ref()) {
        let set_on_pod = Resources::from_list(Some(pod));
        if pod.contains_key("cpu") {
            total.cpu = set_on_pod.cpu;
        }
        if pod.contains_key("memory") {
            total.memory = set_on_pod.memory;
        }
    }

    total.add(Resources::from_list(spec.overhead.as_ref()))
}

fn requests(container: &Container) -> Resources {
    Resources::from_list(
        container
            .resources
            .as_ref()
            .and_then(|r: &ResourceRequirements| r.requests.as_ref()),
    )
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    /// Parses a pod spec from YAML, so each case looks like the manifest
    /// that it tests.
    fn spec(yaml: &str) -> PodSpec {
        serde_yaml::from_str(yaml).unwrap()
    }

    fn resources(cpu: &str, memory: &str) -> Resources {
        Resources {
            cpu: Amount::parse(cpu).unwrap(),
            memory: Amount::parse(memory).unwrap(),
        }
    }

    #[rstest]
    #[case::no_requests("containers: [{name: app}]", resources("0", "0"))]
    #[case::app_containers_add_up(
        r#"
containers:
  - {name: app, resources: {requests: {cpu: 250m, memory: 64Mi}}}
  - {name: proxy, resources: {requests: {cpu: 100m, memory: 32Mi}}}
"#,
        resources("350m", "96Mi")
    )]
    #[case::limits_are_not_requests(
        "containers: [{name: app, resources: {limits: {cpu: '2', memory: 1Gi}}}]",
        resources("0", "0")
    )]
    #[case::largest_init_container_wins_per_resource(
        r#"
initContainers:
  - {name: migrate, resources: {requests: {cpu: '1', memory: 16Mi}}}
  - {name: fetch, resources: {requests: {cpu: 10m, memory: 512Mi}}}
containers:
  - {name: app, resources: {requests: {cpu: 500m, memory: 128Mi}}}
"#,
        resources("1", "512Mi")
    )]
    #[case::init_containers_smaller_than_the_app(
        r#"
initContainers:
  - {name: migrate, resources: {requests: {cpu: 100m, memory: 16Mi}}}
containers:
  - {name: app, resources: {requests: {cpu: 500m, memory: 128Mi}}}
"#,
        resources("500m", "128Mi")
    )]
    #[case::sidecar_adds_to_the_app(
        r#"
initContainers:
  - {name: mesh, restartPolicy: Always, resources: {requests: {cpu: 100m, memory: 64Mi}}}
containers:
  - {name: app, resources: {requests: {cpu: 500m, memory: 128Mi}}}
"#,
        resources("600m", "192Mi")
    )]
    #[case::sidecar_adds_to_the_init_containers_after_it(
        r#"
initContainers:
  - {name: mesh, restartPolicy: Always, resources: {requests: {cpu: 100m, memory: 64Mi}}}
  - {name: migrate, resources: {requests: {cpu: '2', memory: 16Mi}}}
containers:
  - {name: app, resources: {requests: {cpu: 500m, memory: 128Mi}}}
"#,
        resources("2100m", "192Mi")
    )]
    #[case::sidecar_does_not_add_to_the_init_containers_before_it(
        r#"
initContainers:
  - {name: migrate, resources: {requests: {cpu: '2', memory: 16Mi}}}
  - {name: mesh, restartPolicy: Always, resources: {requests: {cpu: 100m, memory: 64Mi}}}
containers:
  - {name: app, resources: {requests: {cpu: 500m, memory: 128Mi}}}
"#,
        resources("2", "192Mi")
    )]
    #[case::overhead_adds_on_top(
        r#"
overhead: {cpu: 250m, memory: 120Mi}
containers:
  - {name: app, resources: {requests: {cpu: 500m, memory: 128Mi}}}
"#,
        resources("750m", "248Mi")
    )]
    #[case::pod_level_requests_replace_the_containers(
        r#"
resources: {requests: {cpu: '1'}}
overhead: {memory: 8Mi}
containers:
  - {name: app, resources: {requests: {cpu: 500m, memory: 128Mi}}}
"#,
        resources("1", "136Mi")
    )]
    fn requests_of_a_pod(#[case] yaml: &str, #[case] expected: Resources) {
        assert_eq!(pod_requests(&spec(yaml)), expected);
    }
}
