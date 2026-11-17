//! A small cluster to evaluate policies over: a database to protect,
//! a web front end beside it, an agent in a namespace of the operations
//! team, and a blog.

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::api::networking::v1::NetworkPolicy;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use super::Allowed;
use transect_cluster::ownership::Ownership;
use transect_graph::edges::Object;

/// A running pod with one address and a container port named `pg`.
pub fn pod(namespace: &str, name: &str, labels: &str, address: &str) -> Pod {
    serde_yaml::from_str(&format!(
        "metadata: {{namespace: {namespace}, name: {name}, labels: {{{labels}}}}}
spec: {{containers: [{{name: main, ports: [{{name: pg, containerPort: 5432}}]}}]}}
status: {{phase: Running, podIPs: [{{ip: {address}}}]}}"
    ))
    .unwrap()
}

/// `pod` on its machine's network, where its address is the machine's.
pub fn on_machine_network(mut pod: Pod) -> Pod {
    if let Some(spec) = pod.spec.as_mut() {
        spec.host_network = Some(true);
    }
    pod
}

pub fn pods() -> Vec<Pod> {
    vec![
        pod("shop", "db", "app: db", "10.1.0.1"),
        pod("shop", "web", "app: web", "10.1.0.2"),
        pod("watch", "agent", "app: agent", "10.2.0.1"),
        pod("blog", "feed", "app: feed", "10.3.0.1"),
    ]
}

pub fn namespaces() -> Vec<ObjectMeta> {
    let meta = |name: &str, labels: &str| -> ObjectMeta {
        serde_yaml::from_str(&format!("{{name: {name}, labels: {{{labels}}}}}")).unwrap()
    };
    vec![
        meta("shop", "tier: apps"),
        meta("watch", "team: ops"),
        meta("blog", "tier: apps"),
    ]
}

pub fn policy(namespace: &str, spec: &str) -> NetworkPolicy {
    serde_yaml::from_str(&format!(
        "metadata: {{namespace: {namespace}, name: p}}\nspec: {spec}"
    ))
    .unwrap()
}

/// An ingress policy on the database with one rule.
pub fn to_db(rule: &str) -> NetworkPolicy {
    policy(
        "shop",
        &format!(
            "{{podSelector: {{matchLabels: {{app: db}}}}, policyTypes: [Ingress], ingress: [{rule}]}}"
        ),
    )
}

pub fn evaluate(policies: &[NetworkPolicy]) -> Allowed {
    let (pods, namespaces) = (pods(), namespaces());
    Allowed::evaluate(policies, &pods, [], &namespaces, &Ownership::default())
}

pub fn workload(namespace: &str, name: &str) -> Object {
    Object::new("", "Pod", Some(namespace), name)
}
