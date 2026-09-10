// The loading state at the browser: the press that enters it, the film
// that holds it, the return that ends it, and the frames the loop asks
// for while it runs. The lights beside it enter on the `Player`'s
// status and end on the same return, so the cases for them are here as
// well.

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
fn on_a_series() -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.key("right");
    browser.key("enter");
    browser.key("enter");
    browser.tick(PRESS);
    (browser, bus)
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
    let (mut browser, _bus) = on_a_series();

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

// The film ends, and the compositor shows this window again the moment
// the film's surface goes. The return waits for the frame that draws it:
// the fold marks it and the next tick starts it, so its whole length
// runs on frames a person sees and none of it on the seconds under the
// film.
#[test]
fn the_films_end_marks_the_return_and_the_next_frame_starts_it() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Playing)];
    browser.pump(PRESS + 1.0);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Idle)];
    browser.pump(PRESS + 5.0);

    assert!(browser.returning);
    assert!(!browser.covered());
    assert!(!browser.loading.expect("the state holds").leaving());

    browser.tick(PRESS + 7.0);

    assert!(!browser.returning);
    assert!(browser.loading.expect("the state is leaving").leaving());
    assert_eq!(browser.loading.expect("the state").away(PRESS + 7.0), 1.0);
    browser.tick(PRESS + 7.0 + look::RETURN);
    assert!(browser.loading.is_none());
}

// A `Play` that never played ends the same way a film does: the unit
// reaches `Idle` without ever reaching `Playing`, and the page a person
// is waiting on comes back rather than holding its pulse forever.
#[test]
fn a_play_that_never_played_returns_the_page() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Starting)];
    browser.pump(PRESS + 1.0);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Idle)];
    browser.pump(PRESS + 5.0);
    browser.tick(PRESS + 7.0);

    assert!(browser.loading.expect("the state is leaving").leaving());
    browser.tick(PRESS + 7.0 + look::RETURN);
    assert!(browser.loading.is_none());
}

// The curtain is the select's and the lights are the status's, so the
// select alone draws the curtain over a page at full brightness. The
// two states run beside each other in the frame.
#[test]
fn the_ask_for_a_film_draws_the_curtain_and_leaves_the_lights_up() {
    let (mut browser, _bus) = on_a_movie();

    browser.key("enter");

    assert!(browser.loading.is_some());
    assert!(browser.lights.is_none());
    browser.tick(PRESS + look::LIGHTS_DOWN);
    assert!(browser.lights.is_none());
}

// The lights go down from the second the `Player` moves off `Idle`, so
// the film fades in over a dimmed page. A `Play` that is starting has
// covered nothing yet, and the room keeps going down under it.
#[test]
fn the_status_takes_the_lights_down_from_its_own_second() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    let moved = PRESS + 1.0;

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Starting)];
    browser.pump(moved);

    assert_eq!(browser.lights.map(|state| state.level(moved)), Some(1.0));
    assert_eq!(
        browser
            .lights
            .map(|state| state.level(moved + look::LIGHTS_DOWN)),
        Some(look::LIGHTS_FLOOR)
    );
}

// The status is the one way in, so a `Play` that kubectl, another
// client, or an automation created dims this page too. It draws no
// curtain, because a film nobody chose on this page has no title on it
// to draw.
#[test]
fn a_play_this_browser_never_asked_for_takes_the_lights_down() {
    let (mut browser, _bus) = on_bus(3, vec![status(Activity::Starting)]);

    browser.pump(PRESS);

    assert_eq!(browser.lights.map(|state| state.level(PRESS)), Some(1.0));
    assert_eq!(
        browser
            .lights
            .map(|state| state.level(PRESS + look::LIGHTS_DOWN)),
        Some(look::LIGHTS_FLOOR)
    );
    assert!(browser.loading.is_none());
}

// The browser reads the status the broker kept for this screen, so a
// browser that starts under a film that is already playing dims without
// ever reading a `Starting`.
#[test]
fn a_film_already_playing_when_the_browser_starts_takes_the_lights_down() {
    let (mut browser, _bus) = on_bus(3, vec![status(Activity::Playing)]);

    browser.pump(PRESS);

    assert_eq!(
        browser
            .lights
            .map(|state| state.level(PRESS + look::LIGHTS_DOWN)),
        Some(look::LIGHTS_FLOOR)
    );
    assert!(browser.loading.is_none());
}

// The operator publishes a status on every change of the unit, and the
// lights answer the move off `Idle` alone, so the room keeps going down
// from the level it stood at when the next status lands.
#[test]
fn a_second_status_off_idle_does_not_restart_the_descent() {
    let (mut browser, bus) = on_bus(3, vec![status(Activity::Starting)]);
    browser.pump(PRESS);
    let part = PRESS + look::LIGHTS_DOWN / 2.0;
    let stood = browser
        .lights
        .expect("the status entered the lights")
        .level(part);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Playing)];
    browser.pump(part);

    assert_eq!(browser.lights.map(|state| state.level(part)), Some(stood));
    assert_eq!(
        browser
            .lights
            .map(|state| state.level(PRESS + look::LIGHTS_DOWN)),
        Some(look::LIGHTS_FLOOR)
    );
}

// The film covers the page for as long as it plays, and the room stays
// at the floor under it, so the page the film fades out over is the
// dimmed one.
#[test]
fn a_playing_film_holds_the_lights_at_the_floor() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Playing)];
    browser.pump(PRESS + 2.0);

    assert_eq!(
        browser.lights.map(|state| state.level(PRESS + 3_600.0)),
        Some(look::LIGHTS_FLOOR)
    );
}

// The film's end lifts the room on the frame that starts the page's
// return, and the state goes once the room is back at full.
#[test]
fn the_films_end_lifts_the_lights_and_the_state_goes_with_them() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Playing)];
    browser.pump(PRESS + 1.0);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Idle)];
    browser.pump(PRESS + 5.0);
    browser.tick(PRESS + 5.0);

    assert_eq!(
        browser.lights.map(|state| state.level(PRESS + 5.0)),
        Some(look::LIGHTS_FLOOR)
    );
    assert_eq!(
        browser
            .lights
            .map(|state| state.level(PRESS + 5.0 + look::LIGHTS_UP)),
        Some(1.0)
    );

    browser.tick(PRESS + 5.0 + look::LIGHTS_UP);

    assert!(browser.lights.is_none());
}

// A `Play` that never played takes the same lift, so a failed start
// leaves no dim page.
#[test]
fn a_play_that_never_played_lifts_the_lights() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Starting)];
    browser.pump(PRESS + 1.0);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Idle)];
    browser.pump(PRESS + 5.0);
    browser.tick(PRESS + 5.0);

    assert_eq!(
        browser
            .lights
            .map(|state| state.level(PRESS + 5.0 + look::LIGHTS_UP)),
        Some(1.0)
    );

    browser.tick(PRESS + 5.0 + look::LIGHTS_UP);

    assert!(browser.lights.is_none());
}

// The move to `Idle` ends the lights whether or not a curtain ran with
// them, so a `Play` this browser never asked for leaves no dim page
// behind.
#[test]
fn the_end_of_a_film_this_browser_never_asked_for_lifts_the_lights() {
    let (mut browser, bus) = on_bus(3, vec![status(Activity::Playing)]);
    browser.pump(PRESS);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Idle)];
    browser.pump(PRESS + 5.0);
    browser.tick(PRESS + 5.0);

    assert_eq!(
        browser.lights.map(|state| state.level(PRESS + 5.0)),
        Some(look::LIGHTS_FLOOR)
    );

    browser.tick(PRESS + 5.0 + look::LIGHTS_UP);

    assert!(browser.lights.is_none());
    assert!(browser.loading.is_none());
}

// The return runs on the move to `Idle` and not on the word. The
// operator publishes a status on every change of the unit, so a browser
// that read each `Idle` as an end would return the page again and again
// while nothing played.
#[test]
fn a_second_idle_status_marks_no_second_return() {
    let (mut browser, bus) = on_a_movie();
    *bus.inbound.lock().expect("no test panics with the lock") =
        vec![status(Activity::Playing), status(Activity::Idle)];
    browser.pump(PRESS + 1.0);
    browser.tick(PRESS + 1.0);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Idle)];
    browser.pump(PRESS + 2.0);

    assert!(!browser.returning);
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

    *bus.inbound.lock().expect("no test panics with the lock") =
        vec![status(Activity::Playing), status(Activity::Idle)];
    browser.pump(PRESS + 5.0);
    browser.tick(PRESS + 5.0);
    browser.tick(PRESS + 5.0 + look::RETURN);
    browser.minute = Some(MINUTE);

    assert_eq!(browser.next_frame(PRESS + 6.0), Some(MINUTE));
}

// A state held under a film would draw a black frame sixty times a
// second, so the browser asks for none while the shade is down.
// The film covers the surface and the bus says so in the status. The
// crate sends no sleep during a film, so the status is the one word the
// browser has that the film is up. The harness builds no frame while
// the browser answers covered, and the browser starts no read of its
// own either: a read is not a frame, so the harness cannot hold it, and
// a film's progress rows would order one every second.
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
    browser.minute = Some(MINUTE);

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
fn the_films_end_uncovers_the_browser_and_asks_for_the_frame_that_returns() {
    let (mut browser, bus) = on_a_movie();
    browser.key("enter");
    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Playing)];
    browser.pump(PRESS + 1.0);

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Idle)];
    browser.pump(PRESS + 5.0);
    browser.minute = Some(MINUTE);

    assert!(!browser.covered());
    assert_eq!(browser.next_frame(PRESS + 5.0), Some(PRESS + 5.0));
    browser.tick(PRESS + 5.0);
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

// The dim is the frame's own layer over whatever screen is showing, so
// a `Play` that starts while a person is on the home page dims that
// page. The home page draws no curtain front, because a film nobody
// chose there has no title on it to draw.
#[test]
fn the_lights_draw_over_the_home_page() {
    let (mut browser, _bus) = on_bus(3, vec![status(Activity::Starting)]);
    browser.pump(PRESS);
    browser.tick(PRESS + look::LIGHTS_DOWN);

    assert!(matches!(browser.top(), screens::Screen::Home(_)));
    assert!(browser.lights.is_some());
    let curtain = crate::screens::loading::Loading::entered(PRESS).curtain(PRESS);
    assert!(browser.top().front(&browser.store, curtain).is_none());
    let _ = browser.view();
}

// The two states draw together on a title's page: the departing art
// under the dim, and the curtain's logo over it. Both pages a title
// plays from draw the pair, and each draws its own art in it.
#[test]
fn the_lights_and_the_curtain_draw_over_a_movie_page() {
    let (mut browser, bus) = on_a_movie();

    dimmed_under_a_curtain(&mut browser, &bus);

    assert!(matches!(browser.top(), screens::Screen::Movie(_)));
    let _ = browser.view();
}

#[test]
fn the_lights_and_the_curtain_draw_over_a_series_page() {
    let (mut browser, bus) = on_a_series();

    dimmed_under_a_curtain(&mut browser, &bus);

    assert!(matches!(browser.top(), screens::Screen::Series(_)));
    let _ = browser.view();
}

// A select on Play, and the status that follows it, so the frame carries
// the curtain and the dim at once.
fn dimmed_under_a_curtain(browser: &mut Browser<Fake, NoArt>, bus: &FakeBus) {
    browser.key("enter");

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Starting)];
    browser.pump(PRESS + 0.1);
    browser.tick(PRESS + look::LIGHTS_DOWN);

    assert!(browser.loading.is_some());
    assert!(browser.lights.is_some());
}

// A film writes a progress row about once a second, and each one marks
// the source changed. A read under the cover draws nothing and costs a
// full read of the screen, so nothing is read until the cover lifts.
#[test]
fn a_change_under_the_film_reads_nothing() {
    let (mut browser, _bus) = on_bus(3, vec![status(Activity::Playing)]);
    browser.pump(1.0);
    assert!(browser.covered());

    browser.source.calls.clear();
    browser.source.changed = true;
    browser.pump(2.0);

    assert_eq!(browser.source.calls, Vec::<&str>::new());
}

// The held read runs on the moment the cover lifts, so the first frame
// after the film draws the rows that changed under it.
#[test]
fn the_change_under_the_film_is_read_when_the_film_ends() {
    let (mut browser, bus) = on_bus(3, vec![status(Activity::Playing)]);
    browser.pump(1.0);
    browser.source.changed = true;
    browser.pump(2.0);
    browser.source.calls.clear();

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Idle)];
    browser.pump(3.0);

    assert!(browser.source.calls.contains(&"pool"));
}

// The held read covers the page a person left, so a page deep in the
// stack comes back current after the film.
#[test]
fn a_page_held_under_the_film_is_read_again_when_the_film_ends() {
    let (mut browser, bus) = on_a_movie();
    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Playing)];
    browser.pump(PRESS + 1.0);
    browser.source.calls.clear();
    browser.source.changed = true;
    browser.pump(PRESS + 2.0);
    assert_eq!(browser.source.calls, Vec::<&str>::new());

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Idle)];
    browser.pump(PRESS + 3.0);

    assert!(browser.source.calls.contains(&"movie"));
}

// A cover that lifts with nothing changed under it reads nothing.
#[test]
fn a_film_that_changed_nothing_reads_nothing_when_it_ends() {
    let (mut browser, bus) = on_bus(3, vec![status(Activity::Playing)]);
    browser.pump(1.0);
    browser.source.calls.clear();

    *bus.inbound.lock().expect("no test panics with the lock") = vec![status(Activity::Idle)];
    browser.pump(2.0);

    assert_eq!(browser.source.calls, Vec::<&str>::new());
}

// A film writes a progress row a second, whether or not it plays on
// this screen. A shown browser reads once the rows go quiet, not once
// per row, and the loop wakes for the second that read comes due.
#[test]
fn the_progress_rows_a_film_writes_are_coalesced() {
    let (mut browser, _bus) = on_bus(3, Vec::new());
    browser.source.calls.clear();

    for second in 0..5 {
        browser.source.progressed = true;
        browser.pump(f64::from(second));
    }

    assert_eq!(browser.source.calls, Vec::<&str>::new());

    assert_eq!(browser.next_frame(4.0), Some(6.0));
    browser.pump(7.0);

    assert!(browser.source.calls.contains(&"pool"));
}
