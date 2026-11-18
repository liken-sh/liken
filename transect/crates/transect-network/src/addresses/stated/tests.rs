use std::sync::Arc;

use k8s_openapi::api::core::v1::{Node, PodSpec, Service};
use rstest::{fixture, rstest};

use super::*;
use crate::entry::fixtures::labels;
use crate::entry::selects;
use transect_cluster::ownership;
use transect_graph::edges::{Stated as Written, Way};

fn deployment(name: &str) -> Object {
    Object::new("apps", "Deployment", Some("shop"), name)
}

fn workload(name: &str) -> Endpoint {
    Endpoint::Object(deployment(name))
}

fn service(name: &str) -> Endpoint {
    Endpoint::Object(Object::new("", "Service", Some("shop"), name))
}

/// A custom resource that writes the `Secret` login, which cache
/// reads.
fn producer() -> Object {
    Object::new("logins.example.com", "Login", Some("shop"), "cache-login")
}

fn settings() -> Object {
    Object::new("", "ConfigMap", Some("shop"), "settings")
}

fn yaml<K: serde::de::DeserializeOwned>(text: &str) -> K {
    serde_yaml::from_str(text).unwrap()
}

/// The namespace shop, where web and cache read the `ConfigMap`
/// settings and db is behind the `Service` db, and the machine alpha.
struct Shop {
    addresses: Addresses,
    graph: Graph,
    ownership: Ownership,
    plurals: Plurals,
    known: HashSet<Object>,
    readers: Readers,
}

#[fixture]
fn shop() -> Shop {
    let services: Vec<Arc<Service>> = vec![Arc::new(yaml(
        "metadata: {namespace: shop, name: db}\nspec: {ports: [{port: 5432}], selector: {app: db}}",
    ))];
    let nodes: Vec<Arc<Node>> = vec![Arc::new(yaml("metadata: {name: alpha}"))];
    let addresses = Addresses::build(&services, &[], &nodes, "cluster.local");
    let db = deployment("db");
    let db_labels = labels(&[("app", "db")]);
    let graph = Graph::new([selects(&services, [(&db, vec![&db_labels])])]);
    let ownership = ownership::fixtures::build(&[
        ("apps", "Deployment", Some("shop"), "web", vec![]),
        ("apps", "Deployment", Some("shop"), "db", vec![]),
        ("apps", "Deployment", Some("shop"), "cache", vec![]),
    ]);
    let plurals = [
        (("apps", "Deployment"), "deployments"),
        (("", "Service"), "services"),
        (("", "Node"), "nodes"),
        (("", "ConfigMap"), "configmaps"),
    ];
    let plurals = plurals
        .into_iter()
        .map(|((group, kind), plural)| ((group.into(), kind.into()), plural.into()))
        .collect();
    let known = HashSet::from([
        deployment("web"),
        deployment("db"),
        deployment("cache"),
        Object::new("", "Service", Some("shop"), "db"),
        Object::new("", "Node", None, "alpha"),
        settings(),
    ]);
    let reads: PodSpec =
        yaml("containers: [{name: app, envFrom: [{configMapRef: {name: settings}}]}]");
    let signs_in: PodSpec =
        yaml("containers: [{name: app, envFrom: [{secretRef: {name: login}}]}]");
    let (web, cache) = (deployment("web"), deployment("cache"));
    let readers = readers([(&web, vec![&reads]), (&cache, vec![&reads, &signs_in])]);
    let login = Object::new("", "Secret", Some("shop"), "login");
    let readers = readers.with_written(HashMap::from([(producer(), login)]));
    Shop {
        addresses,
        graph,
        ownership,
        plurals,
        known,
        readers,
    }
}

fn written() -> Provenance {
    Provenance::Intent(Written::Annotation {
        field: "depends-on".into(),
        on: "Deployment web".into(),
    })
}

/// Each edge's ends, and each unread entry by the name of the workload
/// whose card shows it.
type Drawn = (Vec<(Endpoint, Endpoint)>, Vec<(String, String)>);

impl Shop {
    /// What `entries` draw when they are listed on `on`.
    fn stated(&self, on: Object, entries: &[&str]) -> Drawn {
        let reading = Reading {
            addresses: &self.addresses,
            graph: &self.graph,
            ownership: &self.ownership,
            plurals: &self.plurals,
            known: &self.known,
            readers: &self.readers,
        };
        let listed = Listed {
            entries: entries.iter().map(|entry| entry.to_string()).collect(),
            provenance: written(),
        };
        let stated = Stated::read(Kind::DependsOn, &reading, [(&on, &listed)]);
        let edges = stated
            .edges
            .into_iter()
            .map(|edge| (edge.from, edge.to))
            .collect();
        let mut unread: Vec<(String, String)> = stated
            .unread
            .into_iter()
            .flat_map(|(object, entries)| {
                entries
                    .into_iter()
                    .map(move |u| (object.name.clone(), u.entry))
            })
            .collect();
        unread.sort();
        (edges, unread)
    }
}

#[rstest]
#[case::a_short_name("db", vec![workload("db")])]
#[case::a_long_name_and_port("db.shop.svc.cluster.local:5432", vec![workload("db")])]
#[case::a_url_outside("https://api.example.com/v1", vec![Endpoint::Outside("api.example.com".into())])]
#[case::a_host_outside("backups.example.com:443", vec![Endpoint::Outside("backups.example.com".into())])]
#[case::a_service_not_there("http://gone.shop:9000/check", vec![service("gone")])]
#[case::a_service_not_there_under_svc("gone.shop.svc", vec![service("gone")])]
#[case::a_service_not_there_in_full("gone.shop.svc.cluster.local:80", vec![service("gone")])]
#[case::two_labels_whose_second_is_no_namespace("files.archive", vec![Endpoint::Outside("files.archive".into())])]
#[case::a_workload("deployments/cache", vec![workload("cache")])]
#[case::a_service("shop/services/db", vec![workload("db")])]
#[case::a_machine("nodes/alpha", vec![Endpoint::Machine("alpha".into())])]
#[case::itself("deployments/web", vec![])]
fn a_workloads_entry_reaches_what_it_names(
    shop: Shop,
    #[case] entry: &str,
    #[case] to: Vec<Endpoint>,
) {
    let (edges, unread) = shop.stated(deployment("web"), &[entry]);
    let expected: Vec<(Endpoint, Endpoint)> =
        to.into_iter().map(|to| (workload("web"), to)).collect();
    assert_eq!((edges, unread), (expected, vec![]));
}

#[rstest]
#[case::an_object_that_is_not_there("deployments/gone")]
#[case::a_resource_not_served("widgets/web")]
#[case::prose("the billing team's API")]
fn an_entry_that_names_nothing_readable_stays_text(shop: Shop, #[case] entry: &str) {
    let (edges, unread) = shop.stated(deployment("web"), &[entry]);
    assert_eq!(
        (edges, unread),
        (vec![], vec![("web".into(), entry.into())])
    );
}

#[rstest]
fn a_config_maps_entry_reaches_from_each_workload_that_reads_it(shop: Shop) {
    let (edges, _) = shop.stated(settings(), &["db", "nonsense entry"]);
    let mut froms: Vec<Endpoint> = edges.into_iter().map(|(from, _)| from).collect();
    froms.sort();
    assert_eq!(froms, vec![workload("cache"), workload("web")]);
}

#[rstest]
fn a_producers_entry_reaches_from_each_workload_that_reads_its_secret(shop: Shop) {
    let (edges, unread) = shop.stated(producer(), &["db"]);
    assert_eq!(
        (edges, unread),
        (vec![(workload("cache"), workload("db"))], vec![])
    );
}

fn login() -> Object {
    Object::new("", "Secret", Some("shop"), "login")
}

#[rstest]
fn a_secrets_entry_reaches_from_each_workload_that_reads_it(shop: Shop) {
    let (edges, unread) = shop.stated(login(), &["db"]);
    assert_eq!(
        (edges, unread),
        (vec![(workload("cache"), workload("db"))], vec![])
    );
}

fn through(way: Way) -> Provenance {
    written().through(way)
}

/// The edge to db and the unread entry have the same provenance.  It
/// records whether transect read the list from an annotation, from a
/// `Secret`'s annotations, or through the object that writes a `Secret`.
#[rstest]
#[case::a_workloads_own(deployment("web"), written())]
#[case::a_config_maps(settings(), written())]
#[case::a_secrets_own(login(), through(Way::Annotations { secret: "login".into() }))]
#[case::a_producers(producer(), through(Way::Producer { secret: "login".into() }))]
fn records_how_a_list_was_read(shop: Shop, #[case] on: Object, #[case] expected: Provenance) {
    let reading = Reading {
        addresses: &shop.addresses,
        graph: &shop.graph,
        ownership: &shop.ownership,
        plurals: &shop.plurals,
        known: &shop.known,
        readers: &shop.readers,
    };
    let listed = Listed {
        entries: vec!["db".into(), "words here".into()],
        provenance: written(),
    };
    let stated = Stated::read(Kind::DependsOn, &reading, [(&on, &listed)]);
    let mut found: Vec<&Provenance> = stated.edges.iter().map(|edge| &edge.provenance).collect();
    found.extend(stated.unread.values().flatten().map(|u| &u.provenance));
    found.dedup();
    assert_eq!(found, vec![&expected]);
}

#[rstest]
fn a_list_on_what_calls_nothing_draws_nothing(shop: Shop) {
    let service = Object::new("", "Service", Some("shop"), "db");
    assert_eq!(
        shop.stated(service, &["api.example.com", "words here"]),
        (vec![], vec![])
    );
}

#[rstest]
fn an_authenticates_through_list_draws_its_own_kind(shop: Shop) {
    let reading = Reading {
        addresses: &shop.addresses,
        graph: &shop.graph,
        ownership: &shop.ownership,
        plurals: &shop.plurals,
        known: &shop.known,
        readers: &shop.readers,
    };
    let listed = Listed {
        entries: vec![
            "https://id.example.com/".into(),
            "a person at the desk".into(),
        ],
        provenance: written(),
    };
    let web = deployment("web");
    let stated = Stated::read(Kind::AuthenticatesThrough, &reading, [(&web, &listed)]);
    let kinds: Vec<Kind> = stated.edges.iter().map(|edge| edge.kind).collect();
    let unread: Vec<Kind> = stated.unread[&web].iter().map(|u| u.kind).collect();
    assert_eq!(
        (kinds, unread),
        (
            vec![Kind::AuthenticatesThrough],
            vec![Kind::AuthenticatesThrough]
        )
    );
}
