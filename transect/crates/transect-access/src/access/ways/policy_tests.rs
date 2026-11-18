//! Tests of the NetworkPolicy gate of a way in.  The gate is `On` when
//! a policy limits who may call the pods on the way's ports, and `Off`
//! when no policy selects the pods or a policy admits any caller on
//! those ports.  It is `Half` when the callers are limited on some of
//! the ports or workloads and not on others.

use std::collections::BTreeSet;

use k8s_openapi::api::networking::v1::NetworkPolicyPort;
use rstest::rstest;

use super::fixture::{Cluster, deployment};
use super::*;
use transect_network::policy::{Admission, Caller, Ports};

fn ports(text: &str) -> Ports {
    let ports: Vec<NetworkPolicyPort> = serde_yaml::from_str(text).unwrap();
    Ports::of_rule(&ports, &[])
}

fn admits(policy: &str, callers: Vec<Caller>, on: &str) -> Admission {
    Admission {
        policy: policy.into(),
        ports: ports(on),
        callers: callers.into_iter().collect::<BTreeSet<Caller>>(),
        whole: Vec::new(),
        everywhere: false,
    }
}

fn caller(namespace: &str, name: &str) -> Caller {
    Caller::Workload {
        namespace: namespace.into(),
        owner: deployment(namespace, name),
    }
}

fn block(cidr: &str, except: &[&str]) -> Caller {
    Caller::Addresses {
        cidr: cidr.into(),
        except: except.iter().map(|e| e.to_string()).collect(),
    }
}

const BROKER: &str = r#"
kind: Service
metadata: {namespace: home, name: broker}
spec: {type: LoadBalancer, selector: {app: broker}, ports: [{port: 1883}, {port: 8883}]}
status: {loadBalancer: {ingress: [{ip: 192.0.2.10}]}}
"#;

const STOREFRONT: &str = r#"
kind: Service
metadata: {namespace: shop, name: storefront}
spec: {selector: {app: storefront}, ports: [{port: 80, targetPort: http}]}
---
kind: Pod
metadata: {namespace: shop, name: storefront-1, labels: {app: storefront}}
spec: {containers: [{name: main, ports: [{name: http, containerPort: 8080}]}]}
---
kind: Ingress
metadata: {namespace: shop, name: shop}
spec:
  rules:
  - host: a.example.test
    http: {paths: [{path: /, pathType: Prefix, backend: {service: {name: storefront, port: {number: 80}}}}]}
"#;

/// The policy gate of the first row of `text`, with `admitted`
/// isolating `workload`, or with no policy when `admitted` is `None`.
fn gate(text: &str, workload: &str, admitted: Option<Vec<Admission>>) -> (State, String) {
    let cluster = Cluster::of(text);
    let cluster = match admitted {
        Some(admitted) => cluster.isolated(workload, admitted),
        None => cluster,
    };
    let ways = cluster.ways();
    let way = ways.rows.first().expect("the cluster has no way in");
    (way.policy.state, way.policy.title.clone())
}

#[rstest]
#[case::no_policy(
    BROKER,
    "broker",
    None,
    State::Off,
    "No NetworkPolicy selects its pods, so any pod may call them."
)]
#[case::a_workload_on_every_port(
    BROKER, "broker",
    Some(vec![admits("broker-from-the-app", vec![caller("home", "app")], "[]")]),
    State::On, "A NetworkPolicy selects its pods and names who may call them."
)]
#[case::any_caller_on_both_ports(
    BROKER, "broker",
    Some(vec![admits("broker-from-anywhere", vec![Caller::Anything], "[{port: 1883}, {port: 8883}]")]),
    State::Off, "NetworkPolicy broker-from-anywhere admits any caller on 1883 and 8883."
)]
#[case::every_address_on_one_port(
    BROKER, "broker",
    Some(vec![
        admits("broker-from-every-address", vec![block("0.0.0.0/0", &[])], "[{port: 1883}]"),
        admits("broker-from-the-app", vec![caller("home", "app")], "[{port: 8883}]"),
    ]),
    State::Half, "NetworkPolicy broker-from-every-address admits any caller on 1883; a NetworkPolicy limits who may call on 8883."
)]
#[case::every_v6_address(
    BROKER, "broker",
    Some(vec![admits("broker-from-every-address", vec![block("::/0", &[])], "[]")]),
    State::Off, "NetworkPolicy broker-from-every-address admits any caller on 1883 and 8883."
)]
#[case::every_address_but_some(
    BROKER, "broker",
    Some(vec![admits("broker-but-not-there", vec![block("0.0.0.0/0", &["192.0.2.0/24"])], "[]")]),
    State::On, "A NetworkPolicy selects its pods and names who may call them."
)]
#[case::the_ingress_controller_admitted(
    STOREFRONT, "storefront",
    Some(vec![admits("storefront-from-the-controller", vec![caller("edge", "controller")], "[{port: 8080}]")]),
    State::On, "A NetworkPolicy selects its pods, admits the ingress controller Deployment edge/controller, and names who else may call them."
)]
#[case::the_ingress_controller_not_admitted(
    STOREFRONT, "storefront",
    Some(vec![admits("storefront-from-the-app", vec![caller("shop", "app")], "[{port: 8080}]")]),
    State::On, "A NetworkPolicy selects its pods and does not admit the ingress controller Deployment edge/controller."
)]
#[case::any_caller_on_the_named_port(
    STOREFRONT, "storefront",
    Some(vec![admits("storefront-from-anywhere", vec![Caller::Anything], "[{port: 8080}]")]),
    State::Off, "NetworkPolicy storefront-from-anywhere admits any caller on 80."
)]
#[case::any_caller_on_another_port(
    STOREFRONT, "storefront",
    Some(vec![
        admits("metrics-from-anywhere", vec![Caller::Anything], "[{port: 9000}]"),
        admits("storefront-from-the-controller", vec![caller("edge", "controller")], "[{port: 8080}]"),
    ]),
    State::On, "A NetworkPolicy selects its pods, admits the ingress controller Deployment edge/controller, and names who else may call them."
)]
fn reads_whether_a_policy_limits_who_may_call(
    #[case] text: &str,
    #[case] workload: &str,
    #[case] admitted: Option<Vec<Admission>>,
    #[case] state: State,
    #[case] title: &str,
) {
    assert_eq!(gate(text, workload, admitted), (state, title.to_string()));
}

/// The gate of a workload on all of its ports, which the namespace and
/// workload access pages draw beside its callers.
#[rstest]
#[case::open(Isolation::Open, vec![], State::Off, "No NetworkPolicy selects its pods, so any pod may call them.")]
#[case::on_its_machines_network(Isolation::MachineNetwork, vec![], State::Off, "No NetworkPolicy applies to a pod on its machine's network.")]
#[case::limited(
    Isolation::Isolated,
    vec![admits("from-the-app", vec![caller("home", "app")], "[]")],
    State::On, "A NetworkPolicy selects its pods and names who may call them."
)]
#[case::any_caller_on_any_port(
    Isolation::Isolated,
    vec![admits("from-anywhere", vec![Caller::Anything], "[]")],
    State::Off, "NetworkPolicy from-anywhere admits any caller on any port."
)]
#[case::any_caller_on_one_port(
    Isolation::Isolated,
    vec![admits("from-anywhere", vec![Caller::Anything], "[{port: 1883}]")],
    State::Half, "NetworkPolicy from-anywhere admits any caller on port 1883, and a NetworkPolicy names who may call on its other ports."
)]
fn reads_a_workloads_own_policy_gate(
    #[case] isolation: Isolation,
    #[case] admitted: Vec<Admission>,
    #[case] state: State,
    #[case] title: &str,
) {
    let gate = super::policy::of_workload(Some(isolation), &admitted);
    assert_eq!((gate.state, gate.title), (state, title.to_string()));
}
