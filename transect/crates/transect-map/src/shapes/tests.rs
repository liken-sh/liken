use std::sync::Arc;

use rstest::rstest;

use super::*;
use transect_resources::resources::DocId;
use transect_resources::shapes::Shapes;

/// An author's shape whose spec is `spec`.
fn authored(spec: &str) -> Shape {
    let text = format!("{{metadata: {{name: plate.example.test}}, spec: {spec}}}");
    transect_resources::shapes::parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap())
        .unwrap()
}

fn library_shape(name: &str) -> Arc<Shape> {
    Shapes::default().resolve(name, "").unwrap().clone()
}

#[rstest]
fn sizes_a_part_from_its_design() {
    let drawing = Drawing::of(&library_shape("box"), Pose::default());
    assert_eq!(
        (drawing.class.as_str(), drawing.style.as_str(), drawing.axis),
        (
            "shape-part shape-fit-wrap shape-align-start",
            "--w-min: 160px; --w-top: 460px; --u: calc(100cqi / 460); --ratio: 460 / 153; --axis-shift: 0",
            true
        )
    );
}

#[rstest]
#[case::a_share_of_the_sketch(
    "screen",
    "shape-part shape-fit-cut shape-align-start shape-display-surface shape-share",
    "--w-min: 240px; --w-top: min(62cqi, 600px); --u: calc(100cqi / 600); --ratio: 600 / 338; --axis-shift: 0"
)]
#[case::as_wide_as_its_text(
    "tag",
    "shape-part shape-fit-wrap shape-align-start shape-follow",
    "--w-min: 80px; --w-top: 320px; --u: 1px"
)]
fn writes_the_classes_and_sizes_that_size_states(
    #[case] name: &str,
    #[case] class: &str,
    #[case] style: &str,
) {
    let drawing = Drawing::of(&library_shape(name), Pose::default());
    assert_eq!(
        (drawing.class.as_str(), drawing.style.as_str()),
        (class, style)
    );
}

#[rstest]
fn keeps_room_above_a_part_for_a_piece_above_its_top() {
    let shape = authored(
        "{summary: A router., size: {design: [300, 90], width: [160, 340]}, pieces: [{name: antenna, at: {left: 30, bottom: 100% - 4, width: 8, height: 60}}]}",
    );
    let drawing = Drawing::of(&shape, Pose::default());
    assert!(
        drawing.style.contains("--room-above: 56"),
        "{}",
        drawing.style
    );
}

/// A speaker's text keeps clear of its drivers, and its source line
/// stands at the bottom inset that it states, under the cone.
#[rstest]
fn moves_the_text_clear_of_a_piece_and_keeps_the_source_line_under_it() {
    let drawing = Drawing::of(&library_shape("speaker"), Pose::default());
    assert_eq!(
        drawing.text,
        "--text-top: 12; --text-right: 14; --text-bottom: 163; --text-left: 14; --source-bottom: 12"
    );
}

/// A box's feet stand under its outline, so they are outside it, and a
/// sketch too narrow to join its parts leaves them out.
#[rstest]
fn draws_the_body_and_each_piece_in_order_with_its_material() {
    let drawing = Drawing::of(&library_shape("box"), Pose::default());
    let classes: Vec<&str> = drawing
        .layers
        .iter()
        .map(|layer| layer.class.as_str())
        .collect();
    assert_eq!(
        classes,
        vec![
            "shape-outline shape-draw-solid",
            "shape-piece shape-draw-line",
            "shape-piece shape-draw-solid shape-outside",
            "shape-piece shape-draw-solid shape-outside",
            "shape-piece shape-draw-mark",
        ]
    );
}

#[rstest]
fn draws_a_stack_as_copies_of_the_body_behind_the_part() {
    let drawing = Drawing::of(
        &library_shape("drum"),
        Pose {
            stack: 2,
            ports: true,
        },
    );
    let copies = drawing
        .layers
        .iter()
        .filter(|layer| layer.class.contains("shape-copy"));
    let ports: Vec<(&str, &str)> = drawing
        .ports
        .iter()
        .map(|p| (p.class.as_str(), p.style.as_str()))
        .collect();
    assert_eq!(
        (copies.count(), ports),
        (
            2,
            vec![
                (
                    "shape-port shape-port-left",
                    "left: 0; top: 45%; height: 30%"
                ),
                (
                    "shape-port shape-port-right",
                    "left: calc(100% + var(--u) * 16); top: calc(45% - var(--u) * 16); height: 30%"
                ),
                (
                    "shape-port shape-port-top",
                    "top: calc(var(--u) * -16); left: calc(30% + var(--u) * 16); width: 40%"
                ),
            ]
        )
    );
}

/// A stack keeps its copies inside the part's area: the room above the
/// part for how far they rise, and the room at its right for how far
/// they move right, so the part's body is narrower by as much.
#[rstest]
#[case::a_part_alone(0, &[])]
#[case::a_stack(2, &["--room-above: 16", "--stack-room: 16"])]
fn keeps_the_copies_of_a_stack_inside_the_part(#[case] stack: u8, #[case] room: &[&str]) {
    let drawing = Drawing::of(
        &library_shape("drum"),
        Pose {
            stack,
            ports: false,
        },
    );
    let kept: Vec<&str> = drawing
        .style
        .split("; ")
        .filter(|property| {
            property.starts_with("--room-above") || property.starts_with("--stack-room")
        })
        .collect();
    assert_eq!(kept, room);
}

#[rstest]
fn turns_the_path_of_a_piece_with_its_shape() {
    let shape = authored(
        "{summary: A bar., size: {design: [100, 50], width: [80, 120]}, turns: true, pieces: [{name: line, at: {left: 0, right: 0, top: 0, height: 2}, view: [100, 2], path: M0 1H100}]}",
    );
    let drawing = Drawing::of(&shape.turned(), Pose::default());
    let path = drawing.layers[1].art.clone().unwrap();
    assert_eq!(
        (
            path.view.as_str(),
            path.data.as_str(),
            path.transform.as_deref()
        ),
        ("0 0 2 100", "M0 1H100", Some("matrix(0 -1 1 0 0 100)"))
    );
}

#[rstest]
fn draws_no_ports_unless_asked() {
    let drawing = Drawing::of(&library_shape("box"), Pose::default());
    assert_eq!(drawing.ports, vec![]);
}

/// An author's shape that uses every field of the form, with markup in
/// its summary and in a piece's name.
fn every_field() -> Shape {
    authored(
        "{summary: '<b>A plate</b>', size: {design: [100, 50], width: [80, 120], share: 50%}, outline: {round: 50% / 22, at: {left: 0, right: 0, top: 4, bottom: 0}},
          text: {inset: [1, 2, 3, 4], align: end, fit: cut}, display: window, turns: true,
          pieces: [{name: '\"><b>', draw: glass, at: {right: 2.5, width: 33, bottom: 100% - 2, height: 6}, view: [33, 6], path: 'M0 0L33 6Z', clear: right}],
          ports: {top: [10%, 20%]}}",
    )
}

/// Every value that a drawing writes into a `style` or a `class` is a
/// number or a fixed word, so it has only the characters of CSS
/// numbers, units, and functions, however an author writes a shape.
#[rstest]
#[case::a_box(library_shape("box").as_ref().clone())]
#[case::a_disc(library_shape("disc").as_ref().clone())]
#[case::a_drum(library_shape("drum").as_ref().clone())]
#[case::a_handheld(library_shape("handheld").as_ref().clone())]
#[case::a_screen(library_shape("screen").as_ref().clone())]
#[case::a_socket(library_shape("socket").as_ref().clone())]
#[case::a_speaker(library_shape("speaker").as_ref().clone())]
#[case::a_tag(library_shape("tag").as_ref().clone())]
#[case::an_authors_shape(every_field())]
#[case::an_authors_shape_on_its_side(every_field().turned())]
fn writes_only_numbers_and_fixed_words_into_the_markup(#[case] shape: Shape) {
    let drawing = Drawing::of(
        &shape,
        Pose {
            stack: 2,
            ports: true,
        },
    );
    let layers = drawing.layers.iter().chain(&drawing.ports);
    let paths = layers.clone().filter_map(|layer| layer.art.as_ref());
    let written: Vec<&str> = [drawing.class.as_str(), &drawing.style, &drawing.text]
        .into_iter()
        .chain(layers.flat_map(|layer| [layer.class.as_str(), &layer.style]))
        .chain(paths.flat_map(|path| [path.view.as_str(), &path.data]))
        .chain(
            drawing
                .layers
                .iter()
                .filter_map(|l| l.art.as_ref()?.transform.as_deref()),
        )
        .collect();
    let allowed = |c: char| c.is_ascii_alphanumeric() || " .:;%()*/+-,_".contains(c);
    let odd: Vec<&str> = written
        .into_iter()
        .filter(|text| !text.chars().all(allowed))
        .collect();
    assert_eq!(odd, Vec::<&str>::new());
}

/// A stack draws one fewer copy of its outline than it has parts, at
/// most `STACK_COPIES`, so a stack of two is two outlines.
#[rstest]
#[case::one_part(1, 0)]
#[case::two_parts(2, 1)]
#[case::three_parts(3, 2)]
#[case::five_parts(5, 2)]
fn draws_one_fewer_copy_than_a_stack_has_parts(#[case] parts: usize, #[case] copies: usize) {
    let shape = library_shape("drum");
    let drawn = text::Drawn {
        drawing: Drawing::of(&shape, Pose::default()),
        stacked: stacked(&shape, Pose::default()),
        label: "replica".into(),
        source: Vec::new(),
        parts: Vec::new(),
        nothing: None,
    };
    let name = |at: usize| transect_graph::links::Reference {
        text: format!("Pod a-{at}"),
        href: None,
    };
    let part = text::Part {
        end: text::End {
            reference: name(0),
            under: None,
            reaches: Vec::new(),
        },
        readings: Vec::new(),
        shows: Vec::new(),
        state: text::State::Plain,
        literal: false,
        why: None,
        stack: match parts {
            1 => Vec::new(),
            parts => (0..parts).map(name).collect(),
        },
    };
    let drawn_copies = drawn
        .drawing_of(&part)
        .layers
        .iter()
        .filter(|layer| layer.class.contains("shape-copy"))
        .count();
    assert_eq!(drawn_copies, copies);
}

#[rstest]
#[case::one_value("Denon AVR-X1700H", &["Denon AVR-X1700H"])]
#[case::a_list("data-a 1Gi, data-b 1Gi", &["data-a 1Gi", "data-b 1Gi"])]
#[case::a_comma_with_no_space("FL,FR", &["FL,FR"])]
fn splits_a_reading_into_the_items_it_lists(#[case] value: &str, #[case] items: &[&str]) {
    let reading = text::Reading {
        name: "claim".into(),
        value: value.into(),
        from: None,
    };
    assert_eq!(reading.items(), items);
}
