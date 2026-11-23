use std::sync::Arc;

use k8s_openapi::api::core::v1::{Node, PodSpec, Service};
use k8s_openapi::api::networking::v1::Ingress;
use rstest::{fixture, rstest};
use serde::de::DeserializeOwned;

use super::*;
use crate::entry::fixtures::{ingress, labels, routing};
use crate::entry::{routes_to, selects};
use transect_cluster::ownership::{self, Ownership};
use transect_graph::edges::{Edge, Object, Provenance};

pub(super) fn object<K: DeserializeOwned>(yaml: &str) -> Arc<K> {
    Arc::new(serde_yaml_ng::from_str(yaml).unwrap())
}

/// `spec` is the rest of the `Service`'s spec, in YAML.
fn service(namespace: &str, name: &str, port: u16, spec: &str) -> Arc<Service> {
    object(&format!(
        "metadata: {{namespace: {namespace}, name: {name}}}\nspec: {{ports: [{{port: {port}}}], {spec}}}"
    ))
}

fn node(name: &str, internal_ip: &str) -> Arc<Node> {
    object(&format!(
        "metadata: {{name: {name}}}\nstatus: {{addresses: [{{type: InternalIP, address: {internal_ip}}}, {{type: Hostname, address: {name}}}]}}"
    ))
}

pub(super) fn deployment(namespace: &str, name: &str) -> Object {
    Object::new("apps", "Deployment", Some(namespace), name)
}

pub(super) fn workload(namespace: &str, name: &str) -> Endpoint {
    Endpoint::Object(deployment(namespace, name))
}

/// The namespaces shop and blog, and the machine alpha.  In shop, web
/// is behind an `Ingress` and a `LoadBalancer`, sign-in serves one path
/// of web's host, and db, cache, and the headless queue each have a
/// `Service`.  payments is an `ExternalName` `Service`, and web-admin is
/// a `Service` with no selector that web owns.  blog has its own db.
struct Cluster {
    services: Vec<Arc<Service>>,
    ingresses: Vec<Arc<Ingress>>,
    nodes: Vec<Arc<Node>>,
    graph: Graph,
    ownership: Ownership,
}

#[fixture]
fn cluster() -> Cluster {
    let mut services = vec![
        service("shop", "web", 80, "selector: {app: web}"),
        service(
            "shop",
            "web-public",
            443,
            "selector: {app: web}, type: LoadBalancer",
        ),
        service("shop", "db", 5432, "selector: {app: db}"),
        service("shop", "cache", 6379, "selector: {app: cache}"),
        service(
            "shop",
            "queue",
            9092,
            "selector: {app: queue}, clusterIP: None",
        ),
        service("shop", "payments", 443, "type: ExternalName"),
        service("shop", "web-admin", 8080, "type: ClusterIP"),
        service("shop", "sign-in", 80, "selector: {app: sign-in}"),
        service("blog", "db", 5432, "selector: {app: db}"),
    ];
    let mut public = (*services[1]).clone();
    public.status = serde_yaml_ng::from_str("loadBalancer: {ingress: [{ip: 192.0.2.10}]}").unwrap();
    services[1] = Arc::new(public);
    let outpost = "{rules: [{host: shop.example.com, http: {paths: [{path: /outpost, pathType: Prefix, backend: {service: {name: sign-in, port: {number: 80}}}}]}}]}";
    let ingresses = vec![
        routing("web", "shop.example.com", "web"),
        ingress("outpost", outpost),
    ];
    let names = [
        ("shop", "web"),
        ("shop", "db"),
        ("shop", "cache"),
        ("shop", "queue"),
        ("shop", "sign-in"),
        ("blog", "db"),
    ];
    let owners: Vec<Object> = names.iter().map(|(ns, n)| deployment(ns, n)).collect();
    let pod_labels: Vec<_> = names.iter().map(|(_, n)| labels(&[("app", n)])).collect();
    let labelled = owners.iter().zip(&pod_labels).map(|(o, l)| (o, vec![l]));
    let graph = Graph::new([selects(&services, labelled), routes_to(&ingresses)]);
    let ownership = ownership::fixtures::build(&[(
        "",
        "Service",
        Some("shop"),
        "web-admin",
        vec![("apps/v1", "Deployment", "web")],
    )]);
    Cluster {
        services,
        ingresses,
        nodes: vec![node("alpha", "10.0.0.1")],
        graph,
        ownership,
    }
}

impl Cluster {
    fn addresses(&self, domain: &str) -> Addresses {
        Addresses::build(&self.services, &self.ingresses, &self.nodes, domain)
    }

    /// What `from` depends on when its container has the values `env`,
    /// each in a variable whose name ends in `_HOST`, and the arguments
    /// `args`.
    fn dependencies(
        &self,
        domain: &str,
        from: &Object,
        env: &[&str],
        args: &[&str],
    ) -> Vec<Dependency> {
        let spec = pod_spec(env, args);
        let addresses = self.addresses(domain);
        of_workload(&addresses, &self.graph, &self.ownership, from, &[&spec])
    }
}

pub(super) fn pod_spec(env: &[&str], args: &[&str]) -> PodSpec {
    let env: Vec<String> = env
        .iter()
        .enumerate()
        .map(|(index, value)| format!("{{name: TARGET_{index}_HOST, value: {value:?}}}"))
        .collect();
    serde_yaml_ng::from_str(&format!(
        "containers: [{{name: app, env: [{}], args: {args:?}}}]",
        env.join(", ")
    ))
    .unwrap()
}

#[rstest]
#[case::a_short_name("db", workload("shop", "db"))]
#[case::a_short_name_and_port("db:5432", workload("shop", "db"))]
#[case::service_dot_namespace("db.shop", workload("shop", "db"))]
#[case::service_dot_namespace_dot_svc("db.shop.svc", workload("shop", "db"))]
#[case::the_whole_name("db.shop.svc.cluster.local", workload("shop", "db"))]
#[case::the_whole_name_and_port("db.shop.svc.cluster.local:5432", workload("shop", "db"))]
#[case::another_namespace("db.blog", workload("blog", "db"))]
#[case::a_url_with_userinfo_and_a_path("postgres://app:secret@db:5432/app", workload("shop", "db"))]
#[case::a_url_with_a_path_and_query(
    "redis://cache.shop.svc:6379/0?timeout=5",
    workload("shop", "cache")
)]
#[case::a_url_without_a_port("http://cache", workload("shop", "cache"))]
#[case::one_pod_of_a_headless_service("queue-0.queue:9092", workload("shop", "queue"))]
#[case::an_ingress_host("https://shop.example.com/login", workload("shop", "web"))]
#[case::an_ingress_hosts_longer_path(
    "https://shop.example.com/outpost/start",
    workload("shop", "sign-in")
)]
#[case::an_ingress_host_alone("shop.example.com", workload("shop", "web"))]
#[case::a_load_balancer_address("192.0.2.10:443", workload("shop", "web"))]
#[case::a_service_that_selects_nothing(
    "https://payments",
    Endpoint::Object(Object::new("", "Service", Some("shop"), "payments"))
)]
#[case::a_machines_address("10.0.0.1:9100", Endpoint::Machine("alpha".into()))]
#[case::a_machines_name("alpha", Endpoint::Machine("alpha".into()))]
fn resolves_a_value_to_what_it_reaches(
    cluster: Cluster,
    #[case] value: &str,
    #[case] to: Endpoint,
) {
    let api = deployment("shop", "api");
    let found = cluster.dependencies(DEFAULT_DOMAIN, &api, &[value], &[]);
    let reached: Vec<&Endpoint> = found.iter().flat_map(|d| &d.to).collect();
    assert_eq!(reached, vec![&to]);
}

#[rstest]
#[case::a_longer_name("dbx")]
#[case::a_name_with_a_prefix("my-db")]
#[case::a_name_run_together("shopdb")]
#[case::a_host_that_ends_in_a_service("db.shop.example.com")]
#[case::a_host_that_starts_with_a_service("db.shop.svc.cluster.local.example.com")]
#[case::a_word_in_a_sentence("the db is down")]
#[case::a_path("/var/lib/db")]
#[case::a_port_the_service_does_not_answer_on("db:9999")]
#[case::another_cluster_domain("db.shop.svc.example.internal")]
#[case::a_service_another_namespace_lacks("cache.blog")]
fn a_value_that_names_no_whole_address_reaches_nothing(cluster: Cluster, #[case] value: &str) {
    let api = deployment("shop", "api");
    assert_eq!(
        cluster.dependencies(DEFAULT_DOMAIN, &api, &[value], &[]),
        vec![]
    );
}

#[rstest]
#[case::in_its_namespace(deployment("shop", "api"), vec![workload("shop", "cache")])]
#[case::from_another_namespace(deployment("blog", "api"), vec![])]
fn a_short_name_resolves_in_its_own_namespace(
    cluster: Cluster,
    #[case] from: Object,
    #[case] reached: Vec<Endpoint>,
) {
    let found = cluster.dependencies(DEFAULT_DOMAIN, &from, &["cache:6379"], &[]);
    let found: Vec<Endpoint> = found.into_iter().flat_map(|d| d.to).collect();
    assert_eq!(found, reached);
}

#[rstest]
#[case::the_default("db.shop.svc.cluster.local", DEFAULT_DOMAIN, 1)]
#[case::a_domain_of_its_own("db.shop.svc.example.internal", "example.internal", 1)]
#[case::the_default_under_another("db.shop.svc.cluster.local", "example.internal", 0)]
fn reads_the_cluster_domain(
    cluster: Cluster,
    #[case] value: &str,
    #[case] domain: &str,
    #[case] count: usize,
) {
    let api = deployment("shop", "api");
    assert_eq!(
        cluster.dependencies(domain, &api, &[value], &[]).len(),
        count
    );
}

#[rstest]
fn reads_arguments_and_shows_only_host_and_port(cluster: Cluster) {
    let api = deployment("shop", "api");
    let found = cluster.dependencies(
        DEFAULT_DOMAIN,
        &api,
        &["postgres://app:secret@db:5432/app", "db:5432"],
        &["--cache=redis://cache:6379", "--verbose"],
    );
    let addresses: Vec<&str> = found.iter().map(|d| d.address.as_str()).collect();
    assert_eq!(addresses, vec!["db:5432", "cache:6379"]);
}

#[rstest]
#[case::a_variable_that_holds_a_host("DB_HOST", 1)]
#[case::a_variable_that_holds_a_name("DB_NAME", 0)]
fn a_bare_name_counts_only_as_a_host(cluster: Cluster, #[case] name: &str, #[case] count: usize) {
    let spec: PodSpec = serde_yaml_ng::from_str(&format!(
        "containers: [{{name: app, env: [{{name: {name}, value: db}}]}}]"
    ))
    .unwrap();
    let api = deployment("shop", "api");
    let addresses = cluster.addresses(DEFAULT_DOMAIN);
    assert_eq!(
        of_workload(
            &addresses,
            &cluster.graph,
            &cluster.ownership,
            &api,
            &[&spec]
        )
        .len(),
        count
    );
}

#[rstest]
#[case::through_its_ingress("https://shop.example.com")]
#[case::through_a_service_it_owns("http://web-admin:8080")]
fn a_workloads_own_address_is_no_dependency(cluster: Cluster, #[case] value: &str) {
    let web = deployment("shop", "web");
    let found = cluster.dependencies(DEFAULT_DOMAIN, &web, &[value], &[]);
    assert_eq!(found, vec![]);
}

#[rstest]
fn a_service_another_workload_owns_is_a_dependency(cluster: Cluster) {
    let api = deployment("shop", "api");
    let found = cluster.dependencies(DEFAULT_DOMAIN, &api, &["http://web-admin:8080"], &[]);
    let reached: Vec<&Endpoint> = found.iter().flat_map(|d| &d.to).collect();
    let admin = Endpoint::Object(Object::new("", "Service", Some("shop"), "web-admin"));
    assert_eq!(reached, vec![&admin]);
}

#[rstest]
fn an_edge_for_each_end_a_workload_reaches(cluster: Cluster) {
    let api = deployment("shop", "api");
    let spec = pod_spec(&["db:5432", "db.shop.svc", "db.blog", "the db"], &[]);
    let edges = depends_on(
        &cluster.addresses(DEFAULT_DOMAIN),
        &cluster.graph,
        &cluster.ownership,
        [(&api, vec![&spec])],
    );
    let ends: Vec<(&Endpoint, &Endpoint)> = edges.iter().map(|e| (&e.from, &e.to)).collect();
    let from = Endpoint::Object(api.clone());
    assert_eq!(
        ends,
        vec![
            (&from, &workload("shop", "db")),
            (&from, &workload("blog", "db")),
        ]
    );
}

fn depends(from: Endpoint, to: Endpoint) -> Edge {
    Edge {
        kind: Kind::DependsOn,
        from,
        to,
        guessed: false,
        provenance: Provenance::Kubernetes,
    }
}

#[rstest]
fn reads_what_crosses_a_namespace() {
    let graph = Graph::new([vec![
        depends(workload("shop", "api"), workload("shop", "db")),
        depends(workload("shop", "api"), workload("auth", "login")),
        depends(workload("shop", "api"), Endpoint::Machine("alpha".into())),
        depends(workload("blog", "web"), workload("shop", "api")),
        depends(workload("blog", "web"), workload("auth", "login")),
    ]]);
    let set = |names: &[&str]| names.iter().map(|n| n.to_string()).collect();
    assert_eq!(
        Across::of(&graph, "shop"),
        Across {
            on: set(&["auth"]),
            machines: set(&["alpha"]),
            by: set(&["blog"]),
        }
    );
}

pub(super) fn repository(namespace: &str) -> Object {
    Object::new("apply.example.test", "Repo", Some(namespace), "fleet")
}

#[rstest]
#[case::the_forges_host("https://shop.example.com/ops/fleet", "deploy", vec![workload("shop", "web")])]
#[case::an_ssh_url_on_the_forges_host(
    "ssh://git@shop.example.com/ops/fleet.git",
    "deploy",
    vec![workload("shop", "web")]
)]
#[case::an_oci_url_on_the_forges_host("oci://shop.example.com/ops/fleet", "deploy", vec![workload("shop", "web")])]
#[case::a_short_name_in_the_sources_namespace("http://web/ops/fleet", "shop", vec![workload("shop", "web")])]
#[case::a_short_name_from_another_namespace("http://web/ops/fleet", "deploy", vec![])]
#[case::a_forge_outside_the_cluster("https://forge.example.org/ops/fleet", "deploy", vec![])]
fn a_source_depends_on_the_forge_it_fetches_from(
    cluster: Cluster,
    #[case] url: &str,
    #[case] namespace: &str,
    #[case] expected: Vec<Endpoint>,
) {
    let source = repository(namespace);
    let edges = sources_depend_on(
        &cluster.addresses(DEFAULT_DOMAIN),
        &cluster.graph,
        [(&source, url)],
    );
    let found: Vec<Edge> = expected
        .into_iter()
        .map(|to| depends(Endpoint::Object(source.clone()), to))
        .collect();
    assert_eq!(edges, found);
}
