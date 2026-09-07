// The picker's layout and the presses it folds, over numbers alone.

use super::*;
use crate::views::REACH;

// The frame the picker draws in.
fn bounds() -> Rectangle {
    area(0.0, 0.0, 1920.0, 1080.0)
}

#[test]
fn a_picker_offers_one_tile_per_person() {
    let picker = Picker::open(3, &[]);

    assert_eq!(picker.tiles(), 3);
    assert_eq!(picker.focus(), Focus::Tile(0));
    assert!(!picker.holds(0));
}

#[test]
fn a_picker_opens_with_the_people_it_was_given_chosen() {
    let picker = Picker::open(3, &[0, 2]);

    assert!(picker.holds(0));
    assert!(!picker.holds(1));
    assert!(picker.holds(2));
    assert_eq!(picker.focus(), Focus::Tile(0));
}

#[test]
fn the_row_of_tiles_centres_across_the_frame() {
    let first = tile(bounds(), 4, 0);
    let last = tile(bounds(), 4, 3);

    assert_eq!(first.width, CIRCLE);
    assert_eq!(first.height, CIRCLE);
    assert_eq!(last.x + last.width - first.x, 4.0 * CIRCLE + 3.0 * GAP);
    assert_eq!(
        first.x - bounds().x,
        bounds().x + bounds().width - (last.x + last.width)
    );
}

#[test]
fn the_tiles_sit_beside_each_other_by_the_pitch() {
    assert_eq!(tile(bounds(), 4, 1).x, tile(bounds(), 4, 0).x + pitch());
    assert_eq!(tile(bounds(), 4, 1).y, tile(bounds(), 4, 0).y);
}

#[test]
fn a_tile_has_room_beside_it_for_the_mark() {
    const { assert!(GAP / 2.0 > REACH) };
}

#[test]
fn the_heading_stands_over_the_tiles_and_the_name_under_them() {
    let heading = heading(bounds());
    let tile = tile(bounds(), 4, 0);
    let name = name(tile);

    assert_eq!(heading.width, bounds().width);
    assert!(heading.y + heading.height < tile.y);
    assert_eq!(tile.y, heading.y + heading.height + HEADING_GAP);
    assert_eq!(name.y, tile.y + tile.height + NAME_GAP);
    assert_eq!(name.width, pitch());
    assert_eq!(name.center_x(), tile.center_x());
}

#[test]
fn the_block_centres_in_the_frame() {
    let heading = heading(bounds());
    let link = link(bounds(), NOBODY);
    let over = heading.y - bounds().y;
    let under = bounds().y + bounds().height - (link.y + link.height);

    assert!((over - under).abs() < 1e-3, "{over} against {under}");
}

#[test]
fn the_link_stands_under_the_names_and_centres_across_the_frame() {
    let name = name(tile(bounds(), 4, 0));
    let link = link(bounds(), START);

    assert_eq!(link.y, name.y + name.height + LINK_GAP);
    assert_eq!(link.center_x(), bounds().center_x());
    assert!(link.width > text::measured(START, look::CAPTION));
    assert_eq!(word_at(link).center_x(), link.center_x());
    assert!(word_at(link).y > link.y);
    assert!(word_at(link).y + word_at(link).height < link.y + link.height);
}

#[test]
fn a_longer_word_takes_a_wider_link() {
    assert!(link(bounds(), NOBODY).width > link(bounds(), START).width);
}

#[test]
fn focus_moves_across_the_tiles_and_stops_at_both_ends() {
    let mut picker = Picker::open(3, &[]);

    assert_eq!(picker.key("left"), None);
    assert_eq!(picker.focus(), Focus::Tile(0));
    picker.key("right");
    picker.key("right");
    assert_eq!(picker.focus(), Focus::Tile(2));
    picker.key("right");
    assert_eq!(picker.focus(), Focus::Tile(2));
    picker.key("left");
    assert_eq!(picker.focus(), Focus::Tile(1));
}

#[test]
fn down_reaches_the_link_and_up_returns_to_the_tile_focus_left() {
    let mut picker = Picker::open(3, &[]);
    picker.key("right");

    assert_eq!(picker.key("down"), None);
    assert_eq!(picker.focus(), Focus::Link);
    assert_eq!(picker.key("down"), None);
    assert_eq!(picker.focus(), Focus::Link);

    assert_eq!(picker.key("up"), None);
    assert_eq!(picker.focus(), Focus::Tile(1));
}

#[test]
fn the_link_reads_nobody_until_a_person_is_chosen_and_start_after() {
    let mut picker = Picker::open(2, &[]);

    assert_eq!(picker.word(), NOBODY);
    picker.key("enter");
    assert_eq!(picker.word(), START);
    picker.key("enter");
    assert_eq!(picker.word(), NOBODY);
}

#[test]
fn enter_on_the_link_with_nobody_chosen_answers_the_empty_set() {
    let mut picker = Picker::open(2, &[]);
    picker.key("down");

    assert_eq!(picker.key("enter"), Some(Vec::new()));
}

#[test]
fn enter_on_the_link_answers_the_people_that_are_chosen() {
    let mut picker = Picker::open(3, &[]);
    picker.key("enter");
    picker.key("right");
    picker.key("right");
    picker.key("enter");
    picker.key("down");

    assert_eq!(picker.key("enter"), Some(vec![0, 2]));
}

#[test]
fn enter_on_a_person_toggles_them_and_answers_nothing() {
    let mut picker = Picker::open(2, &[]);

    assert_eq!(picker.key("enter"), None);
    assert!(picker.holds(0));
    assert_eq!(picker.key("enter"), None);
    assert!(!picker.holds(0));
}

#[test]
fn back_answers_the_people_that_are_chosen() {
    let mut picker = Picker::open(3, &[]);
    picker.key("right");
    picker.key("enter");
    picker.key("right");
    picker.key("enter");

    assert_eq!(picker.key("escape"), Some(vec![1, 2]));
}

#[test]
fn back_with_nobody_chosen_answers_the_empty_set() {
    assert_eq!(Picker::open(3, &[]).key("backspace"), Some(Vec::new()));
}

#[test]
fn a_press_the_picker_binds_nothing_for_changes_nothing() {
    let mut picker = Picker::open(2, &[]);

    assert_eq!(picker.key("up"), None);
    assert_eq!(picker.key("q"), None);
    assert_eq!(picker.focus(), Focus::Tile(0));
    assert!(!picker.holds(0));
}

#[test]
fn a_tile_carries_the_display_name_of_its_person() {
    let people = vec![Person {
        name: "first".into(),
        display_name: "First".into(),
    }];
    let picker = Picker::open(1, &[]);
    let layer = Layer {
        people: &people,
        picker: &picker,
    };

    assert_eq!(layer.caption(0), "First");
}
