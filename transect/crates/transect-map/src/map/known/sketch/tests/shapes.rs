//! The sketch draws each role in the shape that it names: contact
//! between rows, parts that touch in a row, a shape on its side, and
//! the default shape.

use rstest::rstest;

use super::super::*;
use super::{REFERENCES, page_with};
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
        let value = serde_yaml::from_str(&text).unwrap();
        std::sync::Arc::new(transect_resources::shapes::parse(DocId::default(), value).unwrap())
    };
    let shapes = Shapes::with(&[shape("rail", "[bottom]"), shape("cart", "[top]")]);
    let elevation = format!(
        "elevations: [{{name: the kiosk, question: What is it?, sketch: {sketch:?}, roles: {{amp: {{reference: status.amp.name, shape: {amp}, expected: true}}, hand: {{reference: 'spec.handsets[].name', shape: {hand}}}}}}}]"
    );
    let spec = format!("{{{REFERENCES}, {elevation}}}");
    page_with(&spec, Standing::Plain, &shapes).sketch.unwrap()
}

fn areas(sketch: &Sketch) -> Vec<(&str, String)> {
    let roles = sketch.roles.iter();
    roles
        .map(|role| (role.area.as_str(), role.area_class()))
        .collect()
}

#[rstest]
#[case::both_edges_join("rail", "cart", "auto 0px auto", "sketch-area sketch-joined-above")]
#[case::the_upper_edge_does_not_join("box", "cart", "auto var(--sketch-gap) auto", "sketch-area")]
#[case::the_lower_edge_does_not_join("rail", "box", "auto var(--sketch-gap) auto", "sketch-area")]
fn closes_the_gap_between_rows_where_both_parts_join(
    #[case] amp: &str,
    #[case] hand: &str,
    #[case] rows: &str,
    #[case] class: &str,
) {
    let sketch = drawn("amp\nhand", amp, hand);
    assert_eq!(
        (sketch.rows.as_str(), areas(&sketch)),
        (
            rows,
            vec![("amp", "sketch-area".into()), ("hand", class.into())]
        )
    );
}

#[rstest]
#[case::a_shape_that_joins_its_sides("disc", "sketch-area sketch-abutting")]
#[case::a_shape_that_does_not("box", "sketch-area")]
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
    let style = &drawn.roles[0].drawn.drawing.style;
    assert!(style.contains(ratio), "{style}");
}

#[rstest]
fn draws_a_role_whose_shape_is_unknown_as_a_box() {
    let sketch = drawn("amp\nhand", "kettle", "box");
    assert_eq!(sketch.roles[0].drawn.drawing, sketch.roles[1].drawn.drawing);
}
