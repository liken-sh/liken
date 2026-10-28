// The focus outline: how it travels when a press moves focus, how a wall
// scrolls under it, what a held key does to its lengths, and how it
// breathes while focus rests. Every case drives the clock the way the
// harness does: the clock moves, then the key lands.

use iced_winit::core::Rectangle;

use super::*;

// The second the press in each case lands on, long after the browser's
// first frame, so nothing the browser did while it opened still moves.
const PRESS: f64 = 20.0;

// Press this key at this second.
fn press(browser: &mut Browser<Fake, NoArt>, key: &str, at: f64) {
    browser.tick(at);
    browser.key(key);
}

// The rectangle the outline draws at this second.
fn drawn_at(browser: &mut Browser<Fake, NoArt>, at: f64) -> Rectangle {
    browser.tick(at);
    browser.drawn().expect("an outline is on the screen").around
}

// Whether two rectangles are the same place on the glass. A wall that
// centers its row reaches the same place by another sum, so the two
// differ in the last bits of a float.
fn same_place(one: Rectangle, other: Rectangle) -> bool {
    [
        (one.x, other.x),
        (one.y, other.y),
        (one.width, other.width),
        (one.height, other.height),
    ]
    .iter()
    .all(|(a, b)| (a - b).abs() < 0.01)
}

// The rectangle the outline comes to rest on: the mark of the slot that
// holds focus, where every scroll lands.
fn resting(browser: &Browser<Fake, NoArt>) -> Rectangle {
    browser.target().expect("focus has a place").around
}

// The browser on the libraries strip of the home page, at rest on its
// first slot.
fn on_the_libraries() -> Browser<Fake, NoArt> {
    let mut browser = browser(3);
    browser.tick(0.0);
    browser
}

#[test]
fn a_right_press_moves_the_right_edge_ahead_of_the_left() {
    let mut browser = on_the_libraries();
    let from = drawn_at(&mut browser, PRESS);
    press(&mut browser, "right", PRESS);
    let to = resting(&browser);
    assert!(to.x > from.x, "{from:?} {to:?}");

    let part = drawn_at(&mut browser, PRESS + look::LEAD * 0.75);
    let right = part.x + part.width;
    assert!(
        right > (from.x + from.width + to.x + to.width) / 2.0,
        "{part:?}"
    );
    assert!(part.x < (from.x + to.x) / 2.0, "{part:?}");
}

#[test]
fn the_outline_lands_on_the_new_slots_mark_when_the_follow_ends() {
    let mut browser = on_the_libraries();
    press(&mut browser, "right", PRESS);
    let to = resting(&browser);

    assert_ne!(drawn_at(&mut browser, PRESS + look::FOLLOW * 0.9), to);
    assert_eq!(drawn_at(&mut browser, PRESS + look::FOLLOW), to);
}

#[test]
fn a_second_press_mid_travel_starts_from_the_rectangle_drawn_then() {
    let mut browser = on_the_libraries();
    press(&mut browser, "right", PRESS);
    let between = drawn_at(&mut browser, PRESS + 0.1);

    press(&mut browser, "left", PRESS + 0.1);

    assert_eq!(drawn_at(&mut browser, PRESS + 0.1), between);
    let back = resting(&browser);
    assert_ne!(back, between);
    assert_eq!(drawn_at(&mut browser, PRESS + 0.1 + look::FOLLOW), back);
}

// A held key repeats faster than the outline's longest motion, so each
// motion takes the time between two presses, and never less than a third
// of its length.
#[test]
fn a_held_key_shortens_the_motion_to_the_interval_but_not_below_a_third() {
    let cases = [
        (look::FOLLOW / 2.0, look::FOLLOW / 2.0),
        (look::FOLLOW / 10.0, look::FOLLOW * look::HELD_FLOOR),
    ];
    for (interval, length) in cases {
        let mut browser = on_the_libraries();
        press(&mut browser, "right", PRESS);
        press(&mut browser, "left", PRESS + interval);
        let to = resting(&browser);

        let ends = PRESS + interval + length;
        assert_ne!(
            drawn_at(&mut browser, ends - length / 10.0),
            to,
            "{interval}"
        );
        assert_eq!(drawn_at(&mut browser, ends), to, "{interval}");
    }
}

#[test]
fn a_press_that_opens_a_page_lands_the_outline_on_it_at_once() {
    let mut browser = on_the_libraries();
    press(&mut browser, "enter", PRESS);
    let _ = showing_wall(&browser);

    assert_eq!(drawn_at(&mut browser, PRESS), resting(&browser));
}

#[test]
fn up_from_a_wall_travels_onto_the_strip() {
    let mut browser = on_the_libraries();
    press(&mut browser, "enter", PRESS);
    let from = drawn_at(&mut browser, PRESS + 1.0);

    press(&mut browser, "up", PRESS + 1.0);

    assert!(browser.on_strip);
    let to = resting(&browser);
    let part = drawn_at(&mut browser, PRESS + 1.0 + look::LEAD / 2.0);
    assert_ne!(part, from);
    assert_ne!(part, to);
    assert_eq!(drawn_at(&mut browser, PRESS + 1.0 + look::FOLLOW), to);
}

// A long wall centers the row that holds focus, so a press down in its
// middle moves the row and not the slot's place on the glass: the
// outline stands still and the wall slides under it.
#[test]
fn a_wall_that_centers_its_row_slides_under_a_still_outline() {
    let mut browser = browser(60);
    browser.tick(0.0);
    browser.key("enter");
    for _ in 0..3 {
        browser.key("down");
    }
    let from = drawn_at(&mut browser, PRESS);

    press(&mut browser, "down", PRESS);

    assert_eq!(showing_wall(&browser).slots.focus, 4 * wall::COLUMNS);
    assert!(same_place(resting(&browser), from));
    assert!(same_place(
        drawn_at(&mut browser, PRESS + look::LEAD / 2.0),
        from
    ));
    assert_eq!(
        browser.next_frame(PRESS + look::SCROLL / 2.0),
        Some(PRESS + look::SCROLL / 2.0)
    );
}

#[test]
fn the_outline_breathes_only_after_the_rest_has_lasted_the_delay() {
    let mut browser = on_the_libraries();
    press(&mut browser, "right", PRESS);
    let quiet = PRESS + look::FOLLOW;
    let first = quiet + look::BREATH_DELAY;
    let peak = first + look::BREATH / 2.0;

    let cases = [
        (quiet, look::mark()),
        (first - 0.1, look::mark()),
        (peak, look::breath_peak()),
        (first + look::BREATH, look::mark()),
    ];
    for (at, color) in cases {
        browser.tick(at);
        let drawn = browser.drawn().expect("an outline is on the screen").color;
        let channels = [
            (drawn.r, color.r),
            (drawn.g, color.g),
            (drawn.b, color.b),
            (drawn.a, color.a),
        ];
        for (drawn_channel, channel) in channels {
            assert!((drawn_channel - channel).abs() < 1e-4, "{at}: {drawn:?}");
        }
    }
}

#[test]
fn a_press_releases_the_breath_from_the_brightness_drawn_then() {
    let mut browser = on_the_libraries();
    press(&mut browser, "right", PRESS);
    let peak = PRESS + look::FOLLOW + look::BREATH_DELAY + look::BREATH / 2.0;

    press(&mut browser, "left", peak);

    let alpha = |browser: &mut Browser<Fake, NoArt>, at: f64| {
        browser.tick(at);
        browser
            .drawn()
            .expect("an outline is on the screen")
            .color
            .a
    };
    assert!((alpha(&mut browser, peak) - look::breath_peak().a).abs() < 1e-4);
    let half = alpha(&mut browser, peak + look::BREATH_RELEASE / 2.0);
    assert!(
        half < look::breath_peak().a && half > look::mark().a,
        "{half}"
    );
    assert_eq!(
        alpha(&mut browser, peak + look::BREATH_RELEASE),
        look::mark().a
    );
}

// The frames the outline asks for: every frame of a motion, the start of
// the next breath between breaths, every frame of a breath, and none
// under the shade or a film. The lights ask for frames of their own while
// a film starts, so the case reads the outline's share of the schedule.
#[test]
fn the_outline_asks_for_frames_only_while_it_moves_or_breathes() {
    let quiet = PRESS + look::FOLLOW;
    let first = quiet + look::BREATH_DELAY;
    let cases = [
        (None, PRESS + 0.1, Some(PRESS + 0.1)),
        (None, quiet + 1.0, Some(first)),
        (None, first + 1.0, Some(first + 1.0)),
        (
            None,
            first + look::BREATH + 1.0,
            Some(first + look::BREATH_EVERY),
        ),
        (Some(Moment::Sleep), PRESS + 0.1, None),
        (Some(status(Activity::Playing)), PRESS + 0.1, None),
    ];
    for (moment, at, next) in cases {
        let (mut browser, bus) = on_bus(3, Vec::new());
        browser.tick(0.0);
        press(&mut browser, "right", PRESS);
        if let Some(moment) = moment {
            *bus.inbound.lock().expect("no test panics with the lock") = vec![moment];
            browser.pump(PRESS);
        }
        browser.tick(at);
        assert_eq!(browser.outline_frame(at), next, "{at}");
    }
}

#[test]
fn the_shade_takes_the_outline_off_the_glass() {
    let (mut browser, bus) = on_bus(3, Vec::new());
    browser.tick(0.0);
    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Sleep];
    browser.pump(PRESS);

    assert!(browser.asleep());
    assert_eq!(browser.drawn(), None);
}

// A title's page answers where its focus is like every other screen, so
// the outline goes around the focused button and breathes there.
#[test]
fn a_title_page_draws_the_outline_around_its_focus() {
    let mut browser = on_the_libraries();
    press(&mut browser, "enter", PRESS);
    press(&mut browser, "enter", PRESS + 1.0);
    let _ = showing_page(&browser);
    browser.tick(PRESS + 2.0);
    browser.minute = Some(MINUTE);

    let drawn = browser.drawn().expect("the page's focus has an outline");
    assert_eq!(drawn.layer, crate::browser::outline::Layer::Screen);
    assert_eq!(drawn.around, resting(&browser));
    assert_eq!(
        browser.next_frame(PRESS + 2.0),
        Some(PRESS + 1.0 + look::BREATH_DELAY)
    );
}

// The browser on the home page under the person picker, which it raises
// at once because nobody in the room of two has said who is watching.
fn on_the_picker() -> Browser<Fake, NoArt> {
    let (browser, _bus) = playing(vec![one_item()]);
    let mut browser = browser.with_audience(
        ["first", "second"]
            .iter()
            .map(|name| crate::audience::Person {
                name: (*name).to_string(),
                display_name: (*name).to_string(),
                thumbnail: None,
            })
            .collect(),
        Vec::new(),
    );
    browser.tick(0.0);
    assert!(browser.picker.is_some(), "nobody has said who is watching");
    browser
}

#[test]
fn a_press_on_the_picker_moves_the_outline_to_the_next_tile() {
    let mut browser = on_the_picker();
    let from = drawn_at(&mut browser, PRESS);
    press(&mut browser, "right", PRESS);
    let to = resting(&browser);
    assert!(to.x > from.x, "{from:?} {to:?}");

    let part = drawn_at(&mut browser, PRESS + look::LEAD * 0.75);
    assert!(
        part.x < to.x && part.x + part.width > from.x + from.width,
        "{part:?}"
    );
    assert_eq!(drawn_at(&mut browser, PRESS + look::FOLLOW), to);
}

// The outline on the pages a wall or a strip opens.
mod pages;
