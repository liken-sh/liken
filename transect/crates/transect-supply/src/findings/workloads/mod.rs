//! Findings about a workload that runs more than one pod and has no
//! readiness probe or no disruption budget.
//!
//! The workload page and card list each finding about that workload.
//! The cluster sidebar shows one count for each rule instead, because a
//! cluster can have dozens of these findings, and a card for each would
//! hide the namespaces that need attention now.

mod sentence;

use super::Linked;
use transect_ground::guards::Guards;

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Rule {
    /// A `Service` can send requests to its pods before they are ready.
    NoReadinessProbe,
    /// No `PodDisruptionBudget` limits how many of its pods a drain can
    /// evict at once.
    NoDisruptionBudget,
}

/// One finding about one workload.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct WorkloadFinding {
    pub rule: Rule,
    /// How many pods the workload declares.
    pub replicas: i32,
}

/// The findings about a workload with `guards` that declares
/// `replicas` pods.
pub fn of_workload(guards: &Guards, replicas: i32) -> Vec<WorkloadFinding> {
    let rules = [
        (guards.takes_traffic_unready(), Rule::NoReadinessProbe),
        (guards.unbudgeted(), Rule::NoDisruptionBudget),
    ];
    rules
        .into_iter()
        .filter(|(found, _)| *found)
        .map(|(_, rule)| WorkloadFinding { rule, replicas })
        .collect()
}

/// The workloads that each rule found, in the order of `Rule`, each
/// rule's workloads sorted.  A rule that found none is left out.
pub fn counts(
    findings: impl IntoIterator<Item = (WorkloadFinding, Linked)>,
) -> Vec<(Rule, Vec<Linked>)> {
    let mut counts: Vec<(Rule, Vec<Linked>)> = Vec::new();
    for (finding, linked) in findings {
        match counts.iter_mut().find(|(rule, _)| *rule == finding.rule) {
            Some((_, found)) => found.push(linked),
            None => counts.push((finding.rule, vec![linked])),
        }
    }
    counts.sort_by_key(|(rule, _)| *rule);
    for (_, found) in &mut counts {
        found.sort_by(|a, b| a.object.cmp(&b.object));
    }
    counts
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_ground::guards::{Budget, Limit};

    fn guards(replicated: bool, readiness_probe: bool, budgets: usize) -> Guards {
        let budget = Budget {
            name: "web".into(),
            limit: Limit::MaxUnavailable("1".into()),
            allowed: 1,
        };
        Guards {
            replicated,
            scheduled: false,
            spread: None,
            budgets: vec![budget; budgets],
            strategies: vec![],
            readiness_probe,
            served_unready: !readiness_probe,
            cpu_limit: true,
            operator_moves: false,
        }
    }

    #[rstest]
    #[case::guarded(guards(true, true, 1), vec![])]
    #[case::no_probe(guards(true, false, 1), vec![Rule::NoReadinessProbe])]
    #[case::no_budget(guards(true, true, 0), vec![Rule::NoDisruptionBudget])]
    #[case::neither(guards(true, false, 0), vec![Rule::NoReadinessProbe, Rule::NoDisruptionBudget])]
    #[case::one_pod_needs_neither(guards(false, false, 0), vec![])]
    fn finds_what_a_replicated_workload_lacks(#[case] guards: Guards, #[case] rules: Vec<Rule>) {
        let found: Vec<Rule> = of_workload(&guards, 3).iter().map(|f| f.rule).collect();
        assert_eq!(found, rules);
    }

    #[rstest]
    fn counts_each_rule_once_in_order() {
        let finding = |rule, name: &str| {
            let object =
                transect_graph::edges::Object::new("apps", "Deployment", Some("shop"), name);
            let linked = Linked { object, href: None };
            (WorkloadFinding { rule, replicas: 2 }, linked)
        };
        let findings = [
            finding(Rule::NoDisruptionBudget, "web"),
            finding(Rule::NoReadinessProbe, "api"),
            finding(Rule::NoDisruptionBudget, "feed"),
        ];
        let found: Vec<(Rule, Vec<String>)> = counts(findings)
            .into_iter()
            .map(|(rule, found)| (rule, found.into_iter().map(|l| l.object.name).collect()))
            .collect();
        assert_eq!(
            found,
            vec![
                (Rule::NoReadinessProbe, vec!["api".into()]),
                (Rule::NoDisruptionBudget, vec!["feed".into(), "web".into()])
            ]
        );
    }
}
