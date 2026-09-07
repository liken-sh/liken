// The search wall through the browser, over the sample catalog, which
// searches for real: the three ways in, the typing that rereads the wall
// on the same screen, and the grid a remote with no keyboard picks from.

use super::*;
use crate::sample::Catalog;

// The browser over the invented catalog, which answers a search from an
// index of its own, so a press here reads real hits.
fn sampled() -> Browser<Catalog, NoArt> {
    Browser::new(Catalog, NoArt::default())
}

// The search wall the browser is showing.
fn searching(browser: &Browser<Catalog, NoArt>) -> &Wall {
    match browser.top() {
        screens::Screen::Wall(wall) => wall,
        _ => panic!("the browser is not showing a wall"),
    }
}

// What the person has typed into the wall's field.
fn typed(browser: &Browser<Catalog, NoArt>) -> &str {
    searching(browser)
        .search
        .as_ref()
        .expect("the wall types")
        .field
        .text()
}

// Whether the grid is shown on the wall the browser is showing.
fn grid(browser: &Browser<Catalog, NoArt>) -> bool {
    searching(browser)
        .search
        .as_ref()
        .expect("the wall types")
        .keyboard
        .is_some()
}

// The browser with focus on the strip, which an up press past the top
// row of the home page reaches.
fn on_the_strip(browser: &mut Browser<Catalog, NoArt>) {
    for _ in 0..=blocks(browser) {
        browser.key("up");
    }
    assert!(browser.on_strip);
}

// How many rows the home page holds.
fn blocks(browser: &Browser<Catalog, NoArt>) -> usize {
    match browser.top() {
        screens::Screen::Home(home) => home.blocks.len(),
        _ => panic!("the browser is not showing the home page"),
    }
}

#[test]
fn a_letter_on_the_home_page_opens_the_search_wall_seeded_with_it() {
    let mut browser = sampled();

    browser.key("s");

    assert_eq!(browser.stack.len(), 1);
    assert_eq!(typed(&browser), "s");
    assert!(!grid(&browser));
    assert_eq!(
        searching(&browser).slots.query,
        Query::Search { text: "s".into() }
    );
}

#[test]
fn a_second_letter_types_on_the_wall_and_pushes_no_second_one() {
    let mut browser = sampled();
    browser.key("s");

    browser.key("e");

    assert_eq!(browser.stack.len(), 1);
    assert_eq!(typed(&browser), "se");
}

#[test]
fn the_results_and_the_heading_follow_the_typing() {
    let mut browser = sampled();
    browser.key("s");
    let wide = searching(&browser).slots.items.len();

    for word in ["e", "r", "i", "a", "l"] {
        browser.key(word);
    }

    let narrow = searching(&browser).slots.items.len();
    assert!(narrow > 0 && narrow < wide, "{wide} then {narrow}");
    assert_eq!(searching(&browser).heading, format!("serial · {narrow}"));
    assert_eq!(searching(&browser).slots.items[0].kind, "series");
}

#[test]
fn escape_clears_the_text_and_the_next_one_leaves_the_wall() {
    let mut browser = sampled();
    browser.key("s");
    browser.key("e");

    browser.key("escape");

    assert_eq!(typed(&browser), "");
    assert_eq!(browser.stack.len(), 1);

    browser.key("escape");

    assert!(browser.stack.is_empty());
}

#[test]
fn backspace_takes_one_character_back_and_leaves_the_wall_on_an_empty_field() {
    let mut browser = sampled();
    browser.key("a");
    browser.key("b");

    browser.key("backspace");

    assert_eq!(typed(&browser), "a");

    browser.key("backspace");
    assert_eq!(typed(&browser), "");

    browser.key("backspace");
    assert!(browser.stack.is_empty());
}

#[test]
fn the_search_word_opens_an_empty_wall_with_the_grid_and_does_nothing_on_it() {
    let mut browser = sampled();

    browser.key("search");

    assert_eq!(browser.stack.len(), 1);
    assert_eq!(typed(&browser), "");
    assert!(grid(&browser));

    browser.key("search");

    assert_eq!(browser.stack.len(), 1);
    assert!(grid(&browser));
}

#[test]
fn select_on_the_strip_over_the_home_page_opens_the_wall_the_search_word_does() {
    let mut browser = sampled();
    on_the_strip(&mut browser);

    browser.key("enter");

    assert_eq!(browser.stack.len(), 1);
    assert_eq!(typed(&browser), "");
    assert!(grid(&browser));
}

#[test]
fn a_physical_letter_hides_the_grid_and_types() {
    let mut browser = sampled();
    browser.key("search");

    browser.key("b");

    assert!(!grid(&browser));
    assert_eq!(typed(&browser), "b");
}

#[test]
fn select_on_the_grid_types_the_cell_that_holds_its_focus() {
    let mut browser = sampled();
    browser.key("search");
    browser.key("right");

    browser.key("enter");

    assert_eq!(typed(&browser), "b");
    assert!(grid(&browser));
}

#[test]
fn the_shown_grid_takes_the_arrows_and_the_results_keep_their_focus() {
    let mut browser = sampled();
    browser.key("search");
    browser.key("enter");

    browser.key("right");
    browser.key("enter");

    assert_eq!(typed(&browser), "ab");
    assert_eq!(searching(&browser).slots.focus, 0);
}

#[test]
fn with_the_grid_hidden_the_arrows_move_focus_in_the_results() {
    let mut browser = sampled();
    browser.key("s");

    browser.key("right");

    assert_eq!(searching(&browser).slots.focus, 1);
    assert!(!browser.on_strip);
}

#[test]
fn up_from_the_first_row_reaches_the_field_and_select_shows_the_grid_again() {
    let mut browser = sampled();
    browser.key("s");

    browser.key("up");

    assert!(browser.on_strip);
    assert!(!grid(&browser));

    browser.key("enter");

    assert!(grid(&browser));
    assert!(!browser.on_strip);
}

#[test]
fn a_letter_on_the_strip_over_a_search_wall_types_and_gives_the_focus_back() {
    let mut browser = sampled();
    browser.key("s");
    browser.key("up");

    browser.key("e");

    assert!(!browser.on_strip);
    assert_eq!(typed(&browser), "se");
}

#[test]
fn escape_on_the_strip_gives_the_focus_back_and_pops_no_screen() {
    let mut browser = sampled();
    browser.key("s");
    browser.key("up");

    browser.key("escape");

    assert!(!browser.on_strip);
    assert_eq!(browser.stack.len(), 1);
    assert_eq!(typed(&browser), "s");
}

#[test]
fn a_letter_on_a_page_opens_the_search_wall_and_back_returns_to_the_page() {
    let mut browser = sampled();
    browser.key("enter");
    let under = browser.stack.len();

    browser.key("s");

    assert_eq!(browser.stack.len(), under + 1);
    assert_eq!(typed(&browser), "s");

    browser.key("escape");
    browser.key("escape");

    assert_eq!(browser.stack.len(), under);
}

#[test]
fn a_select_on_the_strip_over_a_wall_pushes_an_empty_search_wall_with_the_grid() {
    let mut browser = browser(20);
    browser.key("enter");
    browser.key("up");

    browser.key("enter");

    assert_eq!(browser.stack.len(), 2);
    let search = showing_wall(&browser).search.as_ref().unwrap();
    assert_eq!(search.field.text(), "");
    assert!(search.keyboard.is_some());
}

// Put the browser on a search wall with hits, the grid shown, and a
// slot other than the first focused, so a test sees focus move.
fn typing_over_hits(browser: &mut Browser<Catalog, NoArt>) {
    browser.key("s");
    browser.key("right");
    browser.key("up");
    browser.key("enter");
    assert!(grid(browser));
    assert_eq!(searching(browser).slots.focus, 1);
}

#[test]
fn a_down_press_off_the_bottom_of_the_grid_hides_it_and_lands_on_the_first_result() {
    let mut browser = sampled();
    typing_over_hits(&mut browser);
    browser.key("down");
    browser.key("down");
    browser.key("down");
    assert!(grid(&browser));

    browser.key("down");

    assert!(!grid(&browser));
    assert_eq!(searching(&browser).slots.focus, 0);
    assert!(searching(&browser).marks(true));
    assert_eq!(typed(&browser), "s");
    assert!(!browser.on_strip);
    assert_eq!(browser.stack.len(), 1);
}

#[test]
fn a_down_press_off_the_bottom_of_the_grid_over_an_empty_wall_moves_nothing() {
    let mut browser = sampled();
    browser.key("search");
    browser.key("down");
    browser.key("down");
    browser.key("down");

    assert!(!browser.key("down"));

    assert!(grid(&browser));
    assert_eq!(browser.stack.len(), 1);
}

#[test]
fn escape_on_the_shown_grid_keeps_the_text_and_lands_on_the_first_result() {
    let mut browser = sampled();
    typing_over_hits(&mut browser);
    let hits = searching(&browser).slots.items.len();

    browser.key("escape");

    assert!(!grid(&browser));
    assert_eq!(typed(&browser), "s");
    assert_eq!(searching(&browser).slots.items.len(), hits);
    assert_eq!(searching(&browser).slots.focus, 0);
    assert!(searching(&browser).marks(true));
    assert_eq!(browser.stack.len(), 1);
}

#[test]
fn escape_on_the_shown_grid_over_an_empty_field_leaves_the_wall() {
    let mut browser = sampled();
    browser.key("search");

    browser.key("escape");

    assert!(browser.stack.is_empty());
}

#[test]
fn backspace_on_the_shown_grid_takes_one_character_back_and_keeps_the_grid() {
    let mut browser = sampled();
    browser.key("s");
    browser.key("e");
    browser.key("up");
    browser.key("enter");

    browser.key("backspace");

    assert_eq!(typed(&browser), "s");
    assert!(grid(&browser));
    assert_eq!(browser.stack.len(), 1);
}
