// The browser's second route: the moments the bus delivers, and the key
// each remote's name reaches.

use super::*;

#[test]
fn a_press_on_the_bus_moves_focus_like_the_keyboard() {
    let (mut browser, _bus) = on_bus(3, vec![Moment::Press("KEY_RIGHT")]);

    assert!(browser.pump(1.0));

    assert_eq!(showing_home(&browser).strips[2].focus, 1);
}

#[test]
fn select_on_the_bus_descends() {
    let (mut browser, _bus) = on_bus(3, vec![Moment::Press("KEY_SELECT")]);

    assert!(browser.pump(1.0));

    assert!(matches!(browser.top(), screens::Screen::Wall(_)));
}

#[test]
fn the_arrows_are_themselves() {
    assert_eq!(key_of("KEY_UP"), Some("up"));
    assert_eq!(key_of("KEY_DOWN"), Some("down"));
    assert_eq!(key_of("KEY_LEFT"), Some("left"));
    assert_eq!(key_of("KEY_RIGHT"), Some("right"));
}

#[test]
fn every_name_a_remote_gives_select_is_enter() {
    for name in ["KEY_ENTER", "KEY_OK", "KEY_SELECT", "KEY_KPENTER"] {
        assert_eq!(key_of(name), Some("enter"));
    }
}

#[test]
fn every_name_a_remote_gives_back_is_escape() {
    for name in ["KEY_BACK", "KEY_ESC", "KEY_EXIT"] {
        assert_eq!(key_of(name), Some("escape"));
    }
}

#[test]
fn a_press_this_browser_binds_no_key_for_changes_nothing() {
    assert_eq!(key_of("KEY_PLAYPAUSE"), None);

    let (mut browser, _bus) = on_bus(3, vec![Moment::Press("KEY_PLAYPAUSE")]);

    assert!(browser.pump(1.0));

    assert!(browser.stack.is_empty());
}

#[test]
fn an_empty_bus_folds_nothing() {
    let (mut browser, _bus) = on_bus(3, Vec::new());

    assert!(!browser.pump(1.0));
}

#[test]
fn the_shade_covers_the_browser_and_lifts_it() {
    let (mut browser, _bus) = on_bus(3, vec![Moment::Sleep]);
    browser.pump(1.0);
    assert!(browser.asleep());

    *_bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Wake];
    browser.pump(2.0);

    assert!(!browser.asleep());
}

#[test]
fn a_press_that_arrives_asleep_keeps_the_focus() {
    let (mut browser, _bus) = on_bus(3, vec![Moment::Sleep, Moment::Press("KEY_RIGHT")]);

    browser.pump(1.0);

    assert!(browser.asleep());
    assert_eq!(showing_home(&browser).strips[2].focus, 1);
}

#[test]
fn an_asleep_browser_draws_a_black_frame() {
    let (mut browser, _bus) = on_bus(3, vec![Moment::Sleep]);
    browser.pump(1.0);

    assert!(browser.asleep());
    assert_eq!(browser.background(), Color::BLACK);
    let _ = browser.view();
}

#[test]
fn a_present_asks_the_harness_for_a_fresh_surface() {
    let (mut browser, _bus) = on_bus(3, vec![Moment::Present]);

    browser.pump(1.0);

    assert!(browser.surface_due());
    assert!(!browser.surface_due());
}

#[test]
fn a_focus_changes_nothing() {
    let (mut browser, _bus) = on_bus(3, vec![Moment::Focus { remote: 0 }]);

    assert!(browser.pump(1.0));

    assert!(!browser.asleep());
    assert!(!browser.surface_due());
}

#[test]
fn back_at_the_home_page_asks_for_the_shade() {
    let (mut browser, bus) = on_bus(3, Vec::new());

    browser.key("escape");

    assert_eq!(bus.sleeps.load(Ordering::SeqCst), 1);
    assert!(!browser.asleep());
    assert!(matches!(browser.top(), screens::Screen::Home(_)));
}

#[test]
fn backspace_at_the_home_page_asks_too() {
    let (mut browser, bus) = on_bus(3, Vec::new());

    browser.key("backspace");

    assert_eq!(bus.sleeps.load(Ordering::SeqCst), 1);
}

#[test]
fn back_below_the_top_climbs_and_asks_for_nothing() {
    let (mut browser, bus) = on_bus(3, Vec::new());
    browser.key("enter");

    browser.key("escape");

    assert!(browser.stack.is_empty());
    assert_eq!(bus.sleeps.load(Ordering::SeqCst), 0);
}

#[test]
fn back_at_the_home_page_with_no_bus_asks_no_one() {
    let mut browser = browser(3);

    browser.key("escape");

    assert!(matches!(browser.top(), screens::Screen::Home(_)));
}

#[test]
fn the_wake_reaches_the_bus() {
    let (mut browser, bus) = on_bus(3, Vec::new());

    Screen::wake_by(&mut browser, Arc::new(|| {}));

    assert_eq!(bus.woken.load(Ordering::SeqCst), 1);
}
