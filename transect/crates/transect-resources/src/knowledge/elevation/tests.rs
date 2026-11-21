use std::ops::Range;

use rstest::rstest;

use super::super::{KindKnowledge, parse};
use super::*;
use crate::resources::DocId;

const REFERENCES: &str = "references:
    - {path: status.screen.monitor, label: shows on}
    - {path: status.amp.name, kind: Amp, group: amps.example.com}
    - {path: 'spec.handsets[].name', kind: Handset, group: widgets.example.com}";

/// The knowledge of a kind with `REFERENCES` and the elevations that
/// `elevations` writes.
fn read(elevations: &str) -> Result<KindKnowledge, String> {
    let text = format!(
        "metadata: {{name: kiosks.widgets.example.com}}\nspec:\n  {REFERENCES}\n  elevations:\n{elevations}"
    );
    parse(DocId::default(), serde_yaml::from_str(&text).unwrap())
}

const KIOSK: &str = "
    - name: the kiosk
      question: What is this kiosk made of?
      sketch: |
        face  face  .
        face  face  amp
        .     hand  .
      roles:
        face: {reference: status.screen.monitor, shape: screen, expected: true, reads: [Mode]}
        amp: {reference: status.amp.name}
        hand: {reference: 'spec.handsets[].name', shape: handheld}
";

#[rstest]
fn reads_the_roles_in_the_order_of_the_sketch() {
    let known = read(KIOSK).unwrap();
    let roles: Vec<(&str, &str, Shape, bool, Vec<&str>)> = known.elevations[0]
        .roles
        .iter()
        .map(|role| {
            let reads = role.reads.iter().map(String::as_str).collect();
            let (name, reference) = (role.name.as_str(), role.reference.as_str());
            (name, reference, role.shape, role.expected, reads)
        })
        .collect();
    assert_eq!(
        roles,
        vec![
            (
                "face",
                "status.screen.monitor",
                Shape::Screen,
                true,
                vec!["Mode"]
            ),
            ("amp", "status.amp.name", Shape::Box, false, vec![]),
            (
                "hand",
                "spec.handsets[].name",
                Shape::Handheld,
                false,
                vec![]
            ),
        ]
    );
}

#[rstest]
fn reads_the_sketch_into_the_areas_of_a_grid() {
    let known = read(KIOSK).unwrap();
    let elevation = &known.elevations[0];
    assert_eq!(
        (
            elevation.name.as_str(),
            elevation.question.as_str(),
            elevation.grid.columns(),
            elevation.grid.template_areas(),
        ),
        (
            "the kiosk",
            "What is this kiosk made of?",
            3,
            r#""face face ." "face face amp" ". hand .""#.to_string(),
        )
    );
}

#[rstest]
#[case::two_columns_of_two_rows("face", Some(0..2))]
#[case::the_last_column("amp", Some(2..3))]
#[case::a_middle_column("hand", Some(1..2))]
#[case::a_name_not_in_the_sketch("bell", None)]
fn reads_the_columns_that_an_area_spans(#[case] name: &str, #[case] span: Option<Range<usize>>) {
    let known = read(KIOSK).unwrap();
    assert_eq!(known.elevations[0].grid.span(name), span);
}

/// A role's shape is read from its word, and the page's CSS class ends
/// in the same word.
#[rstest]
#[case::screen("screen", Shape::Screen)]
#[case::a_box("box", Shape::Box)]
#[case::speaker("speaker", Shape::Speaker)]
#[case::handheld("handheld", Shape::Handheld)]
#[case::tag("tag", Shape::Tag)]
fn reads_each_shape_by_its_word(#[case] word: &str, #[case] shape: Shape) {
    let roles = format!("{{face: {{reference: status.screen.monitor, shape: {word}}}}}");
    let known = read(&one("a", "face", &roles)).unwrap();
    let read = known.elevations[0].roles[0].shape;
    assert_eq!((read, read.word()), (shape, word));
}

/// The `shows` of a role whose YAML is `shows`, as the lists of
/// columns of each entry.
fn shows(shows: &str) -> Vec<Vec<String>> {
    let roles = format!("{{face: {{reference: status.screen.monitor{shows}}}}}");
    let known = read(&one("a", "face", &roles)).unwrap();
    let role = &known.elevations[0].roles[0];
    role.shows.iter().map(|show| show.columns.clone()).collect()
}

#[rstest]
#[case::none_written("", vec![])]
#[case::one_column(", shows: [Activity]", vec![vec!["Activity"]])]
#[case::a_list_of_alternatives(", shows: [[Play, Idle]]", vec![vec!["Play", "Idle"]])]
#[case::both_forms(
    ", shows: [Activity, [Play, Idle]]",
    vec![vec!["Activity"], vec!["Play", "Idle"]]
)]
fn reads_each_entry_of_shows_as_a_list_of_alternatives(
    #[case] written: &str,
    #[case] expected: Vec<Vec<&str>>,
) {
    assert_eq!(shows(written), expected);
}

#[rstest]
fn reads_a_kind_with_no_elevations() {
    let text = format!("metadata: {{name: kiosks.widgets.example.com}}\nspec:\n  {REFERENCES}\n");
    let known = parse(DocId::default(), serde_yaml::from_str(&text).unwrap()).unwrap();
    assert_eq!(known.elevations, vec![]);
}

/// One elevation named `name` with `sketch` and `roles`, as YAML under
/// `elevations`.
fn one(name: &str, sketch: &str, roles: &str) -> String {
    format!(
        "    - name: {name}\n      question: What is it?\n      sketch: {sketch:?}\n      roles: {roles}\n"
    )
}

const SCREEN: &str = "{face: {reference: status.screen.monitor}}";

#[rstest]
#[case::a_row_of_another_length(
    one("a", "face face\nface", SCREEN),
    "elevations[0]: row 2 of the sketch has 1 cells, and the first row has 2"
)]
#[case::an_area_that_is_not_a_rectangle(
    one("a", "face face\nface .", SCREEN),
    "elevations[0]: the cells of face in the sketch do not form one filled rectangle"
)]
#[case::an_area_in_two_pieces(
    one("a", "face . face", SCREEN),
    "elevations[0]: the cells of face in the sketch do not form one filled rectangle"
)]
#[case::a_name_with_a_capital(
    one("a", "Face", "{Face: {reference: status.screen.monitor}}"),
    "elevations[0]: the name \"Face\" in the sketch is not a lower-case letter followed by"
)]
#[case::a_name_that_starts_with_a_digit(
    one("a", "1face", "{1face: {reference: status.screen.monitor}}"),
    "elevations[0]: the name \"1face\" in the sketch is not a lower-case letter"
)]
#[case::a_name_with_a_quote(
    one("a", "fa\"ce", SCREEN),
    "elevations[0]: the name \"fa\\\"ce\" in the sketch is not a lower-case letter"
)]
#[case::a_name_no_role_has(
    one("a", "face amp", SCREEN),
    "elevations[0]: the sketch has the name amp, and roles has no amp"
)]
#[case::a_role_the_sketch_does_not_have(
    one(
        "a",
        "face",
        "{face: {reference: status.screen.monitor}, amp: {reference: status.amp.name}}"
    ),
    "elevations[0]: roles has amp, and the sketch has no cell named amp"
)]
#[case::a_reference_that_is_not_listed(
    one("a", "face", "{face: {reference: status.screen.node}}"),
    "elevations[0]: roles.face: reference status.screen.node is the path of no entry of references"
)]
#[case::a_shape_transect_does_not_draw(
    one(
        "a",
        "face",
        "{face: {reference: status.screen.monitor, shape: circle}}"
    ),
    "elevations[0]: roles.face: shape \"circle\" is not one of screen, box, speaker, handheld, or tag"
)]
#[case::two_elevations_of_one_name(
    format!("{}{}", one("a", "face", SCREEN), one("a", "face", SCREEN)),
    "elevations[1]: the name \"a\" is the name of an earlier elevation"
)]
#[case::an_empty_sketch(one("a", "", "{}"), "elevations[0]: the sketch has no rows")]
#[case::an_empty_list_in_shows(
    one(
        "a",
        "face",
        "{face: {reference: status.screen.monitor, shows: [Activity, []]}}"
    ),
    "elevations[0]: roles.face: shows[1] is an empty list, and an entry of shows is a column's name or a list of one or more names"
)]
#[case::an_unknown_field(
    one("a", "face", "{face: {reference: status.screen.monitor, colour: red}}"),
    "unknown field `colour`"
)]
fn refuses_an_elevation_it_cannot_draw(#[case] elevations: String, #[case] error: &str) {
    let found = read(&elevations).unwrap_err();
    assert!(found.contains(error), "{found}");
}
