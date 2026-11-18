//! The backends that a request to each host of an Ingress may reach.

use k8s_openapi::api::networking::v1::{Ingress, IngressBackend};

use super::Sources;
use transect_graph::edges::Object;

/// For each host in `ingress`'s rules, or "any host" for a rule with no
/// host, the backends that a request to that host may reach.  These are
/// the backends of the host's rule paths, plus the default backend
/// unless a path of `/` that is not `Exact` already takes every
/// request.  The default backend also gets an "any host" entry, for
/// hosts that no rule lists.
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

/// The workloads that the Services in `backends` select, where
/// `backends` belong to an Ingress in `namespace`.
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
