//! The most CPU and memory that a pod may use, from the limits of the
//! containers that run together.  The workload page draws its bars
//! against these limits.
//!
//! The app containers and the sidecars, which are the init containers
//! with `restartPolicy: Always`, run at the same time, so their limits
//! add up.  When one of them has no limit for a resource, the pod has no
//! limit for that resource, because that container may use all that the
//! machine has.  Ordinary init containers run one at a time before the
//! others start.  This module leaves them out because a bar of what a
//! running pod uses shows the containers that run now.  A limit set on
//! the pod as a whole, in `spec.resources`, replaces the containers'
//! total for that resource.

use k8s_openapi::api::core::v1::{Container, PodSpec};

use super::amount::Amount;

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Limits {
    pub cpu: Option<Amount>,
    pub memory: Option<Amount>,
}

/// The CPU and memory limits of a pod with `spec`.
pub fn pod_limits(spec: &PodSpec) -> Limits {
    let sidecars = spec
        .init_containers
        .iter()
        .flatten()
        .filter(|init| init.restart_policy.as_deref() == Some("Always"));
    let running: Vec<&Container> = spec.containers.iter().chain(sidecars).collect();
    let total = |name: &str| -> Option<Amount> {
        let set_on_pod = spec
            .resources
            .as_ref()
            .and_then(|resources| resources.limits.as_ref()?.get(name));
        if let Some(quantity) = set_on_pod {
            return Amount::parse(&quantity.0);
        }
        running
            .iter()
            .map(|container| limit(container, name))
            .sum::<Option<Amount>>()
    };
    Limits {
        cpu: total("cpu"),
        memory: total("memory"),
    }
}

fn limit(container: &Container, name: &str) -> Option<Amount> {
    let quantity = container.resources.as_ref()?.limits.as_ref()?.get(name)?;
    Amount::parse(&quantity.0)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn spec(yaml: &str) -> PodSpec {
        serde_yaml_ng::from_str(yaml).unwrap()
    }

    fn amount(text: &str) -> Option<Amount> {
        Amount::parse(text)
    }

    #[rstest]
    #[case::no_limits("containers: [{name: app}]", None, None)]
    #[case::one_container(
        "containers: [{name: app, resources: {limits: {cpu: 500m, memory: 1Gi}}}]",
        amount("500m"),
        amount("1Gi")
    )]
    #[case::containers_add_up(
        r#"
containers:
  - {name: app, resources: {limits: {cpu: 500m, memory: 1Gi}}}
  - {name: proxy, resources: {limits: {cpu: 100m, memory: 64Mi}}}
"#,
        amount("600m"),
        amount("1088Mi")
    )]
    #[case::one_without_a_limit_leaves_none(
        r#"
containers:
  - {name: app, resources: {limits: {memory: 1Gi}}}
  - {name: proxy, resources: {limits: {cpu: 100m, memory: 64Mi}}}
"#,
        None,
        amount("1088Mi")
    )]
    #[case::sidecars_add_and_init_containers_do_not(
        r#"
initContainers:
  - {name: migrate, resources: {limits: {cpu: '4', memory: 4Gi}}}
  - {name: mesh, restartPolicy: Always, resources: {limits: {cpu: 100m, memory: 64Mi}}}
containers:
  - {name: app, resources: {limits: {cpu: 500m, memory: 1Gi}}}
"#,
        amount("600m"),
        amount("1088Mi")
    )]
    #[case::pod_level_limits_replace_the_containers(
        r#"
resources: {limits: {cpu: '1'}}
containers:
  - {name: app, resources: {limits: {memory: 1Gi}}}
"#,
        amount("1"),
        amount("1Gi")
    )]
    fn limits_of_a_pod(
        #[case] yaml: &str,
        #[case] cpu: Option<Amount>,
        #[case] memory: Option<Amount>,
    ) {
        assert_eq!(pod_limits(&spec(yaml)), Limits { cpu, memory });
    }
}
