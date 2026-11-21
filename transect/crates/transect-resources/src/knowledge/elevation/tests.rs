use std::ops::Range;

use rstest::rstest;

use super::super::{KindKnowledge, parse};
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
    let roles: Vec<(&str, &str, &str, bool, Vec<&str>)> = known.elevations[0]
        .roles
        .iter()
        .map(|role| {
            let reads = role.reads.iter().map(String::as_str).collect();
            let (name, reference) = (role.name.as_str(), role.reference.as_str());
            (name, reference, role.shape.as_str(), role.expected, reads)
        })
        .collect();
    assert_eq!(
        roles,
        vec![
            (
                "face",
                "status.screen.monitor",
                "screen",
                true,
                vec!["Mode"]
            ),
            ("amp", "status.amp.name", "box", false, vec![]),
            ("hand", "spec.handsets[].name", "handheld", false, vec![]),
        ]
    );
}

#[rstest]
fn reads_the_sketch_into_a_grid() {
    let known = read(KIOSK).unwrap();
    let elevation = &known.elevations[0];
    let rows: Vec<Vec<Option<&str>>> = elevation
        .grid
        .rows
        .iter()
        .map(|row| row.iter().map(Option::as_deref).collect())
        .collect();
    assert_eq!(
        (
            elevation.name.as_str(),
            elevation.question.as_str(),
            elevation.grid.columns(),
            rows,
        ),
        (
            "the kiosk",
            "What is this kiosk made of?",
            3,
            vec![
                vec![Some("face"), Some("face"), None],
                vec![Some("face"), Some("face"), Some("amp")],
                vec![None, Some("hand"), None],
            ],
        )
    );
}

#[rstest]
#[case::two_columns_of_two_rows("face", Some(0..2), Some(0..2))]
#[case::the_last_column("amp", Some(2..3), Some(1..2))]
#[case::a_middle_column("hand", Some(1..2), Some(2..3))]
#[case::a_name_not_in_the_sketch("bell", None, None)]
fn reads_the_columns_and_rows_that_an_area_spans(
    #[case] name: &str,
    #[case] columns: Option<Range<usize>>,
    #[case] rows: Option<Range<usize>>,
) {
    let known = read(KIOSK).unwrap();
    let grid = &known.elevations[0].grid;
    assert_eq!((grid.columns_of(name), grid.rows_of(name)), (columns, rows));
}

/// A role keeps the name of its shape as it wrote it, and names the
/// library's default when it writes none. `Kinds::resolve` finds the
/// definition.
#[rstest]
#[case::a_bare_name(", shape: screen", "screen")]
#[case::a_whole_name(", shape: crate.widgets.example.com", "crate.widgets.example.com")]
#[case::none(", reads: []", "box")]
fn reads_the_name_of_each_roles_shape(#[case] written: &str, #[case] shape: &str) {
    let roles = format!("{{face: {{reference: status.screen.monitor{written}}}}}");
    let known = read(&one("a", "face", &roles)).unwrap();
    assert_eq!(known.elevations[0].roles[0].shape, shape);
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
