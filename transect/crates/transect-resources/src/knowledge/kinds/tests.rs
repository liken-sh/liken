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

/// The library's shapes and an author's shape, `crate`, in the group of
/// the widget's kind and in the group of the gadget's.
fn shapes() -> Shapes {
    let author = |name: &str| {
        let text = format!(
            "{{metadata: {{name: {name}}}, spec: {{summary: A crate., size: {{design: [100, 50], width: [80, 120]}}}}}}"
        );
        let shape = crate::shapes::parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap());
        std::sync::Arc::new(shape.unwrap())
    };
    Shapes::with(&[
        author("crate.widgets.example.com"),
        author("crate.gadgets.example.com"),
    ])
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

/// A widget whose role `part` reads `REFERENCE` and states `SHAPE`. The
/// reference `spec.gadget` names a `Gadget`, `spec.node` names a `Node`,
/// which has no knowledge, and `spec.note` names no kind.
const WIDGET: &str = "references: [{path: spec.gadget, kind: Gadget, group: gadgets.example.com}, {path: spec.node, kind: Node}, {path: spec.note}],
    roles: {part: {reference: REFERENCE, expected: true SHAPE}},
    elevations: [{name: the bench, question: What is on it?, sketch: part}]";

/// The kind that states each condition: the widget's knowledge is the
/// first document, and the gadget's the second.
fn stated_by(kinds: &Kinds) -> Vec<(&'static str, &'static str)> {
    let kind = |doc: &DocId| ["Widget", "Gadget"][doc.document];
    kinds
        .conditions
        .iter()
        .map(|(doc, c)| (kind(doc), c.reason))
        .collect()
}

/// A part takes the first shape that is stated: its role's, then the one
/// that the knowledge of its kind states, then `box`. A kind's shape
/// resolves in the kind's own group, and a name that is not loaded gives
/// `UnknownShape` to the document that writes it.
#[rstest]
#[case::the_roles_over_the_kinds(Some("drum"), "spec.gadget", Some("disc"), "drum", vec![])]
#[case::an_unknown_role_shape(Some("kettle"), "spec.gadget", Some("disc"), "box", vec![("Widget", "UnknownShape")])]
#[case::the_kinds(None, "spec.gadget", Some("disc"), "disc", vec![])]
#[case::the_kinds_in_its_own_group(None, "spec.gadget", Some("crate"), "crate.gadgets.example.com", vec![])]
#[case::an_unknown_kind_shape(None, "spec.gadget", Some("kettle"), "box", vec![("Gadget", "UnknownShape")])]
#[case::a_kind_that_states_none(None, "spec.gadget", None, "box", vec![])]
#[case::a_kind_with_no_knowledge(None, "spec.node", Some("disc"), "box", vec![])]
#[case::a_value(None, "spec.note", Some("disc"), "box", vec![])]
fn draws_each_part_in_the_first_shape_stated(
    #[case] role: Option<&str>,
    #[case] reference: &str,
    #[case] kind: Option<&str>,
    #[case] drawn: &str,
    #[case] expected: Vec<(&str, &str)>,
) {
    let role = role.map_or_else(String::new, |shape| format!(", shape: {shape}"));
    let widget = WIDGET
        .replace("REFERENCE", reference)
        .replace("SHAPE", &role);
    let gadget = kind.map_or_else(String::new, |shape| format!("shape: {shape}"));
    let documents = [
        ("widgets.widgets.example.com", widget),
        ("gadgets.gadgets.example.com", gadget),
    ];
    let knowledge = documents
        .iter()
        .enumerate()
        .map(|(document, (name, spec))| {
            let text = format!("metadata: {{name: {name}}}\nspec: {{{spec}}}\n");
            let doc = DocId { file: 0, document };
            super::super::parse(doc, serde_yaml_ng::from_str(&text).unwrap()).unwrap()
        });
    let served = Plurals::from([
        (
            ("widgets.example.com".into(), "Widget".into()),
            "widgets".into(),
        ),
        (
            ("gadgets.example.com".into(), "Gadget".into()),
            "gadgets".into(),
        ),
    ]);
    let mut kinds = Kinds::resolve(&knowledge.collect::<Vec<_>>(), &served, &[]);
    kinds.find_shapes(&shapes());
    let widget = kinds.of("widgets.example.com", "Widget").unwrap();
    let part = widget.roles[0].drawn.name.as_str();
    assert_eq!((part, stated_by(&kinds)), (drawn, expected));
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
    let undrawn = super::super::parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap());
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
