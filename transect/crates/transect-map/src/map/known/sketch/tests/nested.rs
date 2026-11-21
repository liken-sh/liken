//! A role whose parts are drawn as an elevation of their own kind: each
//! part's elevation is drawn by the code that draws every elevation,
//! with the roles of the part's kind read against the part.

use askama::Template;
use rstest::rstest;

use super::super::super::Standing;
use super::super::NestedPart;
use super::stock::{Stock, room, room_page, shelves};
use super::why;
use crate::shapes::text::State;

const SHELVES: &str =
    "shelves: {namedBy: {kind: Shelf, path: spec.room}, elevation: ELEVATION, expected: true}";

fn nested(elevation: &str, standing: Standing) -> Vec<NestedPart> {
    let stock = Stock {
        objects: shelves(),
        ..Stock::default()
    };
    let roles = SHELVES.replace("ELEVATION", elevation);
    let page = room_page(&room(&roles, "shelves"), &stock, standing);
    let mut roles = page.sketch.unwrap().plan.roles;
    roles.remove(0).nested
}

/// Each role of a nested elevation, as its area, its parts, and what it
/// shows with none.
type Inner = Vec<(String, Vec<String>, Option<String>)>;

fn inner(part: &NestedPart) -> Inner {
    let roles = part.plan.roles.iter().map(|role| {
        let parts = role
            .drawn
            .parts
            .iter()
            .map(|p| p.end.reference.text.clone());
        let nothing = role.drawn.nothing.as_ref().map(|n| n.note.clone());
        (role.area.clone(), parts.collect(), nothing)
    });
    roles.collect()
}

/// Each shelf is the shelf's elevation. A shelf's room is the page's own
/// object, so its place is blank, though the role is expected. The bins
/// of a shelf name their own elevation, and inside a nested elevation
/// they are drawn in their shape.
#[rstest]
fn draws_each_part_as_the_elevation_of_its_kind() {
    let found = nested("the shelf", Standing::Plain);
    let drawn: Vec<(String, String, String, Inner)> = found
        .iter()
        .map(|part| {
            let name = part.name.text.clone();
            (part.label.clone(), name, part.source.clone(), inner(part))
        })
        .collect();
    let bins = |parts: Vec<&str>, nothing: Option<&str>| {
        let parts = parts.into_iter().map(String::from).collect();
        vec![("bins".to_string(), parts, nothing.map(String::from))]
    };
    assert_eq!(
        drawn,
        vec![
            (
                "shelf".into(),
                "top".into(),
                "from Shelf spec.room".into(),
                bins(vec!["screw"], None)
            ),
            (
                "shelf".into(),
                "low".into(),
                "from Shelf spec.room".into(),
                bins(vec![], Some("no Bin names this Shelf in spec.shelf"))
            ),
        ]
    );
}

#[rstest]
fn draws_each_part_in_its_shape_when_its_kind_states_no_such_elevation() {
    let stock = Stock {
        objects: shelves(),
        ..Stock::default()
    };
    let roles = SHELVES.replace("ELEVATION", "the aisle");
    let page = room_page(&room(&roles, "shelves"), &stock, Standing::Plain);
    let sketch = page.sketch.unwrap();
    let role = &sketch.plan.roles[0];
    let parts: Vec<&str> = role
        .drawn
        .parts
        .iter()
        .map(|p| p.end.reference.text.as_str())
        .collect();
    let under = role.drawn.parts[0].end.under.as_deref().unwrap_or_default();
    assert_eq!(
        (
            role.nested.len(),
            parts,
            under.contains("states no elevation")
        ),
        (0, vec!["top", "low"], true)
    );
}

#[rstest]
#[case::plain(Standing::Plain, State::Plain, "sketch-nested")]
#[case::at_rest(Standing::Resting(why()), State::Resting, "sketch-nested")]
#[case::a_warning(
    Standing::Warning(why()),
    State::Warning,
    "sketch-nested sketch-warning"
)]
#[case::failing(
    Standing::Failing(why()),
    State::Failing,
    "sketch-nested sketch-failing"
)]
fn marks_a_nested_part_that_deviates_as_the_head_of_a_sketch(
    #[case] standing: Standing,
    #[case] state: State,
    #[case] class: &str,
) {
    let found = nested("the shelf", standing);
    assert_eq!((found[0].state, found[0].class()), (state, class));
}

/// The page names each part that a nested elevation draws, so the board
/// under the sketch leaves them out, and the page writes each nested
/// elevation with its name after its grid.
#[rstest]
fn names_and_writes_every_part_that_a_nested_elevation_draws() {
    let stock = Stock {
        objects: shelves(),
        ..Stock::default()
    };
    let roles = SHELVES.replace("ELEVATION", "the shelf");
    let page = room_page(&room(&roles, "shelves"), &stock, Standing::Plain);
    let sketch = page.sketch.as_ref().unwrap();
    let names: Vec<&str> = sketch
        .plan
        .references()
        .iter()
        .map(|r| r.text.as_str())
        .collect();
    let html = page.render().unwrap();
    let written = html.matches("class=\"sketch-nested\"").count();
    assert_eq!((names, written), (vec!["top", "screw", "low"], 2));
}
