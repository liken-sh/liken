use std::sync::Arc;

use k8s_openapi::api::apps::v1::Deployment;
use k8s_openapi::api::core::v1::Service;

use rstest::rstest;

use super::*;
use transect_cluster::ownership::Ownership;
use transect_cluster::workloads::{Objects, Revisions, Workloads};
use transect_graph::edges::Object;

/// A replicated workload with every guard in place: pods spread by
/// anti-affinity, a budget that allows one eviction, a rolling update, a
/// readiness probe, and a CPU limit.
fn guarded() -> Guards {
    Guards {
        replicated: true,
        scheduled: false,
        spread: Some(Spread::AntiAffinity),
        budgets: vec![budget(1)],
        strategies: vec!["RollingUpdate".into()],
        readiness_probe: true,
        served_unready: false,
        cpu_limit: true,
        operator_moves: false,
    }
}

fn budget(allowed: i32) -> Budget {
    Budget {
        name: "web".into(),
        limit: Limit::MinAvailable("1".into()),
        allowed,
    }
}

fn single(guards: Guards) -> Guards {
    Guards {
        replicated: false,
        ..guards
    }
}

fn listed(guards: &Guards) -> Vec<(String, Hold)> {
    let list = guards.list();
    list.iter()
        .map(|guard| (guard.to_string(), guards.hold(guard)))
        .collect()
}

fn row(text: &str, hold: Hold) -> (String, Hold) {
    (text.into(), hold)
}

#[rstest]
#[case::every_guard_in_place(guarded(), vec![
    row("anti-affinity by machine", Hold::Held),
    row("PDB: min 1, 1 may be evicted", Hold::Held),
    row("RollingUpdate", Hold::Held),
])]
#[case::nothing_in_place(
    Guards { spread: None, budgets: vec![], strategies: vec!["Recreate".into()], readiness_probe: false, served_unready: true, cpu_limit: false, ..guarded() },
    vec![
        row("not spread by machine", Hold::Open),
        row("no PodDisruptionBudget", Hold::Warn),
        row("Recreate", Hold::Open),
        row("no readiness probe", Hold::Warn),
        row("no CPU limit", Hold::Open),
    ],
)]
#[case::several_budgets_are_named(
    Guards { budgets: vec![Budget { name: "primary".into(), ..budget(0) }, Budget { name: "replicas".into(), ..budget(1) }], ..guarded() },
    vec![
        row("anti-affinity by machine", Hold::Held),
        row("PDB primary: min 1, none may be evicted", Hold::Warn),
        row("PDB replicas: min 1, 1 may be evicted", Hold::Held),
        row("RollingUpdate", Hold::Held),
    ],
)]
#[case::a_budget_that_lets_none_go(
    Guards { budgets: vec![budget(0)], ..guarded() },
    vec![
        row("anti-affinity by machine", Hold::Held),
        row("PDB: min 1, none may be evicted", Hold::Warn),
        row("RollingUpdate", Hold::Held),
    ],
)]
#[case::a_budget_its_operator_expects(
    Guards { budgets: vec![budget(0)], operator_moves: true, ..guarded() },
    vec![
        row("anti-affinity by machine", Hold::Held),
        row("PDB: min 1, none may be evicted; its operator moves the primary first", Hold::Held),
        row("RollingUpdate", Hold::Held),
    ],
)]
#[case::a_single_pod_needs_no_spread_budget_or_probe(
    single(Guards { spread: None, budgets: vec![], readiness_probe: false, ..guarded() }),
    vec![row("RollingUpdate", Hold::Held), row("no readiness probe", Hold::Open)],
)]
#[case::a_cron_job_has_no_strategy_or_probe(
    single(Guards { scheduled: true, budgets: vec![], strategies: vec![], readiness_probe: false, cpu_limit: false, ..guarded() }),
    vec![row("no CPU limit", Hold::Open)],
)]
fn lists_each_guard_and_how_it_holds(
    #[case] guards: Guards,
    #[case] expected: Vec<(String, Hold)>,
) {
    assert_eq!(listed(&guards), expected);
}

fn deployment(template: &str) -> Arc<Deployment> {
    let template: Vec<String> = template.lines().map(|line| format!("    {line}")).collect();
    Arc::new(
        serde_yaml_ng::from_str(&format!(
            "metadata: {{namespace: shop, name: web}}\nspec:\n  replicas: 3\n  selector: {{}}\n  template:\n{}",
            template.join("\n")
        ))
        .unwrap(),
    )
}

fn budget_for(app: &str) -> Arc<PodDisruptionBudget> {
    Arc::new(
        serde_yaml_ng::from_str(&format!(
            "{{metadata: {{namespace: shop, name: {app}}}, spec: {{selector: {{matchLabels: {{app: {app}}}}}, maxUnavailable: 1}}, status: {{disruptionsAllowed: 1, currentHealthy: 3, desiredHealthy: 2, expectedPods: 3}}}}"
        ))
        .unwrap(),
    )
}

/// A `Service` that sends traffic to the pods labeled `app: web`, and
/// one with no selector, which selects none.
fn services() -> Vec<Arc<Service>> {
    let service = |name: &str, spec: &str| -> Arc<Service> {
        Arc::new(
            serde_yaml_ng::from_str(&format!(
                "{{metadata: {{namespace: shop, name: {name}}}, spec: {spec}}}"
            ))
            .unwrap(),
        )
    };
    vec![
        service("web", "{selector: {app: web}}"),
        service("peers", "{clusterIP: None}"),
    ]
}

fn read(template: &str, budgets: &[Arc<PodDisruptionBudget>]) -> Guards {
    let deployments = [deployment(template)];
    let objects = Objects {
        deployments: &deployments,
        stateful_sets: &[],
        daemon_sets: &[],
        cron_jobs: &[],
        pods: &[],
    };
    let workloads = Workloads::build(&objects, &Revisions::default(), &Ownership::default());
    let web = Object::new("apps", "Deployment", Some("shop"), "web");
    Guards::read(&workloads.get(&web), budgets, &services())
}

#[rstest]
#[case::a_bare_template(
    "{metadata: {labels: {app: cart}}, spec: {containers: [{name: app}]}}",
    (None, false, false, 0)
)]
#[case::a_guarded_template(
    r#"
metadata: {labels: {app: web}}
spec:
  topologySpreadConstraints: [{maxSkew: 1, topologyKey: kubernetes.io/hostname, whenUnsatisfiable: DoNotSchedule}]
  containers:
    - {name: app, readinessProbe: {httpGet: {port: 80}}, resources: {limits: {cpu: 500m}}}
    - {name: proxy, resources: {limits: {cpu: 100m}}}
"#,
    (Some(Spread::TopologySpread), true, true, 1)
)]
#[case::one_container_without_a_cpu_limit(
    r#"
metadata: {labels: {app: cart}}
spec:
  containers:
    - {name: app, readinessProbe: {exec: {command: [ready]}}, resources: {limits: {cpu: 500m}}}
    - {name: proxy}
"#,
    (None, true, false, 0)
)]
fn reads_the_guards_from_the_pod_template(
    #[case] template: &str,
    #[case] expected: (Option<Spread>, bool, bool, usize),
) {
    let guards = read(template, &[budget_for("web"), budget_for("other")]);
    assert_eq!(
        (
            guards.spread,
            guards.readiness_probe,
            guards.cpu_limit,
            guards.budgets.len()
        ),
        expected
    );
    assert!(guards.replicated);
}

#[rstest]
#[case::served_without_a_probe(
    "{metadata: {labels: {app: web}}, spec: {containers: [{name: app}]}}",
    true
)]
#[case::served_with_a_probe(
    "{metadata: {labels: {app: web}}, spec: {containers: [{name: app, readinessProbe: {exec: {command: [ready]}}}]}}",
    false
)]
#[case::selected_by_no_service(
    "{metadata: {labels: {app: peer}}, spec: {containers: [{name: app}]}}",
    false
)]
fn a_missing_probe_matters_only_where_a_service_sends_traffic(
    #[case] template: &str,
    #[case] takes_traffic_unready: bool,
) {
    assert_eq!(
        read(template, &[]).takes_traffic_unready(),
        takes_traffic_unready
    );
}
