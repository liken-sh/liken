//! The workloads that the cluster cannot do without: the Alertmanager
//! that the cluster's alerts go through, the cluster's DNS, and the
//! path for web traffic from outside.  When a loss takes one of these
//! down, the loss shows it first, because the rest of the loss assumes
//! that alerts still page someone and that people can reach the
//! cluster.
//!
//! transect finds each one from `Service` selectors:
//!
//! - alerting: the pods selected by the `Service` that the
//!   `MetricsSource`'s `alerts.service` names, which is the Alertmanager
//!   that transect reads.  When no `Service` is named, the alerting
//!   path is unknown, and the loss shows that.
//! - DNS: the pods that the `kube-dns` `Service` in `kube-system`
//!   selects.  kubeadm and k3s give the resolver's `Service` that
//!   name, and so do most other distributions.
//! - web traffic: the pods that a `LoadBalancer` `Service` selects on
//!   port 80 or 443, because an ingress controller receives the
//!   cluster's web traffic through such a `Service`.  A load balancer
//!   on any other port serves one application's own protocol, such as
//!   SSH or MQTT, and its loss is that application's loss.

use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;

use k8s_openapi::api::core::v1::{Pod, Service};
use kube::ResourceExt;

use transect_cluster::ownership::Ownership;
use transect_graph::edges::Object;
use transect_graph::pods::finished;

/// What a workload does that the cluster cannot do without.  A loss
/// lists them in the order of these variants.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Vital {
    Alerting,
    Dns,
    WayIn,
}

/// The ports that an ingress controller's load balancer listens on for
/// web traffic.
const WEB_PORTS: [i32; 2] = [80, 443];

/// What each vital workload does, keyed by the workload's top owner.  A
/// workload that does more than one gets the first in `Vital`'s order.
pub fn vital(
    pods: &[Arc<Pod>],
    services: &[Arc<Service>],
    ownership: &Ownership,
    alerting: Option<&transect_graph::upstream::Service>,
) -> HashMap<Object, Vital> {
    let running: Vec<&Pod> = pods
        .iter()
        .map(AsRef::as_ref)
        .filter(|p| !finished(p))
        .collect();
    let mut found: HashMap<Object, Vital> = HashMap::new();
    let mut record = |pod: &Pod, vital: Vital| {
        let object = Object::new("", "Pod", pod.namespace().as_deref(), &pod.name_any());
        let top = ownership.top_owner(&object);
        let current = found.entry(top).or_insert(vital);
        *current = (*current).min(vital);
    };
    for service in services {
        let Some(vital) = role(service, alerting) else {
            continue;
        };
        let spec = service.spec.as_ref();
        let Some(selector) = spec.and_then(|spec| spec.selector.as_ref()) else {
            continue;
        };
        let namespace = service.namespace();
        let selected = running.iter().filter(|pod| pod.namespace() == namespace);
        for pod in selected.filter(|pod| selects(selector, pod)) {
            record(pod, vital);
        }
    }
    found
}

fn role(service: &Service, alerting: Option<&transect_graph::upstream::Service>) -> Option<Vital> {
    let spec = service.spec.as_ref()?;
    let named = |declared: &transect_graph::upstream::Service| {
        service.namespace().as_deref() == Some(declared.namespace.as_str())
            && service.name_any() == declared.name
    };
    if alerting.is_some_and(named) {
        return Some(Vital::Alerting);
    }
    if service.namespace().as_deref() == Some("kube-system") && service.name_any() == "kube-dns" {
        return Some(Vital::Dns);
    }
    let balanced = spec.type_.as_deref() == Some("LoadBalancer");
    let ports = spec.ports.iter().flatten();
    let web = ports.into_iter().any(|port| WEB_PORTS.contains(&port.port));
    (balanced && web).then_some(Vital::WayIn)
}

fn selects(selector: &BTreeMap<String, String>, pod: &Pod) -> bool {
    let labels = pod.metadata.labels.as_ref();
    !selector.is_empty()
        && selector
            .iter()
            .all(|(key, value)| labels.and_then(|labels| labels.get(key)) == Some(value))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_cluster::ownership::Entry;

    fn yaml<K: serde::de::DeserializeOwned>(text: &str) -> Arc<K> {
        Arc::new(serde_yaml::from_str(text).unwrap())
    }

    /// A running pod with no owner, so it is its own workload.
    fn pod(namespace: &str, name: &str, app: &str, address: &str) -> Arc<Pod> {
        yaml(&format!(
            "{{metadata: {{namespace: {namespace}, name: {name}, labels: {{app: {app}}}}}, status: {{phase: Running, podIP: {address}}}}}"
        ))
    }

    fn service(namespace: &str, name: &str, spec: &str) -> Arc<Service> {
        yaml(&format!(
            "{{metadata: {{namespace: {namespace}, name: {name}}}, spec: {spec}}}"
        ))
    }

    /// A cluster with an Alertmanager, a resolver, an ingress controller,
    /// one application behind a load balancer of its own, and one
    /// application with a `Service` and no load balancer.
    fn cluster() -> (Vec<Arc<Pod>>, Vec<Arc<Service>>) {
        let pods = vec![
            pod("watch", "alertmanager-0", "alertmanager", "10.0.0.1"),
            pod("watch", "silencer", "silencer", "10.0.0.6"),
            pod("kube-system", "resolver", "dns", "10.0.0.2"),
            pod("edge", "router", "router", "10.0.0.3"),
            pod("chat", "broker", "broker", "10.0.0.4"),
            pod("shop", "web", "web", "10.0.0.5"),
        ];
        let services = vec![
            service("watch", "alertmanager", "{selector: {app: alertmanager}}"),
            service("kube-system", "kube-dns", "{selector: {app: dns}}"),
            service(
                "edge",
                "router",
                "{type: LoadBalancer, selector: {app: router}, ports: [{port: 80}, {port: 443}]}",
            ),
            service(
                "chat",
                "broker",
                "{type: LoadBalancer, selector: {app: broker}, ports: [{port: 1883}]}",
            ),
            service("shop", "web", "{selector: {app: web}, ports: [{port: 80}]}"),
        ];
        (pods, services)
    }

    #[rstest]
    #[case::alerting(Some(("watch", "alertmanager")), "alertmanager-0", Some(Vital::Alerting))]
    #[case::no_alerting_service_named(None, "alertmanager-0", None)]
    #[case::pod_not_selected(Some(("watch", "alertmanager")), "silencer", None)]
    #[case::dns(None, "resolver", Some(Vital::Dns))]
    #[case::web_traffic(None, "router", Some(Vital::WayIn))]
    #[case::load_balancer_on_another_port(None, "broker", None)]
    #[case::web_service_without_a_load_balancer(None, "web", None)]
    #[case::alerting_comes_first(Some(("edge", "router")), "router", Some(Vital::Alerting))]
    fn finds_the_workloads_the_cluster_cannot_do_without(
        #[case] alerting: Option<(&str, &str)>,
        #[case] name: &str,
        #[case] expected: Option<Vital>,
    ) {
        let (pods, services) = cluster();
        let entries = pods.iter().map(|pod| Entry {
            group: "",
            kind: "Pod",
            meta: &pod.metadata,
        });
        let ownership = Ownership::build(entries);
        let alerting = alerting.map(|(namespace, name)| transect_graph::upstream::Service {
            namespace: namespace.into(),
            name: name.into(),
            port: 9093,
        });
        let found = vital(&pods, &services, &ownership, alerting.as_ref());
        let namespace = pods
            .iter()
            .find(|pod| pod.name_any() == name)
            .unwrap()
            .namespace();
        let object = Object::new("", "Pod", namespace.as_deref(), name);
        assert_eq!(found.get(&object).copied(), expected);
    }
}
