//! Small clusters for the ways-in tests.
//!
//! A Service selects the Deployment its `app` selector names, a pod is
//! part of the Deployment its `app` label names, and an Ingress of the
//! class `unserved` has no controller.  Every other Ingress enters
//! through the Deployment `edge/controller`.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;

use k8s_openapi::api::core::v1::{Node, Pod, Service};
use k8s_openapi::api::networking::v1::Ingress;
use kube::ResourceExt;
use serde::Deserialize;

use super::*;
use transect_graph::edges::Provenance;
use transect_network::policy::Admission;
use transect_resources::intent::Line;

/// The objects of one small cluster.
#[derive(Default)]
pub struct Cluster {
    ingresses: Vec<Arc<Ingress>>,
    services: Vec<Arc<Service>>,
    pods: Vec<Arc<Pod>>,
    nodes: Vec<Arc<Node>>,
    /// The workloads, by name, that have a sentence that states they
    /// sign their callers in themselves.
    declared: BTreeSet<String>,
    /// Whether NetworkPolicy isolates each workload, by name.  A
    /// workload not in the map is open.
    isolation: BTreeMap<String, Isolation>,
    /// The rules that admit callers to each isolated workload, by name.
    admissions: BTreeMap<String, Vec<Admission>>,
}

fn typed<K: serde::de::DeserializeOwned>(value: &serde_yaml::Value) -> Arc<K> {
    Arc::new(serde_yaml::from_value(value.clone()).unwrap())
}

pub fn deployment(namespace: &str, name: &str) -> Object {
    Object::new("apps", "Deployment", Some(namespace), name)
}

impl Cluster {
    pub fn of(text: &str) -> Self {
        let mut cluster = Self::default();
        for document in serde_yaml::Deserializer::from_str(text) {
            let value = serde_yaml::Value::deserialize(document).unwrap();
            match value["kind"].as_str().unwrap_or_default() {
                "Ingress" => cluster.ingresses.push(typed(&value)),
                "Service" => cluster.services.push(typed(&value)),
                "Pod" => cluster.pods.push(typed(&value)),
                "Node" => cluster.nodes.push(typed(&value)),
                other => panic!("no {other} in a ways cluster"),
            }
        }
        cluster
    }

    pub fn declaring(mut self, workloads: &[&str]) -> Self {
        self.declared = workloads.iter().map(|w| w.to_string()).collect();
        self
    }

    /// The cluster with `workload` isolated for ingress, and `admitted`
    /// the rules that admit callers to it.
    pub fn isolated(mut self, workload: &str, admitted: Vec<Admission>) -> Self {
        self.isolation.insert(workload.into(), Isolation::Isolated);
        self.admissions.insert(workload.into(), admitted);
        self
    }

    pub fn ways(&self) -> Ways {
        let selected = |service: &Object| {
            let found = self
                .services
                .iter()
                .find(|s| s.namespace() == service.namespace && s.name_any() == service.name);
            let selector = found.and_then(|s| s.spec.as_ref()?.selector.clone());
            let app = selector.and_then(|s| s.get("app").cloned());
            let namespace = service.namespace.as_deref().unwrap_or_default();
            app.map(|app| vec![deployment(namespace, &app)])
                .unwrap_or_default()
        };
        let workload_of = |pod: &Pod| {
            let app = pod.labels().get("app")?.clone();
            Some(deployment(&pod.namespace()?, &app))
        };
        let isolation = |workload: &Object| {
            Some(
                *self
                    .isolation
                    .get(&workload.name)
                    .unwrap_or(&Isolation::Open),
            )
        };
        let admissions = |workload: &Object| {
            let found = self.admissions.get(&workload.name);
            found.cloned().unwrap_or_default()
        };
        let none = |_: &Object| Vec::new();
        let controller = |ingress: &Ingress| {
            let class = ingress
                .spec
                .as_ref()
                .and_then(|s| s.ingress_class_name.clone());
            Controller {
                workload: (class.as_deref() != Some("unserved"))
                    .then(|| deployment("edge", "controller")),
                entry_points: None,
                declared_by: None,
            }
        };
        let declares = |object: &Object| {
            self.declared.contains(&object.name).then(|| Line {
                text: format!("{} signs its callers in itself", object.name),
                provenance: Provenance::Kubernetes,
            })
        };
        let signs_in = BTreeSet::new();
        let sources = Sources {
            ingresses: &self.ingresses,
            services: &self.services,
            pods: &self.pods,
            daemon_sets: &[],
            nodes: &self.nodes,
            load_balancers: &[],
            workload_of: &workload_of,
            selected: &selected,
            isolation: &isolation,
            admissions: &admissions,
            sign_in: &none,
            controller: &controller,
            signs_in: &signs_in,
            declares: &declares,
        };
        Ways::read(&sources)
    }
}

/// Each row in a line: its name, its ports, and the workloads it
/// reaches.
pub fn rows(ways: &Ways) -> Vec<String> {
    let mut rows = ways.rows.clone();
    rows.sort_by(|a, b| a.order(b));
    rows.iter()
        .map(|way| {
            let reaches: Vec<&str> = way.reaches.iter().map(|o| o.name.as_str()).collect();
            format!(
                "{} {} -> {}",
                way.name,
                way.ports.join(","),
                reaches.join(",")
            )
        })
        .collect()
}

/// Each row's name with the state of one of its gates.
pub fn gates(ways: &Ways, gate: fn(&Way) -> &Gate) -> Vec<(String, State)> {
    let mut rows = ways.rows.clone();
    rows.sort_by(|a, b| a.order(b));
    rows.iter()
        .map(|way| (way.name.clone(), gate(way).state))
        .collect()
}
