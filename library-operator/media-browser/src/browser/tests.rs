// The browser through its two routes: the keyboard names a key, the bus
// delivers a press or a moment, and both reach one handler.

use std::sync::Arc;
use std::sync::Mutex;
use std::sync::atomic::{AtomicUsize, Ordering};

use iced_widget::image::Handle;

use super::*;
use crate::bus::screen::Event;
use crate::catalog::{EpisodeRow, LibraryEntry, Title};

#[derive(Default)]
struct Fake {
    movies: usize,
    changed: bool,
    woken: bool,
    calls: Vec<&'static str>,
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
            episode: 1,
            art: String::new(),
        }]
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
    assert_eq!(browser.top().rows[0].detail, "S2 E1");
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

// The bus a test folds through: the messages a broker would deliver, and
// the requests the browser publishes, with no socket under either.
#[derive(Default, Clone)]
struct FakeBus {
    inbound: Arc<Mutex<Vec<Message>>>,
    sleeps: Arc<AtomicUsize>,
    woken: Arc<AtomicUsize>,
}

impl Bus for FakeBus {
    fn drain(&self) -> Vec<Message> {
        std::mem::take(&mut self.inbound.lock().expect("no test panics with the lock"))
    }

    fn request_sleep(&self) {
        self.sleeps.fetch_add(1, Ordering::SeqCst);
    }

    fn wake_on_delivery(&self, _wake: Waker) {
        self.woken.fetch_add(1, Ordering::SeqCst);
    }
}

fn on_bus(movies: usize, messages: Vec<Message>) -> (Browser<Fake, NoPosters>, FakeBus) {
    let bus = FakeBus::default();
    *bus.inbound.lock().expect("no test panics with the lock") = messages;
    (
        browser(movies).with_bus(Some(Box::new(bus.clone()))),
        bus.clone(),
    )
}

#[test]
fn a_press_on_the_bus_moves_focus_like_the_keyboard() {
    let (mut browser, _bus) = on_bus(3, vec![Message::Press("down")]);

    assert!(browser.pump(1.0));

    assert_eq!(browser.top().focus, 1);
}

#[test]
fn select_on_the_bus_descends() {
    let (mut browser, _bus) = on_bus(3, vec![Message::Press("enter")]);

    assert!(browser.pump(1.0));

    assert_eq!(browser.top().shape, Shape::Wall);
}

#[test]
fn an_empty_bus_folds_nothing() {
    let (mut browser, _bus) = on_bus(3, Vec::new());

    assert!(!browser.pump(1.0));
}

#[test]
fn the_shade_covers_the_browser_and_lifts_it() {
    let (mut browser, _bus) = on_bus(3, vec![Message::Screen(Event::Sleep)]);
    browser.pump(1.0);
    assert!(browser.asleep());

    *_bus.inbound.lock().expect("no test panics with the lock") =
        vec![Message::Screen(Event::Wake)];
    browser.pump(2.0);

    assert!(!browser.asleep());
}

#[test]
fn a_press_that_arrives_asleep_keeps_the_focus() {
    let (mut browser, _bus) = on_bus(
        3,
        vec![Message::Screen(Event::Sleep), Message::Press("down")],
    );

    browser.pump(1.0);

    assert!(browser.asleep());
    assert_eq!(browser.top().focus, 1);
}

#[test]
fn an_asleep_browser_draws_a_black_frame() {
    let (mut browser, _bus) = on_bus(3, vec![Message::Screen(Event::Sleep)]);
    browser.pump(1.0);

    assert!(browser.asleep());
    assert_eq!(browser.background(), Color::BLACK);
    let _ = browser.view();
}

#[test]
fn a_present_asks_the_harness_for_a_fresh_surface() {
    let (mut browser, _bus) = on_bus(3, vec![Message::Screen(Event::Present)]);

    browser.pump(1.0);

    assert!(browser.surface_due());
    assert!(!browser.surface_due());
}

#[test]
fn a_focus_changes_nothing() {
    let (mut browser, _bus) = on_bus(3, vec![Message::Screen(Event::Focus { remote: 0 })]);

    assert!(browser.pump(1.0));

    assert_eq!(browser.top().focus, 0);
    assert!(!browser.asleep());
    assert!(!browser.surface_due());
}

#[test]
fn back_at_the_libraries_asks_the_command_pod_for_the_shade() {
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
