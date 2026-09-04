// The frame the clock at the top of every screen asks for, at the second
// the minute turns.

use super::*;

#[test]
fn the_tick_reads_the_minute_the_wall_clock_turns_on() {
    let mut browser = browser(3);
    assert_eq!(browser.minute, None);

    browser.tick(1.0);

    let minute = browser.minute.expect("the tick read the wall clock");
    assert!(minute > 1.0 && minute <= 61.0, "{minute}");
}

#[test]
fn the_browser_asks_for_a_frame_when_the_minute_turns() {
    let mut browser = browser(3);
    browser.tick(1.0);
    browser.minute = Some(MINUTE);

    assert_eq!(browser.next_frame(1.0), Some(MINUTE));
}

#[test]
fn a_covered_browser_asks_for_no_frame_when_the_minute_turns() {
    let (mut browser, _bus) = on_bus(3, vec![Moment::Sleep]);
    browser.pump(1.0);
    browser.tick(1.0);
    browser.minute = Some(MINUTE);

    assert!(browser.asleep());
    assert_eq!(browser.next_frame(1.0), None);
    assert_eq!(browser.face(), None);
}

#[test]
fn the_clock_draws_over_every_screen_the_stack_holds() {
    let mut browser = browser(3);
    browser.source.people = true;
    assert!(browser.face().is_some(), "the home page");

    browser.key("enter");
    let _ = showing_wall(&browser);
    assert!(browser.face().is_some(), "a wall");

    browser.key("enter");
    let _ = showing_page(&browser);
    assert!(browser.face().is_some(), "a movie page");

    browser.key("down");
    browser.key("enter");
    let _ = showing_person(&browser);
    assert!(browser.face().is_some(), "a person's page");
}

#[test]
fn the_clock_draws_over_a_series_page() {
    let mut browser = browser(3);
    browser.key("right");
    browser.key("enter");
    browser.key("enter");

    let _ = showing_series(&browser);
    assert!(browser.face().is_some());
}

#[test]
fn the_view_builds_with_the_clock_over_it() {
    let mut browser = browser(3);
    browser.tick(1.0);

    let _ = browser.view();
}
