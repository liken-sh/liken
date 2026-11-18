use std::collections::{BTreeMap, BTreeSet};
use transect_graph::edges::Provenance;

use k8s_openapi::api::core::v1::{Pod, PodSpec};
use k8s_openapi::api::networking::v1::NetworkPolicy;
use rstest::rstest;

use super::fixtures::*;
use super::*;

/// Who may call the database, each as a name and its ports, or `None`
/// when no policy isolates it.
fn callers_of_db(policies: &[NetworkPolicy]) -> Option<Vec<(String, String)>> {
    let allowed = evaluate(policies);
    let Some(Reach::Isolated(callers)) = allowed.reach(&workload("shop", "db")).cloned() else {
        return None;
    };
    let named = callers.into_iter().map(|(caller, ports)| {
        let name = match caller {
            Caller::Workload { owner, .. } => owner.name,
            Caller::Addresses { cidr, except } if except.is_empty() => cidr,
            Caller::Addresses { cidr, except } => format!("{cidr} except {}", except.join(",")),
            Caller::Anything => "anything".into(),
        };
        (name, ports.to_string())
    });
    Some(named.collect())
}

fn expect(pairs: &[(&str, &str)]) -> Option<Vec<(String, String)>> {
    Some(
        pairs
            .iter()
            .map(|(name, ports)| (name.to_string(), ports.to_string()))
            .collect(),
    )
}

#[rstest]
#[case::no_policy(vec![], None)]
#[case::an_empty_rule_list(vec![policy("shop", "{podSelector: {}, policyTypes: [Ingress]}")], expect(&[]))]
#[case::an_egress_policy_alone(vec![policy("shop", "{podSelector: {}, policyTypes: [Egress]}")], None)]
#[case::another_namespaces_policy(vec![policy("blog", "{podSelector: {}, policyTypes: [Ingress]}")], None)]
#[case::types_filled_in_from_the_rules(vec![policy("shop", "{podSelector: {}, ingress: []}")], expect(&[]))]
#[case::a_pod_selector(vec![to_db("{from: [{podSelector: {matchLabels: {app: web}}}]}")], expect(&[("web", "any port")]))]
#[case::an_empty_pod_selector_is_its_namespace(vec![to_db("{from: [{podSelector: {}}]}")], expect(&[("web", "any port")]))]
#[case::a_namespace_selector(vec![to_db("{from: [{namespaceSelector: {matchLabels: {team: ops}}}]}")], expect(&[("agent", "any port")]))]
#[case::an_empty_namespace_selector_is_every_pod(
    vec![to_db("{from: [{namespaceSelector: {}}]}")],
    expect(&[("feed", "any port"), ("web", "any port"), ("agent", "any port")])
)]
#[case::both_selectors(
    vec![to_db("{from: [{namespaceSelector: {matchLabels: {team: ops}}, podSelector: {matchLabels: {app: agent}}}]}")],
    expect(&[("agent", "any port")])
)]
#[case::both_selectors_need_both(
    vec![to_db("{from: [{namespaceSelector: {matchLabels: {team: ops}}, podSelector: {matchLabels: {app: web}}}]}")],
    expect(&[])
)]
#[case::no_peers_is_anything(
    vec![to_db("{ports: [{port: 5432}]}")],
    expect(&[("feed", "port 5432"), ("web", "port 5432"), ("agent", "port 5432"), ("anything", "port 5432")])
)]
#[case::an_empty_peer_list_is_anything(
    vec![to_db("{from: []}")],
    expect(&[("feed", "any port"), ("web", "any port"), ("agent", "any port"), ("anything", "any port")])
)]
#[case::an_address_block(
    vec![to_db("{from: [{ipBlock: {cidr: 10.2.0.0/16}}]}")],
    expect(&[("agent", "any port"), ("10.2.0.0/16", "any port")])
)]
#[case::an_address_block_outside_the_pods(
    vec![to_db("{from: [{ipBlock: {cidr: 192.0.2.0/24}}]}")],
    expect(&[("192.0.2.0/24", "any port")])
)]
#[case::an_address_block_with_an_exception(
    vec![to_db("{from: [{ipBlock: {cidr: 10.0.0.0/8, except: [10.2.0.0/16]}}]}")],
    expect(&[("feed", "any port"), ("web", "any port"), ("10.0.0.0/8 except 10.2.0.0/16", "any port")])
)]
#[case::a_numbered_port(vec![to_db("{from: [{podSelector: {}}], ports: [{port: 5432}]}")], expect(&[("web", "port 5432")]))]
#[case::a_named_port(vec![to_db("{from: [{podSelector: {}}], ports: [{port: pg}]}")], expect(&[("web", "port 5432")]))]
#[case::a_named_port_the_pod_lacks(vec![to_db("{from: [{podSelector: {}}], ports: [{port: http}]}")], expect(&[]))]
#[case::an_end_port(vec![to_db("{from: [{podSelector: {}}], ports: [{port: 5432, endPort: 5440}]}")], expect(&[("web", "ports 5432–5440")]))]
#[case::rules_unite(
    vec![
        to_db("{from: [{podSelector: {}}], ports: [{port: 5432}]}"),
        to_db("{from: [{podSelector: {}}], ports: [{port: 9187}]}"),
    ],
    expect(&[("web", "ports 5432 and 9187")])
)]
#[case::egress_narrows_the_ports(
    vec![
        to_db("{from: [{podSelector: {}}]}"),
        policy("shop", "{podSelector: {matchLabels: {app: web}}, policyTypes: [Egress], egress: [{to: [{podSelector: {matchLabels: {app: db}}}], ports: [{port: pg}]}]}"),
    ],
    expect(&[("web", "port 5432")])
)]
#[case::egress_elsewhere_blocks_the_call(
    vec![
        to_db("{from: [{podSelector: {}}]}"),
        policy("shop", "{podSelector: {matchLabels: {app: web}}, policyTypes: [Egress], egress: [{to: [{namespaceSelector: {matchLabels: {team: ops}}}]}]}"),
    ],
    expect(&[])
)]
#[case::egress_by_address(
    vec![
        to_db("{from: [{podSelector: {}}]}"),
        policy("shop", "{podSelector: {matchLabels: {app: web}}, policyTypes: [Egress], egress: [{to: [{ipBlock: {cidr: 10.1.0.1/32}}]}]}"),
    ],
    expect(&[("web", "any port")])
)]
#[case::an_empty_egress_list_blocks_the_call(
    vec![
        to_db("{from: [{podSelector: {}}]}"),
        policy("shop", "{podSelector: {matchLabels: {app: web}}, policyTypes: [Egress]}"),
    ],
    expect(&[])
)]
fn evaluates_who_may_call(
    #[case] policies: Vec<NetworkPolicy>,
    #[case] expected: Option<Vec<(String, String)>>,
) {
    assert_eq!(callers_of_db(&policies), expected);
}

#[rstest]
#[case::nothing_isolated(vec![], NamespaceReach::Open)]
#[case::one_workload_isolated(
    vec![to_db("{from: [{podSelector: {}}]}")],
    NamespaceReach::Partly { open: vec![workload("shop", "web")], machine_network: vec![] }
)]
#[case::every_workload_isolated(
    vec![policy("shop", "{podSelector: {}, policyTypes: [Ingress], ingress: [{from: [{podSelector: {}}, {namespaceSelector: {matchLabels: {team: ops}}}]}]}")],
    NamespaceReach::Isolated(BTreeSet::from(["watch".into()]))
)]
fn summarizes_reach_by_namespace(
    #[case] policies: Vec<NetworkPolicy>,
    #[case] expected: NamespaceReach,
) {
    assert_eq!(evaluate(&policies).of_namespace("shop"), Some(expected));
}

#[rstest]
fn a_namespace_with_no_running_pod_has_no_reach() {
    assert_eq!(evaluate(&[]).of_namespace("empty"), None);
}

#[rstest]
fn edges_reach_only_isolated_workloads() {
    let allowed = evaluate(&[to_db("{from: [{podSelector: {matchLabels: {app: web}}}]}")]);
    assert_eq!(
        allowed.edges(),
        vec![Edge {
            kind: Kind::AllowedToCall,
            from: Endpoint::Object(workload("shop", "web")),
            to: Endpoint::Object(workload("shop", "db")),
            guessed: false,
            provenance: Provenance::Kubernetes,
        }]
    );
}

#[rstest]
fn a_finished_pod_is_no_caller() {
    let mut pods = pods();
    pods.push(serde_yaml::from_str("metadata: {namespace: shop, name: job, labels: {app: web}}\nstatus: {phase: Succeeded}").unwrap());
    let policies = [to_db("{from: [{podSelector: {}}]}")];
    let allowed = Allowed::evaluate(&policies, &pods, [], &namespaces(), &Ownership::default());
    assert_eq!(allowed.reach(&workload("shop", "job")), None);
}

fn labels(pairs: &[(&str, &str)]) -> BTreeMap<String, String> {
    pairs
        .iter()
        .map(|(k, v)| (k.to_string(), v.to_string()))
        .collect()
}

/// A `CronJob` with no running pod is evaluated as if it ran a pod with
/// the labels of each of its pod templates.
#[rstest]
#[case::admitted_by_its_template("{from: [{podSelector: {matchLabels: {app: report}}}]}", vec![labels(&[("app", "report")])], true)]
#[case::not_admitted("{from: [{podSelector: {matchLabels: {app: web}}}]}", vec![labels(&[("app", "report")])], false)]
// A `CronJob` template often has no labels. It then contributes no
// label set, and an empty pod selector still matches its pods.
#[case::a_template_with_no_labels("{from: [{podSelector: {}}]}", vec![], true)]
fn a_workload_between_runs_calls_by_its_template(
    #[case] rule: &str,
    #[case] templates: Vec<BTreeMap<String, String>>,
    #[case] admitted: bool,
) {
    let report = Object::new("batch", "CronJob", Some("shop"), "report");
    let spec = PodSpec::default();
    let idle = [Idle {
        owner: &report,
        labels: templates.iter().collect(),
        specs: vec![&spec],
    }];
    let (pods, policies) = (pods(), [to_db(rule)]);
    let allowed = Allowed::evaluate(&policies, &pods, idle, &namespaces(), &Ownership::default());
    let edge = Edge {
        kind: Kind::AllowedToCall,
        from: Endpoint::Object(report.clone()),
        to: Endpoint::Object(workload("shop", "db")),
        guessed: false,
        provenance: Provenance::Kubernetes,
    };
    assert_eq!(allowed.edges().contains(&edge), admitted);
}

/// The fixture's pods, with `hostNetwork: true` on the database's pod.
fn with_db_on_machine_network() -> Vec<Pod> {
    let mut pods = pods();
    pods[0] = on_machine_network(pods.remove(0));
    pods
}

/// No `NetworkPolicy` applies to a pod with `hostNetwork: true`, so a
/// policy that selects it does not limit its callers and draws no edge
/// to it.
#[rstest]
#[case::no_policy(vec![])]
#[case::a_policy_that_selects_it(vec![to_db("{from: [{podSelector: {matchLabels: {app: web}}}]}")])]
#[case::a_policy_on_its_whole_namespace(vec![policy("shop", "{podSelector: {}, policyTypes: [Ingress]}")])]
fn no_policy_applies_on_the_machines_network(#[case] policies: Vec<NetworkPolicy>) {
    let pods = with_db_on_machine_network();
    let allowed = Allowed::evaluate(&policies, &pods, [], &namespaces(), &Ownership::default());
    let to_db = |edge: &Edge| edge.to == Endpoint::Object(workload("shop", "db"));
    assert_eq!(
        (
            allowed.reach(&workload("shop", "db")),
            allowed.edges().iter().any(to_db)
        ),
        (Some(&Reach::MachineNetwork), false)
    );
}

/// No egress policy limits the calls from a pod with `hostNetwork: true`.
#[rstest]
fn no_egress_policy_limits_a_pod_on_its_machines_network() {
    let mut pods = pods();
    pods[1] = on_machine_network(pods.remove(1));
    let policies = [
        to_db("{ports: [{port: 5432}]}"),
        policy(
            "shop",
            "{podSelector: {matchLabels: {app: web}}, policyTypes: [Egress]}",
        ),
    ];
    let allowed = Allowed::evaluate(&policies, &pods, [], &namespaces(), &Ownership::default());
    let Some(Reach::Isolated(callers)) = allowed.reach(&workload("shop", "db")) else {
        panic!("shop/db has no Reach::Isolated, want one");
    };
    let web = Caller::Workload {
        namespace: "shop".into(),
        owner: workload("shop", "web"),
    };
    assert!(callers.contains_key(&web));
}

/// A caller with `hostNetwork: true` connects from its node's address, so
/// a pod or namespace selector does not admit it, and an `ipBlock` that
/// contains the node's address does.
#[rstest]
#[case::a_pod_selector("{from: [{podSelector: {matchLabels: {app: web}}}]}", false)]
#[case::its_whole_namespace("{from: [{podSelector: {}}]}", false)]
#[case::every_namespace("{from: [{namespaceSelector: {}}]}", false)]
#[case::a_block_that_contains_its_address("{from: [{ipBlock: {cidr: 10.1.0.0/24}}]}", true)]
#[case::no_peers("{ports: [{port: 5432}]}", true)]
fn admits_a_caller_on_its_machines_network_by_its_address(
    #[case] rule: &str,
    #[case] admitted: bool,
) {
    let mut pods = pods();
    pods[1] = on_machine_network(pods.remove(1));
    let allowed = Allowed::evaluate(
        &[to_db(rule)],
        &pods,
        [],
        &namespaces(),
        &Ownership::default(),
    );
    let Some(Reach::Isolated(callers)) = allowed.reach(&workload("shop", "db")) else {
        panic!("shop/db has no Reach::Isolated, want one");
    };
    let web = Caller::Workload {
        namespace: "shop".into(),
        owner: workload("shop", "web"),
    };
    assert_eq!(callers.contains_key(&web), admitted);
}

/// Each rule that admits callers is recorded with its policy's name and
/// ports.
#[rstest]
fn records_each_rule_that_admits_callers() {
    let policies = [to_db(
        "{from: [{podSelector: {matchLabels: {app: web}}}], ports: [{port: 5432}]}",
    )];
    let allowed = evaluate(&policies);
    let admissions = allowed.admissions(&workload("shop", "db"));
    let found: Vec<(&str, String, usize)> = admissions
        .iter()
        .map(|a| (a.policy.as_str(), a.ports.to_string(), a.callers.len()))
        .collect();
    assert_eq!(found, vec![("p", "port 5432".to_string(), 1)]);
}

/// The names of the policies that limit calls into a workload, whether
/// or not their rules admit anyone.
#[rstest]
#[case::none(vec![], None)]
#[case::one_with_a_rule(vec![to_db("{ports: [{port: 5432}]}")], Some(vec!["p"]))]
#[case::a_deny_with_no_rules(vec![policy("shop", "{podSelector: {}, policyTypes: [Ingress]}")], Some(vec!["p"]))]
#[case::egress_only(vec![policy("shop", "{podSelector: {}, policyTypes: [Egress]}")], None)]
fn names_the_policies_that_limit_calls_in(
    #[case] policies: Vec<NetworkPolicy>,
    #[case] expected: Option<Vec<&str>>,
) {
    let allowed = evaluate(&policies);
    let found = allowed.limits_in(&workload("shop", "db"));
    let found = found.map(|names| names.iter().map(String::as_str).collect::<Vec<_>>());
    assert_eq!(found, expected);
}
