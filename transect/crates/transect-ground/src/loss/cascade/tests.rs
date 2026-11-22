use rstest::{fixture, rstest};

use super::*;
use transect_graph::edges::{Edge, Provenance};

/// An `Ingress` in shop with `spec` as YAML.
fn ingress(name: &str, spec: &str) -> Arc<Ingress> {
    let yaml = format!("metadata: {{namespace: shop, name: {name}}}\nspec: {spec}");
    Arc::new(serde_yaml::from_str(&yaml).unwrap())
}

/// An `Ingress` in shop that routes `host` to the `Service` `service`.
fn routing(name: &str, host: &str, service: &str) -> Arc<Ingress> {
    let backend = format!("{{service: {{name: {service}, port: {{number: 80}}}}}}");
    let path = format!("{{path: /, pathType: Prefix, backend: {backend}}}");
    ingress(
        name,
        &format!("{{rules: [{{host: {host}, http: {{paths: [{path}]}}}}]}}"),
    )
}

fn deployment(namespace: &str, name: &str) -> Object {
    Object::new("apps", "Deployment", Some(namespace), name)
}

fn jukebox(name: &str) -> Object {
    Object::new("media.example.com", "Jukebox", Some("media"), name)
}

fn service(name: &str) -> Object {
    Object::new("", "Service", Some("shop"), name)
}

fn ingress_object(name: &str) -> Object {
    Object::new("networking.k8s.io", "Ingress", Some("shop"), name)
}

fn edge(kind: Kind, from: Object, to: Object) -> Edge {
    Edge {
        kind,
        from: Endpoint::Object(from),
        to: Endpoint::Object(to),
        guessed: false,
        provenance: Provenance::Kubernetes,
    }
}

/// A shop namespace.  `web`, and `feed` in blog, call its `db`.  Two
/// hosts and a default backend route to `web` through its `Service`, and
/// its `operator` controls two Jukeboxes in media.  No edge arrives at
/// `cache`.
struct Shop {
    graph: Graph,
    ingresses: Vec<Arc<Ingress>>,
}

#[fixture]
fn shop() -> Shop {
    let ingresses = vec![
        routing("public", "shop.example.com", "web"),
        routing("admin", "admin.example.com", "web"),
        routing("other", "api.example.com", "api"),
        ingress(
            "fallback",
            "{defaultBackend: {service: {name: web, port: {number: 80}}}}",
        ),
    ];
    let graph = Graph::new([vec![
        edge(
            Kind::DependsOn,
            deployment("shop", "web"),
            deployment("shop", "db"),
        ),
        edge(
            Kind::DependsOn,
            deployment("blog", "feed"),
            deployment("shop", "db"),
        ),
        edge(Kind::Selects, service("web"), deployment("shop", "web")),
        edge(Kind::RoutesTo, ingress_object("public"), service("web")),
        edge(Kind::RoutesTo, ingress_object("admin"), service("web")),
        edge(Kind::RoutesTo, ingress_object("fallback"), service("web")),
        edge(
            Kind::ControlledBy,
            jukebox("den"),
            deployment("shop", "operator"),
        ),
        edge(
            Kind::ControlledBy,
            jukebox("attic"),
            deployment("shop", "operator"),
        ),
    ]]);
    Shop { graph, ingresses }
}

impl Shop {
    fn of(&self, name: &str) -> Vec<Dependent> {
        let edges = Edges {
            graph: &self.graph,
            ingresses: &self.ingresses,
            intents: &Intents::default(),
        };
        let losses = edges.of(&deployment("shop", name)).into_iter();
        losses.map(|loss| loss.dependent).collect()
    }
}

#[rstest]
#[case::callers_by_namespace(
    "db",
    vec![
        Dependent::Caller(deployment("blog", "feed")),
        Dependent::Caller(deployment("shop", "web")),
    ]
)]
#[case::hosts_by_name(
    "web",
    vec![
        Dependent::Host("admin.example.com".into()),
        Dependent::Host("shop.example.com".into()),
    ]
)]
#[case::custom_resources_it_controls(
    "operator",
    vec![
        Dependent::Controlled(jukebox("attic")),
        Dependent::Controlled(jukebox("den")),
    ]
)]
#[case::nothing_arrives(
    "cache",
    vec![]
)]
fn finds_the_dependents_of_a_down_workload(
    shop: Shop,
    #[case] name: &str,
    #[case] dependents: Vec<Dependent>,
) {
    assert_eq!(shop.of(name), dependents);
}

fn loss(dependent: Dependent, lost: &str) -> Loss {
    Loss {
        dependent,
        lost: deployment("shop", lost),
        provides: None,
        declared_in: None,
    }
}

#[rstest]
fn hops_join_one_dependents_losses_by_namespace() {
    let web = || Dependent::Caller(deployment("shop", "web"));
    let found = hops([
        loss(web(), "db"),
        loss(Dependent::Host("shop.example.com".into()), "web"),
        loss(web(), "cache"),
        loss(Dependent::Caller(deployment("blog", "feed")), "db"),
        loss(web(), "cache"),
    ]);
    assert_eq!(
        found,
        vec![
            Hop {
                namespace: Some("blog".into()),
                dependent: Dependent::Caller(deployment("blog", "feed")),
                lost: vec![deployment("shop", "db")],
                provides: BTreeMap::new(),
                declared_in: Vec::new(),
            },
            Hop {
                namespace: Some("shop".into()),
                dependent: web(),
                lost: vec![deployment("shop", "cache"), deployment("shop", "db")],
                provides: BTreeMap::new(),
                declared_in: Vec::new(),
            },
            Hop {
                namespace: Some("shop".into()),
                dependent: Dependent::Host("shop.example.com".into()),
                lost: vec![deployment("shop", "web")],
                provides: BTreeMap::new(),
                declared_in: Vec::new(),
            },
        ]
    );
}

fn taken(name: &str, state: crate::loss::State) -> crate::loss::Taken {
    crate::loss::Taken {
        object: deployment("shop", name),
        state,
        stranded: None,
        provides: None,
        vital: None,
    }
}

/// A loss leaves `db` and `api` down and `web` short.  `web` and `api`
/// both call `db`, and `feed` calls `web`.
#[rstest]
fn only_a_down_workload_cascades_and_never_into_another_down_one() {
    use crate::loss::State;
    let graph = Graph::new([vec![
        edge(
            Kind::DependsOn,
            deployment("shop", "web"),
            deployment("shop", "db"),
        ),
        edge(
            Kind::DependsOn,
            deployment("shop", "api"),
            deployment("shop", "db"),
        ),
        edge(
            Kind::DependsOn,
            deployment("blog", "feed"),
            deployment("shop", "web"),
        ),
    ]]);
    let edges = Edges {
        graph: &graph,
        ingresses: &[],
        intents: &Intents::default(),
    };
    let taken = [
        taken("db", State::Down { wanted: 1 }),
        taken(
            "web",
            State::Short {
                ready: 1,
                wanted: 2,
            },
        ),
        taken("api", State::Down { wanted: 1 }),
    ];
    assert_eq!(
        crate::loss::cascade_of(&taken, &edges),
        vec![Loss {
            dependent: Dependent::Caller(deployment("shop", "web")),
            lost: deployment("shop", "db"),
            provides: None,
            declared_in: None,
        }]
    );
}

#[rstest]
fn a_loss_has_the_provides_text_of_the_lost_workload(shop: Shop) {
    let meta: k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta = serde_yaml::from_str(
        "{name: db, annotations: {transect.liken.sh/provides: order storage}}",
    )
    .unwrap();
    let objects = [(deployment("shop", "db"), &meta)];
    let overlaid = transect_resources::resources::Overlaid::default();
    let intents = Intents::read(&objects, &[], &overlaid, &Default::default());
    let edges = Edges {
        graph: &shop.graph,
        ingresses: &shop.ingresses,
        intents: &intents,
    };
    let provides: Vec<Option<String>> = edges
        .of(&deployment("shop", "db"))
        .into_iter()
        .map(|loss| loss.provides.map(|line| line.text))
        .collect();
    assert_eq!(provides, vec![Some("order storage".to_string()); 2]);
}

fn intent(field: &str) -> Provenance {
    Provenance::Intent(transect_graph::edges::Stated::Annotation {
        field: field.into(),
        on: "Deployment wiki".into(),
    })
}

/// `wiki` signs its users in through `login` by intent, and `api`
/// calls `login` by its configured address.
#[rstest]
fn an_application_that_signs_in_through_it_loses_sign_in_marked_as_intent() {
    let graph = Graph::new([vec![
        Edge {
            provenance: intent("authenticates-through"),
            ..edge(
                Kind::AuthenticatesThrough,
                deployment("shop", "wiki"),
                deployment("auth", "login"),
            )
        },
        edge(
            Kind::DependsOn,
            deployment("shop", "api"),
            deployment("auth", "login"),
        ),
    ]]);
    let edges = Edges {
        graph: &graph,
        ingresses: &[],
        intents: &Intents::default(),
    };
    let found: Vec<(Dependent, Option<Provenance>)> = edges
        .of(&deployment("auth", "login"))
        .into_iter()
        .map(|loss| (loss.dependent, loss.declared_in))
        .collect();
    assert_eq!(
        found,
        vec![
            (Dependent::Caller(deployment("shop", "api")), None),
            (
                Dependent::SignsIn(deployment("shop", "wiki")),
                Some(intent("authenticates-through"))
            ),
        ]
    );
}
