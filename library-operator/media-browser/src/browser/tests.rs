// The browser through its two routes: the keyboard names a key, the bus
// delivers a press or a moment, and both reach one handler.

use std::sync::Arc;
use std::sync::Mutex;
use std::sync::atomic::{AtomicUsize, Ordering};

use iced_widget::image::Handle;

use super::*;
use crate::catalog::{EpisodeRow, LibraryEntry, PlayItem, Presentation, Title};

// The topic the operator names on a screen pod, so a test reads what the
// browser published on the topic a cluster would give it.
const PLAY_TOPIC: &str = "liken/library/players/house/den-tv/play";

#[derive(Default)]
struct Fake {
    movies: usize,
    changed: bool,
    woken: bool,
    calls: Vec<&'static str>,
    // The play list this source answers, and the last choice it was asked
    // to resolve.
    items: Vec<PlayItem>,
    chosen: Option<(String, Selection)>,
}

impl Source for Fake {
    fn libraries(&mut self) -> Vec<LibraryEntry> {
        self.calls.push("libraries");
        vec![
            LibraryEntry {
                library: "screening/films".into(),
                kind: "movies".into(),
                items: self.movies as u64,
            },
            LibraryEntry {
                library: "screening/serials".into(),
                kind: "series".into(),
                items: 2,
            },
        ]
    }

    fn titles(&mut self, _library: &str, kind: &str) -> Vec<Title> {
        self.calls.push("titles");
        let count = if kind == "movies" { self.movies } else { 2 };
        (1..=count)
            .map(|number| Title {
                id: format!("{kind}:{number}"),
                title: format!("Entry {number}"),
                released: "1980".into(),
                art: String::new(),
            })
            .collect()
    }

    fn seasons(&mut self, _library: &str, _series: &str) -> Vec<i64> {
        self.calls.push("seasons");
        vec![1, 2]
    }

    fn episodes(&mut self, _library: &str, _series: &str, season: i64) -> Vec<EpisodeRow> {
        self.calls.push("episodes");
        vec![EpisodeRow {
            id: "e1".into(),
            title: "Segment 1".into(),
            season,
            episode: 4,
            art: String::new(),
        }]
    }

    fn play(&mut self, library: &str, selection: &Selection) -> Vec<PlayItem> {
        self.calls.push("play");
        self.chosen = Some((library.to_string(), selection.clone()));
        self.items.clone()
    }

    fn changed(&mut self) -> bool {
        std::mem::take(&mut self.changed)
    }

    fn wake_by(&mut self, _wake: Waker) {
        self.woken = true;
    }
}

#[derive(Default)]
struct NoPosters {
    delivers: bool,
}

impl Posters for NoPosters {
    fn poster(&mut self, _: &str, _: &str, _: u32, _: u32) -> Option<Handle> {
        None
    }

    fn delivered(&mut self) -> bool {
        std::mem::take(&mut self.delivers)
    }
}

fn browser(movies: usize) -> Browser<Fake, NoPosters> {
    Browser::new(
        Fake {
            movies,
            ..Fake::default()
        },
        NoPosters::default(),
    )
}

#[test]
fn the_first_screen_lists_the_libraries() {
    let browser = browser(3);
    let top = browser.top();
    assert_eq!(top.shape, Shape::List);
    assert_eq!(top.rows[0].name, "films");
    assert_eq!(top.rows[0].detail, "movies · 3");
    assert_eq!(top.rows[1].name, "serials");
    assert_eq!(top.rows[1].detail, "series · 2");
}

#[test]
fn enter_opens_a_movies_wall() {
    let mut browser = browser(3);
    browser.key("enter");
    assert_eq!(browser.top().shape, Shape::Wall);
    assert_eq!(browser.top().rows.len(), 3);
}

#[test]
fn a_movie_is_as_deep_as_its_kind_goes() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.key("enter");
    assert_eq!(browser.stack.len(), 1);
}

#[test]
fn a_series_library_gives_three_lists() {
    let mut browser = browser(3);
    browser.key("down");
    browser.key("enter");
    assert_eq!(browser.top().shape, Shape::List);
    browser.key("enter");
    assert_eq!(browser.top().rows[0].name, "Season 1");
    browser.key("down");
    browser.key("enter");
    assert_eq!(browser.top().rows[0].detail, "S2 E4");
    browser.key("enter");
    assert_eq!(browser.stack.len(), 3);
}

#[test]
fn back_climbs_one_level_and_stops_at_the_libraries() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.key("escape");
    assert!(browser.stack.is_empty());
    browser.key("escape");
    assert!(browser.stack.is_empty());
    assert_eq!(browser.top().fetch, Fetch::Libraries);
}

#[test]
fn backspace_goes_back_too() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.key("backspace");
    assert!(browser.stack.is_empty());
}

#[test]
fn back_rereads_the_level_it_uncovers() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.key("escape");
    let libraries = browser
        .source
        .calls
        .iter()
        .filter(|call| **call == "libraries");
    assert_eq!(libraries.count(), 2);
}

#[test]
fn arrows_move_focus_on_a_list_and_a_wall() {
    let mut browser = browser(20);
    browser.key("down");
    assert_eq!(browser.top().focus, 1);
    browser.key("up");
    browser.key("enter");
    browser.key("right");
    assert_eq!(browser.top().focus, 1);
    browser.key("down");
    assert_eq!(browser.top().focus, 1 + wall::COLUMNS);
}

#[test]
fn an_empty_wall_selects_nothing() {
    let mut browser = browser(0);
    browser.key("enter");
    browser.key("enter");
    assert_eq!(browser.stack.len(), 1);
}

#[test]
fn pump_without_a_change_folds_nothing() {
    let mut browser = browser(3);
    let reads = browser.source.calls.len();
    assert!(!browser.pump(1.0));
    assert_eq!(browser.source.calls.len(), reads);
}

#[test]
fn pump_rereads_what_is_shown() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.source.movies = 2;
    browser.source.changed = true;
    assert!(browser.pump(1.0));
    assert_eq!(browser.top().rows.len(), 2);
    assert_eq!(browser.source.calls.last(), Some(&"titles"));
    let libraries = browser
        .source
        .calls
        .iter()
        .filter(|call| **call == "libraries");
    assert_eq!(libraries.count(), 1);
}

#[test]
fn a_delivered_poster_draws_a_frame_and_reads_no_rows() {
    let mut browser = browser(3);
    browser.key("enter");
    let reads = browser.source.calls.len();
    browser.posters.get_mut().delivers = true;
    assert!(browser.pump(1.0));
    assert_eq!(browser.source.calls.len(), reads);
    assert!(!browser.pump(2.0));
}

#[test]
fn a_reread_that_shrinks_clamps_the_focus() {
    let mut browser = browser(3);
    browser.key("enter");
    browser.key("right");
    browser.key("right");
    browser.source.movies = 1;
    browser.source.changed = true;
    assert!(browser.pump(1.0));
    assert_eq!(browser.top().focus, 0);
}

#[test]
fn the_wake_reaches_the_source() {
    let mut browser = browser(3);
    Screen::wake_by(&mut browser, Arc::new(|| {}));
    assert!(browser.source.woken);
}

#[test]
fn a_still_browser_schedules_no_frame() {
    assert!(browser(3).next_frame(4.2).is_none());
}

#[test]
fn the_browser_draws_on_the_theme_ground() {
    assert_eq!(browser(3).background(), look::BACKGROUND);
}

#[test]
fn the_view_builds_for_both_shapes() {
    let mut browser = browser(3);
    let _ = browser.view();
    browser.key("enter");
    let _ = browser.view();
}

// One message the browser published, as a test reads it back.
type Published = (String, Vec<u8>, bool);

// The bus a test folds through: the moments the crate would deliver, and
// the requests the browser publishes, with no socket under either.
#[derive(Debug, Default, Clone)]
struct FakeBus {
    inbound: Arc<Mutex<Vec<Moment>>>,
    sleeps: Arc<AtomicUsize>,
    woken: Arc<AtomicUsize>,
    published: Arc<Mutex<Vec<Published>>>,
}

impl Bus for FakeBus {
    fn drain(&self) -> Vec<Moment> {
        std::mem::take(&mut self.inbound.lock().expect("no test panics with the lock"))
    }

    fn sleep(&self) {
        self.sleeps.fetch_add(1, Ordering::SeqCst);
    }

    fn publish(&self, topic: &str, payload: Vec<u8>, retained: bool) {
        self.published
            .lock()
            .expect("no test panics with the lock")
            .push((topic.to_string(), payload, retained));
    }

    fn wake_on_delivery(&self, _wake: Waker) {
        self.woken.fetch_add(1, Ordering::SeqCst);
    }
}

fn on_bus(movies: usize, moments: Vec<Moment>) -> (Browser<Fake, NoPosters>, FakeBus) {
    let bus = FakeBus::default();
    *bus.inbound.lock().expect("no test panics with the lock") = moments;
    (
        browser(movies).with_bus(Some(Box::new(bus.clone())), PLAY_TOPIC.into()),
        bus.clone(),
    )
}

// Whether the browser published anything at all.
fn published_nothing(bus: &FakeBus) -> bool {
    bus.published
        .lock()
        .expect("no test panics with the lock")
        .is_empty()
}

#[test]
fn a_press_on_the_bus_moves_focus_like_the_keyboard() {
    let (mut browser, _bus) = on_bus(3, vec![Moment::Press("KEY_DOWN")]);

    assert!(browser.pump(1.0));

    assert_eq!(browser.top().focus, 1);
}

#[test]
fn select_on_the_bus_descends() {
    let (mut browser, _bus) = on_bus(3, vec![Moment::Press("KEY_SELECT")]);

    assert!(browser.pump(1.0));

    assert_eq!(browser.top().shape, Shape::Wall);
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

    assert_eq!(browser.top().focus, 0);
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
    let (mut browser, _bus) = on_bus(3, vec![Moment::Sleep, Moment::Press("KEY_DOWN")]);

    browser.pump(1.0);

    assert!(browser.asleep());
    assert_eq!(browser.top().focus, 1);
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

    assert_eq!(browser.top().focus, 0);
    assert!(!browser.asleep());
    assert!(!browser.surface_due());
}

#[test]
fn back_at_the_libraries_asks_for_the_shade() {
    let (mut browser, bus) = on_bus(3, Vec::new());

    browser.key("escape");

    assert_eq!(bus.sleeps.load(Ordering::SeqCst), 1);
    assert!(!browser.asleep());
    assert_eq!(browser.top().fetch, Fetch::Libraries);
}

#[test]
fn backspace_at_the_libraries_asks_too() {
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
fn back_at_the_libraries_with_no_bus_asks_no_one() {
    let mut browser = browser(3);

    browser.key("escape");

    assert_eq!(browser.top().fetch, Fetch::Libraries);
}

#[test]
fn the_wake_reaches_the_bus() {
    let (mut browser, bus) = on_bus(3, Vec::new());

    Screen::wake_by(&mut browser, Arc::new(|| {}));

    assert_eq!(bus.woken.load(Ordering::SeqCst), 1);
}

// One resolved item, so a test reads what the browser published rather
// than what a catalog would have answered.
fn one_item() -> PlayItem {
    PlayItem {
        path: "Some Film (1999)/Some Film (1999).mkv".into(),
        slug: "some-film-1999".into(),
        presentation: Presentation {
            kind: "video".into(),
            hint: "movie".into(),
            title: "Some Film".into(),
            year: 1999,
            ..Presentation::default()
        },
    }
}

// The browser on a bus, with the play list its source answers.
fn playing(items: Vec<PlayItem>) -> (Browser<Fake, NoPosters>, FakeBus) {
    let (mut browser, bus) = on_bus(3, Vec::new());
    browser.source.items = items;
    (browser, bus)
}

// The one request the browser published, decoded. The topic is the
// operator's own and a request is a moment, so it is not retained.
fn published(bus: &FakeBus) -> serde_json::Value {
    let plays = bus.published.lock().expect("no test panics with the lock");
    assert_eq!(plays.len(), 1);
    let (topic, payload, retained) = &plays[0];
    assert_eq!(topic, PLAY_TOPIC);
    assert!(!retained);
    serde_json::from_slice(payload).expect("the request is JSON")
}

#[test]
fn a_select_on_a_movie_asks_the_operator_to_play_it() {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.key("enter");

    browser.key("enter");

    assert_eq!(
        browser.source.chosen,
        Some((
            "screening/films".to_string(),
            Selection::Movie {
                id: "movies:1".into()
            }
        ))
    );
    assert_eq!(
        published(&bus),
        serde_json::json!({
            "library": "screening/films",
            "slug": "some-film-1999",
            "items": [{
                "path": "Some Film (1999)/Some Film (1999).mkv",
                "presentation": {
                    "type": "video",
                    "hint": "movie",
                    "title": "Some Film",
                    "year": 1999,
                },
            }],
        })
    );
}

#[test]
fn a_select_on_a_movie_opens_no_level() {
    let (mut browser, _bus) = playing(vec![one_item()]);
    browser.key("enter");

    browser.key("enter");

    assert_eq!(browser.stack.len(), 1);
}

#[test]
fn a_select_on_an_episode_names_the_episode_the_row_carries() {
    let (mut browser, _bus) = playing(vec![one_item()]);
    browser.key("down");
    browser.key("enter");
    browser.key("enter");
    browser.key("down");
    browser.key("enter");

    browser.key("enter");

    assert_eq!(
        browser.source.chosen,
        Some((
            "screening/serials".to_string(),
            Selection::Episode {
                series: "series:1".into(),
                season: 2,
                episode: 4,
            }
        ))
    );
}

#[test]
fn a_select_on_an_episode_publishes_the_list_in_the_order_it_resolved() {
    let mut second = one_item();
    second.path = "Later.mkv".into();
    let (mut browser, bus) = playing(vec![one_item(), second]);
    browser.key("down");
    browser.key("enter");
    browser.key("enter");
    browser.key("enter");

    browser.key("enter");

    let request = published(&bus);
    assert_eq!(
        request["items"][0]["path"],
        "Some Film (1999)/Some Film (1999).mkv"
    );
    assert_eq!(request["items"][1]["path"], "Later.mkv");
}

#[test]
fn a_select_on_a_series_descends_and_asks_for_no_play() {
    let (mut browser, bus) = playing(vec![one_item()]);
    browser.key("down");
    browser.key("enter");

    browser.key("enter");

    assert_eq!(browser.stack.len(), 2);
    assert_eq!(browser.source.chosen, None);
    assert!(published_nothing(&bus));
}

#[test]
fn a_title_with_no_file_to_play_publishes_nothing() {
    let (mut browser, bus) = playing(Vec::new());
    browser.key("enter");

    browser.key("enter");

    assert!(browser.source.chosen.is_some());
    assert!(published_nothing(&bus));
}

// An older library operator names no play topic, and the browser then
// browses and asks for nothing.
#[test]
fn a_select_with_no_play_topic_publishes_nothing() {
    let bus = FakeBus::default();
    let mut browser = browser(3).with_bus(Some(Box::new(bus.clone())), String::new());
    browser.source.items = vec![one_item()];
    browser.key("enter");

    browser.key("enter");

    assert!(browser.source.chosen.is_some());
    assert!(published_nothing(&bus));
}

#[test]
fn a_select_on_a_movie_with_no_bus_publishes_nothing() {
    let mut browser = browser(3);
    browser.source.items = vec![one_item()];
    browser.key("enter");

    browser.key("enter");

    assert_eq!(browser.stack.len(), 1);
    assert!(browser.source.chosen.is_some());
}
