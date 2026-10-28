// The focus outline on the pages a wall or a strip opens: a film's page, a
// show's page, a person's page, and a franchise's page. Each answers where
// its focus is the way the home page and a wall do, so a press inside the
// page moves the outline with its leading edge ahead and lands it when the
// follow ends.

use iced_winit::core::Rectangle;

use super::*;

// The edges of a rectangle that move on a press across, and on a press
// down: the left and the right, or the top and the bottom.
fn across(at: Rectangle) -> (f32, f32) {
    (at.x, at.x + at.width)
}

fn down(at: Rectangle) -> (f32, f32) {
    (at.y, at.y + at.height)
}

// Whether the outline drawn part way through a move from `from` to `to`
// has its far edge past the middle of its travel and its near edge short
// of the middle of its own: the leading edge ahead.
fn leads(from: (f32, f32), part: (f32, f32), to: (f32, f32)) -> bool {
    part.1 > (from.1 + to.1) / 2.0 && part.0 < (from.0 + to.0) / 2.0
}

// The browser on the first film's page, with focus on its first button.
fn on_a_film() -> Browser<Fake, NoArt> {
    let mut browser = browser(3);
    browser.key("enter");
    browser.key("enter");
    let _ = showing_page(&browser);
    browser
}

#[test]
fn a_press_down_on_a_films_page_moves_the_outline_to_the_mark() {
    let mut browser = on_a_film();
    let from = drawn_at(&mut browser, PRESS);
    press(&mut browser, "down", PRESS);
    let to = resting(&browser);
    assert_eq!(showing_page(&browser).focus, Focus::Marks(0));

    let part = drawn_at(&mut browser, PRESS + look::LEAD * 0.75);
    assert!(
        leads(down(from), down(part), down(to)),
        "{from:?} {part:?} {to:?}"
    );
    assert_eq!(drawn_at(&mut browser, PRESS + look::FOLLOW), to);
}

// The browser on the serial's page, with focus on the first still.
fn on_a_serial() -> Browser<Fake, NoArt> {
    let mut browser = browser(3);
    browser.key("right");
    browser.key("enter");
    browser.key("enter");
    let _ = showing_series(&browser);
    browser
}

#[test]
fn a_press_right_on_a_shows_page_moves_the_outline_to_the_next_still() {
    let mut browser = on_a_serial();
    let from = drawn_at(&mut browser, PRESS);
    press(&mut browser, "right", PRESS);
    let to = resting(&browser);
    assert_eq!(showing_series(&browser).focus, SeriesFocus::Still(1));

    let part = drawn_at(&mut browser, PRESS + look::LEAD * 0.75);
    assert!(
        leads(across(from), across(part), across(to)),
        "{from:?} {part:?} {to:?}"
    );
    assert_eq!(drawn_at(&mut browser, PRESS + look::FOLLOW), to);
}

// The browser on the person the first film credits, with focus on the
// first of their works.
fn on_a_person() -> Browser<Fake, NoArt> {
    let mut browser = browser(3);
    browser.source.people = true;
    browser.key("enter");
    browser.key("enter");
    browser.key("down");
    browser.key("down");
    browser.key("enter");
    let _ = showing_person(&browser);
    browser
}

#[test]
fn a_press_right_on_a_persons_page_moves_the_outline_to_the_next_work() {
    let mut browser = on_a_person();
    let from = drawn_at(&mut browser, PRESS);
    press(&mut browser, "right", PRESS);
    let to = resting(&browser);
    assert_eq!(showing_person(&browser).works.focus, 1);

    let part = drawn_at(&mut browser, PRESS + look::LEAD * 0.75);
    assert!(
        leads(across(from), across(part), across(to)),
        "{from:?} {part:?} {to:?}"
    );
    assert_eq!(drawn_at(&mut browser, PRESS + look::FOLLOW), to);
}

// The browser on the Cycle's page, opened from the home page's franchises
// strip, with focus on its first row.
fn on_a_franchise() -> Browser<Fake, NoArt> {
    let mut browser = Browser::new(
        Fake {
            movies: 3,
            recent: true,
            ..Fake::default()
        },
        NoArt::default(),
    );
    for _ in 0..5 {
        browser.key("down");
    }
    browser.key("enter");
    let _ = showing_franchise(&browser);
    browser
}

#[test]
fn a_press_down_on_a_franchises_page_moves_the_outline_to_the_next_row() {
    let mut browser = on_a_franchise();
    let from = drawn_at(&mut browser, PRESS);
    press(&mut browser, "down", PRESS);
    let to = resting(&browser);
    assert_eq!(showing_franchise(&browser).focus, 1);

    let part = drawn_at(&mut browser, PRESS + look::LEAD * 0.75);
    assert!(
        leads(down(from), down(part), down(to)),
        "{from:?} {part:?} {to:?}"
    );
    assert_eq!(drawn_at(&mut browser, PRESS + look::FOLLOW), to);
}
