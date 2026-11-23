use std::collections::HashSet;
use std::sync::Arc;

use k8s_openapi::api::core::v1::{Node, Service};
use rstest::{fixture, rstest};

use super::*;
use crate::addresses::{Addresses, Readers};
use crate::entry::fixtures::{labels, service};
use crate::entry::selects;
use transect_cluster::ownership::{self, Ownership};
use transect_graph::edges::fixtures::device;
use transect_graph::edges::{Edge, Graph, Kind, Plurals, Provenance};
use transect_resources::knowledge::edges;

fn jukebox() -> Object {
    Object::new("widgets.example.com", "Jukebox", Some("shop"), "jukebox-1")
}

fn broker() -> Object {
    Object::new("apps", "Deployment", Some("messages"), "broker")
}

fn dial() -> Object {
    Object::new("widgets.example.com", "Dial", Some("shop"), "dial-1")
}

fn amplifier() -> Object {
    Object::new("amps.example.com", "Amplifier", None, "amp")
}

fn claim() -> Object {
    Object::new("resource.k8s.io", "ResourceClaim", Some("shop"), "idle")
}

fn idle_pod() -> Object {
    Object::new("", "Pod", Some("shop"), "jukebox-1-idle")
}

/// A `Jukebox` in `shop` that owns the pod `jukebox-1-idle`, a `Dial`
/// in `shop`, a `Deployment` in `messages` behind the `Service`
/// `broker`, and the node `alpha`. The `ResourceClaim` `idle` has a
/// radio and a GPU, and the graph already has the `Jukebox`'s edge to
/// the radio.
struct Cluster {
    addresses: Addresses,
    graph: Graph,
    ownership: Ownership,
    plurals: Plurals,
    known: HashSet<Object>,
    readers: Readers,
    devices: Devices,
}

#[fixture]
fn cluster() -> Cluster {
    let services: Vec<Arc<Service>> = vec![service("messages", "broker", "{app: broker}")];
    let nodes: Vec<Arc<Node>> = vec![Arc::new(
        serde_yaml_ng::from_str("metadata: {name: alpha}").unwrap(),
    )];
    let addresses = Addresses::build(&services, &[], &nodes, "cluster.local");
    let broker_labels = labels(&[("app", "broker")]);
    let broker = broker();
    let mut graph = Graph::new([selects(&services, [(&broker, vec![&broker_labels])])]);
    graph.add(vec![Edge {
        kind: Kind::Holds,
        from: Endpoint::Object(jukebox()),
        to: Endpoint::Device(device("radio.example.com", "alpha", "usb-1")),
        guessed: false,
        provenance: Provenance::Kubernetes,
    }]);
    let ownership = ownership::fixtures::build(&[
        (
            "widgets.example.com",
            "Jukebox",
            Some("shop"),
            "jukebox-1",
            vec![],
        ),
        (
            "widgets.example.com",
            "Dial",
            Some("shop"),
            "dial-1",
            vec![],
        ),
        ("apps", "Deployment", Some("messages"), "broker", vec![]),
        (
            "",
            "Pod",
            Some("shop"),
            "jukebox-1-idle",
            vec![("widgets.example.com/v1", "Jukebox", "jukebox-1")],
        ),
    ]);
    let known = HashSet::from([
        jukebox(),
        dial(),
        broker,
        idle_pod(),
        Object::new("", "Service", Some("messages"), "broker"),
        Object::new("", "Node", None, "alpha"),
    ]);
    let devices = Devices::with_claim(
        "shop",
        "idle",
        vec![
            device("radio.example.com", "alpha", "usb-1"),
            device("gpu.example.com", "alpha", "card-0"),
        ],
    );
    Cluster {
        addresses,
        graph,
        ownership,
        plurals: Plurals::default(),
        known,
        readers: Readers::default(),
        devices,
    }
}

fn relation(edge: Option<Kind>, value: &str, names: Names) -> Relation {
    Relation {
        label: "names".into(),
        path: "status.a".into(),
        edge,
        value: value.into(),
        names,
        provenance: Provenance::Kinds {
            knowledge: "jukeboxes.widgets.example.com".into(),
            field: "status.a".into(),
        },
    }
}

fn object(object: Object) -> Names {
    Names::Object(object)
}

impl Cluster {
    fn reading(&self) -> StatedReading<'_> {
        StatedReading {
            addresses: &self.addresses,
            graph: &self.graph,
            ownership: &self.ownership,
            plurals: &self.plurals,
            known: &self.known,
            readers: &self.readers,
        }
    }

    fn reached(&self, relation: &Relation) -> Reached {
        reached(relation, &jukebox(), &self.reading(), &self.devices)
    }

    fn edges(&self, relations: &[Relation]) -> Vec<(Kind, Endpoint)> {
        let reading = self.reading();
        let owns = |end: &Endpoint| reading.owns(&jukebox(), end);
        let drawn = edges(
            &jukebox(),
            relations,
            |r| self.reached(r),
            owns,
            &self.graph,
        );
        drawn.into_iter().map(|edge| (edge.kind, edge.to)).collect()
    }
}

#[rstest]
#[case::a_node_is_its_machine(
    object(Object::new("", "Node", None, "alpha")),
    "alpha",
    vec![Endpoint::Machine("alpha".into())]
)]
#[case::an_address_in_another_namespace(
    Names::Address,
    "broker.messages.svc:80",
    vec![Endpoint::Object(broker())]
)]
#[case::an_address_outside_the_cluster(
    Names::Address,
    "https://api.example.com/v1",
    vec![Endpoint::Outside("api.example.com".into())]
)]
#[case::not_one_address(Names::Address, "two words", vec![])]
#[case::a_watched_object(object(dial()), "dial-1", vec![Endpoint::Object(dial())])]
#[case::a_kind_transect_does_not_watch(
    object(amplifier()),
    "amp",
    vec![Endpoint::Object(amplifier())]
)]
#[case::a_claim_is_its_devices(
    object(claim()),
    "idle",
    vec![
        Endpoint::Device(device("radio.example.com", "alpha", "usb-1")),
        Endpoint::Device(device("gpu.example.com", "alpha", "card-0")),
    ]
)]
#[case::a_value(Names::Value, "tv-1", vec![])]
fn resolves_each_relation_to_its_endpoints(
    cluster: Cluster,
    #[case] names: Names,
    #[case] value: &str,
    #[case] ends: Vec<Endpoint>,
) {
    let found = cluster.reached(&relation(Some(Kind::DependsOn), value, names));
    assert_eq!(found.ends, ends);
}

#[rstest]
fn draws_only_edges_the_graph_and_ownership_lack(cluster: Cluster) {
    let relations = [
        relation(
            Some(Kind::RunsOn),
            "alpha",
            object(Object::new("", "Node", None, "alpha")),
        ),
        relation(Some(Kind::DependsOn), "jukebox-1-idle", object(idle_pod())),
        relation(Some(Kind::Holds), "idle", object(claim())),
        relation(None, "tv-1", Names::Value),
        relation(Some(Kind::DependsOn), "dial-1", object(dial())),
        relation(Some(Kind::DependsOn), "dial-1", object(dial())),
    ];
    assert_eq!(
        cluster.edges(&relations),
        vec![
            (Kind::RunsOn, Endpoint::Machine("alpha".into())),
            (
                Kind::Holds,
                Endpoint::Device(device("gpu.example.com", "alpha", "card-0"))
            ),
            (Kind::DependsOn, Endpoint::Object(dial())),
        ]
    );
}
