// The loading state at the browser: the press that enters it, the film
// that holds it, the return that ends it, and the frames the loop asks
// for while it runs.

use media_screen::status::{Activity, Status};

use super::*;

// The second the press lands on, so the state is entered on a clock that
// has already moved.
const PRESS: f64 = 3.0;

// The browser on a movie page with focus on Play, at the second before
// the press.
fn on_a_movie() -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.key("enter");
    browser.key("enter");
    browser.tick(PRESS);
    (browser, bus)
}

// The browser on a series page with focus on the first still.
fn on_a_series() -> Browser<Fake, NoArt> {
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
// sends the fresh surface and no wake. The page returns on the surface
// alone: the present asks for it, and the return waits until the
// harness reports it up, so the return's frames land on the window a
// person sees and not on the one the film covered.
#[test]
fn a_present_asks_for_the_surface_and_the_return_waits_for_it() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");

    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Present];
    browser.pump(PRESS + 5.0);

    assert!(browser.surface_due());
    assert!(!browser.loading.expect("the state holds").leaving());

    browser.surfaced(PRESS + 7.0);

    assert!(browser.loading.expect("the state is leaving").leaving());
    assert_eq!(browser.loading.expect("the state").away(PRESS + 7.0), 1.0);
    browser.tick(PRESS + 7.0 + look::RETURN);
    assert!(browser.loading.is_none());
}

#[test]
fn a_surface_with_no_present_behind_it_returns_nothing() {
    let (mut browser, _bus) = on_a_movie();
    browser.key("enter");

    browser.surfaced(PRESS + 7.0);

    assert!(!browser.loading.expect("the state holds").leaving());
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
    browser.surfaced(PRESS + 5.0);
    browser.tick(PRESS + 5.0 + look::RETURN);
    browser.minute = Some(MINUTE);

    assert_eq!(browser.next_frame(PRESS + 6.0), Some(MINUTE));
}

// A state held under a film would draw a black frame sixty times a
// second, so the browser asks for none while the shade is down.
// The film covers the surface and the bus says so in the status. The
// crate sends no sleep during a film, so the status is the one word the
// browser has that the film is up. The harness builds no frame while
// the browser answers covered, whatever the page, the store, or the
// source deliver under it, so the browser guards none of those itself.
fn status(activity: Activity) -> Moment {
    Moment::Status(Status {
        activity,
        ..Status::default()
    })
}

#[test]
fn a_playing_status_covers_the_browser_and_holds_the_state() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    assert!(!browser.covered());

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Playing)];
    browser.pump(PRESS + 1.0);

    assert!(browser.covered());
    assert!(browser.loading.is_some());
    assert!(!browser.loading.expect("the state holds").leaving());
}

// A Play that is starting has not covered anything yet, so the page and
// its pulse stay on the screen until the film plays.
#[test]
fn a_starting_status_covers_nothing() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Starting)];
    browser.pump(PRESS + 1.0);

    assert!(!browser.covered());
    assert_eq!(browser.next_frame(PRESS + 1.0), Some(PRESS + 1.0));
}

#[test]
fn an_idle_status_uncovers_the_browser() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    *bus.inbound.lock().expect("no test panics with the lock") =
        vec![status(Activity::Playing), status(Activity::Idle)];
    browser.pump(PRESS + 1.0);

    assert!(!browser.covered());
}

#[test]
fn the_present_uncovers_the_browser_and_the_surface_draws_the_return() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Playing)];
    browser.pump(PRESS + 1.0);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Present];
    browser.pump(PRESS + 5.0);
    browser.surfaced(PRESS + 5.0);

    assert!(!browser.covered());
    assert_eq!(browser.next_frame(PRESS + 5.0), Some(PRESS + 5.0));
    assert!(browser.loading.expect("the state is leaving").leaving());
}

#[test]
fn the_wake_uncovers_the_browser() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Playing)];
    browser.pump(PRESS + 1.0);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::Wake];
    browser.pump(PRESS + 5.0);

    assert!(!browser.covered());
}

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
