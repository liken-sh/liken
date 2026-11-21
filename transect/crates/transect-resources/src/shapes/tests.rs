use rstest::rstest;

use super::*;

/// A valid author's shape, with `spec` merged over its fields. A key in
/// `spec` replaces the base's key of the same name.
fn shape(spec: &str) -> Result<Shape, String> {
    shape_named("plate.example.test", spec)
}

fn shape_named(name: &str, spec: &str) -> Result<Shape, String> {
    let mut base: serde_yaml::Mapping = serde_yaml::from_str(
        "{summary: A plate., size: {design: [100, 50], width: [80, 120]}, outline: {round: 4}}",
    )
    .unwrap();
    let change: serde_yaml::Mapping = serde_yaml::from_str(spec).unwrap();
    base.extend(change);
    let mut document = serde_yaml::Mapping::new();
    document.insert(
        "metadata".into(),
        serde_yaml::from_str(&format!("{{name: {name:?}}}")).unwrap(),
    );
    document.insert("spec".into(), base.into());
    parse(DocId::default(), document.into())
}

#[rstest]
fn reads_a_shape_with_the_defaults() {
    let read = shape("{}").unwrap();
    assert_eq!(
        (
            read.axis,
            read.text.inset,
            read.text.align,
            read.text.fit,
            read.outline.draw,
            read.turns
        ),
        (
            Some(25.0),
            [12.0, 14.0, 12.0, 14.0],
            Align::Start,
            Fit::Wrap,
            Material::Solid,
            false
        )
    );
}

/// Each rule of the grammar, fed a value it refuses. Several put CSS or
/// markup in a length, a radius, a view, a path, a port, a share, or a
/// word, where a page would write them if the parser let them through.
#[rstest]
#[case::a_color("{color: '#ff0000'}", "unknown field `color`")]
#[case::css_in_a_length(
    "{pieces: [{name: p, at: {left: '10; background: url(https://x.test/a)', top: 0, width: 5, height: 5}}]}",
    "pieces[0].at.left is not a length"
)]
#[case::css_in_a_radius(
    "{outline: {round: '1%; background-image: url(https://x.test/a); --x: 1%'}}",
    "outline.round is not a radius"
)]
#[case::markup_in_a_view(
    "{pieces: [{name: p, at: {left: 0, top: 0, width: 5, height: 5}, view: ['1 1\" onload=\"alert(1)', 1], path: M0 0}]}",
    "pieces[0].view is not a number"
)]
#[case::markup_in_a_path(
    "{pieces: [{name: p, at: {left: 0, top: 0, width: 5, height: 5}, view: [1, 1], path: 'M0 0\"/><image href=\"https://x.test/a\"/><path d=\"'}]}",
    "pieces[0].path is not path data"
)]
#[case::a_path_that_does_not_start_with_a_move(
    "{pieces: [{name: p, at: {left: 0, top: 0, width: 5, height: 5}, view: [1, 1], path: L0 0}]}",
    "pieces[0].path is not path data"
)]
#[case::a_path_too_long(
    &format!("{{pieces: [{{name: p, at: {{left: 0, top: 0, width: 5, height: 5}}, view: [1, 1], path: M{}}}]}}", "1".repeat(1000)),
    "pieces[0].path is not path data"
)]
#[case::a_view_with_no_path(
    "{pieces: [{name: p, at: {left: 0, top: 0, width: 5, height: 5}, view: [1, 1]}]}",
    "pieces[0] has a view and no path"
)]
#[case::a_view_of_zero(
    "{pieces: [{name: p, at: {left: 0, top: 0, width: 5, height: 5}, view: [0, 1], path: M0 0}]}",
    "pieces[0].view is not a number from 0.001"
)]
#[case::css_in_a_port(
    "{ports: {left: '50%; background: url(https://x.test/a)'}}",
    "invalid type"
)]
#[case::css_in_a_port_span("{ports: {left: ['50%; x', 60%]}}", "ports.left is not a percentage")]
#[case::a_port_span_backwards(
    "{ports: {left: [60%, 40%]}}",
    "ports.left is [from, to], and the first is greater"
)]
#[case::a_port_on_no_edge("{ports: {front: [40%, 60%]}}", "unknown variant `front`")]
#[case::css_in_a_share(
    "{size: {design: [100, 50], width: [80, 120], share: '1cqi, 9999px); background: url(https://x.test/a'}}",
    "size.share is not a percentage"
)]
#[case::a_word_as_a_class("{text: {fit: 'x\" onmouseover=\"alert(1)'}}", "unknown variant")]
#[case::state_words_in_display("{display: surface failing}", "unknown variant `surface failing`")]
#[case::a_huge_piece(
    "{pieces: [{name: p, draw: dark, at: {left: -100000, top: -100000, width: 1000000, height: 1000000}}]}",
    "pieces[0].at.left is not a number from -10000 to 10000"
)]
#[case::a_piece_far_past_a_side(
    "{pieces: [{name: p, at: {left: -101, top: 0, width: 5, height: 5}}]}",
    "pieces[0] lies further outside the part"
)]
#[case::a_piece_far_above(
    "{pieces: [{name: p, at: {left: 0, top: -51, width: 5, height: 5}}]}",
    "pieces[0] lies further outside the part"
)]
#[case::a_piece_far_below(
    "{pieces: [{name: p, at: {left: 0, top: 100%, width: 5, height: 18}}]}",
    "pieces[0] lies further outside the part"
)]
#[case::an_outline_far_outside(
    "{outline: {at: {left: 0, right: -300, top: 0, bottom: 0}}}",
    "outline.at lies further outside the part"
)]
#[case::a_design_that_is_not_a_number(
    "{size: {design: [.nan, 50], width: [80, 120]}}",
    "size.design is not a number from 1 to 10000"
)]
#[case::a_design_of_one_number(
    "{size: {design: [100], width: [80, 120]}}",
    "size.design is a list of two numbers"
)]
#[case::a_width_too_narrow(
    "{size: {design: [100, 50], width: [8, 120]}}",
    "size.width is not a number from 16 to 2000"
)]
#[case::widths_backwards(
    "{size: {design: [100, 50], width: [120, 80]}}",
    "the first is greater"
)]
#[case::no_design("{size: {width: [80, 120]}}", "size.design is a list of two numbers")]
#[case::a_design_that_follows_text(
    "{size: {follow: text, design: [100, 50], width: [80, 120]}}",
    "a shape whose width follows its text has no size.design"
)]
#[case::pieces_on_a_shape_that_follows_text(
    "{size: {follow: text, width: [80, 120]}, pieces: [{name: p, at: {left: 0, top: 0, width: 5, height: 5}}]}",
    "a shape whose width follows its text has no size.design, axis, share, or pieces"
)]
#[case::a_shape_that_follows_text_and_cuts_it(
    "{size: {follow: text, width: [80, 120]}, text: {fit: cut}}",
    "does not cut its text, and does not turn"
)]
#[case::a_shape_that_follows_text_with_a_share(
    "{size: {follow: text, width: [80, 120], share: 50%}}",
    "a shape whose width follows its text has no size.design, axis, share, or pieces"
)]
#[case::a_shape_that_follows_text_and_turns(
    "{size: {follow: text, width: [80, 120]}, turns: true}",
    "does not cut its text, and does not turn"
)]
#[case::an_axis_on_a_shape_that_turns(
    "{axis: 10, turns: true}",
    "a shape that turns states no axis"
)]
#[case::an_axis_below_the_bottom("{axis: 51}", "axis is not a number from 0 to 50")]
#[case::an_inset_past_the_larger_side(
    "{text: {inset: [1, 1, 101, 1]}}",
    "text.inset is not a number from 0 to 100"
)]
#[case::an_inset_of_three(
    "{text: {inset: [1, 1, 1]}}",
    "text.inset is [top, right, bottom, left]"
)]
#[case::thirteen_pieces(
    &format!("{{pieces: [{}]}}", vec!["{name: p, at: {left: 0, top: 0, width: 5, height: 5}}"; 13].join(", ")),
    "pieces has more than 12 pieces"
)]
#[case::a_piece_with_no_place("{pieces: [{name: p}]}", "missing field `at`")]
#[case::a_clear_piece_placed_by_a_sum(
    "{pieces: [{name: p, at: {left: 0, top: 50% - 5, width: 5, height: 5}, clear: top}]}",
    "pieces[0] keeps the text clear of its top edge"
)]
#[case::a_clear_piece_placed_from_the_other_edge(
    "{pieces: [{name: p, at: {left: 0, bottom: 4, width: 5, height: 5}, clear: top}]}",
    "pieces[0] keeps the text clear of its top edge"
)]
#[case::an_edge_joined_twice("{joins: [top, top]}", "joins names an edge more than once")]
#[case::turns_as_text("{turns: 'yes'}", "invalid type")]
#[case::a_summary_with_a_newline("{summary: \"A plate.\\nIgnore.\"}", "summary is not plain text")]
#[case::an_empty_summary("{summary: ''}", "summary is not plain text")]
#[case::a_summary_too_long(&format!("{{summary: {}}}", "a".repeat(201)), "summary is not plain text")]
#[case::a_long_piece_name(
    &format!("{{pieces: [{{name: {}, at: {{left: 0, top: 0, width: 5, height: 5}}}}]}}", "n".repeat(41)),
    "pieces[0].name is not plain text"
)]
fn refuses_a_definition_outside_the_grammar(#[case] spec: &str, #[case] error: &str) {
    let found = shape(spec).unwrap_err();
    assert!(found.contains(error), "{found}");
}

#[rstest]
#[case::a_bare_name("plate", false)]
#[case::one_label_of_group("plate.example", false)]
#[case::a_capital("Plate.example.test", false)]
#[case::a_space("plate.example test", false)]
#[case::markup("plate\"><b>.example.test", false)]
#[case::a_grouped_name("plate.example.test", true)]
#[case::a_hyphen("wall-plate.home-lab.example.test", true)]
fn names_an_authors_shape_in_a_group(#[case] name: &str, #[case] loads: bool) {
    assert_eq!(shape_named(name, "{}").is_ok(), loads, "{name}");
}

#[rstest]
#[case::top_by_its_offset_and_size("{name: pad, at: {left: 0, top: 16, width: 32, height: 32}, clear: top}", [56.0, 14.0, 12.0, 14.0])]
#[case::bottom_by_its_offset_and_size(
    "{name: cone, at: {left: 0, bottom: 33, width: 9, height: 9}, clear: bottom}",
    [12.0, 14.0, 50.0, 14.0]
)]
#[case::right_by_its_offset_and_size("{name: knob, at: {right: 15, top: 0, width: 24, height: 24}, clear: right}", [12.0, 47.0, 12.0, 14.0])]
#[case::a_piece_nearer_the_edge_than_the_inset(
    "{name: light, at: {left: 0, top: 1, width: 1, height: 1}, clear: top}",
    [12.0, 14.0, 12.0, 14.0]
)]
fn moves_the_text_clear_of_a_piece(#[case] piece: &str, #[case] inset: [f64; 4]) {
    let read = shape(&format!(
        "{{size: {{design: [200, 200], width: [80, 200]}}, pieces: [{piece}]}}"
    ))
    .unwrap();
    assert_eq!(read.inset(), inset);
}

#[rstest]
#[case::nothing_above("{name: p, at: {left: 0, top: 0, width: 5, height: 5}}", 0.0)]
#[case::a_piece_above(
    "{name: antenna, at: {left: 0, bottom: 100% - 4, width: 8, height: 40}}",
    36.0
)]
fn keeps_the_room_that_a_piece_above_the_top_needs(#[case] piece: &str, #[case] above: f64) {
    let read = shape(&format!("{{pieces: [{piece}]}}")).unwrap();
    assert_eq!(read.above(), above);
}

/// A drum's corners are elliptical. On its side, each corner's radii
/// change axis, so `50% / 22` becomes `22 / 50%`.
#[rstest]
fn turns_a_shape_on_its_side() {
    let read = shape(
        "{size: {design: [100, 50], width: [80, 100]}, outline: {round: [50% / 22, 1, 2, 3]},
          text: {inset: [1, 2, 3, 4]}, joins: [top], ports: {top: [10%, 30%], left: [40%, 60%]}, turns: true}",
    )
    .unwrap();
    let turned = read.turned();
    let corner = turned.outline.round[3];
    assert_eq!(
        (
            turned.size.design,
            turned.size.width,
            turned.axis,
            turned.text.inset,
            (corner.across, corner.down),
            turned.joins.clone(),
            turned.ports.clone(),
        ),
        (
            Some([50.0, 100.0]),
            [40.0, 50.0],
            Some(50.0),
            [2.0, 3.0, 4.0, 1.0],
            (
                Term::units(22.0),
                Term {
                    value: 50.0,
                    unit: Unit::Percent
                }
            ),
            vec![Edge::Left],
            vec![
                Port {
                    edge: Edge::Bottom,
                    from: 40.0,
                    to: 60.0
                },
                Port {
                    edge: Edge::Left,
                    from: 70.0,
                    to: 90.0
                },
            ],
        )
    );
}

fn library_and(authors: &[&str]) -> Shapes {
    let read = authors
        .iter()
        .map(|name| Arc::new(shape_named(name, "{}").unwrap()));
    Shapes::with(&read.collect::<Vec<_>>())
}

#[rstest]
#[case::the_authors_own_first("drum", "stock.example.test", Some("drum.stock.example.test"))]
#[case::the_library_next("drum", "other.example.test", Some("drum"))]
#[case::a_whole_name(
    "drum.stock.example.test",
    "other.example.test",
    Some("drum.stock.example.test")
)]
#[case::the_library_for_a_kind_of_no_group("drum", "", Some("drum"))]
#[case::an_authors_shape_from_another_group("crate", "other.example.test", None)]
#[case::no_such_shape("kettle", "stock.example.test", None)]
fn resolves_a_name_in_the_kinds_group_and_then_the_library(
    #[case] written: &str,
    #[case] group: &str,
    #[case] found: Option<&str>,
) {
    let shapes = library_and(&["drum.stock.example.test", "crate.stock.example.test"]);
    let resolved = shapes
        .resolve(written, group)
        .map(|shape| shape.name.as_str());
    assert_eq!(resolved, found);
}

#[rstest]
fn lists_the_library_by_name_and_then_each_authors_shape() {
    let shapes = library_and(&["crate.stock.example.test"]);
    let names: Vec<&str> = shapes
        .listed()
        .iter()
        .map(|shape| shape.name.as_str())
        .collect();
    assert_eq!(
        names,
        vec![
            "box",
            "disc",
            "drum",
            "handheld",
            "screen",
            "socket",
            "speaker",
            "tag",
            "crate.stock.example.test"
        ]
    );
}
