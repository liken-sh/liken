//! The parts of a role whose objects name the page's object, or that
//! follows one more reference, and the rules of every elevation: each
//! object is drawn once, and a role expected in a state of the object is
//! drawn at rest in other states.

use rstest::rstest;

use super::super::super::Standing;
use super::stock::{Ladder, Stock, object, room, room_page, shelves};
use crate::shapes::text::State;

/// Each part of the room's first role as its name, its note, and its
/// state, and the role's source line.
type Parts = (Vec<(String, Option<String>, State)>, String);

fn parts(roles: &str, sketch: &str, stock: &Stock, standing: Standing) -> Parts {
    let page = room_page(&room(roles, sketch), stock, standing);
    let sketch = page.sketch.unwrap();
    let drawn = &sketch.plan.roles[0].drawn;
    let parts = drawn.parts.iter().map(|part| {
        let end = &part.end;
        (end.reference.text.clone(), end.under.clone(), part.state)
    });
    (parts.collect(), drawn.source.clone())
}

/// What the room's role `area` shows with no part, as its text, its
/// note, and its state, or none when the page leaves its area blank.
fn nothing(
    roles: &str,
    sketch: &str,
    stock: &Stock,
    area: &str,
) -> Option<(String, String, State)> {
    let page = room_page(&room(roles, sketch), stock, Standing::Plain);
    let sketch = page.sketch.unwrap();
    let role = sketch
        .plan
        .roles
        .into_iter()
        .find(|role| role.area == area)?;
    let nothing = role.drawn.nothing?;
    Some((nothing.text, nothing.note, nothing.state))
}

const SHELF: &str = "shelf: {namedBy: {kind: Shelf, path: spec.room}, expected: true}";

fn part(name: &str, under: Option<&str>, state: State) -> (String, Option<String>, State) {
    (name.into(), under.map(String::from), state)
}

#[rstest]
fn draws_a_part_for_each_object_that_names_the_pages_object() {
    let stock = Stock {
        objects: shelves(),
        ..Stock::default()
    };
    let expected = (
        vec![
            part("top", None, State::Plain),
            part("low", None, State::Plain),
        ],
        "from Shelf spec.room".to_string(),
    );
    assert_eq!(parts(SHELF, "shelf", &stock, Standing::Plain), expected);
}

#[rstest]
fn says_that_no_object_names_the_pages_object() {
    let found = nothing(SHELF, "shelf", &Stock::default(), "shelf");
    let expected = (
        "no Shelf",
        "no Shelf names this Room in spec.room",
        State::Empty,
    );
    assert_eq!(
        found,
        Some((expected.0.into(), expected.1.into(), expected.2))
    );
}

const LADDER: &str =
    "ladder: {namedBy: {kind: Bin, path: spec.room}, through: spec.ladder, expected: true}";

/// Two bins in the room, whose ladders end as `screw` and `nail` say.
fn ladders(screw: &[Ladder], nail: &[Ladder]) -> Stock {
    Stock {
        objects: vec![
            (object("Bin", "screw"), "{room: back}"),
            (object("Bin", "nail"), "{room: back}"),
        ],
        ladders: [("screw", screw.to_vec()), ("nail", nail.to_vec())].into(),
        ..Stock::default()
    }
}

#[rstest]
#[case::an_object_the_cluster_has(
    ladders(&[Ladder::Found("tall")], &[]),
    Standing::Plain,
    vec![part("Ladder tall", None, State::Plain)]
)]
#[case::an_object_the_cluster_does_not_have(
    ladders(&[Ladder::Missing("gone")], &[]),
    Standing::Missing,
    vec![part("Ladder gone", Some("the cluster has no such Ladder"), State::NoObject)]
)]
#[case::an_object_of_a_kind_not_watched(
    ladders(&[Ladder::Unwatched("far")], &[]),
    Standing::Plain,
    vec![part("Ladder far", None, State::Plain)]
)]
#[case::a_value_that_matches_several_objects(
    ladders(&[Ladder::Ambiguous(2)], &[]),
    Standing::Plain,
    vec![part("spec.ladder of Bin screw", Some("matches 2 objects, so none is drawn"), State::Plain)]
)]
#[case::two_objects_that_name_one(
    ladders(&[Ladder::Found("tall")], &[Ladder::Found("tall")]),
    Standing::Plain,
    vec![part("Ladder tall", None, State::Plain)]
)]
#[case::each_end_of_each_object(
    ladders(&[Ladder::Found("tall")], &[Ladder::Found("short")]),
    Standing::Plain,
    vec![part("Ladder tall", None, State::Plain), part("Ladder short", None, State::Plain)]
)]
fn draws_each_end_of_one_more_reference_as_a_part(
    #[case] stock: Stock,
    #[case] standing: Standing,
    #[case] expected: Vec<(String, Option<String>, State)>,
) {
    let found = parts(LADDER, "ladder", &stock, standing);
    let source = "from Bin spec.room, then spec.ladder".to_string();
    assert_eq!(found, (expected, source));
}

#[rstest]
fn says_that_no_object_names_an_object_through_the_reference() {
    let found = nothing(LADDER, "ladder", &ladders(&[], &[]), "ladder");
    let expected = (
        "no Ladder",
        "no Bin names a Ladder in spec.ladder",
        State::Empty,
    );
    assert_eq!(
        found,
        Some((expected.0.into(), expected.1.into(), expected.2))
    );
}

const AGAIN: &str =
    "again: {namedBy: {kind: Shelf, path: spec.room}, label: spare shelf, expected: EXPECTED}";

#[rstest]
#[case::an_expected_role(
    "true",
    Some((
        "no other spare shelf".to_string(),
        "from Shelf spec.room, except Shelf top, which shelf draws, and Shelf low, which shelf draws".to_string(),
        State::Elsewhere
    ))
)]
#[case::a_role_that_is_not_expected("false", None)]
fn draws_each_object_in_the_first_role_that_reads_it(
    #[case] expected: &str,
    #[case] shown: Option<(String, String, State)>,
) {
    let stock = Stock {
        objects: shelves(),
        ..Stock::default()
    };
    let roles = format!("{SHELF}, {}", AGAIN.replace("EXPECTED", expected));
    assert_eq!(nothing(&roles, "shelf\nagain", &stock, "again"), shown);
}

#[rstest]
fn takes_the_roles_in_the_order_of_their_first_cells() {
    let stock = Stock {
        objects: shelves(),
        ..Stock::default()
    };
    let roles = format!("{SHELF}, {}", AGAIN.replace("EXPECTED", "true"));
    let (found, _) = parts(&roles, "again shelf", &stock, Standing::Plain);
    let names: Vec<String> = found.into_iter().map(|(name, ..)| name).collect();
    assert_eq!(names, vec!["Shelf top", "Shelf low"]);
}

const KEEPER: &str = "keeper: {namedBy: {kind: Keeper, path: spec.room}, expected: {while: Open}}";

#[rstest]
#[case::while_the_condition_is_true(
    vec![("Open", "True", None)],
    ("no Keeper", "no Keeper names this Room in spec.room", State::Empty)
)]
#[case::while_it_is_false(
    vec![("Open", "False", Some("Closed"))],
    ("no Keeper", "expected while Open, now Closed", State::Elsewhere)
)]
#[case::while_it_is_false_with_no_reason(
    vec![("Open", "False", None)],
    ("no Keeper", "expected while Open, now Open is False", State::Elsewhere)
)]
#[case::while_the_object_has_no_such_condition(
    vec![("Ready", "True", None)],
    ("no Keeper", "expected while Open, and the Room has no Open condition", State::Elsewhere)
)]
fn expects_a_role_while_a_condition_of_the_pages_object_is_true(
    #[case] conditions: Vec<(&'static str, &'static str, Option<&'static str>)>,
    #[case] expected: (&str, &str, State),
) {
    let stock = Stock {
        conditions,
        ..Stock::default()
    };
    let found = nothing(KEEPER, "keeper", &stock, "keeper");
    assert_eq!(
        found,
        Some((expected.0.into(), expected.1.into(), expected.2))
    );
}

#[rstest]
fn draws_a_part_of_a_role_expected_in_another_state() {
    let stock = Stock {
        objects: vec![(object("Keeper", "ann"), "{room: back}")],
        conditions: vec![("Open", "False", Some("Closed"))],
        ..Stock::default()
    };
    let (found, _) = parts(KEEPER, "keeper", &stock, Standing::Plain);
    assert_eq!(found, vec![part("ann", None, State::Plain)]);
}

/// A column whose roles the page leaves out takes no width, and one
/// with an empty cell keeps it.
#[rstest]
#[case::a_column_of_roles_left_out("shelf keeper", "minmax(160px, 1fr) 0px 0px")]
#[case::a_column_with_an_empty_cell(
    "shelf keeper\n. .",
    "minmax(160px, 1fr) var(--sketch-column-gap) minmax(0, 1fr)"
)]
fn gives_no_width_to_a_column_of_roles_the_page_leaves_out(
    #[case] sketch: &str,
    #[case] columns: &str,
) {
    let stock = Stock {
        objects: shelves(),
        ..Stock::default()
    };
    let roles = format!("{SHELF}, keeper: {{namedBy: {{kind: Keeper, path: spec.room}}}}");
    let page = room_page(&room(&roles, sketch), &stock, Standing::Plain);
    assert_eq!(page.sketch.unwrap().plan.columns, columns);
}
