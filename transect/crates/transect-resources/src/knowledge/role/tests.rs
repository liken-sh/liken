use rstest::rstest;

use super::super::{KindKnowledge, parse};
use super::{Column, Role};
use crate::knowledge::Through;
use crate::resources::DocId;

const REFERENCES: &str = "references: [
    {path: status.screen.monitor, label: shows on},
    {path: status.amp.name, kind: Amp, group: amps.example.com},
    {path: status.play, kind: Play, group: plays.example.com},
    {path: 'spec.handsets[].name', kind: Handset, group: widgets.example.com}]";

/// The knowledge of a kind with `REFERENCES` and the roles that `roles`
/// writes in YAML.
fn read(roles: &str) -> Result<KindKnowledge, String> {
    let text = format!(
        "metadata: {{name: kiosks.widgets.example.com}}\nspec: {{{REFERENCES}, roles: {roles}}}\n"
    );
    parse(DocId::default(), serde_yaml::from_str(&text).unwrap())
}

fn role(written: &str) -> Role {
    let known = read(&format!("{{face: {written}}}")).unwrap();
    known.roles[0].clone()
}

#[rstest]
fn reads_the_roles_in_the_order_of_the_document() {
    let known = read(
        "{hand: {reference: 'spec.handsets[].name', shape: handheld}, face: {reference: status.screen.monitor, shape: screen, expected: true}, amp: {reference: status.amp.name}}",
    )
    .unwrap();
    let roles: Vec<(&str, &str, Option<&str>, bool)> = known
        .roles
        .iter()
        .map(|r| {
            (
                r.name.as_str(),
                r.reference.as_str(),
                r.shape.as_deref(),
                r.expected,
            )
        })
        .collect();
    assert_eq!(
        roles,
        vec![
            ("hand", "spec.handsets[].name", Some("handheld"), false),
            ("face", "status.screen.monitor", Some("screen"), true),
            ("amp", "status.amp.name", None, false),
        ]
    );
}

fn column(name: &str, through: Option<&str>) -> Column {
    Column {
        name: name.into(),
        through: through.map(Through::new),
    }
}

#[rstest]
#[case::none("{reference: status.amp.name}", vec![])]
#[case::names("{reference: status.amp.name, reads: [Model, Power]}", vec![column("Model", None), column("Power", None)])]
#[case::through_a_reference(
    "{reference: status.amp.name, reads: [Model, {column: Charge, through: status.cell}]}",
    vec![column("Model", None), column("Charge", Some("status.cell"))]
)]
fn reads_each_column_of_reads(#[case] written: &str, #[case] expected: Vec<Column>) {
    assert_eq!(role(written).reads, expected);
}

#[rstest]
#[case::none_written("", vec![])]
#[case::one_column(", shows: [Activity]", vec![vec![column("Activity", None)]])]
#[case::a_list_of_alternatives(
    ", shows: [[Play, Idle]]",
    vec![vec![column("Play", None), column("Idle", None)]]
)]
#[case::both_forms(
    ", shows: [Activity, [Play, Idle]]",
    vec![vec![column("Activity", None)], vec![column("Play", None), column("Idle", None)]]
)]
#[case::through_a_reference_of_the_kind(
    ", shows: [[{column: Title, through: status.play}, Idle]]",
    vec![vec![column("Title", Some("status.play")), column("Idle", None)]]
)]
fn reads_each_entry_of_shows_as_a_list_of_alternatives(
    #[case] written: &str,
    #[case] expected: Vec<Vec<Column>>,
) {
    let role = role(&format!("{{reference: status.screen.monitor{written}}}"));
    let shows: Vec<Vec<Column>> = role.shows.into_iter().map(|s| s.columns).collect();
    assert_eq!(shows, expected);
}

#[rstest]
#[case::a_reference_that_is_not_listed(
    "{face: {reference: status.screen.node}}",
    "roles.face: reference status.screen.node is the path of no entry of references"
)]
#[case::an_empty_list_in_shows(
    "{face: {reference: status.screen.monitor, shows: [Activity, []]}}",
    "roles.face: shows[1] is an empty list"
)]
#[case::shows_through_no_reference(
    "{face: {reference: status.screen.monitor, shows: [{column: Title, through: status.film}]}}",
    "roles.face: shows[0] follows status.film, which is the path of no entry of references that names a kind"
)]
#[case::shows_through_a_value(
    "{face: {reference: status.screen.monitor, shows: [[Idle, {column: Title, through: status.screen.monitor}]]}}",
    "roles.face: shows[0] follows status.screen.monitor, which is the path of no entry"
)]
#[case::reads_through_a_part_that_is_a_value(
    "{face: {reference: status.screen.monitor, reads: [{column: Charge, through: status.cell}]}}",
    "roles.face: reads Charge through a reference of the part's kind, and the reference status.screen.monitor names no kind"
)]
#[case::a_role_name_with_a_capital(
    "{Face: {reference: status.screen.monitor}}",
    "roles has the name \"Face\", which is not a lower-case letter"
)]
#[case::an_unknown_field(
    "{face: {reference: status.screen.monitor, colour: red}}",
    "roles.face: unknown field `colour`"
)]
#[case::an_unknown_field_of_a_column(
    "{face: {reference: status.amp.name, reads: [{column: Charge, through: status.cell, colour: red}]}}",
    "roles.face"
)]
fn refuses_a_role_it_cannot_draw(#[case] roles: &str, #[case] error: &str) {
    let found = read(roles).unwrap_err();
    assert!(found.contains(error), "{found}");
}
