//! The text of the workload findings: for one workload, on its page and
//! card, and as a count in the cluster sidebar.

use super::{Rule, WorkloadFinding};

impl WorkloadFinding {
    pub fn title(&self) -> &'static str {
        match self.rule {
            Rule::NoReadinessProbe => "No readiness probe",
            Rule::NoDisruptionBudget => "No PodDisruptionBudget",
        }
    }

    pub fn body(&self) -> String {
        match self.rule {
            Rule::NoReadinessProbe => "During a rollout, its Service sends requests to each new \
                 pod as soon as it starts, before the pod can answer them."
                .into(),
            Rule::NoDisruptionBudget => format!(
                "No PodDisruptionBudget limits evictions, so a drain can evict {} at once.",
                match self.replicas {
                    2 => "both pods".to_string(),
                    replicas => format!("all {replicas} pods"),
                }
            ),
        }
    }
}

impl Rule {
    /// The sidebar title for the number of `workloads` that this rule
    /// flags.
    pub fn count_title(self, workloads: usize) -> String {
        match (self, workloads) {
            (Self::NoReadinessProbe, 1) => {
                "1 replicated workload behind a Service has no readiness probe".into()
            }
            (Self::NoReadinessProbe, _) => {
                format!("{workloads} replicated workloads behind a Service have no readiness probe")
            }
            (Self::NoDisruptionBudget, 1) => {
                "1 replicated workload has no PodDisruptionBudget".into()
            }
            (Self::NoDisruptionBudget, _) => {
                format!("{workloads} replicated workloads have no PodDisruptionBudget")
            }
        }
    }

    pub fn count_body(self) -> &'static str {
        match self {
            Self::NoReadinessProbe => {
                "During a rollout, their Services send requests to each new pod as soon as it \
                 starts, before the pod can answer them."
            }
            Self::NoDisruptionBudget => {
                "No PodDisruptionBudget limits their evictions, so a drain can evict all of a \
                 workload's pods at once."
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::one_without_a_probe(
        Rule::NoReadinessProbe,
        1,
        "1 replicated workload behind a Service has no readiness probe"
    )]
    #[case::several_without_a_probe(
        Rule::NoReadinessProbe,
        12,
        "12 replicated workloads behind a Service have no readiness probe"
    )]
    #[case::one_without_a_budget(
        Rule::NoDisruptionBudget,
        1,
        "1 replicated workload has no PodDisruptionBudget"
    )]
    #[case::several_without_a_budget(
        Rule::NoDisruptionBudget,
        5,
        "5 replicated workloads have no PodDisruptionBudget"
    )]
    fn writes_the_sidebar_title(#[case] rule: Rule, #[case] workloads: usize, #[case] title: &str) {
        assert_eq!(rule.count_title(workloads), title);
    }

    #[rstest]
    fn a_budget_finding_counts_the_workloads_pods() {
        let finding = WorkloadFinding {
            rule: Rule::NoDisruptionBudget,
            replicas: 3,
        };
        assert_eq!(
            (finding.title(), finding.body()),
            (
                "No PodDisruptionBudget",
                "No PodDisruptionBudget limits evictions, so a drain can evict all 3 pods at once."
                    .into()
            )
        );
    }
}
