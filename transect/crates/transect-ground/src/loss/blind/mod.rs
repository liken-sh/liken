//! transect reads usage, traffic, and alert routes from Prometheus, and
//! log findings from a log store.  When a lost machine runs either of
//! them, the map gets no data from it during the loss, so the loss page
//! says so.
//!
//! transect finds the Prometheus pod by the `hostname` that Prometheus
//! returns from `/api/v1/status/runtimeinfo`, which in a pod is the pod's
//! name.  The address transect connects to does not identify the pod,
//! because it can be a port-forward.  transect finds the log store's pods
//! by the selector of the `Service` that the `LogSource` names.

pub mod poll;

use std::collections::BTreeMap;
use std::sync::Arc;

use k8s_openapi::api::core::v1::{Pod, Service};
use kube::ResourceExt;

use super::Lost;
use transect_graph::pods::finished;

/// A source of transect's data that runs on a lost machine.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Blind {
    /// "Prometheus", or "the log store of LogSource logs".
    pub source: String,
    /// The data that the map loses while the source is down.
    pub loses: &'static str,
    pub machine: String,
}

/// Where transect reads its data from.
pub struct Sources<'a> {
    /// The host name that Prometheus returns, or `None` until a read
    /// succeeds.
    pub prometheus: Option<&'a str>,
    /// The `LogSource`'s name, and the namespace and name of its `Service`.
    pub log_source: Option<(&'a str, &'a str, &'a str)>,
}

/// The sources whose running pods are on a machine of `lost`.
pub fn blinded(
    lost: &Lost,
    sources: &Sources,
    pods: &[Arc<Pod>],
    services: &[Arc<Service>],
) -> Vec<Blind> {
    let running = pods.iter().filter(|pod| !finished(pod));
    let on_lost = |pod: &&Arc<Pod>| {
        let node = pod.spec.as_ref().and_then(|spec| spec.node_name.as_deref());
        node.filter(|node| lost.contains(node)).map(String::from)
    };
    let mut found = Vec::new();
    if let Some(name) = sources.prometheus {
        let pod = running.clone().find(|pod| pod.name_any() == name);
        if let Some(machine) = pod.as_ref().and_then(on_lost) {
            found.push(Blind {
                source: "Prometheus".into(),
                loses: "usage, traffic, and where alerts go",
                machine,
            });
        }
    }
    if let Some((source, namespace, name)) = sources.log_source {
        let service = services
            .iter()
            .find(|s| s.namespace().as_deref() == Some(namespace) && s.name_any() == name);
        let selector = service
            .and_then(|s| s.spec.as_ref())
            .and_then(|spec| spec.selector.as_ref());
        let selected = running
            .filter(|pod| pod.namespace().as_deref() == Some(namespace))
            .filter(|pod| selector.is_some_and(|selector| selects(selector, pod)));
        if let Some(machine) = selected.filter_map(|pod| on_lost(&pod)).next() {
            found.push(Blind {
                source: format!("the log store of LogSource {source}"),
                loses: "the log findings",
                machine,
            });
        }
    }
    found
}

fn selects(selector: &BTreeMap<String, String>, pod: &Pod) -> bool {
    let labels = pod.metadata.labels.as_ref();
    !selector.is_empty()
        && selector
            .iter()
            .all(|(key, value)| labels.and_then(|labels| labels.get(key)) == Some(value))
}

/// "transect loses data too: Prometheus runs on beta, so the map loses
/// usage, traffic, and where alerts go."
pub fn text(blind: &[Blind]) -> Option<String> {
    let parts: Vec<String> = blind
        .iter()
        .map(|one| {
            format!(
                "{} runs on {}, so the map loses {}",
                one.source, one.machine, one.loses
            )
        })
        .collect();
    (!parts.is_empty()).then(|| format!("transect loses data too: {}.", parts.join("; ")))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn pod(namespace: &str, name: &str, node: &str, labels: &str) -> Arc<Pod> {
        let yaml = format!(
            "{{metadata: {{namespace: {namespace}, name: {name}, labels: {labels}}}, spec: {{nodeName: {node}, containers: []}}}}"
        );
        Arc::new(serde_yaml_ng::from_str(&yaml).unwrap())
    }

    fn pods() -> Vec<Arc<Pod>> {
        vec![
            pod("metrics", "prometheus-0", "beta", "{app: prometheus}"),
            pod("logs", "store-0", "gamma", "{app: store}"),
        ]
    }

    fn services() -> Vec<Arc<Service>> {
        let yaml = "{metadata: {namespace: logs, name: store}, spec: {selector: {app: store}}}";
        vec![Arc::new(serde_yaml_ng::from_str(yaml).unwrap())]
    }

    fn lost(names: &[&str]) -> Lost {
        Lost::of(names.iter().copied(), ["alpha", "beta", "gamma"])
    }

    const SOURCES: Sources = Sources {
        prometheus: Some("prometheus-0"),
        log_source: Some(("logs", "logs", "store")),
    };

    #[rstest]
    #[case::neither(&["alpha"], None)]
    #[case::prometheus(&["beta"], Some("transect loses data too: Prometheus runs on beta, so the map loses usage, traffic, and where alerts go."))]
    #[case::both(&["beta", "gamma"], Some("transect loses data too: Prometheus runs on beta, so the map loses usage, traffic, and where alerts go; the log store of LogSource logs runs on gamma, so the map loses the log findings."))]
    fn writes_when_a_loss_stops_transects_own_sources(
        #[case] names: &[&str],
        #[case] expected: Option<&str>,
    ) {
        let blind = blinded(&lost(names), &SOURCES, &pods(), &services());
        assert_eq!(text(&blind).as_deref(), expected);
    }
}
