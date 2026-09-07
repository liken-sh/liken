// The home word through the browser: where home lands and what it
// reads, and that the space moves no focus on any screen.

use super::*;

// A browser three pages deep: the movies wall, one film's page, and the
// page of a person that film credits.
fn three_deep() -> Browser<Fake, NoArt> {
    let mut browser = browser(3);
    browser.source.people = true;
    browser.key("enter");
    browser.key("enter");
    browser.key("down");
    browser.key("enter");
    assert_eq!(browser.stack.len(), 3);
    browser
}

#[test]
fn home_from_three_pages_deep_lands_on_the_home_page() {
    let mut browser = three_deep();

    browser.key("home");

    assert!(browser.stack.is_empty());
    assert!(matches!(browser.top(), screens::Screen::Home(_)));
}

#[test]
fn home_from_a_page_with_nothing_changed_reads_the_home_page_no_further() {
    let mut browser = three_deep();
    browser.source.calls.clear();

    browser.key("home");

    assert!(browser.source.calls.is_empty());
}

#[test]
fn home_from_a_page_after_a_change_reads_the_home_page_again() {
    let mut browser = three_deep();
    browser.source.changed = true;
    browser.pump(1.0);
    browser.source.calls.clear();

    browser.key("home");

    assert!(browser.source.calls.contains(&"pool"));
}

#[test]
fn home_on_the_home_page_hops_to_the_top_and_reads_nothing() {
    let mut browser = browser(3);
    browser.key("down");
    assert_ne!(showing_home(&browser).focus, 0);
    browser.source.calls.clear();

    browser.key("home");

    assert!(browser.stack.is_empty());
    assert_eq!(showing_home(&browser).focus, 0);
    assert!(!browser.on_strip);
    assert!(browser.source.calls.is_empty());
}

// The space is the one typed word that opens no search wall: it edits a
// field that is already open and starts none, so no screen binds it.

#[test]
fn a_space_moves_no_focus_on_the_home_page() {
    let mut browser = browser(3);
    browser.key("right");

    browser.key(" ");

    assert_eq!(strip_at(&browser, 4).focus, 1);
    assert!(browser.stack.is_empty());
}

#[test]
fn a_space_moves_no_focus_on_a_wall() {
    let mut browser = browser(20);
    browser.key("enter");
    browser.key("right");

    browser.key(" ");

    assert_eq!(showing_wall(&browser).slots.focus, 1);
    assert_eq!(browser.stack.len(), 1);
}

#[test]
fn a_space_moves_no_focus_on_the_strip() {
    let mut browser = browser(20);
    browser.key("enter");
    browser.key("up");
    browser.key("right");

    browser.key(" ");

    assert!(browser.on_strip);
}
