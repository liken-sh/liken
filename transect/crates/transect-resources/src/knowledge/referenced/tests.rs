use rstest::rstest;

use super::*;
use crate::knowledge::fixtures::knowledge;
use crate::knowledge::{Duty, Names, Reached};
use transect_graph::edges::{Kind, Plurals};

const GATES: &str = "{referencedBy: [{kind: Ingress, group: networking.k8s.io, annotation: example.com/gates, name: '{namespace}-{name}@crd'}], references: [{path: spec.check.address, address: true, edge: authenticates through}]}";

fn kinds() -> Kinds {
    let plurals = Plurals::from([(
        ("gates.example.com".to_string(), "Gate".to_string()),
        "gates".to_string(),
    )]);
    Kinds::resolve(
        &[knowledge("gates.gates.example.com", GATES)],
        &plurals,
        &[],
    )
}

fn gate(namespace: &str, name: &str) -> Object {
    Object::new("gates.example.com", "Gate", Some(namespace), name)
}

fn ingress(name: &str) -> Object {
    Object::new("networking.k8s.io", "Ingress", Some("shop"), name)
}

fn meta(annotation: &str) -> ObjectMeta {
    serde_yaml::from_str(&format!(
        "{{annotations: {{example.com/gates: '{annotation}'}}}}"
    ))
    .unwrap()
}

/// The gates that `referrals` finds for the `Ingress` web in shop, which
/// has `annotation`, and blog in shop, which has none.  Each result is
/// the `Ingress` name, the gate's namespace, and the gate's name.
fn named(annotation: &str, targets: &[Object]) -> Vec<(String, String, String)> {
    let (web, blog) = (meta(annotation), ObjectMeta::default());
    let annotated = [(ingress("web"), &web), (ingress("blog"), &blog)];
    referrals(&kinds(), targets, &annotated)
        .into_iter()
        .map(|r| {
            let namespace = r.target.namespace.unwrap_or_default();
            (r.referrer.name, namespace, r.target.name)
        })
        .collect()
}

fn found(ingress: &str, namespace: &str, gate: &str) -> (String, String, String) {
    (ingress.into(), namespace.into(), gate.into())
}

#[rstest]
#[case::one("id-login@crd", vec![found("web", "id", "login")])]
#[case::two_and_spaces(
    "id-login@crd, shop-headers@crd",
    vec![found("web", "id", "login"), found("web", "shop", "headers")]
)]
#[case::dashes_in_the_name("id-sso-login@crd", vec![found("web", "id", "sso-login")])]
#[case::another_provider("id-login@file", vec![])]
#[case::no_such_gate("id-gone@crd", vec![])]
#[case::names_that_render_alike("a-b-c@crd", vec![])]
fn matches_each_entry_against_each_gates_rendered_name(
    #[case] annotation: &str,
    #[case] expected: Vec<(String, String, String)>,
) {
    let targets = [
        gate("id", "login"),
        gate("id", "sso-login"),
        gate("shop", "headers"),
        gate("a-b", "c"),
        gate("a", "b-c"),
    ];
    assert_eq!(named(annotation, &targets), expected);
}

const ISSUERS: &str = "{referencedBy: [{kind: Ingress, group: networking.k8s.io, annotation: certs.example.com/issuer, name: '{name}', role: issues-tls}]}";

/// The issuers that `referrals` finds when the `Ingress` web in shop has
/// `annotation`.  The issuers that exist are internal in shop, and
/// internal and public in ops.  Each result is the issuer's namespace,
/// its name, and its role.
fn issued(annotation: &str) -> Vec<(String, String, Option<Duty>)> {
    let plurals = Plurals::from([(
        ("certs.example.com".to_string(), "Issuer".to_string()),
        "issuers".to_string(),
    )]);
    let kinds = Kinds::resolve(
        &[knowledge("issuers.certs.example.com", ISSUERS)],
        &plurals,
        &[],
    );
    let issuer =
        |namespace, name| Object::new("certs.example.com", "Issuer", Some(namespace), name);
    let targets = [
        issuer("shop", "internal"),
        issuer("ops", "internal"),
        issuer("ops", "public"),
    ];
    let meta: ObjectMeta = serde_yaml::from_str(&format!(
        "{{annotations: {{certs.example.com/issuer: '{annotation}'}}}}"
    ))
    .unwrap();
    referrals(&kinds, &targets, &[(ingress("web"), &meta)])
        .into_iter()
        .map(|r| {
            (
                r.target.namespace.unwrap_or_default(),
                r.target.name,
                r.role,
            )
        })
        .collect()
}

#[rstest]
#[case::the_one_beside_it("internal", vec![("shop".into(), "internal".into(), Some(Duty::IssuesTls))])]
#[case::none_beside_it("public", vec![])]
fn a_name_without_a_namespace_names_the_object_beside_the_referrer(
    #[case] annotation: &str,
    #[case] expected: Vec<(String, String, Option<Duty>)>,
) {
    assert_eq!(issued(annotation), expected);
}

fn idp() -> Endpoint {
    Endpoint::Object(Object::new("apps", "Deployment", Some("id"), "provider"))
}

fn app(name: &str) -> Object {
    Object::new("apps", "Deployment", Some("shop"), name)
}

fn relation(edge: Option<Kind>) -> Relation {
    Relation {
        label: "authenticates through".into(),
        path: "spec.check.address".into(),
        edge,
        value: "http://provider.id:9000/check".into(),
        names: Names::Address,
        provenance: Provenance::Kinds {
            knowledge: "gates.gates.example.com".into(),
            field: "spec.check.address".into(),
        },
    }
}

/// The edges that `referred_edges` draws for the `Ingress` web's
/// referral to the gate login, when login has `relations` and `behind`
/// lists the applications behind web.  Each edge is returned as its
/// kind, its two ends, and its provenance title.
fn drawn(relations: Vec<Relation>, behind: Vec<Object>) -> Vec<(Kind, Endpoint, Endpoint, String)> {
    let referrals = [Referral {
        referrer: ingress("web"),
        target: gate("id", "login"),
        annotation: "example.com/gates".into(),
        role: None,
    }];
    let relations = HashMap::from([(gate("id", "login"), relations)]);
    let reached = |_: &Object, _: &Relation| Reached {
        ends: vec![idp()],
        guessed: false,
    };
    let owns = |application: &Object, end: &Endpoint| Endpoint::Object(application.clone()) == *end;
    let edges = referred_edges(
        &referrals,
        &relations,
        reached,
        |_| behind.clone(),
        owns,
        &Graph::default(),
    );
    edges
        .into_iter()
        .map(|e| (e.kind, e.from, e.to, e.provenance.title()))
        .collect()
}

const TITLE: &str = "from spec.check.address of Gate login, named by example.com/gates on Ingress web in the KindKnowledge gates.gates.example.com";

#[rstest]
fn each_application_behind_the_referrer_takes_the_gates_edge() {
    let edges = drawn(
        vec![relation(Some(Kind::AuthenticatesThrough))],
        vec![app("web"), app("api")],
    );
    let edge = |name| {
        (
            Kind::AuthenticatesThrough,
            Endpoint::Object(app(name)),
            idp(),
            TITLE.to_string(),
        )
    };
    assert_eq!(edges, vec![edge("web"), edge("api")]);
}

#[rstest]
#[case::a_value_draws_no_edge(vec![relation(None)], vec![app("web")])]
#[case::nothing_behind_the_referrer(vec![relation(Some(Kind::AuthenticatesThrough))], vec![])]
#[case::the_provider_itself(
    vec![relation(Some(Kind::AuthenticatesThrough))],
    vec![Object::new("apps", "Deployment", Some("id"), "provider")]
)]
fn draws_nothing_when_nothing_signs_in(
    #[case] relations: Vec<Relation>,
    #[case] behind: Vec<Object>,
) {
    assert_eq!(drawn(relations, behind), vec![]);
}
