// The identity block's placement and colours, over numbers alone.

use media_screen::status::{Component, Status};

use super::*;
use crate::screens::audience::{NOBODY, link};
use crate::views::area;

// The frame the picker draws in.
fn bounds() -> Rectangle {
    area(0.0, 0.0, 1920.0, 1080.0)
}

// One part with a name and nothing else: a wired part that reports no
// presence, no charge, and no focus.
fn part(name: &str) -> Component {
    Component {
        name: name.into(),
        kind: "display".into(),
        ..Component::default()
    }
}

// A controller with a presence, a charge, and a focus flag.
fn controller(name: &str, connected: bool, battery: i64, focused: bool) -> Component {
    Component {
        name: name.into(),
        kind: Component::REMOTE.into(),
        connected: Some(connected),
        battery: Some(battery),
        focused: Some(focused),
    }
}

// The unit the local captures invent: a screen, speakers, and two
// controllers, one of them away.
fn den() -> Unit {
    Unit::of(&Status {
        display_name: "The Den".into(),
        components: vec![
            part("Living Room TV"),
            part("Soundbar"),
            controller("Remote", true, 72, true),
            controller("Gamepad", false, 18, false),
        ],
        ..Status::default()
    })
}

// The block the unit draws in the frame, which every case here expects.
fn drawn(unit: &Unit) -> Block<'_> {
    block(unit, bounds()).expect("a named unit draws a block")
}

#[test]
fn the_last_part_is_on_the_bottom_margin() {
    let unit = den();
    let block = drawn(&unit);

    assert_eq!(block.lines[3].run.at, Point::new(96.0, 990.0));
    assert_eq!(block.lines[3].run.words, "Gamepad");
}

#[test]
fn the_parts_stack_upward_one_step_apart_in_the_order_the_status_lists_them() {
    let unit = den();
    let block = drawn(&unit);
    let tops: Vec<(f32, &str)> = block
        .lines
        .iter()
        .map(|line| (line.run.at.y, line.run.words))
        .collect();

    assert_eq!(
        tops,
        vec![
            (990.0 - 3.0 * ITEM_STEP, "Living Room TV"),
            (990.0 - 2.0 * ITEM_STEP, "Soundbar"),
            (990.0 - ITEM_STEP, "Remote"),
            (990.0, "Gamepad"),
        ]
    );
}

#[test]
fn the_name_heads_the_list_one_wider_step_up_and_one_size_larger() {
    let unit = den();
    let block = drawn(&unit);

    assert_eq!(block.header.words, "The Den");
    assert_eq!(block.header.at.y, block.lines[0].run.at.y - HEADER_STEP);
    const { assert!(HEADER_STEP > ITEM_STEP) };
    assert!(block.header.size > block.lines[0].run.size);
}

#[test]
fn every_line_is_flush_with_the_screen_margin() {
    let unit = den();
    let block = drawn(&unit);
    let lefts: Vec<f32> = std::iter::once(block.header.at.x)
        .chain(block.lines.iter().map(|line| line.run.at.x))
        .collect();

    assert_eq!(lefts, vec![screen::MARGIN_X; 5]);
}

#[test]
fn a_unit_with_no_parts_is_a_name_on_the_bottom_margin() {
    let unit = Unit::of(&Status {
        display_name: "The Den".into(),
        ..Status::default()
    });

    assert_eq!(drawn(&unit).header.at, Point::new(96.0, 990.0));
}

#[test]
fn a_unit_with_no_name_draws_nothing() {
    let unit = Unit::of(&Status {
        components: vec![part("Living Room TV")],
        ..Status::default()
    });

    assert!(block(&unit, bounds()).is_none());
}

#[test]
fn a_part_with_no_name_is_dropped() {
    let unit = Unit::of(&Status {
        display_name: "The Den".into(),
        components: vec![part(""), part("Soundbar")],
        ..Status::default()
    });

    assert_eq!(unit.parts.len(), 1);
    assert_eq!(drawn(&unit).lines[0].run.words, "Soundbar");
}

#[test]
fn a_part_that_is_away_draws_dim_and_every_other_part_draws_full() {
    let cases = [(None, 1.0), (Some(true), 1.0), (Some(false), DIM)];
    for (connected, opacity) in cases {
        let unit = Unit::of(&Status {
            display_name: "The Den".into(),
            components: vec![Component {
                connected,
                ..part("Remote")
            }],
            ..Status::default()
        });

        assert_eq!(drawn(&unit).lines[0].run.color.a, opacity, "{connected:?}");
    }
}

#[test]
fn every_bar_ends_on_one_column_left_of_the_markers_place() {
    let unit = den();
    let block = drawn(&unit);
    let rights: Vec<f32> = block
        .lines
        .iter()
        .filter_map(|line| line.bar.map(|bar| bar.track.x + bar.track.width))
        .collect();

    assert_eq!(rights, vec![BAR_RIGHT, BAR_RIGHT]);
    const { assert!(BAR_RIGHT < screen::MARGIN_X - MARKER_GAP - MARKER_R) };
}

#[test]
fn a_part_that_reports_no_charge_draws_no_bar() {
    let unit = den();
    let block = drawn(&unit);
    let bars: Vec<bool> = block.lines.iter().map(|line| line.bar.is_some()).collect();

    assert_eq!(bars, vec![false, false, true, true]);
}

#[test]
fn a_charge_fills_its_share_of_the_bar_and_no_more() {
    let cases = [
        (0, 0.0),
        (50, BAR_LENGTH / 2.0),
        (100, BAR_LENGTH),
        (150, BAR_LENGTH),
        (-5, 0.0),
    ];
    for (level, width) in cases {
        assert_eq!(filled(level), width, "{level}");
    }
}

#[test]
fn the_fill_starts_where_the_track_starts() {
    let unit = den();
    let block = drawn(&unit);
    let bar = block.lines[2].bar.expect("the remote reports a charge");

    assert_eq!(bar.fill.x, bar.track.x);
    assert_eq!(bar.fill.y, bar.track.y);
    assert_eq!(bar.fill.width, filled(72));
}

#[test]
fn a_bar_takes_the_colour_of_its_line() {
    let unit = den();
    let block = drawn(&unit);
    let colours: Vec<(Color, Color)> = block
        .lines
        .iter()
        .filter_map(|line| line.bar.map(|bar| (bar.color, line.run.color)))
        .collect();

    assert_eq!(colours[0].0, colours[0].1);
    assert_eq!(colours[1].0, colours[1].1);
    assert_eq!(colours[1].0.a, DIM);
}

#[test]
fn the_focused_part_takes_the_marker_in_the_left_margin() {
    let unit = den();
    let block = drawn(&unit);
    let markers: Vec<Option<Point>> = block
        .lines
        .iter()
        .map(|line| line.marker.map(|marker| marker.center))
        .collect();

    assert_eq!(
        markers,
        vec![
            None,
            None,
            Some(Point::new(
                96.0 - MARKER_GAP,
                990.0 - ITEM_STEP - MARKER_RISE
            )),
            None,
        ]
    );
}

#[test]
fn the_marker_reads_one_step_under_its_line() {
    let cases = [(true, MARKER_REST), (false, MARKER_DIM)];
    for (connected, opacity) in cases {
        let unit = Unit::of(&Status {
            display_name: "The Den".into(),
            components: vec![controller("Remote", connected, 50, true)],
            ..Status::default()
        });
        let marker = drawn(&unit).lines[0].marker.expect("a focused part");

        assert_eq!(marker.color.a, opacity, "{connected}");
    }
}

#[test]
fn a_hexagon_has_six_corners_at_the_markers_radius() {
    let center = Point::new(78.0, 900.0);
    let reaches = vertices(center, MARKER_R).map(|point| {
        let reach = (point.x - center.x).hypot(point.y - center.y);
        (reach * 1000.0).round()
    });

    assert_eq!(reaches, [(MARKER_R * 1000.0).round(); 6]);
}

#[test]
fn the_block_is_under_the_pickers_link() {
    let unit = den();
    let block = drawn(&unit);
    let link = link(bounds(), NOBODY);

    let top = block.header.at.y - line_box(block.header.size);

    assert!(top > link.y + link.height, "{top}");
}
