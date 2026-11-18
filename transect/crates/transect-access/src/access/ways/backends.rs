//! The Service that a way in goes through and the port of the
//! workload's pods that it reaches.  A workload's drawing ends the
//! way's line at that port.

use k8s_openapi::api::core::v1::{ContainerPort, Pod, Service, ServicePort};
use k8s_openapi::api::networking::v1::{IngressBackend, ServiceBackendPort};
use k8s_openapi::apimachinery::pkg::util::intstr::IntOrString;
use kube::ResourceExt;

use transect_graph::edges::Object;

#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Backend {
    pub service: Option<Object>,
    pub port: Target,
}

/// A port of a workload's pods, by number or by the name a container
/// gives it.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Target {
    Number(i32),
    Name(String),
}

/// `service` as a graph `Object`.
pub fn object(service: &Service) -> Object {
    Object::new(
        "",
        "Service",
        service.namespace().as_deref(),
        &service.name_any(),
    )
}

/// The pod port that the Service port `port` forwards to: its
/// `targetPort`, or `port.port` when `targetPort` is unset.
pub fn target(port: &ServicePort) -> Target {
    match &port.target_port {
        Some(IntOrString::Int(number)) => Target::Number(*number),
        Some(IntOrString::String(name)) => Target::Name(name.clone()),
        None => Target::Number(port.port),
    }
}

/// One `Backend` for each port of `service`, at the pod port that it
/// forwards to.
pub fn of_service(service: &Service) -> Vec<Backend> {
    let ports = service
        .spec
        .iter()
        .flat_map(|spec| spec.ports.iter().flatten());
    let found = ports.map(|port| Backend {
        service: Some(object(service)),
        port: target(port),
    });
    let mut found: Vec<Backend> = found.collect();
    found.dedup();
    found
}

/// The pod ports that the backends of an Ingress in `namespace` lead
/// to, looked up in `services`.  It leaves out a backend whose port
/// matches no port of its Service, because that backend reaches no pod.
pub fn of_backends<'a>(
    namespace: Option<String>,
    backends: impl IntoIterator<Item = &'a IngressBackend>,
    services: &[std::sync::Arc<Service>],
) -> Vec<Backend> {
    let mut found: Vec<Backend> = Vec::new();
    for backend in backends {
        let Some(named) = &backend.service else {
            continue;
        };
        let service = services
            .iter()
            .find(|service| service.namespace() == namespace && service.name_any() == named.name);
        let Some(service) = service else {
            continue;
        };
        let ports = service
            .spec
            .iter()
            .flat_map(|spec| spec.ports.iter().flatten());
        let mut matched = ports.filter(|port| matches(port, named.port.as_ref()));
        if let Some(port) = matched.next() {
            let backend = Backend {
                service: Some(object(service)),
                port: target(port),
            };
            if !found.contains(&backend) {
                found.push(backend);
            }
        }
    }
    found
}

/// Whether the Ingress backend port `requested` refers to the Service
/// port `port`, by number or by name.  When it gives neither, every
/// port matches, so `of_backends` uses the Service's first port.
fn matches(port: &ServicePort, requested: Option<&ServiceBackendPort>) -> bool {
    match requested {
        Some(ServiceBackendPort {
            number: Some(number),
            ..
        }) => port.port == *number,
        Some(ServiceBackendPort {
            name: Some(name), ..
        }) => port.name.as_deref() == Some(name.as_str()),
        _ => true,
    }
}

/// The container ports of `pod`.  When `host` is true, only the ports
/// whose host port callers outside the machine can reach.
pub fn of_pod(pod: &Pod, host: bool) -> Vec<Backend> {
    let containers = pod.spec.iter().flat_map(|spec| spec.containers.iter());
    let ports = containers.flat_map(|c| c.ports.iter().flatten());
    let ports = ports.filter(|port| !host || host_port(port).is_some());
    ports
        .map(|port| Backend {
            service: None,
            port: Target::Number(port.container_port),
        })
        .collect()
}

/// The host port of `port` that callers outside the machine can
/// connect to.  It is `None` when the port has no host port, or when
/// its `hostIP` is a loopback address.
pub fn host_port(port: &ContainerPort) -> Option<i32> {
    let loopback = port
        .host_ip
        .as_deref()
        .and_then(|ip| ip.parse::<std::net::IpAddr>().ok());
    match loopback {
        Some(ip) if ip.is_loopback() => None,
        _ => port.host_port,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use k8s_openapi::api::networking::v1::Ingress;

    use super::*;

    fn service() -> Service {
        serde_yaml::from_str("{metadata: {namespace: shop, name: web}, spec: {ports: [{name: http, port: 80, targetPort: http}, {name: admin, port: 81, targetPort: 9000}, {name: plain, port: 82}]}}").unwrap()
    }

    fn ingress(port: &str) -> Ingress {
        serde_yaml::from_str(&format!("{{metadata: {{namespace: shop, name: web}}, spec: {{rules: [{{host: a.example.test, http: {{paths: [{{path: /, pathType: Prefix, backend: {{service: {{name: web, port: {port}}}}}}}]}}}}]}}}}")).unwrap()
    }

    #[rstest]
    #[case::a_named_target("{number: 80}", vec![Target::Name("http".into())])]
    #[case::a_numbered_target("{name: admin}", vec![Target::Number(9000)])]
    #[case::the_port_itself("{number: 82}", vec![Target::Number(82)])]
    #[case::no_such_port("{number: 99}", vec![])]
    fn follows_an_ingress_to_its_pods_port(#[case] port: &str, #[case] expected: Vec<Target>) {
        let services = vec![std::sync::Arc::new(service())];
        let ingress = ingress(port);
        let rules = ingress.spec.iter().flat_map(|s| s.rules.iter().flatten());
        let paths = rules.flat_map(|r| r.http.iter().flat_map(|h| &h.paths));
        let found = of_backends(ingress.namespace(), paths.map(|p| &p.backend), &services);
        let ports: Vec<Target> = found.into_iter().map(|b| b.port).collect();
        assert_eq!(ports, expected);
    }
}
