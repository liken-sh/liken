//! The entry: how a request reaches a workload.  An `Ingress` routes
//! a host to a `Service` by its backend, and a `Service` selects the
//! workload's pods by their labels.
//!
//! This module builds the selects and routes-to edges from each
//! `Service` and `Ingress`, as every edge kind is built in its own
//! domain.  The workload page follows the edges backward from the
//! workload, to draw each `Service` that reaches it and each `Ingress`
//! that reaches those.

pub mod controller;
pub mod entry_points;
mod ingress;
pub mod poll;
mod queries;
mod routes;
mod sentence;
mod service;
pub mod serving;
pub mod traffic;

use std::collections::HashMap;
use std::sync::Arc;

use k8s_openapi::api::core::v1::Service;
use k8s_openapi::api::networking::v1::Ingress;
use kube::ResourceExt;

pub use controller::IngressController;
pub use ingress::{IngressBox, Tls};
pub use queries::EntryQueries;
pub use routes::{routes_to, selects};
pub use service::{Endpoints, ServiceBox};
pub use serving::{Serving, runs};
pub use traffic::Answers;

use transect_graph::edges::{Endpoint, Graph, Kind, Object, Plurals};

/// The issuer of each `Ingress`'s TLS certificate, by the `Ingress`.
pub type Issuers = HashMap<Object, Object>;

/// The Ingresses and Services that reach one workload.
#[derive(Debug, Default, PartialEq)]
pub struct Entry {
    pub ingresses: Vec<IngressBox>,
    pub services: Vec<ServiceBox>,
}

impl Entry {
    /// The Services whose selects edges reach `workload` in
    /// `graph`, and the Ingresses whose routes-to edges reach
    /// those Services, each group sorted by name.
    pub fn build(
        graph: &Graph,
        workload: &Object,
        services: &[Arc<Service>],
        ingresses: &[Arc<Ingress>],
        endpoints: &Endpoints,
        issuers: &Issuers,
    ) -> Self {
        let none = Plurals::default();
        Self::served(
            graph,
            workload,
            services,
            ingresses,
            endpoints,
            issuers,
            (&Serving::default(), &none),
        )
    }

    /// `build`, with the workload that runs each `Ingress`'s
    /// controller, as `serving` finds it.
    pub fn served(
        graph: &Graph,
        workload: &Object,
        services: &[Arc<Service>],
        ingresses: &[Arc<Ingress>],
        endpoints: &Endpoints,
        issuers: &Issuers,
        (serving, plurals): (&Serving, &Plurals),
    ) -> Self {
        let to_workload = Endpoint::Object(workload.clone());
        let reaching_services = graph.reaching(Kind::Selects, &to_workload);
        let mut found_services: Vec<ServiceBox> = reaching_services
            .iter()
            .filter_map(|end| find(services, end))
            .map(|service| ServiceBox::build(service, endpoints))
            .collect();
        found_services.sort_by(|a, b| a.name.cmp(&b.name));
        let mut reaching_ingresses: Vec<&Endpoint> = Vec::new();
        for service in reaching_services {
            for ingress in graph.reaching(Kind::RoutesTo, service) {
                if !reaching_ingresses.contains(&ingress) {
                    reaching_ingresses.push(ingress);
                }
            }
        }
        let mut found_ingresses: Vec<IngressBox> = reaching_ingresses
            .into_iter()
            .filter_map(|end| Some((end, find(ingresses, end)?)))
            .map(|(end, ingress)| {
                let issuer = match end {
                    Endpoint::Object(object) => issuers.get(object),
                    _ => None,
                };
                let issuer = issuer.map(|issuer| issued_by(issuer, plurals));
                IngressBox::build(ingress, issuer).served_by(serving, plurals)
            })
            .collect();
        found_ingresses.sort_by(|a, b| a.name.cmp(&b.name));
        Self {
            ingresses: found_ingresses,
            services: found_services,
        }
    }
}

impl Entry {
    /// The entry with each `Ingress`'s traffic from `answers`.
    pub fn with_traffic(self, answers: Answers) -> Self {
        let ingresses = self.ingresses.into_iter();
        Self {
            ingresses: ingresses
                .map(|ingress| ingress.with_traffic(answers))
                .collect(),
            ..self
        }
    }
}

/// `issuer` as an `Ingress`'s TLS line names it: by its name, linked
/// to its page.
pub fn issued_by(issuer: &Object, plurals: &Plurals) -> transect_graph::links::Reference {
    transect_graph::links::Reference {
        text: issuer.name.clone(),
        href: transect_graph::links::page(issuer, plurals),
    }
}

/// The object of `objects` that `end` names.
fn find<'a, K: kube::Resource>(objects: &'a [Arc<K>], end: &Endpoint) -> Option<&'a K> {
    let Endpoint::Object(object) = end else {
        return None;
    };
    let objects = objects.iter().map(AsRef::as_ref);
    objects.into_iter().find(|candidate| {
        candidate.meta().namespace == object.namespace && candidate.name_any() == object.name
    })
}

#[cfg(test)]
pub mod fixtures {
    use std::collections::BTreeMap;

    use super::*;

    pub fn labels(pairs: &[(&str, &str)]) -> BTreeMap<String, String> {
        let pairs = pairs.iter().map(|(k, v)| (k.to_string(), v.to_string()));
        pairs.collect()
    }

    /// A `ClusterIP` `Service` on port 80 with `selector` as YAML.
    pub fn service(namespace: &str, name: &str, selector: &str) -> Arc<Service> {
        Arc::new(
            serde_yaml::from_str(&format!(
                "metadata: {{namespace: {namespace}, name: {name}}}\nspec: {{selector: {selector}, ports: [{{port: 80}}]}}"
            ))
            .unwrap(),
        )
    }

    /// An `Ingress` in shop with `spec` as YAML.
    pub fn ingress(name: &str, spec: &str) -> Arc<Ingress> {
        Arc::new(
            serde_yaml::from_str(&format!(
                "metadata: {{namespace: shop, name: {name}}}\nspec: {spec}"
            ))
            .unwrap(),
        )
    }

    /// An `Ingress` in shop that routes `host` to the `Service` `service`.
    pub fn routing(name: &str, host: &str, service: &str) -> Arc<Ingress> {
        ingress(
            name,
            &format!(
                "{{rules: [{{host: {host}, http: {{paths: [{{path: /, pathType: Prefix, backend: {{service: {{name: {service}, port: {{number: 80}}}}}}}}]}}}}]}}"
            ),
        )
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::*;
    use super::*;

    fn deployment(name: &str) -> Object {
        Object::new("apps", "Deployment", Some("shop"), name)
    }

    /// The names of the Ingresses and Services that reach
    /// `workload`, in a shop whose web and api Deployments each
    /// have a `Service`, whose web `Service` has two Ingresses,
    /// and whose worker has neither.
    fn reaching(workload: &str) -> (Vec<String>, Vec<String>) {
        let services = [
            service("shop", "web", "{app: web}"),
            service("shop", "web-internal", "{app: web}"),
            service("shop", "api", "{app: api}"),
        ];
        let ingresses = [
            routing("public", "shop.example.com", "web"),
            routing("admin", "admin.example.com", "web"),
            routing("internal", "web.internal", "web-internal"),
            routing("stale", "old.example.com", "gone"),
        ];
        let (web, api, worker) = (deployment("web"), deployment("api"), deployment("worker"));
        let (web_labels, api_labels) = (labels(&[("app", "web")]), labels(&[("app", "api")]));
        let worker_labels = labels(&[("app", "worker")]);
        let graph = Graph::new([
            selects(
                &services,
                [
                    (&web, vec![&web_labels]),
                    (&api, vec![&api_labels]),
                    (&worker, vec![&worker_labels]),
                ],
            ),
            routes_to(&ingresses),
        ]);
        let found = Entry::build(
            &graph,
            &deployment(workload),
            &services,
            &ingresses,
            &Endpoints::default(),
            &Issuers::new(),
        );
        let names = |names: Vec<&String>| names.into_iter().cloned().collect();
        (
            names(found.ingresses.iter().map(|i| &i.name).collect()),
            names(found.services.iter().map(|s| &s.name).collect()),
        )
    }

    #[rstest]
    #[case::two_services_and_three_ingresses("web", &["admin", "internal", "public"], &["web", "web-internal"])]
    #[case::a_service_and_no_ingress("api", &[], &["api"])]
    #[case::neither("worker", &[], &[])]
    fn finds_what_reaches_a_workload(
        #[case] workload: &str,
        #[case] ingresses: &[&str],
        #[case] services: &[&str],
    ) {
        assert_eq!(reaching(workload), (strings(ingresses), strings(services)));
    }

    fn strings(names: &[&str]) -> Vec<String> {
        names.iter().map(|name| name.to_string()).collect()
    }
}
