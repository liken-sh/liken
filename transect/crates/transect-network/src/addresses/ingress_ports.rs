//! Tests of an `Ingress` host on a port the `Ingress` does not answer
//! on: a forge whose web host is an `Ingress` and whose SSH port is a
//! `NodePort` or `LoadBalancer` `Service` of the same workload.

use std::sync::Arc;

use k8s_openapi::api::core::v1::Service;
use k8s_openapi::api::networking::v1::Ingress;
use rstest::{fixture, rstest};

use super::tests::{deployment, object, pod_spec, repository, workload};
use super::*;
use crate::entry::fixtures::{labels, routing};
use crate::entry::{routes_to, selects};
use transect_cluster::ownership::Ownership;
use transect_graph::edges::{Edge, Graph, Object, Provenance};

fn service(name: &str, app: &str, spec: &str) -> Arc<Service> {
    object(&format!(
        "metadata: {{namespace: shop, name: {name}}}\nspec: {{selector: {{app: {app}}}, {spec}}}"
    ))
}

/// A forge behind `forge.example.com`, with SSH on port 22 of a
/// `NodePort` `Service` at node port 32222 and on port 2222 of a
/// `LoadBalancer`; a wiki behind `wiki.example.com` with no `Service`
/// but its `Ingress`'s.
struct Forges {
    addresses: Addresses,
    graph: Graph,
}

#[fixture]
fn forges() -> Forges {
    let services = vec![
        service("forge", "forge", "ports: [{port: 3000}]"),
        service(
            "forge-ssh",
            "forge",
            "type: NodePort, ports: [{port: 22, nodePort: 32222}]",
        ),
        service(
            "forge-ssh-lb",
            "forge",
            "type: LoadBalancer, ports: [{port: 2222, nodePort: 31022}]",
        ),
        service(
            "forge-internal",
            "forge",
            "type: ClusterIP, ports: [{port: 7999, nodePort: 30999}]",
        ),
        service("wiki", "wiki", "ports: [{port: 80}]"),
    ];
    let ingresses: Vec<Arc<Ingress>> = vec![
        routing("forge", "forge.example.com", "forge"),
        routing("wiki", "wiki.example.com", "wiki"),
    ];
    let owners = [deployment("shop", "forge"), deployment("shop", "wiki")];
    let pod_labels = [labels(&[("app", "forge")]), labels(&[("app", "wiki")])];
    let labelled = owners.iter().zip(&pod_labels).map(|(o, l)| (o, vec![l]));
    let graph = Graph::new([selects(&services, labelled), routes_to(&ingresses)]);
    Forges {
        addresses: Addresses::build(&services, &ingresses, &[], DEFAULT_DOMAIN),
        graph,
    }
}

fn depends(to: Endpoint, guessed: bool) -> Edge {
    Edge {
        kind: Kind::DependsOn,
        from: Endpoint::Object(repository("deploy")),
        to,
        guessed,
        provenance: Provenance::Kubernetes,
    }
}

#[rstest]
#[case::the_web_host("https://forge.example.com/org/repo.git", vec![depends(workload("shop", "forge"), false)])]
#[case::a_node_port(
    "ssh://git@forge.example.com:32222/org/repo.git",
    vec![depends(workload("shop", "forge"), false)]
)]
#[case::a_load_balancers_port(
    "ssh://git@forge.example.com:2222/org/repo.git",
    vec![depends(workload("shop", "forge"), false)]
)]
#[case::a_node_ports_service_port(
    "ssh://git@forge.example.com:22/org/repo.git",
    vec![depends(workload("shop", "forge"), false)]
)]
#[case::a_load_balancers_node_port(
    "ssh://git@forge.example.com:31022/org/repo.git",
    vec![depends(workload("shop", "forge"), false)]
)]
#[case::a_port_no_service_exposes(
    "ssh://git@forge.example.com:2200/org/repo.git",
    vec![depends(workload("shop", "forge"), true)]
)]
#[case::a_cluster_ip_services_node_port(
    "ssh://git@forge.example.com:30999/org/repo.git",
    vec![depends(workload("shop", "forge"), true)]
)]
#[case::a_host_with_only_its_ingress(
    "ssh://git@wiki.example.com:2222/org/repo.git",
    vec![depends(workload("shop", "wiki"), true)]
)]
#[case::a_host_outside_the_cluster("ssh://git@forge.example.org:32222/org/repo.git", vec![])]
fn a_source_on_another_port_reaches_the_ingress_workload(
    forges: Forges,
    #[case] url: &str,
    #[case] expected: Vec<Edge>,
) {
    let source = repository("deploy");
    let edges = sources_depend_on(&forges.addresses, &forges.graph, [(&source, url)]);
    assert_eq!(edges, expected);
}

#[rstest]
#[case::a_service_on_the_port("forge.example.com:32222", false)]
#[case::no_service_on_the_port("forge.example.com:2200", true)]
fn a_workload_configured_with_another_port_reaches_the_ingress_workload(
    forges: Forges,
    #[case] value: &str,
    #[case] guessed: bool,
) {
    let api = deployment("shop", "api");
    let spec = pod_spec(&[value], &[]);
    let found = of_workload(
        &forges.addresses,
        &forges.graph,
        &Ownership::default(),
        &api,
        &[&spec],
    );
    assert_eq!(
        found,
        vec![Dependency {
            address: value.into(),
            to: vec![workload("shop", "forge")],
            guessed,
        }]
    );
}

/// The forge reconciles from an applier in `deploy` that reconciles
/// from the repository, so a source fetched over the forge's SSH port
/// completes a cycle.
#[rstest]
fn a_forge_that_serves_its_own_manifests_over_ssh_is_a_loop(forges: Forges) {
    let source = repository("deploy");
    let applier = Object::new("apply.example.test", "Tree", Some("deploy"), "forge");
    let reconciles = |from: Endpoint, to: &Object| Edge {
        kind: Kind::ReconcilesFrom,
        from,
        to: Endpoint::Object(to.clone()),
        guessed: false,
        provenance: Provenance::Kubernetes,
    };
    let url = "ssh://git@forge.example.com:32222/org/repo.git";
    let mut graph = forges.graph;
    graph.add(vec![
        reconciles(workload("shop", "forge"), &applier),
        reconciles(Endpoint::Object(applier.clone()), &source),
    ]);
    let sources = sources_depend_on(&forges.addresses, &graph, [(&source, url)]);
    graph.add(sources);
    let namespaces: Vec<Vec<String>> = graph.loops().into_iter().map(|l| l.namespaces).collect();
    assert_eq!(namespaces, vec![vec!["deploy".to_string(), "shop".into()]]);
}
