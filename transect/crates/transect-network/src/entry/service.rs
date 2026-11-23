//! A `Service` as a workload page shows it: its type, its ports, and
//! how many of its endpoints are ready.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use k8s_openapi::api::core::v1::Service;
use k8s_openapi::api::discovery::v1::EndpointSlice;
use kube::ResourceExt;

/// The label that holds the name of a slice's `Service`.
const SERVICE_NAME: &str = "kubernetes.io/service-name";

/// The endpoint count of each `Service`, by namespace and name, and the
/// pods behind its ready endpoints.
#[derive(Debug, Default)]
pub struct Endpoints {
    counts: HashMap<(String, String), Count>,
    ready: HashMap<(String, String), Vec<String>>,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Count {
    pub ready: usize,
    pub total: usize,
}

impl Endpoints {
    /// A dual-stack `Service` lists each pod in an IPv4 slice and an
    /// IPv6 slice, so each pod counts once, by its `targetRef`.  An
    /// endpoint with no `targetRef` counts once, by its first address.  An
    /// endpoint with no `ready` condition counts as ready, as the
    /// `EndpointSlice` API tells consumers to do.
    pub fn build(slices: &[Arc<EndpointSlice>]) -> Self {
        let mut seen: HashMap<(String, String), HashSet<String>> = HashMap::new();
        let mut counts: HashMap<(String, String), Count> = HashMap::new();
        let mut ready_pods: HashMap<(String, String), Vec<String>> = HashMap::new();
        for slice in slices {
            let (Some(namespace), Some(service)) =
                (slice.namespace(), slice.labels().get(SERVICE_NAME))
            else {
                continue;
            };
            let key = (namespace, service.clone());
            for endpoint in slice.endpoints.iter().flatten() {
                let target = endpoint.target_ref.as_ref().and_then(|t| t.name.clone());
                let Some(id) = target.or_else(|| endpoint.addresses.first().cloned()) else {
                    continue;
                };
                if !seen.entry(key.clone()).or_default().insert(id) {
                    continue;
                }
                let ready = endpoint.conditions.as_ref().and_then(|c| c.ready);
                let ready = ready.unwrap_or(true);
                let count = counts.entry(key.clone()).or_default();
                count.total += 1;
                count.ready += usize::from(ready);
                let target = endpoint.target_ref.as_ref();
                let pod = target.filter(|t| t.kind.as_deref() == Some("Pod"));
                if let (true, Some(name)) = (ready, pod.and_then(|t| t.name.clone())) {
                    ready_pods.entry(key.clone()).or_default().push(name);
                }
            }
        }
        for pods in ready_pods.values_mut() {
            pods.sort();
        }
        Self {
            counts,
            ready: ready_pods,
        }
    }

    /// The names of the pods behind the ready endpoints of the `Service`
    /// `namespace`/`name`, in the order of their names.
    pub fn ready_pods(&self, namespace: &str, name: &str) -> &[String] {
        let key = (namespace.to_owned(), name.to_owned());
        self.ready.get(&key).map_or(&[], Vec::as_slice)
    }

    pub fn of(&self, namespace: &str, name: &str) -> Count {
        let key = (namespace.to_owned(), name.to_owned());
        self.counts.get(&key).copied().unwrap_or_default()
    }
}

#[derive(Debug, PartialEq)]
pub struct ServiceBox {
    pub name: String,
    pub namespace: String,
    /// `ClusterIP`, `NodePort`, `LoadBalancer`, or `headless` for a
    /// `ClusterIP` `Service` with no address of its own.
    pub kind: String,
    /// Each port, such as `80` or `53/UDP`.  TCP ports have no suffix.
    pub ports: Vec<String>,
    pub endpoints: Count,
}

impl ServiceBox {
    pub fn build(service: &Service, endpoints: &Endpoints) -> Self {
        let spec = service.spec.as_ref();
        let kind = spec.and_then(|spec| spec.type_.clone());
        let headless = spec.and_then(|spec| spec.cluster_ip.as_deref()) == Some("None");
        let kind = match kind {
            _ if headless => "headless".into(),
            Some(kind) => kind,
            None => "ClusterIP".into(),
        };
        let ports = spec.and_then(|spec| spec.ports.as_ref());
        let ports = ports
            .into_iter()
            .flatten()
            .map(|port| match port.protocol.as_deref() {
                None | Some("TCP") => port.port.to_string(),
                Some(protocol) => format!("{}/{protocol}", port.port),
            });
        let namespace = service.namespace().unwrap_or_default();
        Self {
            name: service.name_any(),
            namespace: namespace.clone(),
            kind,
            ports: ports.collect(),
            endpoints: endpoints.of(&namespace, &service.name_any()),
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::entry::fixtures::service;

    /// An IPv4 slice of `service` in shop, with `endpoints` in YAML.
    fn slice(service: &str, endpoints: &str) -> Arc<EndpointSlice> {
        Arc::new(
            serde_yaml_ng::from_str(&format!(
                "metadata: {{namespace: shop, name: {service}-x, labels: {{kubernetes.io/service-name: {service}}}}}\naddressType: IPv4\nendpoints: {endpoints}"
            ))
            .unwrap(),
        )
    }

    fn pod_endpoint(pod: &str, address: &str, ready: Option<bool>) -> String {
        let ready = ready.map_or(String::new(), |ready| format!("ready: {ready}"));
        format!(
            "{{addresses: [{address}], conditions: {{{ready}}}, targetRef: {{kind: Pod, name: {pod}}}}}"
        )
    }

    #[rstest]
    #[case::all_ready(
        vec![slice("web", &format!("[{}, {}]", pod_endpoint("web-1", "10.0.0.1", Some(true)), pod_endpoint("web-2", "10.0.0.2", Some(true))))],
        Count { ready: 2, total: 2 }
    )]
    #[case::one_not_ready(
        vec![slice("web", &format!("[{}, {}]", pod_endpoint("web-1", "10.0.0.1", Some(true)), pod_endpoint("web-2", "10.0.0.2", Some(false))))],
        Count { ready: 1, total: 2 }
    )]
    #[case::unknown_is_ready(
        vec![slice("web", &format!("[{}]", pod_endpoint("web-1", "10.0.0.1", None)))],
        Count { ready: 1, total: 1 }
    )]
    #[case::one_pod_in_two_slices(
        vec![
            slice("web", &format!("[{}]", pod_endpoint("web-1", "10.0.0.1", Some(true)))),
            slice("web", &format!("[{}]", pod_endpoint("web-1", "fd00::1", Some(true)))),
        ],
        Count { ready: 1, total: 1 }
    )]
    #[case::by_address_with_no_target(
        vec![slice("web", "[{addresses: [10.0.0.1]}, {addresses: [10.0.0.2]}]")],
        Count { ready: 2, total: 2 }
    )]
    #[case::another_services_slice(
        vec![slice("api", &format!("[{}]", pod_endpoint("api-1", "10.0.0.1", Some(true))))],
        Count::default()
    )]
    #[case::an_empty_slice(vec![slice("web", "[]")], Count::default())]
    fn counts_a_services_endpoints(#[case] slices: Vec<Arc<EndpointSlice>>, #[case] count: Count) {
        assert_eq!(Endpoints::build(&slices).of("shop", "web"), count);
    }

    #[rstest]
    #[case::each_ready_pod(
        vec![slice("web", &format!("[{}, {}]", pod_endpoint("web-2", "10.0.0.2", Some(true)), pod_endpoint("web-1", "10.0.0.1", None)))],
        &["web-1", "web-2"]
    )]
    #[case::not_a_pod_that_is_not_ready(
        vec![slice("web", &format!("[{}, {}]", pod_endpoint("web-1", "10.0.0.1", Some(true)), pod_endpoint("web-2", "10.0.0.2", Some(false))))],
        &["web-1"]
    )]
    #[case::one_pod_in_two_slices(
        vec![
            slice("web", &format!("[{}]", pod_endpoint("web-1", "10.0.0.1", Some(true)))),
            slice("web", &format!("[{}]", pod_endpoint("web-1", "fd00::1", Some(true)))),
        ],
        &["web-1"]
    )]
    #[case::no_pod_behind_an_address(vec![slice("web", "[{addresses: [10.0.0.1]}]")], &[])]
    fn names_the_pods_behind_the_ready_endpoints(
        #[case] slices: Vec<Arc<EndpointSlice>>,
        #[case] pods: &[&str],
    ) {
        assert_eq!(Endpoints::build(&slices).ready_pods("shop", "web"), pods);
    }

    fn with_spec(spec: &str) -> Service {
        serde_yaml_ng::from_str(&format!(
            "metadata: {{namespace: shop, name: web}}\nspec: {spec}"
        ))
        .unwrap()
    }

    #[rstest]
    #[case::cluster_ip("{ports: [{port: 8000}]}", "ClusterIP", &["8000"])]
    #[case::headless("{clusterIP: None, ports: [{port: 6379}]}", "headless", &["6379"])]
    #[case::load_balancer("{type: LoadBalancer, ports: [{port: 139}, {port: 445}]}", "LoadBalancer", &["139", "445"])]
    #[case::udp("{type: NodePort, ports: [{port: 53, protocol: UDP}]}", "NodePort", &["53/UDP"])]
    #[case::no_ports("{}", "ClusterIP", &[])]
    fn reads_a_services_type_and_ports(
        #[case] spec: &str,
        #[case] kind: &str,
        #[case] ports: &[&str],
    ) {
        let found = ServiceBox::build(&with_spec(spec), &Endpoints::default());
        assert_eq!(
            (found.kind.as_str(), found.ports),
            (kind, ports.iter().map(|p| p.to_string()).collect())
        );
    }

    #[rstest]
    fn reads_its_own_endpoints() {
        let slices = [slice(
            "web",
            &format!("[{}]", pod_endpoint("web-1", "10.0.0.1", Some(true))),
        )];
        let found = ServiceBox::build(
            &service("shop", "web", "{app: web}"),
            &Endpoints::build(&slices),
        );
        assert_eq!(found.endpoints, Count { ready: 1, total: 1 });
    }
}
