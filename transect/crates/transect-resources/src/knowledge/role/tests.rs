use rstest::rstest;

use super::super::{KindKnowledge, parse};
use super::{Column, Expected, Role};
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
    let roles: Vec<(&str, Option<&str>, Option<&str>, &Expected)> = known
        .roles
        .iter()
        .map(|r| {
            (
                r.name.as_str(),
                r.fill.reference(),
                r.shape.as_deref(),
                &r.expected,
            )
        })
        .collect();
    assert_eq!(
        roles,
        vec![
            (
                "hand",
                Some("spec.handsets[].name"),
                Some("handheld"),
                &Expected::Never
            ),
            (
                "face",
                Some("status.screen.monitor"),
                Some("screen"),
                &Expected::Always
            ),
            ("amp", Some("status.amp.name"), None, &Expected::Never),
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
#[case::both_a_reference_and_named_by(
    "{face: {reference: status.amp.name, namedBy: {kind: Amp, path: spec.kiosk}}}",
    "roles.face: the role has both reference and namedBy"
)]
#[case::neither_a_reference_nor_named_by(
    "{face: {shape: screen}}",
    "roles.face: the role has neither reference nor namedBy"
)]
#[case::named_by_with_no_kind(
    "{face: {namedBy: {path: spec.kiosk}}}",
    "roles.face: namedBy has no kind"
)]
#[case::named_by_with_no_path("{face: {namedBy: {kind: Amp}}}", "roles.face: namedBy has no path")]
#[case::an_empty_elevation(
    "{face: {reference: status.amp.name, elevation: ''}}",
    "roles.face: elevation is empty"
)]
#[case::an_empty_label(
    "{face: {reference: status.amp.name, label: ' '}}",
    "roles.face: label is empty"
)]
#[case::an_empty_through(
    "{face: {reference: status.amp.name, through: ''}}",
    "roles.face: through is empty"
)]
#[case::through_a_value(
    "{face: {reference: status.screen.monitor, through: status.cell}}",
    "roles.face: the role follows status.cell from status.screen.monitor, which names no kind"
)]
#[case::an_elevation_of_values(
    "{face: {reference: status.screen.monitor, elevation: the screen}}",
    "roles.face: the role draws each part as \"the screen\", and the reference status.screen.monitor names no kind"
)]
#[case::expected_as_a_word(
    "{face: {reference: status.amp.name, expected: sometimes}}",
    "roles.face: expected is sometimes, and it is true, false, or {while: <condition type>}"
)]
#[case::expected_while_with_another_key(
    "{face: {reference: status.amp.name, expected: {until: Open}}}",
    "roles.face: expected is until: Open, and it is true"
)]
#[case::expected_while_nothing(
    "{face: {reference: status.amp.name, expected: {while: ''}}}",
    "roles.face: expected is while: ''"
)]
#[case::an_unknown_field_of_a_column(
    "{face: {reference: status.amp.name, reads: [{column: Charge, through: status.cell, colour: red}]}}",
    "roles.face"
)]
fn refuses_a_role_it_cannot_draw(#[case] roles: &str, #[case] error: &str) {
    let found = read(roles).unwrap_err();
    assert!(found.contains(error), "{found}");
}

#[rstest]
#[case::always("true", Expected::Always)]
#[case::never("false", Expected::Never)]
#[case::while_a_condition_is_true("{while: Open}", Expected::While("Open".into()))]
fn reads_when_a_role_is_expected(#[case] written: &str, #[case] expected: Expected) {
    let role = role(&format!(
        "{{reference: status.amp.name, expected: {written}}}"
    ));
    assert_eq!(role.expected, expected);
}

#[rstest]
#[case::the_references_label("{reference: status.screen.monitor}", "shows on")]
#[case::the_references_edge("{reference: status.amp.name}", "depends on")]
#[case::the_roles_own("{reference: status.amp.name, label: amplified by}", "amplified by")]
#[case::a_kind_in_words("{namedBy: {kind: ShelfLabel, path: spec.kiosk}}", "shelf label")]
#[case::a_kind_in_capitals("{namedBy: {kind: UPS, path: spec.kiosk}}", "ups")]
#[case::the_roles_own_over_the_kind(
    "{namedBy: {kind: ShelfLabel, path: spec.kiosk}, label: tag}",
    "tag"
)]
fn labels_each_part_of_a_role(#[case] written: &str, #[case] label: &str) {
    let known = read(&format!("{{face: {written}}}")).unwrap();
    assert_eq!(known.roles[0].label(&known.references), label);
}
