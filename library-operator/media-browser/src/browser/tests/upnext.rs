// What a play request says follows the work it starts, by the page the
// press landed on and the way that page was reached.

use super::*;
use crate::bus::next;
use crate::screens::franchise::Franchise;

const ORDERS: &str = "screening/orders";
const CYCLE: &str = "franchise:name:the-cycle";
const RUN: &str = "franchise:name:the-run";

// The offer the one published request carries, or nothing where it carries
// none.
fn offered(bus: &FakeBus) -> Option<serde_json::Value> {
    published(bus).get("next").cloned()
}

// The browser over a catalog of five films that are in the sets and the
// orders, with focus on the series page of the fake serial.
fn on_a_series() -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.key("right");
    browser.key("enter");
    browser.key("enter");
    (browser, bus)
}

// The browser on one franchise's page, which is the way into a page that
// carries the order it was reached through.
fn on_a_franchise(id: &str) -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.source.orders = true;
    browser.source.sets = true;
    browser.source.movies = 5;
    let page = Franchise::open(ORDERS, id, &mut browser.source).expect("the catalog holds it");
    browser.take(Step::Open(screens::Screen::Franchise(Box::new(page))));
    (browser, bus)
}

// The browser on one film's page, reached from the library wall.
fn on_a_film(sets: bool) -> (Browser<Fake, NoArt>, FakeBus) {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.source.sets = sets;
    browser.key("enter");
    browser.key("enter");
    (browser, bus)
}

#[test]
fn a_play_from_a_series_wall_names_the_next_episode() {
    let (mut browser, bus) = on_a_series();

    browser.key("enter");

    assert_eq!(
        offered(&bus),
        Some(serde_json::json!({
            "library": SERIALS,
            "reason": "Next in The Serial · S01",
            "title": "E02 · Segment 2",
            "detail": "The Serial · S01 · E02 · 46m",
            "art": "s1e2.jpg",
            "request": {
                "library": SERIALS,
                "selection": {"episode": {
                    "series": SERIAL, "season": 1, "episode": 2,
                }},
            },
        }))
    );
}

#[test]
fn the_last_episode_of_a_series_offers_nothing_after_it() {
    let (mut browser, bus) = on_a_series();
    browser.key("down");
    for _ in 0..3 {
        browser.key("right");
    }
    assert_eq!(showing_series(&browser).focus, SeriesFocus::Still(7));

    browser.key("enter");

    assert_eq!(offered(&bus), None);
}

#[test]
fn a_film_opened_through_a_franchise_page_names_the_next_member() {
    let (mut browser, bus) = on_a_franchise(CYCLE);

    browser.key("enter");
    assert_eq!(showing_page(&browser).id, "movies:2");
    browser.key("enter");

    assert_eq!(
        offered(&bus),
        Some(serde_json::json!({
            "library": "screening/films",
            "reason": "Next in The Cycle",
            "title": "Entry 3",
            "detail": "1980",
            "art": "movies:3.jpg",
            "request": {
                "library": "screening/films",
                "selection": {"movie": {"id": "movies:3"}},
                "via": {"library": ORDERS, "id": CYCLE, "position": 2},
            },
        }))
    );
}

// The film is in a set as well, and the order the page was reached through
// is the one the offer follows.
#[test]
fn a_film_reached_through_a_franchise_follows_the_order_and_not_its_set() {
    let (mut browser, bus) = on_a_franchise(CYCLE);
    assert!(showing_franchise(&browser).rows[0].cell.id == "movies:2");

    browser.key("enter");
    assert!(showing_page(&browser).set.is_some());
    browser.key("enter");

    assert_eq!(
        offered(&bus).expect("an offer")["reason"],
        "Next in The Cycle"
    );
}

#[test]
fn an_episode_of_a_series_reached_through_a_franchise_names_the_next_episode() {
    let (mut browser, bus) = on_a_franchise(RUN);

    browser.key("enter");
    assert_eq!(showing_series(&browser).id, LAST_SERIAL);
    browser.key("enter");

    assert_eq!(
        offered(&bus),
        Some(serde_json::json!({
            "library": SERIALS,
            "reason": "Next in The Run · S01",
            "title": "E02 · Segment 2",
            "detail": "Last Serial · S01 · E02 · 46m",
            "art": "s1e2.jpg",
            "request": {
                "library": SERIALS,
                "selection": {"episode": {
                    "series": LAST_SERIAL, "season": 1, "episode": 2,
                }},
                "via": {"library": ORDERS, "id": RUN, "position": 1},
            },
        }))
    );
}

// The run of the series member ends at the fourth episode, so the leaf
// after it is the film the order names next.
#[test]
fn the_last_covered_episode_of_a_member_names_the_member_after_it() {
    let (mut browser, bus) = on_a_franchise(RUN);
    browser.key("enter");
    for _ in 0..3 {
        browser.key("right");
    }

    browser.key("enter");

    assert_eq!(
        offered(&bus),
        Some(serde_json::json!({
            "library": "screening/films",
            "reason": "Next in The Run",
            "title": "Entry 4",
            "detail": "1980",
            "art": "movies:4.jpg",
            "request": {
                "library": "screening/films",
                "selection": {"movie": {"id": "movies:4"}},
                "via": {"library": ORDERS, "id": RUN, "position": 2},
            },
        }))
    );
}

#[test]
fn the_last_member_of_a_franchise_offers_nothing_after_it() {
    let (mut browser, bus) = on_a_franchise(RUN);
    browser.key("down");

    browser.key("enter");
    assert_eq!(showing_page(&browser).id, "movies:4");
    browser.key("enter");

    assert_eq!(offered(&bus), None);
}

#[test]
fn a_film_of_a_set_names_the_next_film_of_the_set() {
    let (mut browser, bus) = on_a_film(true);

    browser.key("enter");

    assert_eq!(
        offered(&bus),
        Some(serde_json::json!({
            "library": "screening/films",
            "reason": "Next in The Entries",
            "title": "Entry 2",
            "detail": "1980 · 1h 30m · PG",
            "request": {
                "library": "screening/films",
                "selection": {"movie": {"id": "movies:2"}},
            },
        }))
    );
}

#[test]
fn a_film_in_no_set_and_no_order_offers_nothing() {
    let (mut browser, bus) = on_a_film(false);

    browser.key("enter");

    assert_eq!(offered(&bus), None);
}

#[test]
fn a_trailer_offers_nothing_after_it() {
    let (mut browser, bus) = on_a_film(true);
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
    assert_eq!(offered(&bus), None);
}

#[test]
fn the_request_block_comes_back_the_way_it_went_out() {
    let (mut browser, bus) = on_a_franchise(RUN);
    browser.key("enter");

    browser.key("enter");

    let block = offered(&bus).expect("an offer")["request"].to_string();
    let back = next::request(block.as_bytes()).expect("the block decodes");
    assert_eq!(back.library, SERIALS);
    assert_eq!(
        back.selection,
        Selection::Episode {
            series: LAST_SERIAL.into(),
            season: 1,
            episode: 2,
        }
    );
    assert_eq!(
        back.via,
        Some(crate::screens::InFranchise {
            library: ORDERS.into(),
            id: RUN.into(),
            position: 1,
        })
    );
}

// One person at the screen, so a progress read answers the rows the fixture
// holds.
fn watcher() -> (Vec<crate::audience::Person>, Vec<String>) {
    (
        vec![crate::audience::Person {
            name: "first".into(),
            display_name: "First".into(),
        }],
        vec!["first".into()],
    )
}

// The browser after one ask off the Player's commands topic, over the
// catalog of five films that are in the sets and the orders.
fn taking(payload: Vec<u8>) -> (Browser<Fake, NoArt>, FakeBus) {
    let bus = FakeBus::default();
    *bus.inbound.lock().expect("no test panics with the lock") = vec![Moment::PlayNext(payload)];
    let mut browser = browser(5);
    browser.source.items = vec![one_item()];
    browser.source.orders = true;
    browser.source.sets = true;
    browser.source.episodes_reached = vec![crate::catalog::Progress {
        play: "play-1".into(),
        position: 900,
        duration: 2_760,
        finished: false,
        recorded: 10,
        season: 1,
        episode: 2,
        ..crate::catalog::Progress::default()
    }];
    browser.source.reached.insert(
        ("screening/films".to_string(), "movies:3".to_string()),
        crate::catalog::Progress {
            play: "play-2".into(),
            position: 600,
            duration: 5_400,
            recorded: 20,
            ..crate::catalog::Progress::default()
        },
    );
    let (people, preset) = watcher();
    let mut browser = browser
        .with_bus(
            Some(Box::new(bus.clone())),
            PLAY_TOPIC.into(),
            AUDIENCE_TOPIC.into(),
        )
        .with_audience(people, preset);
    browser.pump(0.0);
    (browser, bus)
}

fn asking(request: serde_json::Value) -> Vec<u8> {
    request.to_string().into_bytes()
}

#[test]
fn an_ask_on_the_commands_topic_plays_the_work_the_block_names() {
    let (browser, bus) = taking(asking(serde_json::json!({
        "library": SERIALS,
        "selection": {"episode": {"series": LAST_SERIAL, "season": 1, "episode": 2}},
        "via": {"library": ORDERS, "id": RUN, "position": 1},
    })));

    assert_eq!(
        browser.source.chosen,
        Some((
            SERIALS.to_string(),
            Selection::Episode {
                series: LAST_SERIAL.into(),
                season: 1,
                episode: 2,
            }
        ))
    );
    let request = published(&bus);
    assert_eq!(request["start"], "900");
    assert_eq!(
        request["next"],
        serde_json::json!({
            "library": SERIALS,
            "reason": "Next in The Run · S01",
            "title": "E03 · Segment 3",
            "detail": "Last Serial · S01 · E03 · 46m",
            "art": "s1e3.jpg",
            "request": {
                "library": SERIALS,
                "selection": {"episode": {
                    "series": LAST_SERIAL, "season": 1, "episode": 3,
                }},
                "via": {"library": ORDERS, "id": RUN, "position": 1},
            },
        })
    );
}

#[test]
fn an_ask_for_a_film_reads_its_own_set_and_its_own_position() {
    let (_browser, bus) = taking(asking(serde_json::json!({
        "library": "screening/films",
        "selection": {"movie": {"id": "movies:3"}},
    })));

    let request = published(&bus);
    assert_eq!(request["start"], "600");
    assert_eq!(
        request["next"],
        serde_json::json!({
            "library": "screening/films",
            "reason": "Next in The Entries",
            "title": "Entry 4",
            "detail": "1980 · 1h 30m · PG",
            "request": {
                "library": "screening/films",
                "selection": {"movie": {"id": "movies:4"}},
            },
        })
    );
}

// The block names an episode no play of these people reached, so the play
// begins at the start of it.
#[test]
fn an_ask_for_a_work_they_have_not_started_names_no_second() {
    let (_browser, bus) = taking(asking(serde_json::json!({
        "library": SERIALS,
        "selection": {"episode": {"series": SERIAL, "season": 2, "episode": 1}},
    })));

    let request = published(&bus);
    assert_eq!(request.get("start"), None);
    assert_eq!(request["next"]["title"], "E02 · Segment 2");
}

#[test]
fn an_ask_this_browser_did_not_write_starts_nothing() {
    let (browser, bus) = taking(b"take the offer".to_vec());

    assert_eq!(browser.source.chosen, None);
    assert!(published_nothing(&bus));
}
