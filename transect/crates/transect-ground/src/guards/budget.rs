//! A `PodDisruptionBudget` limits how many of a workload's pods a drain,
//! such as the drain before a machine reboots, may evict at once.

use std::collections::BTreeMap;
use std::sync::Arc;

use k8s_openapi::api::policy::v1::PodDisruptionBudget;
use k8s_openapi::apimachinery::pkg::util::intstr::IntOrString;
use kube::ResourceExt;

use transect_graph::selector;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Budget {
    pub name: String,
    /// `minAvailable` or `maxUnavailable`, whichever the budget sets: a
    /// count or a percentage.
    pub limit: Limit,
    /// How many pods may be evicted now, from the disruption controller's
    /// `status.disruptionsAllowed`.
    pub allowed: i32,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Limit {
    MinAvailable(String),
    MaxUnavailable(String),
    /// Neither is set, which the API server defaults to
    /// `minAvailable: 1`, though only on create.
    None,
}

/// The budgets in `namespace` that select any pod with one of
/// `labels`.  A budget with no selector selects nothing, and one with an
/// empty selector selects every pod in its namespace, as `policy/v1`
/// defines it.
pub fn covering(
    budgets: &[Arc<PodDisruptionBudget>],
    namespace: &str,
    labels: &[&BTreeMap<String, String>],
) -> Vec<Budget> {
    let mut covering: Vec<Budget> = budgets
        .iter()
        .filter(|budget| budget.namespace().as_deref() == Some(namespace))
        .filter(|budget| {
            let selector = budget.spec.as_ref().and_then(|spec| spec.selector.as_ref());
            selector.is_some_and(|selector| {
                labels
                    .iter()
                    .any(|labels| selector::matches(selector, labels))
            })
        })
        .map(|budget| read(budget))
        .collect();
    covering.sort_by(|a, b| a.name.cmp(&b.name));
    covering
}

fn read(budget: &PodDisruptionBudget) -> Budget {
    let spec = budget.spec.as_ref();
    let text = |value: &IntOrString| match value {
        IntOrString::Int(count) => count.to_string(),
        IntOrString::String(text) => text.clone(),
    };
    let limit = match (
        spec.and_then(|spec| spec.min_available.as_ref()),
        spec.and_then(|spec| spec.max_unavailable.as_ref()),
    ) {
        (Some(min), _) => Limit::MinAvailable(text(min)),
        (None, Some(max)) => Limit::MaxUnavailable(text(max)),
        (None, None) => Limit::None,
    };
    Budget {
        name: budget.name_any(),
        limit,
        allowed: budget
            .status
            .as_ref()
            .and_then(|s| s.disruptions_allowed)
            .unwrap_or(0),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn budget(namespace: &str, name: &str, spec: &str, allowed: i32) -> Arc<PodDisruptionBudget> {
        Arc::new(
            serde_yaml_ng::from_str(&format!(
                "{{metadata: {{namespace: {namespace}, name: {name}}}, spec: {spec}, status: {{disruptionsAllowed: {allowed}, currentHealthy: 0, desiredHealthy: 0, expectedPods: 0}}}}"
            ))
            .unwrap(),
        )
    }

    fn labels(pairs: &[(&str, &str)]) -> BTreeMap<String, String> {
        let pairs = pairs.iter().map(|(k, v)| (k.to_string(), v.to_string()));
        pairs.collect()
    }

    fn names(budgets: &[Arc<PodDisruptionBudget>], pod: &[(&str, &str)]) -> Vec<String> {
        let pod = labels(pod);
        let covering = covering(budgets, "shop", &[&pod]);
        covering.into_iter().map(|budget| budget.name).collect()
    }

    #[rstest]
    #[case::matching_labels(
        vec![budget("shop", "web", "{selector: {matchLabels: {app: web}}, minAvailable: 1}", 2)],
        &[("app", "web")], &["web"]
    )]
    #[case::other_labels(
        vec![budget("shop", "web", "{selector: {matchLabels: {app: web}}}", 1)],
        &[("app", "cart")], &[]
    )]
    #[case::another_namespace(
        vec![budget("blog", "web", "{selector: {matchLabels: {app: web}}}", 1)],
        &[("app", "web")], &[]
    )]
    #[case::an_empty_selector_selects_all(
        vec![budget("shop", "all", "{selector: {}}", 1)],
        &[("app", "web")], &["all"]
    )]
    #[case::no_selector_selects_none(vec![budget("shop", "none", "{}", 1)], &[("app", "web")], &[])]
    #[case::sorted_by_name(
        vec![
            budget("shop", "replicas", "{selector: {matchLabels: {app: db}}}", 1),
            budget("shop", "primary", "{selector: {matchExpressions: [{key: app, operator: In, values: [db]}]}}", 0),
        ],
        &[("app", "db")], &["primary", "replicas"]
    )]
    fn finds_the_budgets_that_select_a_workloads_pods(
        #[case] budgets: Vec<Arc<PodDisruptionBudget>>,
        #[case] pod: &[(&str, &str)],
        #[case] expected: &[&str],
    ) {
        assert_eq!(names(&budgets, pod), expected);
    }

    #[rstest]
    #[case::min_count("{selector: {}, minAvailable: 1}", Limit::MinAvailable("1".into()))]
    #[case::max_percent("{selector: {}, maxUnavailable: 25%}", Limit::MaxUnavailable("25%".into()))]
    #[case::neither("{selector: {}}", Limit::None)]
    fn reads_the_limit_and_allowed_count_of_a_budget(#[case] spec: &str, #[case] limit: Limit) {
        let budgets = [budget("shop", "web", spec, 2)];
        let pod = labels(&[]);
        assert_eq!(
            covering(&budgets, "shop", &[&pod]),
            vec![Budget {
                name: "web".into(),
                limit,
                allowed: 2
            }]
        );
    }
}
