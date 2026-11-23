//! Tests that build the boxes from a namespace with workloads, pods,
//! and Services.

use k8s_openapi::api::apps::v1::Deployment;
use k8s_openapi::api::batch::v1::CronJob;
use rstest::rstest;
use serde::de::DeserializeOwned;

use super::super::line::{LastRun, Spread};
use super::*;
use transect_observe::health::{Kind, Reason, Workload};

fn yaml<K: DeserializeOwned>(text: &str) -> K {
    serde_yaml_ng::from_str(text).unwrap()
}

fn deployment(name: &str, replicas: i32, template: &str) -> Arc<Deployment> {
    Arc::new(yaml(&format!(
        "metadata: {{namespace: shop, name: {name}}}\nspec: {{replicas: {replicas}, selector: {{}}, template: {{spec: {template}}}}}"
    )))
}

fn cron_job(name: &str) -> Arc<CronJob> {
    Arc::new(yaml(&format!(
        "metadata: {{namespace: shop, name: {name}}}\nspec: {{schedule: '@daily', jobTemplate: {{}}}}\nstatus: {{lastScheduleTime: '2026-01-01T00:00:00Z', lastSuccessfulTime: '2026-01-01T00:01:00Z'}}"
    )))
}

/// A pod of the app labelled `app`, on `node`, in `phase`, whose
/// `volumes` are the YAML `volumes`.
fn pod(app: &str, node: &str, phase: &str, volumes: &str) -> Pod {
    yaml(&format!(
        "metadata: {{namespace: shop, name: {app}-1, labels: {{app: {app}}}}}\nspec: {{nodeName: {node}, containers: [], volumes: {volumes}}}\nstatus: {{phase: {phase}}}"
    ))
}

fn service(app: &str) -> Arc<Service> {
    Arc::new(yaml(&format!(
        "metadata: {{namespace: shop, name: {app}}}\nspec: {{selector: {{app: {app}}}}}"
    )))
}

fn workload(group: &str, kind: &str, name: &str) -> Object {
    Object::new(group, kind, Some("shop"), name)
}

struct Namespace {
    workloads: Vec<Object>,
    pods: Vec<(Object, Pod)>,
    deployments: Vec<Arc<Deployment>>,
    cron_jobs: Vec<Arc<CronJob>>,
    services: Vec<Arc<Service>>,
    by_workload: HashMap<Object, Health>,
}

impl Namespace {
    fn new() -> Self {
        Self {
            workloads: Vec::new(),
            pods: Vec::new(),
            deployments: Vec::new(),
            cron_jobs: Vec::new(),
            services: Vec::new(),
            by_workload: HashMap::new(),
        }
    }

    /// A `Deployment` named `name`, with one pod on each node in `nodes`.
    fn deployment(mut self, name: &str, nodes: &[&str], template: &str, volumes: &str) -> Self {
        let owner = workload("apps", "Deployment", name);
        for node in nodes {
            self.pods
                .push((owner.clone(), pod(name, node, "Running", volumes)));
        }
        self.deployments
            .push(deployment(name, nodes.len() as i32, template));
        self.workloads.push(owner);
        self
    }

    fn helper(self, name: &str, node: &str) -> Self {
        self.deployment(name, &[node], "{containers: []}", "[]")
    }

    fn custom(mut self, name: &str, nodes: &[&str]) -> Self {
        let owner = workload("cache.example.io", "Cache", name);
        for node in nodes {
            self.pods
                .push((owner.clone(), pod(name, node, "Running", "[]")));
        }
        self.workloads.push(owner);
        self
    }

    fn cron_job(mut self, name: &str, node: &str) -> Self {
        let owner = workload("batch", "CronJob", name);
        self.pods
            .push((owner.clone(), pod(name, node, "Succeeded", "[]")));
        self.cron_jobs.push(cron_job(name));
        self.workloads.push(owner);
        self
    }

    fn service(mut self, app: &str) -> Self {
        self.services.push(service(app));
        self
    }

    fn failing(mut self, owner: Object, reason: Reason) -> Self {
        self.by_workload
            .insert(owner, Health::from_reasons(vec![reason]));
        self
    }

    fn boxes(&self) -> Vec<WorkloadBox> {
        let mut pods: HashMap<Object, Vec<&Pod>> = HashMap::new();
        for (owner, pod) in &self.pods {
            pods.entry(owner.clone()).or_default().push(pod);
        }
        let objects = Objects {
            deployments: &self.deployments,
            stateful_sets: &[],
            daemon_sets: &[],
            cron_jobs: &self.cron_jobs,
            pods: &[],
        };
        let workloads: Vec<&Object> = self.workloads.iter().collect();
        boxes(
            &workloads,
            &pods,
            &objects,
            &self.services,
            &self.by_workload,
            &Plurals::from([(("cache.example.io".into(), "Cache".into()), "caches".into())]),
            &HashMap::new(),
        )
    }
}

fn titles(boxes: &[WorkloadBox]) -> Vec<String> {
    boxes
        .iter()
        .map(|one| match &one.title {
            Title::Workload { kind, name } => format!("{kind} {name}"),
            Title::Helpers(names) => format!("helpers {}", names.join(",")),
        })
        .collect()
}

const SPREAD: &str = "{containers: [], affinity: {podAntiAffinity: {requiredDuringSchedulingIgnoredDuringExecution: [{topologyKey: kubernetes.io/hostname}]}}}";

#[rstest]
fn busiest_first_then_helpers_then_scheduled() {
    let namespace = Namespace::new()
        .cron_job("backups", "alpha")
        .helper("relay", "beta")
        .deployment("web", &["alpha", "beta", "gamma"], SPREAD, "[]")
        .helper("mcp", "alpha")
        .custom("sessions", &["alpha", "beta"]);
    assert_eq!(
        titles(&namespace.boxes()),
        vec![
            "Deployment web",
            "Cache sessions",
            "helpers mcp,relay",
            "CronJob backups",
        ]
    );
}

#[rstest]
#[case::a_volume(
    Namespace::new().helper("relay", "beta").deployment("mcp", &["alpha"], "{containers: []}", "[{name: data, persistentVolumeClaim: {claimName: data}}]").helper("exporter", "alpha"),
    vec!["Deployment mcp", "helpers exporter,relay"],
)]
#[case::a_service(
    Namespace::new().helper("relay", "beta").helper("mcp", "alpha").helper("exporter", "alpha").service("mcp"),
    vec!["Deployment mcp", "helpers exporter,relay"],
)]
#[case::one_helper_alone(
    Namespace::new().helper("relay", "beta"),
    vec!["Deployment relay"],
)]
#[case::a_custom_kind_with_one_pod(
    Namespace::new().helper("relay", "beta").custom("sessions", &["alpha"]).helper("mcp", "alpha"),
    vec!["Cache sessions", "helpers mcp,relay"],
)]
fn only_bare_single_pod_workloads_are_helpers(
    #[case] namespace: Namespace,
    #[case] expected: Vec<&str>,
) {
    assert_eq!(titles(&namespace.boxes()), expected);
}

#[rstest]
fn each_box_reads_its_line_and_machines() {
    let namespace = Namespace::new()
        .deployment("web", &["gamma", "alpha", "alpha"], SPREAD, "[]")
        .custom("sessions", &["beta"])
        .cron_job("backups", "alpha");
    let found: Vec<(Line, Vec<String>, bool)> = namespace
        .boxes()
        .into_iter()
        .map(|one| (one.line, one.machines, one.scheduled))
        .collect();
    let names = |names: &[&str]| names.iter().map(|&name| name.to_string()).collect();
    assert_eq!(
        found,
        vec![
            (
                Line::Replicas {
                    replicas: 3,
                    spread: Some(Spread::AntiAffinity)
                },
                names(&["alpha", "gamma"]),
                false,
            ),
            (Line::Pods(1), names(&["beta"]), false),
            (Line::LastRun(LastRun::Succeeded), names(&["alpha"]), true),
        ]
    );
}

#[rstest]
fn the_helpers_box_takes_its_worst_helper() {
    let reason = Reason::NoReadyReplicas {
        workload: Workload {
            kind: Kind::Deployment,
            name: "relay".into(),
        },
        desired: 1,
    };
    let namespace = Namespace::new()
        .helper("relay", "beta")
        .helper("mcp", "alpha")
        .failing(workload("apps", "Deployment", "relay"), reason);
    let boxes = namespace.boxes();
    assert_eq!(boxes[0].health.level, Level::Failing);
    assert_eq!(
        boxes[0].machines,
        vec!["alpha".to_string(), "beta".to_string()]
    );
}

#[rstest]
fn each_box_links_to_its_workloads_page_but_the_helpers_box() {
    let namespace = Namespace::new()
        .deployment("web", &["alpha", "beta"], SPREAD, "[]")
        .custom("sessions", &["beta"])
        .helper("relay", "beta")
        .helper("mcp", "alpha");
    let links: Vec<Option<String>> = namespace.boxes().into_iter().map(|one| one.href).collect();
    assert_eq!(
        links,
        vec![
            Some("/namespaces/shop/deployments/web".into()),
            Some("/namespaces/shop/caches.cache.example.io/sessions".into()),
            None,
        ]
    );
}
