//! The rows of a quick search. The rows for what the query matches as an
//! address come first, then the matches in the search index. Each row
//! shows the health of the object it opens.

use std::collections::{HashMap, HashSet};

use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;

use super::super::model::Model;
use transect_graph::edges::Object;
use transect_map::search::{Entry, MOST, Results, Row};
use transect_observe::health::{self, Health, Level};

/// The rows `query` finds in `model`.
pub fn search_results(model: &Model, query: &str, list: &'static str) -> Results {
    let addressed = model.addressed(query);
    let found = model.search.find(query);
    let mut seen = HashSet::new();
    let entries: Vec<&Entry> = addressed
        .iter()
        .chain(found)
        .filter(|entry| seen.insert((entry.href.clone(), entry.name.clone())))
        .take(MOST)
        .collect();
    let levels = levels(model, &entries);
    let rows = entries.into_iter().map(|entry| {
        let level = entry.object.as_ref().and_then(|object| levels.get(object));
        Row::of(entry, level.copied())
    });
    Results {
        query: query.trim().into(),
        rows: rows.collect(),
        list,
    }
}

/// The health level of each namespace, workload, pod, and machine in
/// `entries`, as the map assesses it for the current minute. A
/// namespace or workload with nothing wrong is healthy. A pod's level
/// comes from `health::pod_reasons`. A machine is failing while its
/// `Node` is not `Ready`. Other kinds have no level and are left out.
fn levels(model: &Model, entries: &[&Entry]) -> HashMap<Object, Level> {
    let now = Timestamp::now();
    let minute = model.minute(now);
    let health = &minute.health;
    let snapshot = &model.snapshot;
    let objects: Vec<&Object> = entries.iter().filter_map(|e| e.object.as_ref()).collect();
    let mut found = HashMap::new();
    let namespaces: HashSet<&str> = objects
        .iter()
        .filter_map(|object| match object.kind.as_str() {
            "Namespace" => Some(object.name.as_str()),
            _ => object.namespace.as_deref(),
        })
        .collect();
    for namespace in namespaces {
        let Some(of_namespace) = health.get(namespace) else {
            continue;
        };
        let object = Object::new("", "Namespace", None, namespace);
        found.insert(object, of_namespace.level);
        let by_workload = health::by_workload(of_namespace, namespace, &snapshot.ownership);
        found.extend(by_workload.into_iter().map(|(o, h)| (o, h.level)));
    }
    let workloads: HashSet<&Object> = snapshot.ownership.all_workloads().collect();
    let level = |object: &Object| match (object.group.as_str(), object.kind.as_str()) {
        ("", "Namespace") => Some(found.get(object).copied().unwrap_or_default()),
        ("", "Pod") => snapshot
            .pods
            .iter()
            .find(|pod| pod.namespace() == object.namespace && pod.name_any() == object.name)
            .map(|pod| Health::from_reasons(health::pod_reasons(pod, now)).level),
        ("", "Node") => snapshot
            .nodes
            .iter()
            .find(|node| node.name_any() == object.name)
            .map(|node| match transect_ground::machines::ready(node) {
                true => Level::Healthy,
                false => Level::Failing,
            }),
        _ if workloads.contains(object) => Some(found.get(object).copied().unwrap_or_default()),
        _ => None,
    };
    objects
        .into_iter()
        .filter_map(|object| Some((object.clone(), level(object)?)))
        .collect()
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use rstest::rstest;

    use super::*;
    use crate::web::fixture;

    /// The fixture cluster with an extra pod whose only container is in
    /// `CrashLoopBackOff`, and with its first machine not `Ready`.
    fn model() -> Model {
        let mut snapshot = fixture::snapshot();
        snapshot.pods.push(Arc::new(
            serde_yaml::from_str(
                "metadata: {namespace: acme-ops, name: crashing-pod, uid: crashing-pod}
spec: {containers: [{name: main}]}
status:
  phase: Running
  containerStatuses: [{name: main, ready: false, restartCount: 9, image: x, imageID: '', state: {waiting: {reason: CrashLoopBackOff}}}]",
            )
            .unwrap(),
        ));
        snapshot.nodes[0] = Arc::new(
            serde_yaml::from_str(
                "metadata: {name: node-alpha}
status: {conditions: [{type: Ready, status: 'False'}]}",
            )
            .unwrap(),
        );
        Model::build(snapshot)
    }

    #[rstest]
    #[case::a_healthy_workload("storefront", Some(Level::Healthy))]
    #[case::a_healthy_pod("log-shipper-bb", Some(Level::Healthy))]
    #[case::a_crash_looping_pod("crashing-pod", Some(Level::Warning))]
    #[case::a_ready_machine("node-beta", Some(Level::Healthy))]
    #[case::a_machine_not_ready("node-alpha", Some(Level::Failing))]
    #[case::a_custom_resource_nothing_assesses("gizmo-red", None)]
    #[case::a_fixed_page("changed", None)]
    fn a_row_shows_the_health_of_what_it_opens(
        #[case] query: &str,
        #[case] expected: Option<Level>,
    ) {
        let results = search_results(&model(), query, transect_map::search::DIALOG_LIST);
        assert_eq!(results.rows.first().map(|row| row.level), Some(expected));
    }
}
