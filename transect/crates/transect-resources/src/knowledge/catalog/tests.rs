use rstest::rstest;

use super::super::role::First;
use super::super::{KindKnowledge, parse};
use super::*;
use crate::resources::DocId;
use crate::shapes::Shapes;

#[rstest]
fn loads_each_pattern_of_the_catalog() {
    let failed: Vec<String> = MODULES
        .iter()
        .filter_map(|module| load(module).err())
        .collect();
    assert_eq!(failed, Vec::<String>::new());
}

/// The knowledge of a kind that fills every role of `definition` that
/// a kind fills, each from a reference of its own.
fn filling(definition: &Definition) -> KindKnowledge {
    let mut references = Vec::new();
    let mut roles = Vec::new();
    for template in &definition.roles {
        let Source::Fill {
            group, kind, list, ..
        } = &template.source
        else {
            continue;
        };
        let path = match list {
            true => format!("spec.{}[]", template.name),
            false => format!("spec.{}", template.name),
        };
        references.push(format!(
            "{{path: '{path}', kind: {kind}, group: '{group}'}}"
        ));
        roles.push(format!("{}: {{reference: '{path}'}}", template.name));
    }
    let text = format!(
        "metadata: {{name: stores.stores.example.test}}\nspec: {{references: [{}], pattern: {{name: {}, roles: {{{}}}}}}}\n",
        references.join(", "),
        definition.name,
        roles.join(", ")
    );
    parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap()).unwrap()
}

/// Each role of each pattern names a shape that the library has, and
/// each wire meets a port of the shapes of its roles on the facing edge,
/// as the code that draws a kind's knowledge finds them.
#[rstest]
fn draws_each_pattern_with_the_librarys_shapes_and_ports() {
    let faults: Vec<(Vec<String>, Vec<String>)> = definitions()
        .iter()
        .map(|definition| filling(definition).draw_with(&Shapes::default(), |_, _| None))
        .collect();
    let none = vec![(Vec::new(), Vec::new()); definitions().len()];
    assert_eq!(faults, none);
}

/// A pattern whose roles `roles` writes, with the elevation of a primary
/// with replicas, read against the module of that pattern.
fn definition(roles: &str) -> Result<Definition, String> {
    let text = format!(
        "{{name: a-pattern, title: a pattern, roles: {roles}, elevations: [{{name: the store, question: Where?, sketch: one}}]}}"
    );
    let written: Written = serde_yaml_ng::from_str(&text).unwrap();
    read(written, &primary_with_replicas::MODULE)
}

#[rstest]
#[case::expected_in_one_state(
    "{one: {fill: required, kind: Pod, expected: {while: Ready}}}",
    "roles.one: expected is while Ready, and a pattern describes every kind that fills it"
)]
#[case::a_derive_with_no_function(
    "{one: {derive: guess}}",
    "roles.one: derive is \"guess\", which the pattern's module has no function for"
)]
#[case::a_fill_of_another_word(
    "{one: {fill: always, kind: Pod}}",
    "roles.one: fill is \"always\", and it is \"required\" or \"optional\""
)]
#[case::a_fill_with_no_kind(
    "{one: {fill: required}}",
    "roles.one: the role is filled by a reference, so it names the kind of its parts"
)]
#[case::a_derive_with_a_kind(
    "{one: {derive: controller, kind: Pod}}",
    "roles.one: a derived role takes its parts from its function"
)]
#[case::both_a_fill_and_a_derive(
    "{one: {fill: required, kind: Pod, derive: controller}}",
    "roles.one: the role has one of fill and derive"
)]
#[case::neither(
    "{one: {label: one}}",
    "roles.one: the role has one of fill and derive"
)]
#[case::two_roles_that_fail_over(
    "{one: {fill: required, kind: Pod, failover: true}, two: {fill: required, kind: Pod, failover: true}}",
    "roles.two: failover is on one already"
)]
fn refuses_a_pattern_it_cannot_draw(#[case] roles: &str, #[case] error: &str) {
    let found = definition(roles).unwrap_err();
    assert!(found.contains(error), "{found}");
}

const REFERENCES: &str = "references: [
    {path: status.currentPrimary, kind: Pod},
    {path: 'status.instances[]', kind: Pod},
    {path: status.targetPrimary, kind: Pod},
    {path: status.writeService, kind: Service},
    {name: '{name}-ro', kind: Service},
    {name: '{name}-0', kind: Pod},
    {name: '{name}-serving', kind: Service, group: serving.example.test},
    {path: status.phase}]";

const FILLED: &str =
    "instances: {reference: 'status.instances[]'}, primary: {reference: status.currentPrimary}";

/// The knowledge of a store with `REFERENCES` and the `pattern` that
/// `pattern` writes.
fn store(pattern: &str) -> Result<KindKnowledge, String> {
    let text = format!(
        "metadata: {{name: ledgers.ledgers.example.test}}\nspec: {{{REFERENCES}, pattern: {pattern}}}\n"
    );
    parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap())
}

#[rstest]
fn fills_each_role_from_the_kinds_references_or_its_function() {
    let pattern = format!(
        "{{name: primary-with-replicas, roles: {{{FILLED}, read-service: {{reference: '{{name}}-ro'}}}}}}"
    );
    let known = store(&pattern).unwrap();
    let pattern = known.pattern.unwrap();
    let roles: Vec<(&str, First, Option<&str>)> = pattern
        .roles
        .iter()
        .map(|role| {
            (
                role.name.as_str(),
                role.fill.first.clone(),
                role.unresolved(),
            )
        })
        .collect();
    let reference = |path: &str| First::Reference(path.into());
    assert_eq!(
        roles,
        vec![
            ("instances", reference("status.instances[]"), None),
            ("primary", reference("status.currentPrimary"), None),
            (
                "target",
                First::Unfilled,
                Some("pattern.roles has no target, so the KindKnowledge names no Pod for it")
            ),
            (
                "write-service",
                First::Unfilled,
                Some(
                    "pattern.roles has no write-service, so the KindKnowledge names no Service for it"
                )
            ),
            ("read-service", reference("{name}-ro"), None),
            (
                "replicas",
                First::Derived("instances-but-primary".into()),
                None
            ),
            ("promoter", First::Derived("controller".into()), None),
        ]
    );
    assert_eq!(
        (pattern.title.as_str(), pattern.failover()),
        ("primary with replicas", Some("status.currentPrimary"))
    );
}

#[rstest]
#[case::a_pattern_of_no_catalog(
    "{name: a-queue}",
    "pattern.name is \"a-queue\", which is not a pattern of transect's catalog: primary-with-replicas"
)]
#[case::a_role_the_pattern_derives(
    &format!("{{name: primary-with-replicas, roles: {{{FILLED}, replicas: {{reference: 'status.instances[]'}}}}}}"),
    "pattern.roles has replicas, which primary-with-replicas does not ask a kind to fill"
)]
#[case::a_role_of_no_pattern(
    &format!("{{name: primary-with-replicas, roles: {{{FILLED}, standby: {{reference: status.targetPrimary}}}}}}"),
    "pattern.roles has standby, which primary-with-replicas does not ask a kind to fill"
)]
#[case::no_required_role(
    "{name: primary-with-replicas, roles: {instances: {reference: 'status.instances[]'}}}",
    "pattern.roles has no primary, which primary-with-replicas requires"
)]
#[case::a_field_other_than_reference(
    "{name: primary-with-replicas, roles: {primary: {reference: status.currentPrimary, shape: box}}}",
    "pattern.roles.primary: unknown field `shape`"
)]
#[case::a_reference_of_no_entry(
    "{name: primary-with-replicas, roles: {primary: {reference: status.leader}}}",
    "pattern.roles.primary: reference status.leader is the path or the name of no entry of references"
)]
#[case::a_reference_of_another_kind(
    "{name: primary-with-replicas, roles: {primary: {reference: status.writeService}}}",
    "pattern.roles.primary: reference status.writeService names no Pod"
)]
#[case::a_kind_of_the_same_name_in_another_group(
    "{name: primary-with-replicas, roles: {instances: {reference: 'status.instances[]'}, primary: {reference: status.currentPrimary}, write-service: {reference: '{name}-serving'}}}",
    "pattern.roles.write-service: reference {name}-serving names no Service, and the role's parts are each a Service"
)]
#[case::a_reference_of_a_value(
    "{name: primary-with-replicas, roles: {primary: {reference: status.phase}}}",
    "pattern.roles.primary: reference status.phase names no Pod"
)]
#[case::a_list_read_as_one_value(
    "{name: primary-with-replicas, roles: {instances: {reference: status.currentPrimary}}}",
    "pattern.roles.instances: reference status.currentPrimary reads one value, and the role reads a list"
)]
#[case::a_list_by_a_name_template(
    "{name: primary-with-replicas, roles: {instances: {reference: '{name}-0'}}}",
    "pattern.roles.instances: reference {name}-0 reads one value, and the role reads a list"
)]
#[case::an_unknown_field(
    "{name: primary-with-replicas, shape: drum}",
    "pattern: unknown field `shape`"
)]
fn refuses_a_pattern_that_the_kind_cannot_fill(#[case] pattern: &str, #[case] error: &str) {
    let found = store(pattern).unwrap_err();
    assert!(found.contains(error), "{found}");
}
