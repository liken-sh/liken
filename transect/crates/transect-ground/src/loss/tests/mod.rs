use std::sync::Arc;

use k8s_openapi::api::apps::v1::{DaemonSet, Deployment, StatefulSet};
use k8s_openapi::api::batch::v1::CronJob;
use k8s_openapi::api::core::v1::Node;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use rstest::{fixture, rstest};
use serde::de::DeserializeOwned;

use super::*;
use crate::devices::Devices;
use crate::storage::Storage;
use crate::storage::fixtures::{claim, local, nfs};
use transect_cluster::ownership::Entry;
use transect_cluster::ownership::fixtures::meta;
use transect_cluster::workloads::{Objects, Revisions};
use transect_graph::edges::Graph;

fn yaml<K: DeserializeOwned>(text: &str) -> Arc<K> {
    Arc::new(serde_yaml::from_str(text).unwrap())
}

/// A ready, running pod in `shop` on `node`, controlled by `owner`,
/// given as its apiVersion, kind, and name.
fn pod(name: &str, owner: (&str, &str, &str), node: &str) -> Arc<Pod> {
    let (api_version, kind, owner) = owner;
    yaml(&format!(
        r#"
metadata:
  namespace: shop
  name: {name}
  ownerReferences: [{{apiVersion: {api_version}, kind: {kind}, name: {owner}, uid: x, controller: true}}]
spec: {{nodeName: {node}, containers: [{{name: app}}]}}
status:
  phase: Running
  conditions: [{{type: Ready, status: 'True'}}]
"#
    ))
}

fn deployment(name: &str, replicas: i32) -> Arc<Deployment> {
    yaml(&format!(
        "{{metadata: {{namespace: shop, name: {name}}}, spec: {{replicas: {replicas}, selector: {{}}, template: {{}}}}}}"
    ))
}

fn stateful_set(name: &str, replicas: i32) -> Arc<StatefulSet> {
    yaml(&format!(
        "{{metadata: {{namespace: shop, name: {name}}}, spec: {{replicas: {replicas}, selector: {{}}, template: {{}}}}}}"
    ))
}

fn daemon_set(name: &str) -> Arc<DaemonSet> {
    yaml(&format!(
        "{{metadata: {{namespace: shop, name: {name}}}, spec: {{selector: {{}}, template: {{}}}}}}"
    ))
}

fn cron_job(name: &str) -> Arc<CronJob> {
    yaml(&format!(
        "{{metadata: {{namespace: shop, name: {name}}}, spec: {{schedule: '@daily', jobTemplate: {{spec: {{template: {{}}}}}}}}}}"
    ))
}

const REPLICA_SET: &str = "apps/v1";

/// A pod of the `Deployment` `deployment`, through its `ReplicaSet`.
fn deployment_pod(deployment: &str, suffix: &str, node: &str) -> Arc<Pod> {
    let replica_set = format!("{deployment}-5d8f");
    let name = format!("{replica_set}-{suffix}");
    pod(&name, (REPLICA_SET, "ReplicaSet", &replica_set), node)
}

/// The cluster that these tests lose machines from, across alpha, beta,
/// and gamma: a `Deployment` `web` of three, two on alpha; `api` of
/// two, one each on alpha and beta; `docs` of one on gamma, on an NFS
/// share; a `StatefulSet` `db` of one on alpha, on alpha's own disk; a
/// `DaemonSet` `agent` on every machine; and a `CronJob`'s pod running
/// on alpha.
struct Shop {
    deployments: Vec<Arc<Deployment>>,
    stateful_sets: Vec<Arc<StatefulSet>>,
    daemon_sets: Vec<Arc<DaemonSet>>,
    cron_jobs: Vec<Arc<CronJob>>,
    pods: Vec<Arc<Pod>>,
    /// The ReplicaSets and the `Job` between the controllers
    /// and their pods, as the ownership index reads them.
    between: Vec<(&'static str, &'static str, ObjectMeta)>,
    storage: Storage,
    nodes: Vec<Arc<Node>>,
    /// For each workload whose kind knowledge names a primary, the name
    /// of the pod that accepts writes.
    primaries: HashMap<Object, String>,
}

/// `pod`, mounting the claim `claim`.
fn mounting(pod: Arc<Pod>, claim: &str) -> Arc<Pod> {
    let mut pod = Arc::unwrap_or_clone(pod);
    let volume = serde_yaml::from_str(&format!(
        "{{name: data, persistentVolumeClaim: {{claimName: {claim}}}}}"
    ))
    .unwrap();
    pod.spec.as_mut().unwrap().volumes = Some(vec![volume]);
    Arc::new(pod)
}

#[fixture]
fn shop() -> Shop {
    let rs = |deployment: &str| {
        let name = format!("{deployment}-5d8f");
        let owner = [("apps/v1", "Deployment", deployment)];
        ("apps", "ReplicaSet", meta(Some("shop"), &name, &owner))
    };
    let job_owner = [("batch/v1", "CronJob", "report")];
    let job = ("batch", "Job", meta(Some("shop"), "report-1", &job_owner));
    Shop {
        deployments: vec![
            deployment("web", 3),
            deployment("api", 2),
            deployment("docs", 1),
        ],
        stateful_sets: vec![stateful_set("db", 1)],
        daemon_sets: vec![daemon_set("agent")],
        cron_jobs: vec![cron_job("report")],
        pods: vec![
            deployment_pod("web", "a", "alpha"),
            deployment_pod("web", "b", "alpha"),
            deployment_pod("web", "c", "beta"),
            deployment_pod("api", "a", "alpha"),
            deployment_pod("api", "b", "beta"),
            mounting(deployment_pod("docs", "a", "gamma"), "docs"),
            mounting(pod("db-0", ("apps/v1", "StatefulSet", "db"), "alpha"), "db"),
            pod("agent-a", ("apps/v1", "DaemonSet", "agent"), "alpha"),
            pod("agent-b", ("apps/v1", "DaemonSet", "agent"), "beta"),
            pod("agent-c", ("apps/v1", "DaemonSet", "agent"), "gamma"),
            pod("report-1-x", ("batch/v1", "Job", "report-1"), "alpha"),
        ],
        between: vec![rs("web"), rs("api"), rs("docs"), job],
        storage: Storage::build(
            &[
                claim("shop", "db", Some("pv-db")),
                claim("shop", "docs", Some("pv-docs")),
            ],
            &[local("pv-db", "alpha"), nfs("pv-docs", "nas.example.com")],
            &[],
        ),
        nodes: vec![
            node("alpha", "True"),
            node("beta", "True"),
            node("gamma", "True"),
        ],
        primaries: HashMap::new(),
    }
}

/// A machine whose `Ready` condition is `ready`.
fn node(name: &str, ready: &str) -> Arc<Node> {
    yaml(&format!(
        "{{metadata: {{name: {name}}}, status: {{conditions: [{{type: Ready, status: '{ready}'}}]}}}}"
    ))
}

impl Shop {
    fn objects(&self) -> Objects<'_> {
        Objects {
            deployments: &self.deployments,
            stateful_sets: &self.stateful_sets,
            daemon_sets: &self.daemon_sets,
            cron_jobs: &self.cron_jobs,
            pods: &self.pods,
        }
    }

    fn ownership(&self) -> Ownership {
        let entry = |group, kind, meta| Entry { group, kind, meta };
        let entries = [
            self.deployments
                .iter()
                .map(|d| entry("apps", "Deployment", &d.metadata))
                .collect::<Vec<_>>(),
            self.stateful_sets
                .iter()
                .map(|s| entry("apps", "StatefulSet", &s.metadata))
                .collect(),
            self.daemon_sets
                .iter()
                .map(|d| entry("apps", "DaemonSet", &d.metadata))
                .collect(),
            self.cron_jobs
                .iter()
                .map(|c| entry("batch", "CronJob", &c.metadata))
                .collect(),
            self.between
                .iter()
                .map(|(group, kind, meta)| entry(group, kind, meta))
                .collect(),
            self.pods
                .iter()
                .map(|p| entry("", "Pod", &p.metadata))
                .collect(),
        ];
        Ownership::build(entries.into_iter().flatten())
    }

    /// The loss of `machines`.
    fn loss(&self, machines: &[&str]) -> Loss {
        let ownership = self.ownership();
        let workloads = Workloads::build(&self.objects(), &Revisions::default(), &ownership);
        let known = ["alpha", "beta", "gamma"];
        let lost = Lost::of(machines.iter().copied(), known);
        let devices = Devices::default();
        let reading = Reading {
            pods: &self.pods,
            workloads: &workloads,
            ownership: &ownership,
            stranding: Stranding {
                storage: &self.storage,
                devices: &devices,
                nodes: &self.nodes,
            },
            edges: cascade::Edges {
                graph: &Graph::default(),
                ingresses: &[],
                intents: &transect_resources::intent::Intents::default(),
            },
            primaries: &self.primaries,
            vital: &HashMap::new(),
        };
        assess(lost, &reading)
    }

    /// The loss of `machines`, as the number of pods lost, each
    /// workload's name and state, and the number of `DaemonSet`s.
    fn lose(&self, machines: &[&str]) -> (usize, Vec<(String, State)>, usize) {
        let loss = self.loss(machines);
        let taken = loss.workloads.into_iter();
        let taken = taken
            .map(|taken| (taken.object.name, taken.state))
            .collect();
        (loss.pods, taken, loss.daemon_sets)
    }
}

fn down(name: &str, wanted: i32) -> (String, State) {
    (name.into(), State::Down { wanted })
}

fn short(name: &str, ready: i32, wanted: i32) -> (String, State) {
    (name.into(), State::Short { ready, wanted })
}

#[rstest]
#[case::some_of_a_deployment_and_a_stateful_set_of_one(
    &["alpha"],
    6,
    vec![short("api", 1, 2), down("db", 1), short("web", 1, 3)],
)]
#[case::every_pod_of_a_deployment(
    &["alpha", "beta"],
    9,
    vec![down("api", 2), down("db", 1), down("web", 3)],
)]
#[case::a_deployment_of_one_and_none_of_the_others(&["gamma"], 2, vec![down("docs", 1)])]
#[case::a_machine_the_cluster_does_not_have(&["delta"], 0, vec![])]
fn loses_each_workload_by_its_ready_pods_elsewhere(
    shop: Shop,
    #[case] machines: &[&str],
    #[case] pods: usize,
    #[case] taken: Vec<(String, State)>,
) {
    let (counted, found, _) = shop.lose(machines);
    assert_eq!((counted, found), (pods, taken));
}

#[rstest]
#[case::the_primary(2, &["alpha"], Some(State::Primary { ready: 1, wanted: 2 }))]
#[case::a_replica(2, &["beta"], Some(State::Short { ready: 1, wanted: 2 }))]
#[case::the_primary_with_enough_replicas_left(
    1,
    &["alpha"],
    Some(State::Primary { ready: 1, wanted: 1 })
)]
#[case::a_spare_replica(1, &["beta"], None)]
fn losing_the_primary_stops_writes_and_a_replica_only_leaves_it_short(
    mut shop: Shop,
    #[case] replicas: i32,
    #[case] machines: &[&str],
    #[case] state: Option<State>,
) {
    shop.deployments[1] = deployment("api", replicas);
    let api = Object::new("apps", "Deployment", Some("shop"), "api");
    shop.primaries.insert(api, "api-5d8f-a".into());
    let (_, taken, _) = shop.lose(machines);
    let found = taken.into_iter().find(|(found, _)| found == "api");
    assert_eq!(found.map(|(_, state)| state), state);
}

#[rstest]
#[case::on_the_lost_machines_disk(&["alpha"], vec![("db".into(), true)])]
#[case::on_an_nfs_share(&["gamma"], vec![("docs".into(), false)])]
fn a_down_workload_is_stranded_by_what_its_pods_need(
    shop: Shop,
    #[case] machines: &[&str],
    #[case] stranded: Vec<(String, bool)>,
) {
    let loss = shop.loss(machines);
    let down = loss
        .workloads
        .into_iter()
        .filter(|taken| matches!(taken.state, State::Down { .. }));
    let found: Vec<(String, bool)> = down
        .map(|taken| (taken.object.name, taken.stranded.is_some()))
        .collect();
    assert_eq!(found, stranded);
}

const MOVES: Option<Stranded> = None;

fn on_disk() -> Option<Stranded> {
    Some(Stranded::Disk {
        machine: "alpha".into(),
    })
}

#[rstest]
#[case::every_pod_stranded(vec![on_disk(), on_disk()], on_disk())]
#[case::one_pod_can_move(vec![on_disk(), MOVES], None)]
#[case::no_pods(vec![], None)]
fn a_workload_is_stranded_when_every_pod_there_is(
    #[case] reasons: Vec<Option<Stranded>>,
    #[case] stranded: Option<Stranded>,
) {
    assert_eq!(super::stranded(reasons.into_iter()), stranded);
}

#[rstest]
#[case::one_machine(&["alpha"], 1)]
#[case::two_machines(&["alpha", "beta"], 1)]
#[case::none(&["delta"], 0)]
fn a_daemon_set_counts_once_and_is_never_short(
    shop: Shop,
    #[case] machines: &[&str],
    #[case] daemon_sets: usize,
) {
    assert_eq!(shop.lose(machines).2, daemon_sets);
}

#[rstest]
#[case::wants_nothing(0, &["alpha"], false, None)]
#[case::every_pod_elsewhere(1, &["beta"], false, None)]
#[case::its_only_pod_there(1, &["alpha"], false, Some(State::Down { wanted: 1 }))]
#[case::one_of_two_there(2, &["alpha", "beta"], false, Some(State::Short { ready: 1, wanted: 2 }))]
#[case::the_primary_of_two_there(2, &["alpha", "beta"], true, Some(State::Primary { ready: 1, wanted: 2 }))]
#[case::the_primary_with_a_spare_elsewhere(
    1,
    &["alpha", "beta"],
    true,
    Some(State::Primary { ready: 1, wanted: 1 })
)]
#[case::the_primary_and_every_replica(2, &["alpha", "alpha"], true, Some(State::Down { wanted: 2 }))]
fn the_rule(
    #[case] wanted: i32,
    #[case] nodes: &[&str],
    #[case] primary_lost: bool,
    #[case] state: Option<State>,
) {
    let pods: Vec<Arc<Pod>> = nodes
        .iter()
        .enumerate()
        .map(|(index, node)| deployment_pod("web", &index.to_string(), node))
        .collect();
    let running: Vec<&Pod> = pods.iter().map(AsRef::as_ref).collect();
    let on_alpha = |pod: &Pod| pod.spec.as_ref().unwrap().node_name.as_deref() == Some("alpha");
    assert_eq!(rule::state(wanted, &running, on_alpha, primary_lost), state);
}

#[rstest]
#[case::supposed("True", false)]
#[case::really("Unknown", true)]
fn says_what_happens_next(mut shop: Shop, #[case] ready: &str, #[case] really_lost: bool) {
    shop.nodes[0] = node("alpha", ready);
    let next = shop.loss(&["alpha"]).next;
    let stuck: Vec<(usize, Vec<String>)> = next
        .stuck
        .iter()
        .map(|(reason, names)| {
            (
                reason.rank(),
                names.iter().map(|n| n.shown.clone()).collect(),
            )
        })
        .collect();
    assert_eq!(
        (next.usual, stuck, next.really_lost),
        (
            Some(crate::placement::Eviction::After(0)),
            vec![(0, vec!["db".to_string()])],
            really_lost
        )
    );
}

mod view;
