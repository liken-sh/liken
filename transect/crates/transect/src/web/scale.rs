//! How long one model build and one render of each page take on a large
//! synthetic cluster: 50 namespaces, each with eight Deployments and
//! two StatefulSets of ten pods, a `Service` per workload, an address
//! from each workload to the next one's `Service`, and four
//! NetworkPolicies.  That is 5,000 pods, 500 workloads, and 200
//! policies.
//!
//! The bound is generous, because the machine that runs the test sets
//! the time.  The test fails on a regression to work that grows with
//! pairs of pods.  It is ignored by default because a debug build is
//! far slower.  Run it with
//!
//! ```text
//! cargo test --release -- --ignored --nocapture renders_a_large_cluster
//! ```

use std::sync::Arc;
use std::time::{Duration, Instant};

use k8s_openapi::api::apps::v1::{Deployment, ReplicaSet, StatefulSet};
use k8s_openapi::api::core::v1::{Namespace, Node, Pod, Service};
use k8s_openapi::api::networking::v1::NetworkPolicy;
use kube::api::PartialObjectMetaExt;
use rstest::rstest;

use super::model::Model;
use super::pages::{Focus, cluster_page, namespace_page, workload_page};
use super::render::Part;
use super::snapshot::Snapshot;
use super::template::template;
use transect_cluster::ownership::{Entry, Ownership};
use transect_graph::edges::Shown;
use transect_network::addresses::Addresses;
use transect_observe::usage;
use transect_resources::layers::Layers;

const NAMESPACES: usize = 50;
const DEPLOYMENTS: usize = 8;
const STATEFUL_SETS: usize = 2;
const REPLICAS: usize = 10;
const MACHINES: usize = 20;

/// The longest one build and one render of each page may take together.
const BOUND: Duration = Duration::from_secs(5);

fn parse<T: serde::de::DeserializeOwned>(yaml: &str) -> Arc<T> {
    Arc::new(serde_yaml::from_str(yaml).unwrap())
}

fn namespace(index: usize) -> Arc<Namespace> {
    parse(&format!(
        "metadata: {{name: ns-{index}, labels: {{team: t{}}}}}",
        index % 5
    ))
}

fn machine(index: usize) -> Arc<Node> {
    parse(&format!(
        "metadata: {{name: machine-{index}}}
status: {{addresses: [{{type: InternalIP, address: 192.168.0.{index}}}]}}"
    ))
}

/// The containers of `app`'s pods: one that calls `upstream` and
/// serves on a port named `http`.
fn containers(app: &str, upstream: &str) -> String {
    format!(
        "containers: [{{name: main, image: example/{app},
  env: [{{name: UPSTREAM, value: '{upstream}'}}],
  ports: [{{name: http, containerPort: 8080}}]}}]"
    )
}

fn template_yaml(app: &str, upstream: &str) -> String {
    let containers = containers(app, upstream);
    format!("{{metadata: {{labels: {{app: {app}}}}}, spec: {{{containers}}}}}")
}

fn deployment(namespace: &str, app: &str, upstream: &str) -> Arc<Deployment> {
    parse(&format!(
        "metadata: {{namespace: {namespace}, name: {app}, uid: {namespace}-{app}}}
spec: {{replicas: {REPLICAS}, selector: {{matchLabels: {{app: {app}}}}}, template: {}}}
status: {{replicas: {REPLICAS}, readyReplicas: {REPLICAS}, availableReplicas: {REPLICAS}, updatedReplicas: {REPLICAS}}}",
        template_yaml(app, upstream)
    ))
}

fn stateful_set(namespace: &str, app: &str, upstream: &str) -> Arc<StatefulSet> {
    parse(&format!(
        "metadata: {{namespace: {namespace}, name: {app}, uid: {namespace}-{app}}}
spec: {{replicas: {REPLICAS}, serviceName: {app}, selector: {{matchLabels: {{app: {app}}}}}, template: {}}}
status: {{replicas: {REPLICAS}, readyReplicas: {REPLICAS}, availableReplicas: {REPLICAS}, updatedReplicas: {REPLICAS}, currentReplicas: {REPLICAS}}}",
        template_yaml(app, upstream)
    ))
}

fn owner(kind: &str, api: &str, namespace: &str, name: &str) -> String {
    format!(
        "ownerReferences: [{{apiVersion: {api}, kind: {kind}, name: {name}, uid: {namespace}-{name}, controller: true}}]"
    )
}

fn replica_set(namespace: &str, app: &str) -> Arc<kube::api::PartialObjectMeta<ReplicaSet>> {
    let meta: k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta =
        serde_yaml::from_str(&format!(
            "{{namespace: {namespace}, name: {app}-abc, uid: {namespace}-{app}-abc, {}}}",
            owner("Deployment", "apps/v1", namespace, app)
        ))
        .unwrap();
    Arc::new(meta.into_response_partial())
}

/// A running pod of `app`, owned by `owner_ref`, with `labels` beside
/// its app label.  `at` numbers it, for its address and machine.
fn pod(
    namespace: &str,
    app: &str,
    name: &str,
    owner_ref: &str,
    labels: &str,
    at: usize,
) -> Arc<Pod> {
    let containers = containers(app, "http://unused");
    let (a, b, c) = (at / 65536 % 256, at / 256 % 256, at % 256);
    parse(&format!(
        "metadata: {{namespace: {namespace}, name: {name}, labels: {{app: {app}, {labels}}}, {owner_ref}}}
spec: {{nodeName: machine-{}, {containers}}}
status: {{phase: Running, podIPs: [{{ip: 10.{a}.{b}.{c}}}]}}",
        at % MACHINES
    ))
}

fn service(namespace: &str, app: &str) -> Arc<Service> {
    parse(&format!(
        "metadata: {{namespace: {namespace}, name: {app}}}
spec: {{selector: {{app: {app}}}, ports: [{{port: 80, targetPort: http}}], clusterIP: 10.96.0.1}}"
    ))
}

/// A default deny, an allowance for the namespace's own pods, one for
/// a team's namespaces to reach the first app, and an egress rule on
/// the StatefulSets.
fn policies(namespace: &str) -> Vec<Arc<NetworkPolicy>> {
    let policy = |name: &str, spec: &str| -> Arc<NetworkPolicy> {
        parse(&format!(
            "metadata: {{namespace: {namespace}, name: {name}}}\nspec: {spec}"
        ))
    };
    vec![
        policy("deny", "{podSelector: {}, policyTypes: [Ingress]}"),
        policy(
            "same",
            "{podSelector: {}, policyTypes: [Ingress], ingress: [{from: [{podSelector: {}}]}]}",
        ),
        policy(
            "team",
            "{podSelector: {matchLabels: {app: app-0}}, policyTypes: [Ingress],
  ingress: [{from: [{namespaceSelector: {matchLabels: {team: t1}}}], ports: [{port: http}]}]}",
        ),
        policy(
            "out",
            "{podSelector: {matchExpressions: [{key: app, operator: In, values: [db-0, db-1]}]},
  policyTypes: [Egress], egress: [{to: [{namespaceSelector: {}}], ports: [{port: 53, protocol: UDP}]}]}",
        ),
    ]
}

/// The snapshot of the whole synthetic cluster.
fn large_cluster() -> Snapshot {
    let mut snapshot = Snapshot::empty();
    snapshot.namespaces = (0..NAMESPACES).map(namespace).collect();
    snapshot.nodes = (0..MACHINES).map(machine).collect();
    for index in 0..NAMESPACES {
        let ns = format!("ns-{index}");
        let next = format!("ns-{}", (index + 1) % NAMESPACES);
        let upstream = |app: &str| format!("http://{app}.{next}.svc.cluster.local:80");
        for d in 0..DEPLOYMENTS {
            let app = format!("app-{d}");
            snapshot
                .deployments
                .push(deployment(&ns, &app, &upstream(&app)));
            snapshot.replica_sets.push(replica_set(&ns, &app));
            let owner_ref = owner("ReplicaSet", "apps/v1", &ns, &format!("{app}-abc"));
            for r in 0..REPLICAS {
                let name = format!("{app}-abc-{r}");
                let at = snapshot.pods.len();
                let labels = "pod-template-hash: abc";
                snapshot
                    .pods
                    .push(pod(&ns, &app, &name, &owner_ref, labels, at));
            }
            snapshot.services.push(service(&ns, &app));
        }
        for s in 0..STATEFUL_SETS {
            let app = format!("db-{s}");
            snapshot
                .stateful_sets
                .push(stateful_set(&ns, &app, &upstream(&app)));
            let owner_ref = owner("StatefulSet", "apps/v1", &ns, &app);
            for r in 0..REPLICAS {
                let name = format!("{app}-{r}");
                let at = snapshot.pods.len();
                let labels = format!("statefulset.kubernetes.io/pod-name: {name}");
                snapshot
                    .pods
                    .push(pod(&ns, &app, &name, &owner_ref, &labels, at));
            }
            snapshot.services.push(service(&ns, &app));
        }
        snapshot.network_policies.extend(policies(&ns));
    }
    snapshot.ownership = ownership(&snapshot);
    snapshot.addresses = Addresses::build(
        &snapshot.services,
        &snapshot.ingresses,
        &snapshot.nodes,
        transect_network::addresses::DEFAULT_DOMAIN,
    );
    snapshot
}

fn ownership(snapshot: &Snapshot) -> Ownership {
    let entry = |group, kind, meta| Entry { group, kind, meta };
    let pods = snapshot.pods.iter().map(|o| entry("", "Pod", &o.metadata));
    let sets = snapshot.replica_sets.iter();
    let sets = sets.map(|o| entry("apps", "ReplicaSet", &o.metadata));
    let deployments = snapshot.deployments.iter();
    let deployments = deployments.map(|o| entry("apps", "Deployment", &o.metadata));
    let stateful = snapshot.stateful_sets.iter();
    let stateful = stateful.map(|o| entry("apps", "StatefulSet", &o.metadata));
    let services = snapshot
        .services
        .iter()
        .map(|o| entry("", "Service", &o.metadata));
    Ownership::build(
        pods.chain(sets)
            .chain(deployments)
            .chain(stateful)
            .chain(services),
    )
}

#[rstest]
#[ignore = "a timing; run it in release mode"]
fn renders_a_large_cluster() {
    let (layers, usage, shown) = (Layers::default(), usage::Reading::Off, Shown::default());
    let snapshot = large_cluster();
    assert_eq!(snapshot.pods.len(), 5000);
    let started = Instant::now();
    let model = Model::build(snapshot);
    let built = started.elapsed();
    eprintln!("the model: {built:?}");
    let timed = |name: &str, render: &dyn Fn() -> Option<String>| {
        let started = Instant::now();
        let html = render().unwrap();
        eprintln!("{name}: {:?}, {} bytes", started.elapsed(), html.len());
        started.elapsed()
    };
    let cluster = timed("the cluster, ns-0 selected", &|| {
        template(
            cluster_page(
                &model,
                &layers,
                &usage,
                Focus::Select(Some("ns-0")),
                &shown,
                k8s_openapi::jiff::Timestamp::now(),
            ),
            Part::Whole,
        )
    });
    let namespace = timed("a namespace", &|| {
        template(
            namespace_page(
                &model,
                &layers,
                &usage,
                "ns-0",
                &shown,
                k8s_openapi::jiff::Timestamp::now(),
            )?,
            Part::Whole,
        )
    });
    let workload = timed("a workload", &|| {
        let page = workload_page(
            &model,
            &layers,
            &usage,
            super::pages::WorkloadAt {
                namespace: "ns-0",
                resource: "deployments",
                name: "app-0",
            },
            &shown,
            k8s_openapi::jiff::Timestamp::now(),
        );
        template(page?, Part::Whole)
    });
    assert!(built + cluster + namespace + workload < BOUND);
}
