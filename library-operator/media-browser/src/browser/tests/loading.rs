// The loading state at the browser: the press that enters it, the film
// that holds it, the return that ends it, and the frames the loop asks
// for while it runs.

use super::*;

// The second the press lands on, so the state is entered on a clock that
// has already moved.
const PRESS: f64 = 3.0;

// The browser on a movie page with focus on Play, at the second before
// the press.
fn on_a_movie() -> (Browser<Fake, NoPosters>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.key("enter");
    browser.key("enter");
    browser.tick(PRESS);
    (browser, bus)
}

// The browser on a series page with focus on the first still.
fn on_a_series() -> Browser<Fake, NoPosters> {
    let (mut browser, _bus) = playing(vec![one_item()]);
    browser.key("right");
    browser.key("enter");
    browser.key("enter");
    browser.tick(PRESS);
    browser
}

#[test]
fn play_on_a_movie_page_enters_the_state_in_the_same_frame() {
    let (mut browser, bus) = on_a_movie();

    browser.key("enter");

    assert_eq!(browser.loading.map(|state| state.away(PRESS)), Some(0.0));
    assert_eq!(
        browser
            .loading
            .map(|state| state.away(PRESS + look::DEPARTURE)),
        Some(1.0)
    );
    assert!(!published_nothing(&bus));
}

#[test]
fn an_episode_enters_the_state_as_a_movie_does() {
    let mut browser = on_a_series();

    browser.key("enter");

    assert!(browser.loading.is_some());
    assert!(matches!(browser.top(), screens::Screen::Series(_)));
}

#[test]
fn a_choice_that_plays_nothing_enters_no_state() {
    let (mut browser, _bus) = playing(Vec::new());
    browser.key("enter");
    browser.key("enter");
    browser.tick(PRESS);

    browser.key("enter");

    assert!(browser.loading.is_none());
}

#[test]
fn the_film_holds_the_state_under_it() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");

    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Sleep];
    browser.pump(PRESS + 1.0);
    browser.tick(PRESS + 1.0);

    assert!(browser.asleep());
    assert!(browser.loading.is_some());
    assert!(!browser.loading.expect("the state holds").leaving());
}

#[test]
fn the_wake_after_the_film_returns_the_page() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Sleep];
    browser.pump(PRESS + 1.0);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Wake];
    browser.pump(PRESS + 300.0);

    assert!(browser.loading.expect("the state is leaving").leaving());
    browser.tick(PRESS + 300.0 + look::RETURN);
    assert!(browser.loading.is_none());
    assert!(matches!(browser.top(), screens::Screen::Movie(_)));
}

// The `Play` ends while the browser was never covered, so the crate
// sends the fresh surface and no wake. The page returns on that alone.
#[test]
fn a_present_returns_the_page_too() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");

    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Present];
    browser.pump(PRESS + 5.0);

    assert!(browser.loading.expect("the state is leaving").leaving());
    assert!(browser.surface_due());
}

#[test]
fn back_during_the_state_returns_the_page_and_cancels_nothing() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    let published = bus.published.lock().expect("no test panics").len();
    browser.tick(PRESS + 0.5);

    browser.key("escape");

    assert!(browser.loading.expect("the state is leaving").leaving());
    assert_eq!(
        bus.published.lock().expect("no test panics").len(),
        published
    );
    assert_eq!(bus.sleeps.load(Ordering::SeqCst), 0);
    assert_eq!(browser.stack.len(), 2);
}

#[test]
fn a_press_during_the_state_reaches_no_screen() {
    let (mut browser, _bus) = on_a_movie();
    browser.source.trailers = true;
    browser.key("enter");

    browser.key("right");

    assert_eq!(showing_page(&browser).focus, Focus::Buttons(0));
}

#[test]
fn the_loop_draws_the_state_and_goes_quiet_after_it() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    browser.minute = Some(MINUTE);

    assert_eq!(browser.next_frame(PRESS + 0.1), Some(PRESS + 0.1));

    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Present];
    browser.pump(PRESS + 5.0);
    browser.tick(PRESS + 5.0 + look::RETURN);
    browser.minute = Some(MINUTE);

    assert_eq!(browser.next_frame(PRESS + 6.0), Some(MINUTE));
}

// A state held under a film would draw a black frame sixty times a
// second, so the browser asks for none while the shade is down.
#[test]
fn a_state_under_the_film_asks_for_no_frame() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");

    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Sleep];
    browser.pump(PRESS + 1.0);

    assert_eq!(browser.next_frame(PRESS + 1.0), None);
}

#[test]
fn the_state_draws_over_the_page() {
    let (mut browser, _bus) = on_a_movie();
    browser.key("enter");
    browser.tick(PRESS + look::DEPARTURE);

    let _ = browser.view();
}
