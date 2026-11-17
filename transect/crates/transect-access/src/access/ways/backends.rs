//! Where a way in arrives inside its workload: the Service it passes,
//! and the port the workload answers it on.  A workload's drawing ends
//! the way's line at that port.

use k8s_openapi::api::core::v1::{ContainerPort, Pod, Service, ServicePort};
use k8s_openapi::api::networking::v1::{IngressBackend, ServiceBackendPort};
use k8s_openapi::apimachinery::pkg::util::intstr::IntOrString;
use kube::ResourceExt;

use transect_graph::edges::Object;

/// The Service a way passes, when it passes one, and the port its
/// workload answers on.
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

/// The Service `service` as an object.
pub fn object(service: &Service) -> Object {
    Object::new(
        "",
        "Service",
        service.namespace().as_deref(),
        &service.name_any(),
    )
}

/// The port of the pods behind `port`: its `targetPort`, or its own
/// number when it names none.
pub fn target(port: &ServicePort) -> Target {
    match &port.target_port {
        Some(IntOrString::Int(number)) => Target::Number(*number),
        Some(IntOrString::String(name)) => Target::Name(name.clone()),
        None => Target::Number(port.port),
    }
}

/// Each port of `service`, as a way through it reaches its pods.
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

/// The backends `backends` of an Ingress in `namespace` lead to,
/// through the Services in `services`.  A backend port that names no
/// port of its Service reaches no pod, so it is left out.
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
        let mut matched = ports.filter(|port| names(port, named.port.as_ref()));
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

/// Whether an `Ingress`'s backend port `asked` names the Service's
/// `port`: by number or by name.  When it names neither, every port
/// matches, and `of_backends` takes the Service's first.
fn names(port: &ServicePort, asked: Option<&ServiceBackendPort>) -> bool {
    match asked {
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

/// The ports `pod`'s containers answer on: those with a host port that
/// answers outside its machine when `host` is true, and every one
/// otherwise.
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

/// The host port `port` opens to callers outside its machine: none when
/// it has none, or when its `hostIP` is a loopback address, which only
/// the machine itself can connect to.
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
