//! The sketch draws each role in the shape that it names or that the
//! knowledge of its kind states: contact between rows, parts that touch
//! in a row, a shape on its side, and the default shape.

use rstest::rstest;

use super::super::*;
use super::{REFERENCES, page_of_known, page_with};
use crate::map::known::Standing;
use transect_resources::knowledge::fixtures::knowledge;
use transect_resources::resources::DocId;
use transect_resources::shapes::Shapes;

/// The sketch of the kiosk whose `amp` and `hand` roles have the shapes
/// `amp` and `hand`, in a grid of `sketch`, with an author's `rail`,
/// which joins on its bottom, and `cart`, which joins on its top.
fn drawn(sketch: &str, amp: &str, hand: &str) -> Sketch {
    let shape = |name: &str, joins: &str| {
        let text = format!(
            "{{metadata: {{name: {name}.widgets.example.com}}, spec: {{summary: A part., size: {{design: [100, 50], width: [80, 120]}}, joins: {joins}}}}}"
        );
        let value = serde_yaml_ng::from_str(&text).unwrap();
        std::sync::Arc::new(transect_resources::shapes::parse(DocId::default(), value).unwrap())
    };
    let shapes = Shapes::with(&[shape("rail", "[bottom]"), shape("cart", "[top]")]);
    let roles = format!(
        "roles: {{amp: {{reference: status.amp.name, shape: {amp}, expected: true}}, hand: {{reference: 'spec.handsets[].name', shape: {hand}}}}}"
    );
    let elevation =
        format!("elevations: [{{name: the kiosk, question: What is it?, sketch: {sketch:?}}}]");
    let spec = format!("{{{REFERENCES}, {roles}, {elevation}}}");
    page_with(&spec, Standing::Plain, &shapes).sketch.unwrap()
}

fn areas(sketch: &Sketch) -> Vec<(&str, String)> {
    let roles = sketch.plan.roles.iter();
    roles
        .map(|role| (role.area.as_str(), role.area_class()))
        .collect()
}

#[rstest]
#[case::both_edges_join(
    "rail",
    "cart",
    "auto 0px auto",
    ["sketch-area sketch-joined-below sketch-top-row", "sketch-area sketch-joined-above sketch-bottom-row"]
)]
#[case::the_upper_edge_does_not_join(
    "box",
    "cart",
    "auto var(--sketch-gap) auto",
    ["sketch-area sketch-top-row", "sketch-area sketch-bottom-row"]
)]
#[case::the_lower_edge_does_not_join(
    "rail",
    "box",
    "auto var(--sketch-gap) auto",
    ["sketch-area sketch-top-row", "sketch-area sketch-bottom-row"]
)]
fn closes_the_gap_between_rows_where_both_parts_join(
    #[case] amp: &str,
    #[case] hand: &str,
    #[case] rows: &str,
    #[case] classes: [&str; 2],
) {
    let sketch = drawn("amp\nhand", amp, hand);
    assert_eq!(
        (sketch.plan.rows.as_str(), areas(&sketch)),
        (
            rows,
            vec![("amp", classes[0].into()), ("hand", classes[1].into())]
        )
    );
}

#[rstest]
#[case::a_shape_that_joins_its_sides("disc", "sketch-area sketch-bottom-row sketch-abutting")]
#[case::a_shape_that_does_not("box", "sketch-area sketch-bottom-row")]
fn lets_the_parts_of_a_shape_that_joins_its_sides_touch(#[case] hand: &str, #[case] class: &str) {
    let sketch = drawn("amp\nhand", "box", hand);
    assert_eq!(areas(&sketch)[1].1, class);
}

#[rstest]
#[case::a_wide_area("amp amp\nhand hand", "--ratio: 273 / 164")]
#[case::a_square_area("amp\nhand", "--ratio: 164 / 273")]
#[case::a_tall_area("amp hand\namp hand", "--ratio: 164 / 273")]
fn lays_a_shape_that_turns_on_its_side_in_a_wide_area(#[case] sketch: &str, #[case] ratio: &str) {
    let drawn = drawn(sketch, "speaker", "box");
    let style = &drawn.plan.roles[0].drawn.drawing.style;
    assert!(style.contains(ratio), "{style}");
}

#[rstest]
fn draws_a_role_whose_shape_is_unknown_as_a_box() {
    let sketch = drawn("amp\nhand", "kettle", "box");
    assert_eq!(
        sketch.plan.roles[0].drawn.drawing,
        sketch.plan.roles[1].drawn.drawing
    );
}

/// The sketch of the kiosk whose expected role `lamp` names nothing, in
/// the shape that `shape` writes for the role, when the knowledge of a
/// `Lamp` states a `disc`.
fn lamp(shape: &str) -> Sketch {
    let roles = format!("roles: {{lamp: {{reference: status.lamp.name, expected: true{shape}}}}}");
    let elevation = "elevations: [{name: the kiosk, question: What is it?, sketch: lamp}]";
    let spec = format!("{{{REFERENCES}, {roles}, {elevation}}}");
    let mut known = knowledge("kiosks.widgets.example.com", &spec);
    let shapes = Shapes::default();
    let disc = shapes.resolve("disc", "").cloned();
    known.draw_with(&shapes, |_, kind| disc.clone().filter(|_| kind == "Lamp"));
    page_of_known(&known, Standing::Plain, None, None)
        .sketch
        .unwrap()
}

#[rstest]
fn draws_an_expected_role_with_no_part_in_the_shape_of_its_kind() {
    let (stated, of_kind) = (lamp(", shape: disc"), lamp(""));
    let empty = of_kind.plan.roles[0].drawn.nothing.is_some();
    assert_eq!(
        (empty, &of_kind.plan.roles[0].drawn),
        (true, &stated.plan.roles[0].drawn)
    );
}

/// A row of a `slab`, an author's shape that joins on every edge, a
/// `disc`, which joins its sides, and `shape`. The slab and the disc
/// touch, so the row is on one axis, and the slab joins its top and its
/// bottom, so each other area of the row adds nothing to the row there.
fn row_with(shape: &str) -> Vec<String> {
    let slab = {
        let text = "{metadata: {name: slab.widgets.example.com}, spec: {summary: A slab., size: {design: [100, 50], width: [80, 120]}, joins: [left, right, top, bottom]}}";
        let value = serde_yaml_ng::from_str(text).unwrap();
        std::sync::Arc::new(transect_resources::shapes::parse(DocId::default(), value).unwrap())
    };
    let shapes = Shapes::with(&[slab]);
    let roles = format!(
        "roles: {{amp: {{reference: status.amp.name, shape: slab, expected: true}}, lamp: {{reference: status.lamp.name, shape: disc, expected: true}}, hand: {{reference: 'spec.handsets[].name', shape: {shape}}}}}"
    );
    let elevation = "elevations: [{name: the kiosk, question: What is it?, sketch: amp lamp hand}]";
    let spec = format!("{{{REFERENCES}, {roles}, {elevation}}}");
    let sketch = page_with(&spec, Standing::Plain, &shapes).sketch.unwrap();
    sketch
        .plan
        .roles
        .iter()
        .map(SketchRole::area_class)
        .collect()
}

#[rstest]
#[case::a_box("box")]
#[case::a_screen("screen")]
#[case::a_speaker("speaker")]
#[case::a_handheld("handheld")]
#[case::a_disc("disc")]
#[case::a_drum("drum")]
#[case::a_tag("tag")]
#[case::a_socket("socket")]
fn adds_nothing_to_a_row_on_one_axis_on_an_edge_that_another_part_joins(#[case] shape: &str) {
    let classes = row_with(shape);
    let free = "sketch-on-axis sketch-free-top sketch-free-bottom";
    assert!(classes[0].ends_with("sketch-on-axis"), "{classes:?}");
    assert!(classes[1].ends_with(free), "{classes:?}");
    assert!(classes[2].ends_with(free), "{classes:?}");
}
