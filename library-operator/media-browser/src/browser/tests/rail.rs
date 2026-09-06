// The rail on a wall, through the presses that reach it: the length
// that draws one, the move onto a bar, the jump a select on a bar makes,
// and the order the button cycles.

use super::*;
use crate::catalog::Sort;
use crate::sample::Catalog;
use crate::screens::wall::Focus as WallFocus;

// The invented library, which holds thousands of titles.
const FEATURES: &str = "sample/features";

// The fake catalog with this many movies in its library, which is how a
// case names the length of its wall.
fn films(movies: usize) -> Fake {
    Fake {
        movies,
        ..Fake::default()
    }
}

fn library(library: &str, sort: Sort) -> Query {
    Query::Library {
        library: library.into(),
        sort,
    }
}

// The sample library newest first, which is long enough for a rail of
// several bars.
fn dated() -> Wall {
    Wall::open(library(FEATURES, Sort::Newest), &mut Catalog)
}

#[test]
fn a_wall_of_four_screens_draws_no_rail_and_a_longer_one_draws_one() {
    let short = Wall::open(library("screening/films", Sort::Title), &mut films(48));
    assert!(short.bars.is_empty());

    let long = Wall::open(library("screening/films", Sort::Title), &mut films(49));
    assert!(!long.bars.is_empty());
}

#[test]
fn a_right_press_at_the_edge_of_a_row_enters_the_rail_at_the_bar_over_it() {
    let mut wall = dated();
    let row = wall.bars[3].bar.first;
    wall.slots.focus = row * wall::COLUMNS + wall::COLUMNS - 1;

    wall.key("right", &mut Catalog);

    let WallFocus::Rail(cell) = wall.focus else {
        panic!("a right press at the edge of a row enters the rail");
    };
    let bar = &wall.bars[cell - 1].bar;
    assert!(bar.first <= row && row <= bar.last, "{bar:?} covers {row}");
}

#[test]
fn a_right_press_inside_a_row_moves_across_the_slots() {
    let mut wall = dated();
    wall.slots.focus = 2;

    wall.key("right", &mut Catalog);

    assert_eq!(wall.focus, WallFocus::Slots);
    assert_eq!(wall.slots.focus, 3);
}

#[test]
fn a_right_press_on_the_last_slot_of_a_short_row_enters_the_rail() {
    let mut source = films(49);
    let mut wall = Wall::open(library("screening/films", Sort::Title), &mut source);
    wall.slots.focus = 48;

    wall.key("right", &mut source);

    assert_eq!(wall.focus, WallFocus::Rail(1));
}

#[test]
fn a_select_on_a_bar_lands_on_the_first_item_it_covers() {
    let mut wall = dated();
    wall.focus = WallFocus::Rail(3);

    wall.key("enter", &mut Catalog);

    assert_eq!(wall.focus, WallFocus::Slots);
    assert_eq!(wall.slots.focus, wall.bars[2].item);
}

#[test]
fn a_left_press_on_a_bar_returns_to_the_first_item_it_covers() {
    let mut wall = dated();
    wall.focus = WallFocus::Rail(2);

    wall.key("left", &mut Catalog);

    assert_eq!(wall.focus, WallFocus::Slots);
    assert_eq!(wall.slots.focus, wall.bars[1].item);
}

#[test]
fn a_select_on_the_button_cycles_the_order_and_the_bars() {
    let mut wall = Wall::open(library(FEATURES, Sort::Title), &mut Catalog);
    let first = wall.slots.items[0].name.clone();
    assert_eq!(wall.slots.query.sort_word(), Some("Title"));
    assert_eq!(wall.bars.len(), 1);
    wall.focus = WallFocus::Rail(0);

    wall.key("enter", &mut Catalog);

    assert_eq!(wall.focus, WallFocus::Rail(0));
    assert_eq!(wall.slots.query.sort_word(), Some("Newest"));
    assert_eq!(wall.slots.focus, 0);
    assert_ne!(wall.slots.items[0].name, first);
    assert!(wall.bars.len() > 1, "{:?}", wall.bars);
    assert_eq!(wall.heading, wall.slots.heading());
}

#[test]
fn an_up_press_on_the_button_moves_nothing() {
    let mut wall = dated();
    wall.focus = WallFocus::Rail(0);

    assert!(matches!(wall.key("up", &mut Catalog), Step::Still));
    assert_eq!(wall.focus, WallFocus::Rail(0));
}

#[test]
fn up_and_down_walk_the_button_and_the_bars() {
    let mut wall = dated();
    wall.focus = WallFocus::Rail(0);

    wall.key("down", &mut Catalog);
    assert_eq!(wall.focus, WallFocus::Rail(1));

    wall.key("up", &mut Catalog);
    assert_eq!(wall.focus, WallFocus::Rail(0));
}

#[test]
fn a_genre_wall_in_its_leading_order_draws_two_bars() {
    let mut source = films(60);
    let query = Query::Genre {
        name: "Western".into(),
        order: Order::Released,
        sort: GenreSort::Leads,
    };
    let mut wall = Wall::open(query, &mut source);
    assert_eq!(
        wall.bars
            .iter()
            .map(|jump| jump.bar.label.as_str())
            .collect::<Vec<&str>>(),
        ["primarily Western", "other Western"]
    );

    wall.focus = WallFocus::Rail(2);
    wall.key("enter", &mut source);

    assert_eq!(wall.focus, WallFocus::Slots);
    assert_eq!(wall.slots.items[wall.slots.focus].name, "The Serial");
}

#[test]
fn a_wall_under_the_strip_draws_no_mark_of_its_own() {
    let mut browser = browser(60);
    browser.key("enter");
    assert!(showing_wall(&browser).marks(true));

    for _ in 0..6 {
        browser.key("right");
    }
    let wall = showing_wall(&browser);
    assert!(!wall.marks(true));
    assert_eq!(wall.marked_cell(true), Some(1));

    browser.key("up");
    browser.key("up");

    assert!(browser.on_strip);
    let held = !browser.on_strip;
    let wall = showing_wall(&browser);
    assert!(!wall.marks(held));
    assert_eq!(wall.marked_cell(held), None);
    let _ = browser.view();
}
