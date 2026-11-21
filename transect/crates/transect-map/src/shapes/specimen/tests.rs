use std::sync::Arc;

use askama::Template;
use rstest::rstest;

use super::*;
use transect_resources::resources::{Condition, DocId, Sorts, from_text};
use transect_resources::shapes::library;

/// The `Shape` documents of a resources directory, sorted as the
/// binary sorts them.
#[derive(Default)]
struct Loaded {
    directory: Directory,
    shapes: Vec<Arc<Shape>>,
}

impl Sorts for Loaded {
    fn directory(&mut self) -> &mut Directory {
        &mut self.directory
    }

    fn take(&mut self, doc: DocId, kind: &str, value: serde_yaml::Value) -> Option<Condition> {
        (kind == "Shape").then(|| match transect_resources::shapes::parse(doc, value) {
            Ok(shape) => {
                self.shapes.push(Arc::new(shape));
                Condition::loaded()
            }
            Err(message) => Condition::left_out("Invalid", message),
        })
    }
}

/// A directory of one file, `shapes.yaml`, with an author's shape whose
/// summary has markup in it and a shape that does not load.
fn directory() -> (Directory, Shapes) {
    let text = "
apiVersion: transect.liken.sh/v1alpha1
kind: Shape
metadata: {name: plate.example.test}
spec:
  summary: <b>A plate</b> & a bolt.
  size: {design: [100, 50], width: [80, 120]}
  joins: [top]
  ports: {left: [40%, 60%]}
  display: window
  turns: true
---
apiVersion: transect.liken.sh/v1alpha1
kind: Shape
metadata: {name: glowing.example.test}
spec: {summary: A glow., size: {design: [100, 50], width: [80, 120]}, color: red}
";
    let loaded: Loaded = from_text("shapes.yaml", text);
    (loaded.directory, Shapes::with(&loaded.shapes))
}

fn page() -> ShapesPage {
    let (directory, shapes) = directory();
    ShapesPage::build(&shapes, &directory)
}

#[rstest]
fn lists_the_library_by_name_and_then_each_file_of_authors() {
    let page = page();
    let groups: Vec<(&str, Vec<&str>)> = page
        .groups
        .iter()
        .map(|group| {
            (
                group.heading.as_str(),
                group.shapes.iter().map(|s| s.name.as_str()).collect(),
            )
        })
        .collect();
    assert_eq!(
        groups,
        vec![
            (
                "the library",
                vec![
                    "box", "disc", "drum", "handheld", "screen", "socket", "speaker", "tag"
                ]
            ),
            ("shapes.yaml", vec!["plate.example.test"]),
        ]
    );
}

#[rstest]
fn says_what_each_shape_joins_where_wires_attach_and_its_display() {
    let page = page();
    let plate = &page.groups[1].shapes[0];
    assert_eq!(
        plate.facts,
        "joins on its top; wires at left 40% to 60%; display: window; lies on its side in a wide area"
    );
}

#[rstest]
fn lists_each_shape_that_did_not_load() {
    let page = page();
    let left_out: Vec<(&str, &str)> = page
        .left_out
        .iter()
        .map(|l| (l.path.as_str(), l.name.as_str()))
        .collect();
    assert_eq!(
        (
            left_out,
            page.left_out[0].condition.contains("unknown field `color`")
        ),
        (vec![("shapes.yaml", "glowing.example.test")], true)
    );
}

/// The words of every state of plan 17's table, which a cell of each
/// shape draws.
const STATES: [&str; 8] = [
    "shape-plain",
    "shape-warning",
    "shape-no-object",
    "shape-failing",
    "shape-resting",
    "shape-value",
    "shape-empty",
    "shape-elsewhere",
];

/// The classes of the section of `name` on the rendered page.
fn classes_of(html: &str, name: &str) -> Vec<String> {
    let start = html
        .find(&format!("<section class=\"shapes-shape\" id=\"{name}\""))
        .unwrap();
    let section = &html[start..];
    let section = &section[..section.find("</section>").unwrap()];
    let classes = section.split("class=\"").skip(1);
    let classes = classes.flat_map(|rest| rest.split('"').next().unwrap().split_whitespace());
    classes.map(String::from).collect()
}

/// Each shape of the library is drawn in each state, and no class on
/// the page names a shape, so a state looks the same on every shape.
#[rstest]
#[case::a_box("box")]
#[case::a_disc("disc")]
#[case::a_drum("drum")]
#[case::a_handheld("handheld")]
#[case::a_screen("screen")]
#[case::a_socket("socket")]
#[case::a_speaker("speaker")]
#[case::a_tag("tag")]
fn draws_each_shape_in_every_state_with_no_class_that_names_a_shape(#[case] name: &str) {
    let html = page().render().unwrap();
    let classes = classes_of(&html, name);
    let missing: Vec<&str> = STATES
        .into_iter()
        .filter(|state| !classes.iter().any(|c| c == state))
        .collect();
    let names: Vec<&str> = library::shapes()
        .iter()
        .map(|shape| shape.name.as_str())
        .collect();
    let naming: Vec<&String> = classes
        .iter()
        .filter(|class| class.split('-').any(|word| names.contains(&word)))
        .collect();
    assert_eq!((missing, naming), (vec![], Vec::<&String>::new()));
}

#[rstest]
#[case::a_shape_that_turns("speaker", 12)]
#[case::a_shape_that_does_not("box", 11)]
fn draws_a_shape_on_its_side_only_when_it_turns(#[case] name: &str, #[case] cells: usize) {
    let page = page();
    let shape = page.groups[0]
        .shapes
        .iter()
        .find(|s| s.name == name)
        .unwrap();
    assert_eq!(shape.cells.len(), cells);
}

#[rstest]
fn marks_the_ports_on_the_first_drawing_alone() {
    let page = page();
    let shape = page.groups[0]
        .shapes
        .iter()
        .find(|s| s.name == "box")
        .unwrap();
    let marked: Vec<usize> = shape
        .cells
        .iter()
        .map(|cell| cell.drawn.drawing.ports.len())
        .collect();
    assert_eq!(marked, vec![2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
}

/// The text of a summary reaches the page escaped.
#[rstest]
fn escapes_the_summary_of_an_authors_shape() {
    let html = page().render().unwrap();
    assert_eq!(
        (
            html.contains("<b>A plate"),
            html.contains("&#60;b&#62;A plate&#60;/b&#62; &#38; a bolt.")
        ),
        (false, true)
    );
}
