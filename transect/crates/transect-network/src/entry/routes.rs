//! The selects edges from each `Service` to its workloads, and the
//! routes-to edges from each `Ingress` to its `Service` objects.

use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;

use k8s_openapi::api::core::v1::Service;
use k8s_openapi::api::networking::v1::Ingress;
use kube::ResourceExt;

use transect_graph::edges::{Edge, Endpoint, Kind, Object, Provenance};

type Labels = BTreeMap<String, String>;

const INGRESS_GROUP: &str = "networking.k8s.io";

fn service_object(namespace: &str, name: &str) -> Object {
    Object::new("", "Service", Some(namespace), name)
}

fn ingress_object(namespace: &str, name: &str) -> Object {
    Object::new(INGRESS_GROUP, "Ingress", Some(namespace), name)
}

/// A selects edge from each `Service` to each workload in its namespace
/// that has a label set matching the `Service`'s selector.  `workloads`
/// pairs each top-level owner with the labels of its pods, or of its pod
/// templates when it has no pods, so that a workload scaled to zero
/// still matches.  A `Service` with no selector or an empty one has its
/// endpoints managed by hand, and selects nothing.
pub fn selects<'a>(
    services: &[Arc<Service>],
    workloads: impl IntoIterator<Item = (&'a Object, Vec<&'a Labels>)>,
) -> Vec<Edge> {
    let mut by_namespace: HashMap<String, Vec<(String, &Labels)>> = HashMap::new();
    for service in services {
        let selector = service
            .spec
            .as_ref()
            .and_then(|spec| spec.selector.as_ref());
        let (Some(namespace), Some(selector)) = (service.namespace(), selector) else {
            continue;
        };
        if !selector.is_empty() {
            let entry = by_namespace.entry(namespace).or_default();
            entry.push((service.name_any(), selector));
        }
    }
    let mut edges = Vec::new();
    for (workload, labels) in workloads {
        let Some(namespace) = workload.namespace.as_deref() else {
            continue;
        };
        for (name, selector) in by_namespace.get(namespace).into_iter().flatten() {
            let matches = |labels: &&Labels| {
                selector
                    .iter()
                    .all(|(key, value)| labels.get(key) == Some(value))
            };
            if labels.iter().any(matches) {
                edges.push(Edge {
                    kind: Kind::Selects,
                    from: Endpoint::Object(service_object(namespace, name)),
                    to: Endpoint::Object(workload.clone()),
                    guessed: false,
                    provenance: Provenance::Kubernetes,
                });
            }
        }
    }
    edges
}

/// One routes-to edge from each `Ingress` to each `Service` in its
/// rules or its default backend.  A `resource` backend adds no edge.
pub fn routes_to(ingresses: &[Arc<Ingress>]) -> Vec<Edge> {
    let mut edges = Vec::new();
    for ingress in ingresses {
        let (Some(namespace), Some(spec)) = (ingress.namespace(), ingress.spec.as_ref()) else {
            continue;
        };
        let rules = spec.rules.iter().flatten();
        let paths = rules.flat_map(|rule| rule.http.iter().flat_map(|http| &http.paths));
        let backends = spec
            .default_backend
            .iter()
            .chain(paths.map(|path| &path.backend));
        let mut names: Vec<&str> = Vec::new();
        for service in backends.filter_map(|backend| backend.service.as_ref()) {
            if !names.contains(&service.name.as_str()) {
                names.push(&service.name);
            }
        }
        let from = Endpoint::Object(ingress_object(&namespace, &ingress.name_any()));
        edges.extend(names.into_iter().map(|name| Edge {
            kind: Kind::RoutesTo,
            from: from.clone(),
            to: Endpoint::Object(service_object(&namespace, name)),
            guessed: false,
            provenance: Provenance::Kubernetes,
        }));
    }
    edges
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::entry::fixtures::{ingress, labels, service};

    fn deployment(namespace: &str, name: &str) -> Object {
        Object::new("apps", "Deployment", Some(namespace), name)
    }

    /// The names of each edge's two ends.
    fn ends(edges: Vec<Edge>) -> Vec<(String, String)> {
        let name = |end: Endpoint| match end {
            Endpoint::Object(object) => object.name,
            other => format!("{other:?}"),
        };
        let pairs = edges
            .into_iter()
            .map(|edge| (name(edge.from), name(edge.to)));
        pairs.collect()
    }

    #[rstest]
    #[case::a_matching_selector(service("shop", "web", "{app: web}"), "shop", &[("app", "web"), ("hash", "5d8f")], vec![("web", "web")])]
    #[case::needs_every_label(service("shop", "web", "{app: web, tier: front}"), "shop", &[("app", "web")], vec![])]
    #[case::another_namespace(service("blog", "web", "{app: web}"), "shop", &[("app", "web")], vec![])]
    #[case::an_empty_selector(service("shop", "web", "{}"), "shop", &[("app", "web")], vec![])]
    fn a_service_selects_a_workload_by_its_pods_labels(
        #[case] service: Arc<Service>,
        #[case] namespace: &str,
        #[case] pod_labels: &[(&str, &str)],
        #[case] expected: Vec<(&str, &str)>,
    ) {
        let workload = deployment(namespace, "web");
        let pod_labels = labels(pod_labels);
        let edges = selects(&[service], [(&workload, vec![&pod_labels])]);
        let expected: Vec<(String, String)> = expected
            .into_iter()
            .map(|(a, b)| (a.into(), b.into()))
            .collect();
        assert_eq!(ends(edges), expected);
    }

    #[rstest]
    fn a_service_with_no_selector_selects_nothing() {
        let service: Service =
            serde_yaml_ng::from_str("metadata: {namespace: shop, name: web}\nspec: {}").unwrap();
        let workload = deployment("shop", "web");
        let pod_labels = labels(&[("app", "web")]);
        let edges = selects(&[Arc::new(service)], [(&workload, vec![&pod_labels])]);
        assert_eq!(edges, vec![]);
    }

    #[rstest]
    #[case::a_rule("{rules: [{host: shop.example.com, http: {paths: [{path: /, pathType: Prefix, backend: {service: {name: web, port: {number: 80}}}}]}}]}", vec!["web"])]
    #[case::the_default_backend("{defaultBackend: {service: {name: web, port: {number: 80}}}}", vec!["web"])]
    #[case::each_service_once("{rules: [{http: {paths: [{path: /, pathType: Prefix, backend: {service: {name: web, port: {number: 80}}}}, {path: /api, pathType: Prefix, backend: {service: {name: api, port: {number: 80}}}}, {path: /static, pathType: Prefix, backend: {service: {name: web, port: {number: 80}}}}]}}]}", vec!["web", "api"])]
    #[case::a_resource_backend("{defaultBackend: {resource: {kind: Bucket, name: assets}}}", vec![])]
    fn an_ingress_routes_to_the_services_its_backends_name(
        #[case] spec: &str,
        #[case] expected: Vec<&str>,
    ) {
        let expected: Vec<(String, String)> = expected
            .into_iter()
            .map(|name| ("front".into(), name.into()))
            .collect();
        assert_eq!(ends(routes_to(&[ingress("front", spec)])), expected);
    }
}
