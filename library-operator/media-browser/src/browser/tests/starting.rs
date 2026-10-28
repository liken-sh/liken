// The line the loading state draws under the mark: the work that starts,
// where the page's logo names another work. A press on a page and a
// person who takes the up-next offer enter the same state, so both are
// here, and the two name an episode in the same words.

use super::*;

const PRESS: f64 = 3.0;

// The line the state draws at the second it was entered.
fn line(browser: &Browser<Fake, NoArt>) -> Option<String> {
    let state = browser.loading.as_ref()?;
    Some(state.curtain(PRESS).line.to_string())
}

// The browser on a series page with focus on the first button of the first
// episode's row.
fn on_a_series() -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.key("right");
    browser.key("enter");
    browser.key("enter");
    browser.key("enter");
    browser.tick(PRESS);
    (browser, bus)
}

// The browser on the page of the first film of a set, with focus on Play.
fn on_a_film() -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.source.sets = true;
    browser.key("enter");
    browser.key("enter");
    browser.tick(PRESS);
    (browser, bus)
}

// The person takes the offer the player's display drew, which reaches the
// browser as a block on the Player's commands topic.
fn take_the_offer(browser: &mut Browser<Fake, NoArt>, bus: &FakeBus, request: serde_json::Value) {
    *bus.inbound.lock().expect("no test panics with the lock") =
        vec![Moment::PlayNext(request.to_string().into_bytes())];
    browser.pump(PRESS);
}

#[test]
fn an_episode_pressed_on_its_series_page_is_named_under_the_mark() {
    let (mut browser, _bus) = on_a_series();

    browser.key("enter");

    assert_eq!(line(&browser).as_deref(), Some("E01 · Segment 1"));
}

#[test]
fn a_film_pressed_on_its_own_page_draws_no_line() {
    let (mut browser, _bus) = on_a_film();

    browser.key("enter");

    assert_eq!(line(&browser).as_deref(), Some(""));
}

#[test]
fn a_trailer_pressed_on_its_films_page_draws_no_line() {
    let (mut browser, _bus) = on_a_film();
    browser.source.trailers = true;
    browser.key("escape");
    browser.key("enter");
    browser.key("right");

    browser.key("enter");

    assert_eq!(
        browser.source.chosen,
        Some((
            "screening/films".to_string(),
            Selection::Trailer {
                id: "movies:1".into()
            }
        ))
    );
    assert_eq!(line(&browser).as_deref(), Some(""));
}

// The offer's title and the curtain's line are spelled by one function,
// so the person reads the same words on the card they took and on the
// wait that follows it.
#[test]
fn the_offer_taken_names_the_episode_in_the_offers_words() {
    let (mut browser, bus) = on_a_series();

    take_the_offer(
        &mut browser,
        &bus,
        serde_json::json!({
            "library": SERIALS,
            "selection": {"episode": {"series": SERIAL, "season": 1, "episode": 2}},
        }),
    );

    assert!(matches!(browser.top(), screens::Screen::Series(_)));
    assert_eq!(line(&browser).as_deref(), Some("E02 · Segment 2"));
}

// The next film of a set starts under the logo of the film before it, so
// the logo names the wrong work and the line names the right one.
#[test]
fn the_next_film_of_a_set_is_named_under_the_logo_of_the_last() {
    let (mut browser, bus) = on_a_film();

    take_the_offer(
        &mut browser,
        &bus,
        serde_json::json!({
            "library": "screening/films",
            "selection": {"movie": {"id": "movies:2"}},
        }),
    );

    assert!(matches!(browser.top(), screens::Screen::Movie(_)));
    assert_eq!(line(&browser).as_deref(), Some("Entry 2"));
}

// The state the first press entered still stands under the film when the
// person takes the offer, so the offer renames it in place. Entering it
// again would run the page's departure a second time, and the page and
// its logo would show through the fading film.
#[test]
fn the_offer_taken_over_a_standing_state_keeps_its_clock() {
    let (mut browser, bus) = on_a_series();
    browser.key("enter");
    let later = PRESS + 600.0;

    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::PlayNext(
        serde_json::json!({
            "library": SERIALS,
            "selection": {"episode": {"series": SERIAL, "season": 1, "episode": 2}},
        })
        .to_string()
        .into_bytes(),
    )];
    browser.pump(later);

    let state = browser.loading.as_ref().expect("the state still stands");
    assert_eq!(state.curtain(later).away, 1.0);
    assert_eq!(state.curtain(later).line, "E02 · Segment 2");
}

// The person takes the offer at the credits, and the film's surface goes
// before the request that names the next episode reaches the browser. The
// state that stood under the film shows again in that gap, and it must not
// name the episode that just ended. The line waits for the request, and
// the name it carries fades in over the departure's length from the second
// it lands, the way the mark faded in under the first one.
#[test]
fn the_offer_taken_never_draws_the_name_of_the_episode_that_ended() {
    let (mut browser, bus) = on_a_series();
    browser.key("enter");
    let fold = |browser: &mut Browser<Fake, NoArt>, moment: Moment, at: f64| {
        *bus.inbound.lock().expect("no test panics with the lock") = vec![moment];
        browser.pump(at);
    };
    let played = PRESS + 1.0;
    fold(&mut browser, status(Activity::Playing), played);
    let uncovered = played + 600.0;
    fold(&mut browser, status(Activity::Starting), uncovered);

    let state = browser.loading.as_ref().expect("the state still stands");
    assert_eq!(state.curtain(uncovered).line, "");

    let named = uncovered + 0.1;
    let offer = serde_json::json!({
        "library": SERIALS,
        "selection": {"episode": {"series": SERIAL, "season": 1, "episode": 2}},
    });
    fold(
        &mut browser,
        Moment::PlayNext(offer.to_string().into_bytes()),
        named,
    );

    let state = browser.loading.as_ref().expect("the state still stands");
    let cases = [
        (named, 0.0),
        (named + look::DEPARTURE / 2.0, 0.5),
        (named + look::DEPARTURE, 1.0),
    ];
    for (at, shown) in cases {
        let curtain = state.curtain(at);
        assert_eq!(curtain.line, "E02 · Segment 2");
        assert_eq!(curtain.named, shown, "{at}");
    }
}

// The line of the first press fades in with the mark, from the second the
// press entered the state.
#[test]
fn the_line_of_a_press_fades_in_with_the_mark() {
    let (mut browser, _bus) = on_a_series();

    browser.key("enter");

    let state = browser
        .loading
        .as_ref()
        .expect("the press entered the state");
    for at in [
        PRESS,
        PRESS + look::DEPARTURE / 2.0,
        PRESS + look::DEPARTURE,
    ] {
        let curtain = state.curtain(at);
        assert_eq!(curtain.named, curtain.away, "{at}");
    }
}
