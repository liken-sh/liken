// The browser through its two routes: the keyboard names a key, the bus
// delivers a press or a moment, and both reach one handler.
//
// This file holds what every group of tests under it builds from: the
// catalog they read, the store they draw from, and the bus they fold.

mod loading;
mod moments;
mod paging;
mod plays;
mod prefetch;
mod volume;

use std::sync::Arc;
use std::sync::Mutex;
use std::sync::atomic::{AtomicUsize, Ordering};

use super::*;
use crate::catalog::Answer;
use crate::catalog::{
    Credit, CreditSlot, Credits, Episode, FileFacts, LibraryEntry, MovieDetails, MovieSet, Person,
    PlayItem, Presentation, Query, SeriesDetails, Slot, Title,
};
use crate::posters::Art;
use crate::screens::movie::Focus;
use crate::screens::series::Focus as SeriesFocus;
use crate::screens::wall::Wall;
use crate::views::{band, wall};

// The topic the operator names on a screen pod, so a test reads what the
// browser published on the topic a cluster would give it.
const PLAY_TOPIC: &str = "liken/library/players/house/den-tv/play";

// The set the first three movies of the fake library belong to.
const SET: &str = "set:films";

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
    // Whether the series library holds any episode at all. A library the
    // scanner has not reached looks like that.
    empty: bool,
    // Whether the movies carry a trailer file, and whether they carry a
    // set and the art a page draws over.
    trailers: bool,
    sets: bool,
    // Whether the movies credit anybody, so a page carries the
    // stripes a walk to a person's page starts from.
    people: bool,
}

// The one person the fake library credits, and the directory their
// entry sits in.
const PLAYER: &str = "A Player";
const ENTRY: &str = ".contributors/A Player";

// The number at the end of a fake id. It places a movie in the library's
// one set.
fn numbered(id: &str) -> usize {
    id.rsplit(':')
        .next()
        .and_then(|tail| tail.parse().ok())
        .unwrap_or(0)
}

impl Fake {
    fn member(&self, number: usize) -> Title {
        Title {
            id: format!("movies:{number}"),
            title: format!("Entry {number}"),
            released: "1980".into(),
            duration: 5_400,
            rating: "PG".into(),
            ..Title::default()
        }
    }
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

    fn wall(&mut self, query: &Query) -> Answer {
        self.calls.push("wall");
        match query {
            Query::Library { library } => {
                let kind = match library.as_str() {
                    "screening/films" => "movies",
                    _ => "series",
                };
                let count = if kind == "movies" { self.movies } else { 2 };
                Answer {
                    name: library.rsplit('/').next().unwrap_or_default().to_string(),
                    slots: (1..=count)
                        .map(|number| {
                            Slot::of(
                                library,
                                kind,
                                Title {
                                    id: format!("{kind}:{number}"),
                                    title: format!("Entry {number}"),
                                    released: "1980".into(),
                                    ..Title::default()
                                },
                            )
                        })
                        .collect(),
                }
            }
            Query::Person { path, .. } => {
                if !self.people || path != ENTRY {
                    return Answer::default();
                }
                Answer {
                    name: PLAYER.into(),
                    slots: (1..=self.movies)
                        .map(|number| Slot {
                            library: "screening/films".into(),
                            kind: "movies".into(),
                            id: format!("movies:{number}"),
                            title: format!("Entry {number}"),
                            released: "1980".into(),
                            art: format!("{number}.jpg"),
                            parts: "Director".into(),
                            ..Slot::default()
                        })
                        .collect(),
                }
            }
            Query::Set { .. } => Answer::default(),
        }
    }

    fn series(&mut self, _library: &str, id: &str) -> Option<SeriesDetails> {
        self.calls.push("series");
        if !id.starts_with("series:") {
            return None;
        }
        Some(SeriesDetails {
            title: "The Serial".into(),
            released: "1980".into(),
            rating: "TV-14".into(),
            plot: "A plot.".into(),
            seasons: 2,
            backdrop: format!("{id}.backdrop.jpg"),
            ..SeriesDetails::default()
        })
    }

    fn episodes(&mut self, _library: &str, _series: &str) -> Vec<Episode> {
        self.calls.push("episodes");
        if self.empty {
            return Vec::new();
        }
        (1..=2)
            .flat_map(|season| {
                (1..=4).map(move |episode| Episode {
                    id: format!("episode:{season}:{episode}"),
                    season,
                    episode,
                    title: format!("Segment {episode}"),
                    released: format!("{}", 1979 + season),
                    duration: 2_760,
                    plot: format!("The plot of S{season} E{episode}."),
                    art: format!("s{season}e{episode}.jpg"),
                })
            })
            .collect()
    }

    fn movie(&mut self, _library: &str, id: &str) -> Option<MovieDetails> {
        self.calls.push("movie");
        let number = numbered(id);
        if !id.starts_with("movies:") || number == 0 || number > self.movies {
            return None;
        }
        Some(MovieDetails {
            title: format!("Entry {number}"),
            released: "1980".into(),
            duration: 5_400,
            rating: "PG".into(),
            plot: "A plot.".into(),
            cast: vec![Credit {
                name: "A Player".into(),
                role: "The Part".into(),
            }],
            set_id: if self.sets && number <= 3 {
                SET.into()
            } else {
                String::new()
            },
            backdrop: format!("{id}.backdrop.jpg"),
            trailer: if self.trailers {
                format!("{id}.trailer.mkv")
            } else {
                String::new()
            },
            ..MovieDetails::default()
        })
    }

    fn set(&mut self, _library: &str, id: &str) -> Option<MovieSet> {
        self.calls.push("set");
        if id != SET {
            return None;
        }
        Some(MovieSet {
            title: "The Entries".into(),
            members: (1..=3.min(self.movies))
                .map(|number| self.member(number))
                .collect(),
        })
    }

    fn play(&mut self, library: &str, selection: &Selection) -> Vec<PlayItem> {
        self.calls.push("play");
        self.chosen = Some((library.to_string(), selection.clone()));
        self.items.clone()
    }

    fn credits(&mut self, _library: &str, _id: &str) -> Credits {
        self.calls.push("credits");
        if !self.people {
            return Credits::default();
        }
        Credits {
            directors: vec![CreditSlot {
                name: PLAYER.into(),
                role: String::new(),
                contributor: ENTRY.into(),
                headshot: true,
            }],
            ..Credits::default()
        }
    }

    fn person(&mut self, library: &str, path: &str) -> Option<Person> {
        self.calls.push("person");
        if !self.people || path != ENTRY {
            return None;
        }
        Some(Person {
            library: library.to_string(),
            path: path.to_string(),
            name: PLAYER.into(),
            ..Person::default()
        })
    }

    fn files(&mut self, _library: &str, _item: &str) -> Vec<FileFacts> {
        Vec::new()
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
    // Every ask the views and the prefetch made: the library, the art,
    // and the size they asked at.
    asked: Vec<(String, String, u32, u32)>,
}

impl Posters for NoPosters {
    fn poster(&mut self, library: &str, art: &str, width: u32, height: u32) -> Option<Art> {
        self.asked
            .push((library.to_string(), art.to_string(), width, height));
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

// The wall the browser is showing, so a test reads the screen it is on.
fn showing_wall(browser: &Browser<Fake, NoPosters>) -> &Wall {
    match browser.top() {
        screens::Screen::Wall(wall) => wall,
        _ => panic!("the browser is not showing a wall"),
    }
}

// The movie page the browser is showing.
fn showing_page(browser: &Browser<Fake, NoPosters>) -> &screens::movie::Movie {
    match browser.top() {
        screens::Screen::Movie(page) => page,
        _ => panic!("the browser is not showing a page"),
    }
}

// The person's page the browser is showing.
fn showing_person(browser: &Browser<Fake, NoPosters>) -> &screens::person::Person {
    match browser.top() {
        screens::Screen::Person(page) => page,
        _ => panic!("the browser is not showing a person's page"),
    }
}

// The series page the browser is showing.
fn showing_series(browser: &Browser<Fake, NoPosters>) -> &screens::series::Series {
    match browser.top() {
        screens::Screen::Series(page) => page,
        _ => panic!("the browser is not showing a series page"),
    }
}
// A browser on a wall with sets, after the frame the first press asked
// for, so a test starts at the beginning of a rest.
fn resting(movies: usize) -> Browser<Fake, NoPosters> {
    let mut browser = browser(movies);
    browser.source.sets = true;
    browser.tick(0.0);
    browser.key("enter");
    browser
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
