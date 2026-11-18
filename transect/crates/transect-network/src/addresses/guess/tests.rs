use std::collections::HashSet;
use std::sync::Arc;

use k8s_openapi::api::core::v1::{Node, Service};
use rstest::{fixture, rstest};

use super::*;
use crate::addresses::{Addresses, Readers, StatedReading};
use crate::entry::fixtures::{labels, service};
use crate::entry::selects;
use transect_cluster::ownership::{self, Ownership};
use transect_graph::edges::ClusterScoped;
use transect_graph::edges::{Endpoint, Kind, Plurals, Provenance};
use transect_resources::knowledge::fixtures::knowledge;

fn widget() -> Object {
    Object::new("widgets.example.com", "Widget", Some("shop"), "front")
}

fn deployment(name: &str) -> Object {
    Object::new("apps", "Deployment", Some("shop"), name)
}

/// The `shop` namespace: a `Deployment` `db` behind the `Service`
/// `db`, a `Deployment` and a `ConfigMap` that are both named `cache`,
/// a `Deployment` `queue`, a `Widget` `front`, and the node `alpha`.
struct Shop {
    addresses: Addresses,
    graph: Graph,
    ownership: Ownership,
    plurals: Plurals,
    scoped: ClusterScoped,
    watched: HashSet<Object>,
    readers: Readers,
}

#[fixture]
fn shop() -> Shop {
    let services: Vec<Arc<Service>> = vec![service("shop", "db", "{app: db}")];
    let nodes: Vec<Arc<Node>> = vec![Arc::new(
        serde_yaml::from_str("metadata: {name: alpha}").unwrap(),
    )];
    let addresses = Addresses::build(&services, &[], &nodes, "cluster.local");
    let db = deployment("db");
    let db_labels = labels(&[("app", "db")]);
    let graph = Graph::new([selects(&services, [(&db, vec![&db_labels])])]);
    let ownership = ownership::fixtures::build(&[
        (
            "widgets.example.com",
            "Widget",
            Some("shop"),
            "front",
            vec![],
        ),
        ("apps", "Deployment", Some("shop"), "db", vec![]),
        ("apps", "Deployment", Some("shop"), "cache", vec![]),
        ("apps", "Deployment", Some("shop"), "queue", vec![]),
    ]);
    let kinds = [
        ("apps", "Deployment"),
        ("", "Service"),
        ("", "Node"),
        ("", "Secret"),
        ("", "ConfigMap"),
        ("widgets.example.com", "Widget"),
    ];
    let plurals = kinds
        .into_iter()
        .map(|(group, kind)| {
            (
                (group.into(), kind.into()),
                format!("{}s", kind.to_lowercase()),
            )
        })
        .collect();
    let watched = HashSet::from([
        widget(),
        db,
        deployment("cache"),
        deployment("queue"),
        Object::new("", "ConfigMap", Some("shop"), "cache"),
        Object::new("", "Service", Some("shop"), "db"),
        Object::new("", "Node", None, "alpha"),
    ]);
    Shop {
        addresses,
        graph,
        ownership,
        plurals,
        scoped: ClusterScoped::from([("".to_string(), "Node".to_string())]),
        watched,
        readers: Readers::default(),
    }
}

impl Shop {
    fn guesses(&self, kinds: &Kinds, yaml: &str) -> Vec<(Endpoint, String)> {
        let reading = StatedReading {
            addresses: &self.addresses,
            graph: &self.graph,
            ownership: &self.ownership,
            plurals: &self.plurals,
            known: &self.watched,
            readers: &self.readers,
        };
        let lookup = Lookup {
            plurals: &self.plurals,
            scoped: &self.scoped,
            watched: &self.watched,
            reading: &reading,
        };
        let data = serde_yaml::from_str(yaml).unwrap();
        let edges = guesses(kinds, &widget(), &data, &lookup, &self.graph);
        assert!(edges.iter().all(|e| e.guessed && e.kind == Kind::DependsOn));
        edges
            .into_iter()
            .map(|edge| match edge.provenance {
                Provenance::Guess { field } => (edge.to, field),
                other => panic!("the edge has provenance {other:?}, want a guess"),
            })
            .collect()
    }
}

fn to(object: Object, field: &str) -> (Endpoint, String) {
    (Endpoint::Object(object), field.into())
}

#[rstest]
#[case::a_secret_ref(
    "spec: {secretRef: {name: login}}",
    vec![to(Object::new("", "Secret", Some("shop"), "login"), "spec.secretRef")]
)]
#[case::a_service_name_is_the_workload_behind_it(
    "spec: {serviceName: db}",
    vec![to(deployment("db"), "spec.serviceName")]
)]
#[case::a_node_name_is_its_machine(
    "status: {nodeName: alpha}",
    vec![(Endpoint::Machine("alpha".into()), "status.nodeName".into())]
)]
#[case::a_name_matching_one_object("spec: {backendName: queue}", vec![to(deployment("queue"), "spec.backendName")])]
#[case::a_name_matching_two("spec: {backendName: cache}", vec![])]
#[case::a_name_matching_none("spec: {backendName: gone}", vec![])]
#[case::a_service_that_is_not_there("spec: {serviceName: gone}", vec![])]
#[case::an_address_in_the_cluster(
    "spec: {upstream: 'db.shop.svc:80'}",
    vec![to(deployment("db"), "spec.upstream")]
)]
#[case::an_address_outside_draws_nothing("spec: {upstream: 'https://api.example.com/'}", vec![])]
#[case::itself("spec: {parentRef: {kind: Widget, name: front}}", vec![])]
fn guesses_what_each_field_names(
    shop: Shop,
    #[case] yaml: &str,
    #[case] expected: Vec<(Endpoint, String)>,
) {
    assert_eq!(shop.guesses(&Kinds::default(), yaml), expected);
}

#[rstest]
fn a_kind_with_knowledge_gets_no_guesses(shop: Shop) {
    let plurals = Plurals::from([(
        ("widgets.example.com".to_string(), "Widget".to_string()),
        "widgets".to_string(),
    )]);
    let kinds = Kinds::resolve(
        &[knowledge("widgets.widgets.example.com", "{}")],
        &plurals,
        &[],
    );
    assert_eq!(shop.guesses(&kinds, "spec: {serviceName: db}"), vec![]);
}
