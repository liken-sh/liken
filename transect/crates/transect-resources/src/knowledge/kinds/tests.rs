use rstest::rstest;

use super::super::fixtures::{knowledge, plurals};
use super::*;
use crate::shapes::Shapes;

fn resolved(name: &str, spec: &str) -> Kinds {
    Kinds::resolve(&[knowledge(name, spec)], &plurals(), &["apps", "platform"])
}

fn reasons(kinds: &Kinds) -> Vec<&'static str> {
    kinds.conditions.iter().map(|(_, c)| c.reason).collect()
}

#[rstest]
#[case::served("widgets.widgets.example.com", "{}", true, vec![])]
#[case::not_served("gadgets.widgets.example.com", "{}", false, vec!["NotServed"])]
#[case::an_unknown_layer("widgets.widgets.example.com", "{layer: edge}", true, vec!["UnknownLayer"])]
#[case::a_declared_layer("widgets.widgets.example.com", "{layer: platform}", true, vec![])]
fn matches_each_name_to_a_served_kind(
    #[case] name: &str,
    #[case] spec: &str,
    #[case] found: bool,
    #[case] expected: Vec<&'static str>,
) {
    let kinds = resolved(name, spec);
    let widget = kinds.of("widgets.example.com", "Widget");
    assert_eq!((widget.is_some(), reasons(&kinds)), (found, expected));
}

const ELEVATION: &str = "references: [{path: spec.node, kind: Node}], roles: {node: {reference: spec.node, shape: SHAPE}}, elevations: [{name: the rack, question: Where is it?, sketch: node}]";

/// The library's shapes and one author's shape, `crate`, in the group
/// of the widget's kind.
fn shapes() -> Shapes {
    let author = crate::shapes::parse(
        DocId::default(),
        serde_yaml::from_str(
            "{metadata: {name: crate.widgets.example.com}, spec: {summary: A crate., size: {design: [100, 50], width: [80, 120]}}}",
        )
        .unwrap(),
    )
    .unwrap();
    Shapes::with(&[std::sync::Arc::new(author)])
}

/// A role's shape, as `find_shapes` finds it among the library's shapes
/// and one author's shape in the kind's group, with the condition for a
/// shape that is not there.
#[rstest]
#[case::the_library("drum", "drum", vec![])]
#[case::the_kinds_own_group("crate", "crate.widgets.example.com", vec![])]
#[case::a_whole_name("crate.widgets.example.com", "crate.widgets.example.com", vec![])]
#[case::no_such_shape("kettle", "box", vec!["UnknownShape"])]
fn draws_each_role_in_the_shape_it_names(
    #[case] written: &str,
    #[case] drawn: &str,
    #[case] expected: Vec<&'static str>,
) {
    let spec = format!("{{{}}}", ELEVATION.replace("SHAPE", written));
    let mut kinds = Kinds::resolve(
        &[knowledge("widgets.widgets.example.com", &spec)],
        &plurals(),
        &[],
    );
    kinds.find_shapes(&shapes());
    let widget = kinds.of("widgets.example.com", "Widget").unwrap();
    let role = &widget.roles[0];
    assert_eq!(
        (role.drawn.name.as_str(), reasons(&kinds)),
        (drawn, expected)
    );
}

/// A document whose kind the cluster does not serve is left out with
/// `NotServed`, so its shapes are checked only when it is served.
#[rstest]
fn checks_no_shape_of_a_kind_the_cluster_does_not_serve() {
    let spec = format!("{{{}}}", ELEVATION.replace("SHAPE", "kettle"));
    let mut kinds = resolved("gadgets.widgets.example.com", &spec);
    kinds.find_shapes(&Shapes::default());
    assert_eq!(reasons(&kinds), vec!["NotServed"]);
}

const WIRED: &str = "references: [{path: spec.a, kind: Node}, {path: spec.b, kind: Node}],
    roles: {a: {reference: spec.a, shape: FROM}, b: {reference: spec.b, shape: TO}},
    elevations: [{name: the line, question: What passes?, sketch: SKETCH, wires: [{from: a, to: b, carries: power}]}]";

/// A wire that meets a role whose shape has no port on the facing edge
/// is not drawn, and its knowledge gets `NoPort`. A handheld and a tag
/// have no port, a box has ports on its sides, a socket has one on its
/// bottom, and a drum has one on its top.
#[rstest]
#[case::boxes_side_by_side("box", "box", "a b", 1, vec![])]
#[case::to_a_handheld("box", "handheld", "a b", 0, vec!["NoPort"])]
#[case::from_a_tag("tag", "box", "a b", 0, vec!["NoPort"])]
#[case::down_from_a_box_to_a_drum("box", "drum", "\"a\\nb\"", 0, vec!["NoPort"])]
#[case::down_from_a_socket_to_a_drum("socket", "drum", "\"a\\nb\"", 1, vec![])]
fn leaves_out_a_wire_that_meets_no_port(
    #[case] from: &str,
    #[case] to: &str,
    #[case] sketch: &str,
    #[case] wires: usize,
    #[case] expected: Vec<&'static str>,
) {
    let wired = WIRED.replace("FROM", from).replace("TO", to);
    let spec = format!("{{{}}}", wired.replace("SKETCH", sketch));
    let text = format!("metadata: {{name: widgets.widgets.example.com}}\nspec: {spec}\n");
    let undrawn = super::super::parse(DocId::default(), serde_yaml::from_str(&text).unwrap());
    let mut kinds = Kinds::resolve(&[undrawn.unwrap()], &plurals(), &[]);
    kinds.find_shapes(&Shapes::default());
    let widget = kinds.of("widgets.example.com", "Widget").unwrap();
    assert_eq!(
        (widget.elevations[0].wires.len(), reasons(&kinds)),
        (wires, expected)
    );
}

#[rstest]
fn drops_a_layer_no_layers_declares() {
    let kinds = resolved(
        "widgets.widgets.example.com",
        "{layer: edge, summary: Turns.}",
    );
    let widget = kinds.of("widgets.example.com", "Widget").unwrap();
    assert_eq!(
        (widget.layer.as_deref(), widget.summary.as_deref()),
        (None, Some("Turns."))
    );
}

#[rstest]
#[case::operator("{disruptions: operator}", true)]
#[case::not_declared("{}", false)]
fn reports_whether_the_operator_moves_work_before_eviction(
    #[case] spec: &str,
    #[case] moves: bool,
) {
    let kinds = resolved("widgets.widgets.example.com", spec);
    let widget = Object::new("widgets.example.com", "Widget", Some("shop"), "a");
    assert_eq!(kinds.moves_before_eviction(&widget), moves);
}
