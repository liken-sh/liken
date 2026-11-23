//! Builds the logs lens of a pod or a workload from the snapshot.

use std::sync::Arc;

use k8s_openapi::api::core::v1::Pod;
use k8s_openapi::jiff::Timestamp;
use kube::ResourceExt;

use super::super::model::Model;
use super::super::snapshot::Snapshot;
use transect_graph::edges::Object;
use transect_map::lenses::logs::{Container, LogsPage, LogsRequest, Subject};
use transect_observe::health::{self, Health, Level};
use transect_observe::logs::Tails;
use transect_resources::layers::Layers;

/// A pod the lens can show, with its health level.
type Rated = (Arc<Pod>, Level);

/// The request for a logs lens. `resource` is `pods` for a pod's lens,
/// and `pod` and `container` come from the query.
pub struct Lens<'a> {
    pub namespace: &'a str,
    pub resource: &'a str,
    pub name: &'a str,
    pub pod: Option<&'a str>,
    pub container: Option<&'a str>,
}

/// The subject of the lens and the pods it can show, each with its
/// health at `now`, or `None` when the cluster has no such pod or
/// workload. A workload's pods sort least healthy first, then newest,
/// then by name, and the lens opens on the first, because a reader
/// opens a workload's logs for a failing pod or for the latest run.
fn pods_of(snapshot: &Snapshot, lens: &Lens, now: Timestamp) -> Option<(Subject, Vec<Rated>)> {
    let level = |pod: &Pod| Health::from_reasons(health::pod_reasons(pod, now)).level;
    let in_namespace = snapshot
        .pods
        .iter()
        .filter(|pod| pod.namespace().as_deref() == Some(lens.namespace));
    if lens.resource == "pods" {
        let pod = in_namespace
            .clone()
            .find(|pod| pod.name_any() == lens.name)?;
        let object = Object::new("", "Pod", Some(lens.namespace), lens.name);
        let owner = snapshot.ownership.top_owner(&object);
        let workload = (owner != object).then(|| {
            let href = snapshot.pages().href(&owner);
            (owner.name.clone(), href)
        });
        let subject = Subject::Pod {
            name: lens.name.into(),
            workload,
        };
        return Some((subject, vec![(pod.clone(), level(pod))]));
    }
    let owner = snapshot.ownership.workloads(lens.namespace).find(|owner| {
        owner.name == lens.name
            && transect_graph::links::kinds::workload(owner, &snapshot.plurals).as_deref()
                == Some(lens.resource)
    })?;
    let path = transect_graph::links::page(owner, &snapshot.plurals)?;
    let mut pods: Vec<Rated> = in_namespace
        .filter(|pod| {
            let object = Object::new("", "Pod", Some(lens.namespace), &pod.name_any());
            snapshot.ownership.top_owner(&object) == *owner
        })
        .map(|pod| (pod.clone(), level(pod)))
        .collect();
    pods.sort_by_key(|(pod, level)| {
        let created = pod.metadata.creation_timestamp.as_ref().map(|t| t.0);
        (
            std::cmp::Reverse(*level),
            std::cmp::Reverse(created),
            pod.name_any(),
        )
    });
    let subject = Subject::Workload {
        name: lens.name.into(),
        path,
    };
    Some((subject, pods))
}

/// The pod whose logs the lens shows: the pod in the query, otherwise
/// the first pod, or `None` for a workload with no pods.
pub fn shown_pod(model: &Model, lens: &Lens, now: Timestamp) -> Option<Arc<Pod>> {
    let (_, pods) = pods_of(&model.snapshot, lens, now)?;
    let pods: Vec<&Arc<Pod>> = pods.iter().map(|(pod, _)| pod).collect();
    let asked = lens
        .pod
        .and_then(|asked| pods.iter().find(|pod| pod.name_any() == asked));
    asked.or(pods.first()).map(|pod| (*pod).clone())
}

/// The containers of `pod` whose logs the lens can show, in the order
/// that `transect_observe::logs::containers` returns them.
fn containers(pod: &Pod) -> Vec<Container> {
    let spec = pod.spec.as_ref();
    let inits = spec.and_then(|spec| spec.init_containers.as_ref());
    let init = |name: &str| inits.into_iter().flatten().any(|c| c.name == name);
    let read = transect_observe::logs::containers(pod).into_iter();
    read.map(|name| Container {
        init: init(&name),
        name,
    })
    .collect()
}

/// Builds the logs page for `lens`, with the tails that `logs` returns
/// for the shown pod, or `None` when the cluster has no such pod or
/// workload.
pub fn logs_page(
    model: &Model,
    layers: &Layers,
    lens: &Lens,
    logs: impl Fn(&str, &str) -> Option<Arc<Tails>>,
    now: Timestamp,
) -> Option<LogsPage> {
    let snapshot = &model.snapshot;
    let layer = snapshot.layer_of(lens.namespace, layers)?;
    let (subject, pods) = pods_of(snapshot, lens, now)?;
    let shown_pod = shown_pod(model, lens, now);
    let containers = shown_pod.as_deref().map(containers).unwrap_or_default();
    let tails = shown_pod
        .as_ref()
        .and_then(|pod| logs(lens.namespace, &pod.name_any()));
    let request = LogsRequest {
        namespace: lens.namespace,
        layer,
        subject,
        pods: pods
            .iter()
            .map(|(pod, level)| (pod.name_any(), *level))
            .collect(),
        pod: lens.pod,
        containers,
        container: lens.container,
    };
    let mut page = LogsPage::build(request, tails);
    if lens.resource != "pods" && super::has_access(model, lens.namespace, lens.resource, lens.name)
    {
        let path = page.tabs.tabs[0].href.clone();
        page.tabs = page.tabs.with_access(&path);
    }
    if let Some(workload) =
        super::workload_at(&model.snapshot, lens.namespace, lens.resource, lens.name)
        && model.vulnerabilities.names(&workload)
    {
        let path = page.tabs.tabs[0].href.clone();
        page.tabs = page.tabs.with_vulnerabilities(&path, false);
    }
    Some(page)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::web::fixture;

    /// A pod of the fixture's storefront whose container is in
    /// `CrashLoopBackOff` when `crashing`.
    fn storefront_pod(name: &str, created: &str, crashing: bool) -> Arc<Pod> {
        let state = match crashing {
            true => "{waiting: {reason: CrashLoopBackOff}}",
            false => "{running: {}}",
        };
        Arc::new(
            serde_yaml_ng::from_str(&format!(
                "metadata:
  namespace: acme-shop
  name: {name}
  uid: {name}
  creationTimestamp: '{created}'
  labels: {{app: storefront}}
  ownerReferences: [{{apiVersion: apps/v1, kind: ReplicaSet, name: storefront-rs, uid: storefront-rs, controller: true}}]
spec: {{containers: [{{name: main}}]}}
status:
  phase: Running
  containerStatuses: [{{name: main, ready: true, restartCount: 4, image: x, imageID: '', state: {state}}}]"
            ))
            .unwrap(),
        )
    }

    #[rstest]
    #[case::the_one_crash_looping(
        storefront_pod("storefront-rs-one", "2026-01-01T00:00:00Z", false),
        storefront_pod("storefront-rs-two", "2026-01-01T00:00:00Z", true),
        "storefront-rs-two"
    )]
    #[case::the_newest_of_healthy_pods(
        storefront_pod("storefront-rs-one", "2026-01-01T00:00:00Z", false),
        storefront_pod("storefront-rs-two", "2026-01-02T00:00:00Z", false),
        "storefront-rs-two"
    )]
    #[case::by_name_when_alike(
        storefront_pod("storefront-rs-one", "2026-01-01T00:00:00Z", false),
        storefront_pod("storefront-rs-two", "2026-01-01T00:00:00Z", false),
        "storefront-rs-one"
    )]
    fn a_workloads_lens_opens_on_its_least_healthy_then_newest_pod(
        #[case] one: Arc<Pod>,
        #[case] two: Arc<Pod>,
        #[case] expected: &str,
    ) {
        let mut snapshot = fixture::snapshot();
        snapshot.pods[0] = one;
        snapshot.pods[1] = two;
        let lens = Lens {
            namespace: "acme-shop",
            resource: "deployments",
            name: "storefront",
            pod: None,
            container: None,
        };
        let shown = shown_pod(
            &Model::build(snapshot),
            &lens,
            k8s_openapi::jiff::Timestamp::now(),
        );
        assert_eq!(shown.map(|pod| pod.name_any()).as_deref(), Some(expected));
    }

    /// The fixture with an init container that has run in its ledger
    /// pod.
    fn with_init() -> Model {
        let mut snapshot = fixture::snapshot();
        snapshot.pods[2] = Arc::new(
            serde_yaml_ng::from_str(
                "metadata: {namespace: acme-ledger, name: ledger-db-0, uid: ledger-db-0, ownerReferences: [{apiVersion: apps/v1, kind: StatefulSet, name: ledger-db, uid: ledger-db, controller: true}]}
spec: {initContainers: [{name: migrate}], containers: [{name: main}]}
status:
  phase: Running
  initContainerStatuses: [{name: migrate, ready: true, restartCount: 0, image: x, imageID: '', state: {terminated: {exitCode: 0}}}]
  containerStatuses: [{name: main, ready: true, restartCount: 0, image: x, imageID: '', state: {running: {}}}]",
            )
            .unwrap(),
        );
        Model::build(snapshot)
    }

    #[rstest]
    #[case::the_app_container(None, Some("main"))]
    #[case::its_init_container(Some("migrate"), Some("migrate"))]
    fn a_pods_lens_offers_its_init_container(
        #[case] asked: Option<&str>,
        #[case] expected: Option<&str>,
    ) {
        let lens = Lens {
            namespace: "acme-ledger",
            resource: "pods",
            name: "ledger-db-0",
            pod: None,
            container: asked,
        };
        let page = logs_page(
            &with_init(),
            &Layers::default(),
            &lens,
            |_, _| None,
            k8s_openapi::jiff::Timestamp::now(),
        )
        .unwrap();
        let labels: Vec<&str> = page.containers.iter().map(|c| c.label.as_str()).collect();
        assert_eq!(
            (labels, page.container.as_deref()),
            (vec!["main", "migrate (init)"], expected)
        );
    }
}
