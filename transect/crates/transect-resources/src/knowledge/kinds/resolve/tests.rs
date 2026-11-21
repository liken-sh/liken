use rstest::rstest;

use super::super::super::Healthy;
use super::super::super::fixtures::knowledge;
use super::*;
use crate::knowledge::role::Role;
use transect_graph::edges::Plurals;

fn plurals() -> Plurals {
    let served = [
        ("Widget", "widgets"),
        ("Pad", "pads"),
        ("Dial", "dials"),
        ("Kiosk", "kiosks"),
    ];
    let served = served.map(|(kind, plural)| {
        (
            ("pads.example.com".to_string(), kind.to_string()),
            plural.to_string(),
        )
    });
    Plurals::from(served)
}

const WIDGET: &str = "{references: [{path: status.pad, kind: Pad, group: pads.example.com}, {path: status.dial, kind: Dial, group: pads.example.com}, {path: status.twin, kind: Widget, group: pads.example.com}], healthy: {through: THROUGH}}";

/// The reason that a widget's health through `through` does not
/// resolve, when the pad's knowledge is `pad`. A dial's health is
/// through a widget, so a widget's health through a dial is a loop.
fn health(through: &str, pad: Option<&str>) -> Option<String> {
    let mut known = vec![knowledge(
        "widgets.pads.example.com",
        &WIDGET.replace("THROUGH", through),
    )];
    known.extend(pad.map(|spec| knowledge("pads.pads.example.com", spec)));
    known.push(knowledge(
        "dials.pads.example.com",
        "{healthy: {through: status.widget}, references: [{path: status.widget, kind: Widget, group: pads.example.com}]}",
    ));
    let kinds = Kinds::resolve(&known, &plurals(), &[]);
    let widget = kinds.of("pads.example.com", "Widget").unwrap();
    let Some(Healthy::Through(through)) = &widget.healthy else {
        return None;
    };
    through.unresolved.clone()
}

#[rstest]
#[case::a_kind_with_a_condition("status.pad", Some("{healthy: {condition: Connected}}"), None)]
#[case::a_kind_with_no_knowledge(
    "status.pad",
    None,
    Some("healthy follows status.pad, and transect has no knowledge of Pad")
)]
#[case::a_kind_that_states_no_condition(
    "status.pad",
    Some("{summary: A pad.}"),
    Some("healthy follows status.pad, and the knowledge of Pad states no condition of its own")
)]
#[case::a_loop_of_two_kinds(
    "status.dial",
    Some("{healthy: {condition: Connected}}"),
    Some("healthy follows status.dial, and the knowledge of Dial states no condition of its own")
)]
#[case::a_kind_through_itself(
    "status.twin",
    Some("{healthy: {condition: Connected}}"),
    Some(
        "healthy follows status.twin, and the knowledge of Widget states no condition of its own"
    )
)]
fn resolves_a_health_through_the_knowledge_of_the_kind_it_names(
    #[case] through: &str,
    #[case] pad: Option<&str>,
    #[case] unresolved: Option<&str>,
) {
    assert_eq!(health(through, pad).as_deref(), unresolved);
}

/// Each `through` that does not resolve gives its document the
/// condition `Unresolved`, and the document stays in use.
#[rstest]
fn gives_a_document_with_a_through_that_does_not_resolve_a_condition() {
    let widget = knowledge(
        "widgets.pads.example.com",
        &WIDGET.replace("THROUGH", "status.pad"),
    );
    let kinds = Kinds::resolve(&[widget], &plurals(), &[]);
    let reasons: Vec<&str> = kinds.conditions.iter().map(|(_, c)| c.reason).collect();
    let kept = kinds.of("pads.example.com", "Widget").is_some();
    assert_eq!((reasons, kept), (vec!["Unresolved"], true));
}

const KIOSK: &str = "{references: [{path: spec.widget, kind: Widget, group: pads.example.com}], roles: {widget: {reference: spec.widget, reads: [Size, {column: Charge, through: THROUGH}]}}}";

/// The reason that the widget's reading of `Charge` does not resolve,
/// when the widget's knowledge is `widget`.
fn reading(through: &str, widget: Option<&str>) -> Option<String> {
    let mut known = vec![knowledge(
        "kiosks.pads.example.com",
        &KIOSK.replace("THROUGH", through),
    )];
    known.extend(widget.map(|spec| knowledge("widgets.pads.example.com", spec)));
    let kinds = Kinds::resolve(&known, &plurals(), &[]);
    let kiosk = kinds.of("pads.example.com", "Kiosk").unwrap();
    let reads = &kiosk.roles[0].reads;
    assert_eq!(reads[0].through, None);
    reads[1].through.as_ref().unwrap().unresolved.clone()
}

#[rstest]
#[case::a_reference_of_the_parts_kind(
    "status.pad",
    Some("{references: [{path: status.pad, kind: Pad, group: pads.example.com}]}"),
    None
)]
#[case::a_part_kind_with_no_knowledge(
    "status.pad",
    None,
    Some("roles.widget reads Charge through status.pad, and transect has no knowledge of Widget")
)]
#[case::no_such_reference(
    "status.cell",
    Some("{references: [{path: status.pad, kind: Pad, group: pads.example.com}]}"),
    Some(
        "roles.widget reads Charge through status.cell, and the knowledge of Widget has no reference at status.cell that names a kind"
    )
)]
#[case::a_reference_that_names_no_kind(
    "status.pad",
    Some("{references: [{path: status.pad}]}"),
    Some(
        "roles.widget reads Charge through status.pad, and the knowledge of Widget has no reference at status.pad that names a kind"
    )
)]
fn resolves_a_reading_through_the_knowledge_of_the_parts_kind(
    #[case] through: &str,
    #[case] widget: Option<&str>,
    #[case] unresolved: Option<&str>,
) {
    assert_eq!(reading(through, widget).as_deref(), unresolved);
}

/// Rooms, shelves, and bins of `stock.example.com`: a shelf names its
/// room, and a bin names its room and its shelf.
fn stock() -> Plurals {
    let served = [("Room", "rooms"), ("Shelf", "shelves"), ("Bin", "bins")];
    let served = served.map(|(kind, plural)| {
        (
            ("stock.example.com".to_string(), kind.to_string()),
            plural.to_string(),
        )
    });
    Plurals::from(served)
}

const SHELF: &str = "{references: [{path: spec.room, kind: Room, group: stock.example.com}], elevations: [{name: the shelf, question: What is on it?, sketch: room}], roles: {room: {reference: spec.room}}}";

const BIN: &str = "{references: [{path: spec.room, kind: Room, group: stock.example.com}, {path: spec.shelf, kind: Shelf, group: stock.example.com}, {path: spec.label}]}";

/// The room's only role, `role`, after `Kinds::resolve`, with the shelf's
/// and the bin's knowledge when each is some, and the messages of the
/// room's conditions.
fn room_role(role: &str, shelf: Option<&str>, bin: Option<&str>) -> (Role, Vec<String>) {
    let mut known = vec![knowledge(
        "rooms.stock.example.com",
        &format!("{{roles: {{part: {role}}}}}"),
    )];
    known.extend(shelf.map(|spec| knowledge("shelves.stock.example.com", spec)));
    known.extend(bin.map(|spec| knowledge("bins.stock.example.com", spec)));
    let kinds = Kinds::resolve(&known, &stock(), &[]);
    let room = kinds.of("stock.example.com", "Room").unwrap();
    let messages = kinds.conditions.iter().map(|(_, c)| c.message.clone());
    (room.roles[0].clone(), messages.collect())
}

#[rstest]
#[case::a_reference_to_the_kind("{namedBy: {kind: Shelf, path: spec.room}}", Some(SHELF), None)]
#[case::a_kind_with_no_knowledge(
    "{namedBy: {kind: Shelf, path: spec.room}}",
    None,
    Some("roles.part is named by Shelf spec.room, and transect has no knowledge of Shelf")
)]
#[case::no_reference_at_the_path(
    "{namedBy: {kind: Shelf, path: spec.aisle}}",
    Some(SHELF),
    Some(
        "roles.part is named by Shelf spec.aisle, and the knowledge of Shelf has no reference at spec.aisle to Room"
    )
)]
#[case::a_reference_to_another_kind(
    "{namedBy: {kind: Bin, path: spec.shelf}}",
    Some(SHELF),
    Some("roles.part is named by Bin spec.shelf, and transect has no knowledge of Bin")
)]
fn resolves_the_objects_that_name_this_one_through_the_naming_kinds_knowledge(
    #[case] role: &str,
    #[case] shelf: Option<&str>,
    #[case] unresolved: Option<&str>,
) {
    let (role, messages) = room_role(role, shelf, None);
    let expected: Vec<String> = unresolved.into_iter().map(String::from).collect();
    assert_eq!(
        (role.unresolved.as_deref(), messages),
        (unresolved, expected)
    );
}

type Followed = (Option<(String, String)>, Option<String>);

#[rstest]
#[case::a_reference_of_the_first_kind(
    "spec.shelf",
    Some(BIN),
    (Some(("stock.example.com".into(), "Shelf".into())), None)
)]
#[case::a_reference_that_names_no_kind(
    "spec.label",
    Some(BIN),
    (None, Some("roles.part follows spec.label, and the knowledge of Bin has no reference at spec.label that names a kind".into()))
)]
#[case::no_reference_at_the_path(
    "spec.aisle",
    Some(BIN),
    (None, Some("roles.part follows spec.aisle, and the knowledge of Bin has no reference at spec.aisle that names a kind".into()))
)]
fn resolves_the_reference_that_a_role_follows_and_finds_its_kind(
    #[case] through: &str,
    #[case] bin: Option<&str>,
    #[case] expected: Followed,
) {
    let role = format!("{{namedBy: {{kind: Bin, path: spec.room}}, through: {through}}}");
    let (role, _) = room_role(&role, Some(SHELF), bin);
    let through = role.fill.through.unwrap();
    assert_eq!((through.target, through.unresolved), expected);
}

#[rstest]
#[case::an_elevation_of_the_parts_kind(
    "{namedBy: {kind: Shelf, path: spec.room}, elevation: the shelf}",
    None
)]
#[case::an_elevation_of_the_kind_at_the_end_of_through(
    "{namedBy: {kind: Bin, path: spec.room}, through: spec.shelf, elevation: the shelf}",
    None
)]
#[case::an_elevation_that_a_named_kind_does_not_state(
    "{namedBy: {kind: Shelf, path: spec.room}, elevation: the aisle}",
    Some(
        "roles.part draws each part as \"the aisle\", and the knowledge of Shelf states no elevation \"the aisle\""
    )
)]
#[case::an_elevation_that_a_kind_does_not_state(
    "{namedBy: {kind: Bin, path: spec.room}, elevation: the bin}",
    Some(
        "roles.part draws each part as \"the bin\", and the knowledge of Bin states no elevation \"the bin\""
    )
)]
fn resolves_the_elevation_that_draws_each_part_in_the_parts_kind(
    #[case] role: &str,
    #[case] unresolved: Option<&str>,
) {
    let (role, _) = room_role(role, Some(SHELF), Some(BIN));
    assert_eq!(role.unresolved.as_deref(), unresolved);
}
