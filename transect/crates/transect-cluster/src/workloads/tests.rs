use std::sync::Arc;

use k8s_openapi::api::apps::v1::{Deployment, StatefulSet};
use k8s_openapi::api::batch::v1::CronJob;
use rstest::rstest;
use serde::de::DeserializeOwned;

use super::*;
use crate::ownership::Entry;

fn yaml<K: DeserializeOwned>(text: &str) -> Arc<K> {
    Arc::new(serde_yaml::from_str(text).unwrap())
}

/// A pod in `shop`, ready or not, controlled by `owner`, given as its
/// `apiVersion`, kind, and name.
fn pod(name: &str, owner: (&str, &str, &str), ready: bool, phase: &str) -> Arc<Pod> {
    let (api_version, kind, owner) = owner;
    yaml(&format!(
        r#"
metadata:
  namespace: shop
  name: {name}
  ownerReferences: [{{apiVersion: {api_version}, kind: {kind}, name: {owner}, uid: x, controller: true}}]
spec:
  containers: [{{name: app, image: example/{owner}:1}}]
status:
  phase: {phase}
  conditions: [{{type: Ready, status: '{}'}}]
"#,
        if ready { "True" } else { "False" }
    ))
}

fn deployment(name: &str, replicas: i32, ready: i32, image: &str) -> Arc<Deployment> {
    yaml(&format!(
        r#"
metadata: {{namespace: shop, name: {name}}}
spec:
  replicas: {replicas}
  selector: {{}}
  template: {{spec: {{containers: [{{name: app, image: {image}}}, {{name: proxy, image: example/proxy:2}}]}}}}
status: {{readyReplicas: {ready}}}
"#
    ))
}

fn stateful_set(name: &str, owner: &str, replicas: i32) -> Arc<StatefulSet> {
    yaml(&format!(
        r#"
metadata:
  namespace: shop
  name: {name}
  ownerReferences: [{{apiVersion: cache.example.io/v1, kind: Cache, name: {owner}, uid: x, controller: true}}]
spec: {{replicas: {replicas}, selector: {{}}, template: {{}}}}
status: {{readyReplicas: {replicas}}}
"#
    ))
}

fn cron_job(name: &str) -> Arc<CronJob> {
    yaml(&format!(
        "{{metadata: {{namespace: shop, name: {name}}}, spec: {{schedule: '@daily', jobTemplate: {{spec: {{template: {{}}}}}}}}}}"
    ))
}

/// The cluster these tests read: a `Deployment` `web` of three, a cache
/// run by an operator through a `StatefulSet` of two, a database whose
/// operator runs two pods itself, and a `CronJob`.
struct Shop {
    deployments: Vec<Arc<Deployment>>,
    stateful_sets: Vec<Arc<StatefulSet>>,
    cron_jobs: Vec<Arc<CronJob>>,
    pods: Vec<Arc<Pod>>,
}

impl Shop {
    fn new() -> Self {
        let database = ("db.example.io/v1", "Database", "orders");
        Self {
            deployments: vec![deployment("web", 3, 2, "example/web:1")],
            stateful_sets: vec![stateful_set("sessions", "sessions", 2)],
            cron_jobs: vec![cron_job("report")],
            pods: vec![
                pod("orders-1", database, true, "Running"),
                pod("orders-2", database, false, "Running"),
                pod("orders-0", database, false, "Failed"),
            ],
        }
    }

    fn objects(&self) -> Objects<'_> {
        Objects {
            deployments: &self.deployments,
            stateful_sets: &self.stateful_sets,
            daemon_sets: &[],
            cron_jobs: &self.cron_jobs,
            pods: &self.pods,
        }
    }

    fn ownership(&self) -> Ownership {
        let entries = (self.stateful_sets.iter().map(|s| Entry {
            group: "apps",
            kind: "StatefulSet",
            meta: &s.metadata,
        }))
        .chain(self.pods.iter().map(|p| Entry {
            group: "",
            kind: "Pod",
            meta: &p.metadata,
        }));
        Ownership::build(entries)
    }
}

/// What a test reads of one workload.
#[derive(Debug, PartialEq)]
struct Read {
    replicas: Option<i32>,
    ready: Option<(i32, i32)>,
    images: Vec<String>,
    scheduled: bool,
}

#[rstest]
#[case::a_deployment(
    Object::new("apps", "Deployment", Some("shop"), "web"),
    Read { replicas: Some(3), ready: Some((2, 3)), images: vec!["example/web:1".into(), "example/proxy:2".into()], scheduled: false },
)]
#[case::a_custom_kind_through_its_stateful_set(
    Object::new("cache.example.io", "Cache", Some("shop"), "sessions"),
    Read { replicas: Some(2), ready: Some((2, 2)), images: vec![], scheduled: false },
)]
#[case::a_custom_kind_that_runs_its_own_pods(
    Object::new("db.example.io", "Database", Some("shop"), "orders"),
    Read { replicas: Some(2), ready: Some((1, 2)), images: vec!["example/orders:1".into()], scheduled: false },
)]
#[case::a_cron_job(
    Object::new("batch", "CronJob", Some("shop"), "report"),
    Read { replicas: None, ready: None, images: vec![], scheduled: true },
)]
fn reads_a_workload_through_its_controllers_or_its_pods(
    #[case] object: Object,
    #[case] read: Read,
) {
    let shop = Shop::new();
    let ownership = shop.ownership();
    let workloads = Workloads::build(&shop.objects(), &Revisions::default(), &ownership);
    let workload = workloads.get(&object);
    assert_eq!(
        Read {
            replicas: workload.replicas(),
            ready: workload.ready(),
            images: workload.images(),
            scheduled: workload.scheduled(),
        },
        read
    );
}
