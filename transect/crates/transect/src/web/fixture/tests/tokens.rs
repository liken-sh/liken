//! The agent system of `tokens.yaml`: the owners of its accounts, the
//! templates that present tokens, and the edges to the programs that
//! accept them.

use rstest::rstest;

use super::super::model;
use transect_graph::edges::{Endpoint, Kind, Object};

fn greenhouse(group: &str, kind: &str, name: &str) -> Object {
    Object::new(group, kind, Some("acme-greenhouse"), name)
}

#[rstest]
#[case::a_mandates_account("simmer-greenhouse", greenhouse("simmer.foo", "Mandate", "greenhouse"))]
#[case::an_account_of_a_label(
    "simmer-greenhouse--status",
    greenhouse("simmer.foo", "Mandate", "greenhouse")
)]
#[case::a_dutys_own_account("simmer-greenhouse--trim", greenhouse("simmer.foo", "Duty", "trim"))]
fn an_account_has_its_owner(#[case] account: &str, #[case] owner: Object) {
    let model = model();
    let account = greenhouse("", "ServiceAccount", account);
    assert_eq!(model.snapshot.ownership.top_owner(&account), owner);
}

/// Each template of `holder`, as its path with the audiences that it
/// presents.
fn presented(holder: &Object) -> Vec<(String, Vec<String>)> {
    let snapshot = model().snapshot;
    let found = snapshot
        .presented
        .iter()
        .filter(|p| &p.holder == holder)
        .cloned();
    let found = found.map(|p| (p.path, p.audiences.into_iter().map(|a| a.name).collect()));
    found.collect()
}

#[rstest]
#[case::the_mandates_own_and_its_controllers(
    greenhouse("simmer.foo", "Mandate", "greenhouse"),
    vec![("spec.template", vec!["lanyard", "midden", "simmer", "transom"])]
)]
#[case::a_turn_merged_with_the_mandate_and_a_label(
    greenhouse("simmer.foo", "Duty", "trim"),
    vec![("spec.template", vec!["gofer", "lanyard", "midden", "simmer", "transom"])]
)]
#[case::a_hook_without_the_mandate(
    greenhouse("simmer.foo", "Duty", "mist"),
    vec![
        ("spec.template", vec!["lanyard", "midden", "simmer", "transom"]),
        ("spec.hooks.before", vec!["lanyard", "simmer"]),
    ]
)]
fn reads_what_each_template_presents(
    #[case] holder: Object,
    #[case] expected: Vec<(&str, Vec<&str>)>,
) {
    let expected: Vec<(String, Vec<String>)> = expected
        .into_iter()
        .map(|(path, audiences)| {
            (
                path.into(),
                audiences.into_iter().map(String::from).collect(),
            )
        })
        .collect();
    assert_eq!(presented(&holder), expected);
}

#[rstest]
fn a_label_names_the_object_that_a_program_injects_for() {
    let trim = greenhouse("simmer.foo", "Duty", "trim");
    let snapshot = model().snapshot;
    let found = snapshot
        .presented
        .iter()
        .find(|p| p.holder == trim)
        .cloned()
        .unwrap();
    let gofer = found
        .audiences
        .into_iter()
        .find(|a| a.name == "gofer")
        .unwrap();
    assert_eq!(
        gofer.names,
        vec![greenhouse(
            "gofer.guid.foo",
            "BrowserProfile",
            "greenhouse-browser"
        )]
    );
}

/// The holders whose templates present a token to the `Deployment`
/// `name` in `acme-agents`, as `Kind name`.
fn callers(name: &str) -> Vec<String> {
    let model = model();
    let graph = model.graph();
    let workload = Endpoint::Object(Object::new("apps", "Deployment", Some("acme-agents"), name));
    let mut found: Vec<String> = graph
        .reaching(Kind::Presents, &workload)
        .into_iter()
        .filter_map(|end| match end {
            Endpoint::Object(object) => Some(format!("{} {}", object.kind, object.name)),
            _ => None,
        })
        .collect();
    found.dedup();
    found
}

#[rstest]
#[case::lanyard("lanyard-api", vec!["Duty mist", "Duty trim", "Mandate greenhouse"])]
#[case::gofer("gofer-api", vec!["Duty trim"])]
#[case::midden("midden-api", vec!["Duty mist", "Duty trim", "Mandate greenhouse"])]
#[case::simmer("simmer-operator", vec!["Duty mist", "Duty trim", "Mandate greenhouse"])]
fn a_program_lists_the_templates_that_call_it(#[case] name: &str, #[case] expected: Vec<&str>) {
    assert_eq!(callers(name), expected);
}

#[rstest]
fn a_mandate_of_another_operator_calls_its_programs() {
    let model = model();
    let graph = model.graph();
    let garden = Endpoint::Object(Object::new(
        "agents.example.test",
        "Mandate",
        Some("acme-garden"),
        "garden",
    ));
    let mut found: Vec<String> = graph
        .leaving(Kind::Presents, &garden)
        .into_iter()
        .filter_map(|end| match end {
            Endpoint::Object(object) => Some(object.name.clone()),
            _ => None,
        })
        .collect();
    found.dedup();
    assert_eq!(
        found,
        vec!["gardener-operator", "keyring-server", "notebook-server"]
    );
}

/// The objects that a `DependsOn` edge from `from` reaches, as `Kind name`.
fn depends_on(from: Object) -> Vec<String> {
    let model = model();
    let graph = model.graph();
    let ends = graph.leaving(Kind::DependsOn, &Endpoint::Object(from));
    let names = ends.into_iter().filter_map(|end| match end {
        Endpoint::Object(object) => Some(format!("{} {}", object.kind, object.name)),
        _ => None,
    });
    names.collect()
}

#[rstest]
fn a_reference_to_an_owned_account_reaches_the_account() {
    let badge = greenhouse("lanyard.guid.foo", "Badge", "greenhouse-badge");
    assert_eq!(
        depends_on(badge),
        vec![
            "ServiceAccount simmer-greenhouse",
            "ServiceAccount simmer-greenhouse--status"
        ]
    );
}
