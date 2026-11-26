use std::ops::Range;

use rstest::rstest;

use super::super::{KindKnowledge, parse};
use super::{Align, slug};
use crate::resources::DocId;
use crate::shapes::{Shapes, library};

const REFERENCES: &str = "references:
    - {path: status.screen.monitor, label: shows on}
    - {path: status.amp.name, kind: Amp, group: amps.example.com}
    - {path: 'spec.handsets[].name', kind: Handset, group: widgets.example.com}
  roles:
    face: {reference: status.screen.monitor, shape: screen, expected: true}
    amp: {reference: status.amp.name}
    hand: {reference: 'spec.handsets[].name', shape: handheld}
    bell: {reference: status.amp.name, shape: speaker}";

/// The knowledge of a kind with `REFERENCES` and the elevations that
/// `elevations` writes.
fn read(elevations: &str) -> Result<KindKnowledge, String> {
    let text = format!(
        "metadata: {{name: kiosks.widgets.example.com}}\nspec:\n  {REFERENCES}\n  elevations:\n{elevations}"
    );
    parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap())
}

const KIOSK: &str = "
    - name: the kiosk
      question: What is this kiosk made of?
      sketch: |
        face  face  .
        face  face  amp
        .     hand  .
    - name: The Wiring!
      question: What feeds what?
      sketch: amp face
      wires:
        - {from: amp, to: face, carries: ' power '}
";

#[rstest]
fn reads_the_sketch_into_a_grid() {
    let known = read(KIOSK).unwrap();
    let elevation = &known.elevations[0];
    let grid = &elevation.sketch().unwrap().grid;
    let rows: Vec<Vec<Option<&str>>> = grid
        .rows
        .iter()
        .map(|row| row.iter().map(Option::as_deref).collect())
        .collect();
    assert_eq!(
        (
            elevation.name.as_str(),
            elevation.question.as_str(),
            grid.columns(),
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

/// A wire as its ends and what it carries.
type Ends<'a> = (&'a str, &'a str, &'a str);

#[rstest]
fn reads_the_slug_and_the_wires_of_each_elevation() {
    let known = read(KIOSK).unwrap();
    let found: Vec<(&str, Vec<Ends>)> = known
        .elevations
        .iter()
        .map(|e| {
            let wires = e.sketch().unwrap().wires.iter();
            let wires = wires.map(|w| (w.from.as_str(), w.to.as_str(), w.carries.as_str()));
            (e.slug.as_str(), wires.collect())
        })
        .collect();
    assert_eq!(
        found,
        vec![
            ("the-kiosk", vec![]),
            ("the-wiring", vec![("amp", "face", "power")])
        ]
    );
}

#[rstest]
#[case::words("the signal path", "the-signal-path")]
#[case::capitals_and_a_run_of_marks("The Room -- At Night", "the-room-at-night")]
#[case::marks_at_each_end("(the store)", "the-store")]
#[case::digits("rack 2", "rack-2")]
#[case::no_letter("--", "")]
fn makes_a_slug_from_a_name(#[case] name: &str, #[case] expected: &str) {
    assert_eq!(slug(name), expected);
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
    let grid = &known.elevations[0].sketch().unwrap().grid;
    assert_eq!((grid.columns_of(name), grid.rows_of(name)), (columns, rows));
}

/// A shape that turns lies on its side in an area wider than it is
/// tall, and stands in any other area.
#[rstest]
#[case::wider("bell bell", true)]
#[case::square("bell", false)]
#[case::taller("\"bell\\nbell\"", false)]
fn poses_a_shape_that_turns_on_its_side_in_a_wide_area(#[case] sketch: &str, #[case] lies: bool) {
    let known = read(&one("a", sketch)).unwrap();
    let speaker = Shapes::default().resolve("speaker", "").unwrap().clone();
    let posed = known.elevations[0]
        .sketch()
        .unwrap()
        .grid
        .posed("bell", &speaker);
    assert_eq!(posed == speaker.turned(), lies);
}

#[rstest]
fn poses_a_shape_that_does_not_turn_as_it_is() {
    let known = read(&one("a", "face face")).unwrap();
    let posed = known.elevations[0]
        .sketch()
        .unwrap()
        .grid
        .posed("face", &library::default());
    assert_eq!(posed, *library::default());
}

#[rstest]
fn reads_a_kind_with_no_elevations() {
    let text = format!("metadata: {{name: kiosks.widgets.example.com}}\nspec:\n  {REFERENCES}\n");
    let known = parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap()).unwrap();
    assert_eq!(known.elevations, vec![]);
}

/// One elevation named `name` with `sketch`, as YAML under
/// `elevations`.
fn one(name: &str, sketch: &str) -> String {
    format!("    - name: {name}\n      question: What is it?\n      sketch: {sketch}\n")
}

/// `one` with `more` added to the elevation, such as its wires.
fn with(name: &str, sketch: &str, more: &str) -> String {
    format!("{}      {more}\n", one(name, sketch))
}

#[rstest]
#[case::a_row_of_another_length(
    one("a", "\"face face\\nface\""),
    "elevations[0]: row 2 of the sketch has 1 cells, and the first row has 2"
)]
#[case::an_area_that_is_not_a_rectangle(
    one("a", "\"face face\\nface .\""),
    "elevations[0]: the cells of face in the sketch do not form one filled rectangle"
)]
#[case::an_area_in_two_pieces(
    one("a", "face . face"),
    "elevations[0]: the cells of face in the sketch do not form one filled rectangle"
)]
#[case::a_name_with_a_capital(
    one("a", "Face"),
    "elevations[0]: the name \"Face\" in the sketch is not a lower-case letter followed by"
)]
#[case::a_name_that_starts_with_a_digit(
    one("a", "1face"),
    "elevations[0]: the name \"1face\" in the sketch is not a lower-case letter"
)]
#[case::a_name_with_a_quote(
    one("a", "'fa\"ce'"),
    "elevations[0]: the name \"fa\\\"ce\" in the sketch is not a lower-case letter"
)]
#[case::a_name_that_is_not_a_role(
    one("a", "face lamp"),
    "elevations[0]: the sketch has the name lamp, and spec.roles has no lamp"
)]
#[case::roles_inside_an_elevation(
    with("a", "face", "roles: {face: {reference: status.screen.monitor}}"),
    "elevations[0]: the elevation has roles, and roles are stated once, in spec.roles"
)]
#[case::two_elevations_of_one_slug(
    format!("{}{}", one("the room", "face"), one("The Room!", "face")),
    "elevations[1]: the name \"The Room!\" makes the slug the-room, which elevations[0] has"
)]
#[case::a_name_with_no_slug(
    one("'--'", "face"),
    "elevations[0]: the name \"--\" has no letter or digit, so it makes no slug"
)]
#[case::an_empty_sketch(one("a", "''"), "elevations[0]: the sketch has no rows")]
#[case::a_wire_to_a_role_the_sketch_does_not_place(
    with("a", "face amp", "wires: [{from: amp, to: hand, carries: sound}]"),
    "elevations[0]: wires[0]: hand is not a role that the sketch places"
)]
#[case::a_wire_via_a_role_the_sketch_does_not_place(
    with(
        "a",
        "face amp",
        "wires: [{from: amp, to: face, via: hand, carries: sound}]"
    ),
    "elevations[0]: wires[0]: hand is not a role that the sketch places"
)]
#[case::a_wire_from_a_role_to_itself(
    with("a", "face amp", "wires: [{from: amp, to: amp, carries: sound}]"),
    "elevations[0]: wires[0]: the wire joins amp to itself"
)]
#[case::a_wire_via_its_own_end(
    with(
        "a",
        "face amp",
        "wires: [{from: amp, to: face, via: face, carries: sound}]"
    ),
    "elevations[0]: wires[0]: via is face, which is an end of the wire"
)]
#[case::a_wire_that_repeats_another(
    with(
        "a",
        "face amp",
        "wires: [{from: amp, to: face, carries: sound}, {from: amp, to: face, carries: power}]"
    ),
    "elevations[0]: wires[1]: an earlier wire already runs from amp to face"
)]
#[case::a_wire_that_carries_no_words(
    with("a", "face amp", "wires: [{from: amp, to: face, carries: ' '}]"),
    "elevations[0]: wires[0]: carries has no words"
)]
#[case::an_unknown_field_of_a_wire(
    with(
        "a",
        "face amp",
        "wires: [{from: amp, to: face, carries: sound, colour: red}]"
    ),
    "unknown field `colour`"
)]
#[case::another_edge(
    with("a", "face amp", "align: middle"),
    "elevations[0]: align is \"middle\", and it is \"top\" or \"bottom\""
)]
fn refuses_an_elevation_it_cannot_draw(#[case] elevations: String, #[case] error: &str) {
    let found = read(&elevations).unwrap_err();
    assert!(found.contains(error), "{found}");
}

#[rstest]
#[case::the_floor_unsaid(one("a", "face amp"), Align::Bottom)]
#[case::the_top(with("a", "face amp", "align: top"), Align::Top)]
#[case::the_bottom(with("a", "face amp", "align: bottom"), Align::Bottom)]
fn reads_the_edge_that_each_part_stands_against(#[case] elevations: String, #[case] align: Align) {
    assert_eq!(
        read(&elevations).unwrap().elevations[0]
            .sketch()
            .unwrap()
            .align,
        align
    );
}
