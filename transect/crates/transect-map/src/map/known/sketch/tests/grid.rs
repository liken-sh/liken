//! The grid of a sketch, and the page around it: the roles laid out on
//! the grid, the sketch in place of the ring of boxes, and the board
//! under the sketch.

use rstest::rstest;

use super::super::super::Standing;
use super::super::SketchRole;
use super::{ELEVATION, REFERENCES, ROLES, page_of, sketch};
use crate::objects::{Center, Section, Side};
use transect_resources::shapes::Shapes;

#[rstest]
fn lays_the_roles_out_on_the_grid_of_the_sketch() {
    let sketch = sketch(Standing::Plain);
    let drawn_in = |name: &str| {
        let shape = Shapes::default().resolve(name, "").unwrap().clone();
        crate::shapes::Drawing::of(&shape, crate::shapes::Pose::default())
    };
    let drawings: Vec<&crate::shapes::Drawing> =
        sketch.plan.roles.iter().map(|r| &r.drawn.drawing).collect();
    let shapes = ["screen", "box", "handheld", "speaker", "box"].map(drawn_in);
    let areas: Vec<String> = sketch
        .plan
        .roles
        .iter()
        .map(SketchRole::area_class)
        .collect();
    assert_eq!(
        (
            sketch.name.as_str(),
            sketch.question.as_str(),
            sketch.plan.columns.as_str(),
            sketch.plan.areas.as_str(),
            sketch.plan.rows.as_str(),
            drawings,
            areas,
            sketch.plan.roles[0].drawn.source.as_str(),
            sketch.caption(),
        ),
        (
            "the kiosk",
            "What is this kiosk made of?",
            "minmax(160px, 1fr) var(--sketch-column-gap) minmax(130px, 1fr) var(--sketch-column-gap) minmax(160px, 1fr)",
            r#""face face face . amp" ". . . . ." "hand . lamp . bell" ". . . . ." "bus . . . .""#,
            "auto var(--sketch-gap) auto var(--sketch-gap) auto",
            shapes.iter().collect::<Vec<_>>(),
            vec![
                "sketch-area sketch-top-row".to_string(),
                "sketch-area sketch-lean-start sketch-top-row".into(),
                "sketch-area sketch-lean-end".into(),
                "sketch-area".into(),
                "sketch-area sketch-lean-end sketch-bottom-row".into(),
            ],
            "from status.screen.monitor",
            "Hover a part for the field it was read from. The places are from the KindKnowledge kiosks.widgets.example.com. A part's shape is from it or from the KindKnowledge of the part's kind, and /shapes draws each shape.".to_string(),
        )
    );
}

#[rstest]
#[case::with_an_elevation(format!("{{{REFERENCES}, {ROLES}, {ELEVATION}}}"), true, 0)]
#[case::without_one(format!("{{{REFERENCES}}}"), false, 6)]
fn draws_a_sketch_in_place_of_the_ring(
    #[case] spec: String,
    #[case] sketched: bool,
    #[case] boxes: usize,
) {
    let page = page_of(&spec, Standing::Plain);
    assert_eq!(
        (page.sketch.is_some(), page.boxes.len(), page.diagram()),
        (sketched, boxes, true)
    );
}

#[rstest]
fn leaves_out_of_the_board_each_value_the_sketch_shows() {
    let spec = format!("{{{REFERENCES}, {ROLES}, {ELEVATION}}}");
    let page = page_of(&spec, Standing::Plain);
    let center = Center {
        kind: "Kiosk".into(),
        name: "front".into(),
        level: None,
        resting: None,
        note: None,
        chips: Vec::new(),
        messages: Vec::new(),
    };
    let section = Section::new("Refers to", Side::Out)
        .literals("shows on", vec!["tv-1".into()])
        .literals("sells", vec!["tea".into()]);
    let page = page.with_board(center, vec![section]);
    let board = &page.board;
    let facts = board.right.iter().flat_map(|section| &section.facts);
    let labels: Vec<&str> = facts.map(|fact| fact.label.as_str()).collect();
    assert_eq!((board.center.is_none(), labels), (true, vec!["sells"]));
}
