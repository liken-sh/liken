// The column of circles: the room it takes in the lane, and where one
// row's circles draw in it.

use super::*;
use crate::screens::franchise::metro;
use crate::views::area;

const REGION: Rectangle = Rectangle {
    x: 100.0,
    y: 50.0,
    width: 1500.0,
    height: 900.0,
};

#[test]
fn the_column_takes_the_widest_stack_and_a_gap_and_nothing_where_no_marker_stands() {
    let cases = [
        (None, Vec::new(), Vec::new(), 0.0),
        (
            Some(1),
            vec!["C".to_string()],
            Vec::new(),
            circles_width(1) + GAP,
        ),
        (
            Some(1),
            vec!["C".to_string(), "K".to_string()],
            Vec::new(),
            circles_width(2) + GAP,
        ),
        (
            None,
            Vec::new(),
            vec![("C".to_string(), 2)],
            circles_width(1) + GAP,
        ),
    ];
    for (room, letters, solo, expected) in cases {
        let marks = Marks {
            bars: Vec::new(),
            room,
            letters,
            solo,
        };
        assert_eq!(width(&marks), expected, "{marks:?}");
    }
}

#[test]
fn the_lane_gives_the_column_its_room_between_the_strip_and_the_cards() {
    let runs: Vec<metro::Run> = Vec::new();
    let bare = wall::Lane::of(REGION, &runs, 120.0, 0.0);
    let marked = wall::Lane::of(REGION, &runs, 120.0, CIRCLE + GAP);

    assert_eq!(bare.people.width, 0.0);
    assert_eq!(marked.people.width, CIRCLE + GAP);
    assert_eq!(marked.people.x, marked.columned.x);
    assert_eq!(marked.cards.x, marked.people.x + marked.people.width);
    assert_eq!(marked.cards.width, bare.cards.width - (CIRCLE + GAP));
    assert_eq!(marked.cards.x + marked.cards.width, REGION.x + REGION.width);
}

#[test]
fn the_circles_of_one_row_stand_at_the_left_of_the_column_on_the_middle_of_the_card() {
    let column = area(300.0, 50.0, CIRCLE + GAP, 900.0);
    let cell = area(342.0, 200.0, 1000.0, 400.0);
    let stack = row_box(column, cell, 3);

    assert_eq!(stack.x, column.x);
    assert_eq!(stack.center_y(), cell.center_y());
    assert_eq!(stack.height, CIRCLE);
    assert_eq!(circle_in(stack, 0).x, column.x);
    assert_eq!(row_box(column, cell, 1).width, CIRCLE);
    assert_eq!(row_box(column, cell, 0).width, 0.0);
}

// A wall of three rows a hundred apart, with no era over any of them.
const TOPS: [f32; 4] = [0.0, 100.0, 200.0, 300.0];

fn column() -> Rectangle {
    area(300.0, 50.0, CIRCLE + GAP, 300.0)
}

fn marks() -> Marks {
    Marks {
        bars: Vec::new(),
        room: Some(1),
        letters: vec!["A".into(), "B".into()],
        solo: vec![("C".into(), 0)],
    }
}

// The middle line of one row's card, which its circles stand on.
fn middle(row: usize) -> f32 {
    wall::cell_box(column(), row, &[], &TOPS, 0.0).center_y()
}

#[test]
fn the_rooms_stack_stands_on_its_own_row_and_a_solo_circle_on_theirs() {
    let drawn = drawn(column(), &marks(), &[], &TOPS, 0.0);
    let letters: Vec<String> = drawn.iter().map(|(letter, _)| letter.clone()).collect();

    assert_eq!(letters, ["A", "B", "C"]);
    assert_eq!(drawn[0].1.center_y(), middle(1));
    assert_eq!(drawn[1].1.center_y(), middle(1));
    assert_eq!(drawn[2].1.center_y(), middle(0));
    assert_eq!(drawn[0].1.x, column().x);
    assert_eq!(drawn[2].1.x, column().x);
    assert!(drawn[1].1.x > drawn[0].1.x);
    assert!(drawn.iter().all(|(_, at)| at.width == CIRCLE));
}

#[test]
fn a_wall_no_thread_stands_on_draws_no_circle() {
    let bare = Marks::default();

    assert_eq!(drawn(column(), &bare, &[], &TOPS, 0.0), []);
    assert!(!bare.marked());
}

#[test]
fn the_wall_scrolled_down_moves_the_circles_up_with_the_rows() {
    let down = 40.0;
    let scrolled = drawn(column(), &marks(), &[], &TOPS, down);

    assert_eq!(scrolled[0].1.center_y(), middle(1) - down);
    assert_eq!(scrolled[2].1.center_y(), middle(0) - down);
}
