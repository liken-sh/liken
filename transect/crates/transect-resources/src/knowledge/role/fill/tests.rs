use rstest::rstest;

use super::super::super::fixtures::knowledge;
use super::*;
use transect_graph::edges::Provenance;

/// The fill of the role `part` that `role` writes, in the knowledge of a
/// room with a reference to its thermostat and one to a label.
fn fill(role: &str) -> Fill {
    let spec = format!(
        "{{references: [{{path: spec.thermostat, kind: Thermostat}}, {{path: spec.label}}], roles: {{part: {role}}}}}"
    );
    let known = knowledge("rooms.stock.example.com", &spec);
    known.roles[0].fill.clone()
}

#[rstest]
#[case::a_reference("{reference: spec.thermostat}", "spec.thermostat")]
#[case::a_reference_then_through(
    "{reference: spec.thermostat, through: spec.vent}",
    "spec.thermostat, then spec.vent"
)]
#[case::the_objects_that_name_this_one(
    "{namedBy: {kind: Shelf, path: spec.room}}",
    "Shelf spec.room"
)]
#[case::named_by_then_through(
    "{namedBy: {kind: Bin, path: spec.room}, through: spec.shelf}",
    "Bin spec.room, then spec.shelf"
)]
#[case::the_controller("{controller: true}", "the controller")]
#[case::what_each_part_owns(
    "{namedBy: {kind: Shelf, path: spec.room}, owns: Job}",
    "Shelf spec.room, then each Job it owns"
)]
fn names_the_fields_that_its_parts_are_read_from(#[case] role: &str, #[case] fields: &str) {
    assert_eq!(fill(role).fields(), fields);
}

#[rstest]
#[case::its_own_group("{namedBy: {kind: Shelf, path: spec.room}}", "stock.example.com")]
#[case::another_group(
    "{namedBy: {kind: Shelf, group: racks.example.com, path: spec.room}}",
    "racks.example.com"
)]
fn takes_the_group_of_the_pages_kind_for_the_objects_that_name_it(
    #[case] role: &str,
    #[case] group: &str,
) {
    let found = fill(role);
    assert_eq!(found.first_kind(&[]), Some((group, "Shelf")));
}

fn object(kind: &str, namespace: &str, name: &str) -> Object {
    Object::new("stock.example.com", kind, Some(namespace), name)
}

fn relation(path: &str, names: Names) -> Relation {
    Relation {
        label: "part of".into(),
        path: path.into(),
        edge: None,
        value: "a".into(),
        names,
        provenance: Provenance::Kubernetes,
    }
}

/// A shelf names the room `back` at `spec.room` and the room `front` at
/// `spec.spill`.
#[rstest]
#[case::the_path_and_the_object(
    object("Shelf", "ops", "top"),
    "spec.room",
    object("Room", "ops", "back"),
    true
)]
#[case::another_path(
    object("Shelf", "ops", "top"),
    "spec.room",
    object("Room", "ops", "front"),
    false
)]
#[case::the_object_in_another_namespace(
    object("Shelf", "ops", "top"),
    "spec.room",
    object("Room", "shop", "back"),
    false
)]
#[case::another_kind(
    object("Bin", "ops", "top"),
    "spec.room",
    object("Room", "ops", "back"),
    false
)]
fn finds_whether_an_object_names_the_pages_object_at_the_path(
    #[case] referrer: Object,
    #[case] path: &str,
    #[case] named: Object,
    #[case] names: bool,
) {
    let relations = [
        relation("spec.room", Names::Object(object("Room", "ops", "back"))),
        relation("spec.spill", Names::Object(object("Room", "ops", "front"))),
    ];
    let named_by = NamedBy {
        kind: "Shelf".into(),
        group: "stock.example.com".into(),
        path: path.into(),
    };
    assert_eq!(named_by.names(&referrer, &relations, &named), names);
}
