// The strip over every screen. An up press that moves nothing on the
// screen puts focus there, from a page as well as from a wall, and
// select there opens the search wall with the grid. This is how a remote
// with only arrows reaches search from a movie's page.

use super::*;

// A browser on a movie's page: the films wall, then the first film,
// which opens with focus on its buttons.
fn on_a_movie() -> Browser<Fake, NoPosters> {
    let mut browser = browser(3);
    browser.source.people = true;
    browser.key("enter");
    browser.key("enter");
    let _ = showing_page(&browser);
    browser
}

#[test]
fn up_from_a_movies_buttons_reaches_the_strip_and_select_opens_search() {
    let mut browser = on_a_movie();

    browser.key("up");

    assert!(browser.on_strip);
    assert_eq!(browser.stack.len(), 2);

    browser.key("enter");

    assert!(!browser.on_strip);
    assert_eq!(browser.stack.len(), 3);
    let search = showing_wall(&browser)
        .search
        .as_ref()
        .expect("the wall types");
    assert_eq!(search.field.text(), "");
    assert!(search.keyboard.is_some());
}

#[test]
fn down_from_the_strip_gives_a_movies_page_its_focus_back() {
    let mut browser = on_a_movie();
    browser.key("up");
    assert!(browser.on_strip);

    browser.key("down");

    assert!(!browser.on_strip);
    assert_eq!(showing_page(&browser).focus, Focus::Buttons(0));
}

#[test]
fn up_from_the_first_row_of_a_persons_works_reaches_the_strip() {
    let mut browser = on_a_movie();
    browser.key("down");
    browser.key("enter");
    let _ = showing_person(&browser);

    browser.key("up");

    assert!(browser.on_strip);

    browser.key("down");
    browser.key("right");
    browser.key("up");

    assert!(browser.on_strip);
}

// A browser on a franchise's page, which the home page's franchises
// strip opens.
fn on_a_franchise() -> Browser<Fake, NoPosters> {
    let mut browser = Browser::new(
        Fake {
            movies: 3,
            recent: true,
            ..Fake::default()
        },
        NoPosters::default(),
    );
    for _ in 0..5 {
        browser.key("down");
    }
    browser.key("enter");
    browser
}

#[test]
fn up_from_the_first_row_of_a_franchise_page_reaches_the_strip() {
    let mut browser = on_a_franchise();
    let _ = showing_franchise(&browser);

    browser.key("up");

    assert!(browser.on_strip);
}

#[test]
fn a_press_the_strip_binds_nothing_for_holds_its_focus() {
    let mut browser = browser(20);
    browser.key("enter");
    browser.key("up");
    assert!(browser.on_strip);

    for word in ["up", "left", "right", " "] {
        browser.key(word);
        assert!(browser.on_strip, "{word}");
    }
}

#[test]
fn the_view_builds_with_the_strip_in_focus_over_a_wall() {
    let mut browser = browser(20);
    browser.tick(1.0);
    browser.key("enter");
    browser.key("up");

    let _ = browser.view();
}
