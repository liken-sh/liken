//! What each host of an Ingress routes to: its own rules' paths, and
//! the default backend where a request may reach it.

use k8s_openapi::api::networking::v1::{Ingress, IngressBackend};

use super::Sources;
use transect_graph::edges::Object;

/// Each host `ingress`'s rules name, or "any host" for a rule that
/// names none, with the backends a request to it may reach: its rules'
/// paths, and the default backend unless a path of `/` takes every
/// request first.  The default backend also gets an "any host" entry,
/// for a host that no rule names.
pub fn routed(ingress: &Ingress) -> Vec<(String, Vec<&IngressBackend>)> {
    let spec = ingress.spec.as_ref();
    let rules = spec
        .and_then(|spec| spec.rules.as_ref())
        .into_iter()
        .flatten();
    let mut found: Vec<(String, Vec<&IngressBackend>, bool)> = Vec::new();
    for rule in rules {
        let host = rule.host.clone().unwrap_or_else(|| ANY_HOST.into());
        let paths = rule.http.iter().flat_map(|http| &http.paths);
        let at = match found.iter().position(|(named, _, _)| *named == host) {
            Some(at) => at,
            None => {
                found.push((host, Vec::new(), false));
                found.len() - 1
            }
        };
        for path in paths {
            let every = path.path.as_deref().is_none_or(|p| p == "/") && path.path_type != "Exact";
            found[at].2 |= every;
            if !found[at].1.contains(&&path.backend) {
                found[at].1.push(&path.backend);
            }
        }
    }
    let default = spec.and_then(|spec| spec.default_backend.as_ref());
    if let Some(default) = default {
        for (_, backends, every) in &mut found {
            if !*every && !backends.contains(&default) {
                backends.push(default);
            }
        }
        if !found.iter().any(|(host, _, _)| host == ANY_HOST) {
            found.push((ANY_HOST.into(), vec![default], true));
        }
    }
    found
        .into_iter()
        .map(|(host, backends, _)| (host, backends))
        .collect()
}

const ANY_HOST: &str = "any host";

/// The workloads behind the Services `backends` of an Ingress in
/// `namespace` route to.
pub fn workloads(
    namespace: Option<&str>,
    backends: &[&IngressBackend],
    sources: &Sources,
) -> Vec<Object> {
    let services = backends
        .iter()
        .filter_map(|backend| backend.service.as_ref());
    let mut found: Vec<Object> = Vec::new();
    for service in services {
        let service = Object::new("", "Service", namespace, &service.name);
        for workload in (sources.selected)(&service) {
            if !found.contains(&workload) {
                found.push(workload);
            }
        }
    }
    found
}
