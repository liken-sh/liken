// The browser through its two routes: the keyboard names a key, the bus
// delivers a press or a moment, and both reach one handler.
//
// This file holds what every group of tests under it builds from: the
// catalog they read, the store they draw from, and the bus they fold.

mod art_counts;
mod audience;
mod banner;
mod clock;
mod home;
mod keys;
mod loading;
mod moments;
mod pages;
mod paging;
mod plays;
mod prefetch;
mod rail;
mod reader;
mod resume;
mod search;
mod strip;
mod upnext;
mod volume;
mod walls;

use std::collections::HashMap;
use std::sync::Arc;
use std::sync::Mutex;
use std::sync::atomic::{AtomicUsize, Ordering};

use super::*;
use crate::art::{ArtCounts, Image};
use crate::catalog::draw::Date;
use crate::catalog::pool::Candidate;
use crate::catalog::{
    Answer, Credit, CreditSlot, Credits, Entry, Episode, FileFacts, Fold, Franchise,
    FranchiseEntry, GenreEntry, GenreSort, Held, Identity, InSeries, LibraryEntry, Membership,
    MovieDetails, MovieSet, Order, Person, PlayItem, Played, Presentation, Progress, Query, Resume,
    SeriesDetails, Slot, Title,
};
use crate::screens::home::Home;
use crate::screens::movie::Focus;
use crate::screens::series::Focus as SeriesFocus;
use crate::screens::wall::Wall;
use crate::views::wall;

// The topic the operator names on a screen pod, so a test reads what the
// browser published on the topic a cluster would give it.
const PLAY_TOPIC: &str = "liken/library/players/house/den-tv/play";

// The second the clock's own frame falls on. The minute turns on the
// wall clock, and no test may depend on it, so a case that measures a
// schedule states the second itself, past every other second it names.
const MINUTE: f64 = 600.0;

// The set the first movies of the fake library belong to, and how many
// of them: four, because a drawn strip of fewer holds nothing.
const SET: &str = "set:films";
const IN_SET: usize = 4;

#[derive(Default, Clone)]
struct Fake {
    movies: usize,
    changed: bool,
    woken: bool,
    calls: Vec<&'static str>,
    // The play list this source answers, and the last choice it was asked
    // to resolve.
    items: Vec<PlayItem>,
    chosen: Option<(String, Selection)>,
    // The work this source names every choice by, so a test reads the
    // identity the browser published rather than the one a catalog holds.
    identity: Identity,
    // Whether the series library holds any episode at all. A library the
    // scanner has not reached looks like that.
    empty: bool,
    // Whether the movies carry a trailer file, and whether they carry a
    // set and the art a page draws over.
    trailers: bool,
    sets: bool,
    // Whether the titles belong to franchise orders, so `franchises_of`
    // answers the three orders below.
    orders: bool,
    // Whether the movies credit anybody, so a page carries the
    // stripes a walk to a person's page starts from.
    people: bool,
    // Whether the recency queries answer anything: an airing episode, a
    // series with a back catalog, and a movie, newest first. Without it
    // the home page is the libraries strip alone.
    recent: bool,
    // Whether the pool holds candidates: two genres, the one person, and
    // the one set, so every draw takes all four and only their order is the
    // date's.
    pool: bool,
    // Whether no title carries a backdrop. A bare catalog exists because
    // the banner holds nothing without backdrops.
    bare: bool,
    // Whether the catalog answers a second source of its own, so the home
    // page reads on a thread.
    threaded: bool,
    // How many home pages this catalog read, which every clone of it
    // counts into.
    reads: Arc<AtomicUsize>,
    // What the audience is in the middle of, which the continue-watching
    // row reads.
    continues: Vec<Resume>,
    // Where the audience reached in one work, by the library and the id
    // that name it, and in each episode of one series. The two pages read
    // them.
    reached: HashMap<(String, String), Progress>,
    episodes_reached: Vec<Progress>,
    // The people the last of the three progress reads named.
    watching: Vec<String>,
}

// The library the fake serial is in, and the serial's id.
const SERIALS: &str = "screening/serials";
const SERIAL: &str = "series:1";

// Two more shows of that library, beside the serial the recency rows hold,
// so a resume and a franchise order can name a show no other row draws.
const OTHER_SERIAL: &str = "series:2";
const LAST_SERIAL: &str = "series:3";

// The one person the fake library credits, and the directory their
// entry sits in.
const PLAYER: &str = "A Player";
// The player's strip heading: the name and, after the dot, the roles
// across the works the fake credits them with.
const PLAYER_STRIP: &str = "A Player · director";
const ENTRY: &str = ".contributors/A Player";

// The catalog's date this many days before today. A date is named by
// its distance from today because the released strip keeps a window of
// today, and no test may depend on the wall clock.
fn days_ago(days: i64) -> String {
    Date::from_seconds(Date::today().seconds() - days * 86_400).iso()
}

// The serial as a wall answers it. The recency queries and the genre
// query both hold it, so a genre wall counts a kind other than a movie.
fn serial() -> Slot {
    Slot {
        library: SERIALS.into(),
        kind: "series".into(),
        id: SERIAL.into(),
        title: "The Serial".into(),
        released: days_ago(14),
        art: "serial.jpg".into(),
        ..Slot::default()
    }
}

// The number at the end of a fake id. It places a movie in the library's
// one set.
fn numbered(id: &str) -> usize {
    id.rsplit(':')
        .next()
        .and_then(|tail| tail.parse().ok())
        .unwrap_or(0)
}

impl Fake {
    // What the recency queries answer, by the fold: every episode folds
    // to the serial under Titles, and the airing episode stands alone
    // under the other two.
    fn recency(&self, fold: Fold) -> Answer {
        if !self.recent {
            return Answer::default();
        }
        let episode = Slot {
            library: SERIALS.into(),
            kind: "episodes".into(),
            id: "episode:1:2".into(),
            title: "Segment 2".into(),
            released: days_ago(2),
            art: "s1e2.jpg".into(),
            duration: 2_760,
            episode: Some(InSeries {
                series: SERIAL.into(),
                name: "The Serial".into(),
                season: 1,
                episode: 2,
            }),
            ..Slot::default()
        };
        let movie = Slot::of("screening/films", "movies", self.member(1));
        Answer {
            name: String::new(),
            slots: match fold {
                Fold::Titles => vec![serial(), movie],
                _ => vec![episode, serial(), movie],
            },
        }
    }

    // The backdrop of one title's page, empty on a bare catalog.
    fn backdrop(&self, id: &str) -> String {
        match self.bare {
            true => String::new(),
            false => format!("{id}.backdrop.jpg"),
        }
    }

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

// One held member of an order, as the strip read answers it.
fn order_member(position: i64, library: &str, id: &str, kind: &str, title: &str) -> Entry {
    Entry {
        position,
        kind: match kind {
            "movies" => crate::catalog::franchise::MOVIE.to_string(),
            _ => crate::catalog::franchise::SERIES.to_string(),
        },
        alias: format!("{kind}:tmdb:{position}"),
        title: title.to_string(),
        held: Some(Held {
            library: library.to_string(),
            id: id.to_string(),
            kind: kind.to_string(),
            title: title.to_string(),
            released: "1980".into(),
            art: format!("{id}.jpg"),
            ..Held::default()
        }),
        ..Entry::default()
    }
}

// The three orders the fake titles belong to. The Cycle ends on the film
// after the finished one. The Saga runs from that film through the last
// serial to the film the audience is in the middle of. The Run puts a film
// after the last serial, and cuts that serial to its first season.
fn orders() -> Vec<Membership> {
    let films = "screening/films";
    let order = |id: &str, title: &str, members: Vec<Entry>| Membership {
        library: "screening/orders".to_string(),
        id: id.to_string(),
        title: title.to_string(),
        movies: members.len() as i64,
        series: 0,
        members,
    };
    vec![
        order(
            "franchise:name:the-cycle",
            "The Cycle",
            vec![
                order_member(1, films, "movies:2", "movies", "Entry 2"),
                order_member(2, films, "movies:3", "movies", "Entry 3"),
            ],
        ),
        order(
            "franchise:name:the-saga",
            "The Saga",
            vec![
                order_member(1, films, "movies:2", "movies", "Entry 2"),
                order_member(2, SERIALS, LAST_SERIAL, "series", "Last Serial"),
                order_member(3, films, "movies:1", "movies", "Entry 1"),
            ],
        ),
        order(
            "franchise:name:the-run",
            "The Run",
            vec![
                Entry {
                    runs: vec![(1, 0)],
                    ..order_member(1, SERIALS, LAST_SERIAL, "series", "Last Serial")
                },
                order_member(2, films, "movies:4", "movies", "Entry 4"),
            ],
        ),
    ]
}

impl Source for Fake {
    // Two franchises, so the home page's franchises strip draws one with art
    // and one with none. The one with no art draws as the tile of words every
    // slot with no art draws.
    fn franchises(&mut self) -> Vec<FranchiseEntry> {
        self.calls.push("franchises");
        vec![
            FranchiseEntry {
                library: "screening/orders".into(),
                id: "franchise:name:the-cycle".into(),
                title: "The Cycle".into(),
                art: "cycle.jpg".into(),
                art_library: "screening/films".into(),
                slug: "the-cycle".into(),
                movies: 3,
                series: 1,
            },
            FranchiseEntry {
                library: "screening/orders".into(),
                id: "franchise:name:the-saga".into(),
                title: "The Saga".into(),
                art: String::new(),
                art_library: String::new(),
                slug: "the-saga".into(),
                movies: 1,
                series: 0,
            },
        ]
    }

    // The orders the fake titles belong to, cut to the ones that hold the
    // item asked for, as the catalog's own read answers them.
    fn franchises_of(&mut self, library: &str, id: &str) -> Vec<Membership> {
        if !self.orders {
            return Vec::new();
        }
        orders()
            .into_iter()
            .filter(|order| {
                order.members.iter().any(|entry| {
                    entry
                        .held
                        .as_ref()
                        .is_some_and(|held| held.library == library && held.id == id)
                })
            })
            .collect()
    }

    // The page of an order is that order, so a page and the row's own walk
    // of it read one list of members. The Saga has no page, so a strip can
    // name a franchise the catalog no longer holds.
    fn franchise(&mut self, library: &str, id: &str) -> Option<Franchise> {
        if id == "franchise:name:the-saga" {
            return None;
        }
        let order = orders().into_iter().find(|order| order.id == id)?;
        Some(Franchise {
            library: library.to_string(),
            id: id.to_string(),
            title: order.title,
            entries: order.members,
            ..Franchise::default()
        })
    }

    fn libraries(&mut self) -> Vec<LibraryEntry> {
        self.calls.push("libraries");
        vec![
            LibraryEntry {
                library: "screening/films".into(),
                kind: "movies".into(),
                items: self.movies as u64,
                art: vec!["1.jpg".into()],
            },
            LibraryEntry {
                library: SERIALS.into(),
                kind: "series".into(),
                items: 2,
                art: vec!["serial.jpg".into()],
            },
        ]
    }

    fn genres(&mut self) -> Vec<GenreEntry> {
        self.calls.push("genres");
        vec![
            GenreEntry {
                name: "Drama".into(),
                titles: 2,
                art: vec![("screening/films".into(), "1.jpg".into())],
            },
            GenreEntry {
                name: "Western".into(),
                titles: 1,
                art: vec![(SERIALS.into(), "serial.jpg".into())],
            },
        ]
    }

    fn wall(&mut self, query: &Query) -> Answer {
        self.calls.push("wall");
        match query {
            Query::Library { library, .. } => {
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
                            duration: 5_400,
                            rating: "PG".into(),
                            parts: "Director".into(),
                            ..Slot::default()
                        })
                        .collect(),
                }
            }
            Query::Set { id, .. } => match self.set("screening/films", id) {
                Some(set) => Answer {
                    name: set.title,
                    slots: set
                        .members
                        .into_iter()
                        .map(|member| Slot::of("screening/films", "movies", member))
                        .collect(),
                },
                None => Answer::default(),
            },
            Query::Franchise { library, id } => {
                crate::catalog::franchise::answer(self.franchise(library, id))
            }
            Query::Released { fold, .. } | Query::Added { fold, .. } => self.recency(*fold),
            Query::Genre { name, .. } => Answer {
                name: name.clone(),
                slots: (1..=self.movies)
                    .map(|number| Slot::of("screening/films", "movies", self.member(number)))
                    .chain(std::iter::once(serial()))
                    .collect(),
            },
            // The fixture matches the typed text against the film titles
            // alone. That is enough to test that a search result is a wall
            // like any other; the tests of the search itself run over the
            // sample source.
            Query::Search { text } => Answer {
                name: String::new(),
                slots: (1..=self.movies)
                    .map(|number| Slot::of("screening/films", "movies", self.member(number)))
                    .filter(|slot| slot.title.to_lowercase().contains(&text.to_lowercase()))
                    .collect(),
            },
        }
    }

    fn pool(&mut self) -> Vec<Candidate> {
        self.calls.push("pool");
        self.reads.fetch_add(1, Ordering::SeqCst);
        if !self.pool {
            return Vec::new();
        }
        vec![
            Candidate {
                query: Query::Genre {
                    name: "Western".into(),
                    order: Order::Released,
                    sort: GenreSort::default(),
                },
                name: "Western".into(),
                weight: 200,
            },
            Candidate {
                query: Query::Genre {
                    name: "Drama".into(),
                    order: Order::Released,
                    sort: GenreSort::default(),
                },
                name: "Drama".into(),
                weight: 40,
            },
            Candidate {
                query: Query::Person {
                    library: "screening/films".into(),
                    path: ENTRY.into(),
                },
                name: PLAYER.into(),
                weight: 4,
            },
            Candidate {
                query: Query::Set {
                    library: "screening/films".into(),
                    id: SET.into(),
                },
                name: "The Entries".into(),
                weight: 3,
            },
        ]
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
            genres: vec!["Adventure".into(), "Mystery".into()],
            plot: "A plot.".into(),
            ratings: vec![("imdb".into(), 8.1), ("themoviedb".into(), 7.0)],
            seasons: 2,
            backdrop: self.backdrop(id),
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
            genres: vec!["Drama".into(), "Western".into()],
            plot: "A plot.".into(),
            ratings: vec![("imdb".into(), 6.5), ("tomatometerallcritics".into(), 83.0)],
            cast: vec![Credit {
                name: "A Player".into(),
                role: "The Part".into(),
            }],
            set_id: if self.sets && number <= IN_SET {
                SET.into()
            } else {
                String::new()
            },
            backdrop: self.backdrop(id),
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
            members: (1..=IN_SET.min(self.movies))
                .map(|number| self.member(number))
                .collect(),
        })
    }

    fn play(&mut self, library: &str, selection: &Selection) -> Vec<PlayItem> {
        self.calls.push("play");
        self.chosen = Some((library.to_string(), selection.clone()));
        self.items.clone()
    }

    fn identity(&mut self, _library: &str, _selection: &Selection) -> Identity {
        self.identity.clone()
    }

    fn continue_watching(&mut self, people: &[String]) -> Vec<Resume> {
        self.calls.push("continue_watching");
        self.watching = people.to_vec();
        self.continues.clone()
    }

    // The plays of one library, out of the same rows the work read answers
    // from, so one fixture field feeds a page and a wall alike.
    fn progress_by_item(&mut self, library: &str, people: &[String]) -> HashMap<String, Played> {
        self.watching = people.to_vec();
        self.reached
            .iter()
            .filter(|((held, _), _)| held == library)
            .map(|((_, id), progress)| (id.clone(), progress.played()))
            .collect()
    }

    fn episode_progress(
        &mut self,
        _library: &str,
        _series: &str,
        people: &[String],
    ) -> Vec<Progress> {
        self.calls.push("episode_progress");
        self.watching = people.to_vec();
        self.episodes_reached.clone()
    }

    // The plays of one work: the rows the continue-watching read answers,
    // the play of the work the `reached` map holds, and the episode rows of a
    // series. The last two are always the audience's own.
    fn plays_of(&mut self, library: &str, id: &str, people: &[String]) -> Vec<Resume> {
        self.calls.push("plays_of");
        self.watching = people.to_vec();
        let mut plays: Vec<Resume> = self
            .continues
            .iter()
            .filter(|play| play.library == library && play.id == id)
            .cloned()
            .collect();
        plays.extend(
            self.reached
                .get(&(library.to_string(), id.to_string()))
                .map(|progress| Resume {
                    library: library.to_string(),
                    kind: "movie".into(),
                    id: id.to_string(),
                    progress: progress.clone(),
                    exact: true,
                    ..Resume::default()
                }),
        );
        plays.extend(self.episodes_reached.iter().map(|progress| Resume {
            library: library.to_string(),
            kind: "series".into(),
            id: id.to_string(),
            progress: progress.clone(),
            exact: true,
            ..Resume::default()
        }));
        plays
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
            headshot: true,
            headshot_library: library.to_string(),
            headshot_path: path.to_string(),
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

    fn reader(&mut self) -> Option<Box<dyn Source + Send>> {
        self.threaded
            .then(|| Box::new(self.clone()) as Box<dyn Source + Send>)
    }
}

#[derive(Default)]
struct NoArt {
    delivers: bool,
    counts: ArtCounts,
    // Every ask the views and the prefetch made: the library, the art,
    // and the size they asked at.
    asked: Vec<(String, String, u32, u32)>,
}

impl Art for NoArt {
    fn covered(&mut self, library: &str, art: &str, width: u32, height: u32) -> Option<Image> {
        self.asked
            .push((library.to_string(), art.to_string(), width, height));
        None
    }

    fn delivered(&mut self) -> bool {
        std::mem::take(&mut self.delivers)
    }

    fn counts(&self) -> ArtCounts {
        self.counts
    }
}

fn browser(movies: usize) -> Browser<Fake, NoArt> {
    Browser::new(
        Fake {
            movies,
            ..Fake::default()
        },
        NoArt::default(),
    )
}

// The home page the browser is showing.
fn showing_home(browser: &Browser<Fake, NoArt>) -> &Home {
    match browser.top() {
        screens::Screen::Home(home) => home,
        _ => panic!("the browser is not showing the home page"),
    }
}

// The franchise page the browser is showing, so a test reads the screen a
// press on the franchises strip opened.
fn showing_franchise(browser: &Browser<Fake, NoArt>) -> &crate::screens::franchise::Franchise {
    match browser.top() {
        screens::Screen::Franchise(page) => page,
        _ => panic!("the browser is not showing a franchise page"),
    }
}

// The wall the browser is showing, so a test reads the screen it is on.
fn showing_wall(browser: &Browser<Fake, NoArt>) -> &Wall {
    match browser.top() {
        screens::Screen::Wall(wall) => wall,
        _ => panic!("the browser is not showing a wall"),
    }
}

// One strip of the home page by its row index, which a test reads a
// strip's focus through.
fn strip_at(browser: &Browser<Fake, NoArt>, index: usize) -> &crate::screens::home::Strip {
    showing_home(browser).blocks[index]
        .strip()
        .expect("the row is a strip")
}

// The movie page the browser is showing.
fn showing_page(browser: &Browser<Fake, NoArt>) -> &screens::movie::Movie {
    match browser.top() {
        screens::Screen::Movie(page) => page,
        _ => panic!("the browser is not showing a page"),
    }
}

// The person's page the browser is showing.
fn showing_person(browser: &Browser<Fake, NoArt>) -> &screens::person::Person {
    match browser.top() {
        screens::Screen::Person(page) => page,
        _ => panic!("the browser is not showing a person's page"),
    }
}

// The series page the browser is showing.
fn showing_series(browser: &Browser<Fake, NoArt>) -> &screens::series::Series {
    match browser.top() {
        screens::Screen::Series(page) => page,
        _ => panic!("the browser is not showing a series page"),
    }
}
// A browser on a wall with sets, after the frame the first press asked
// for, so a test starts at the beginning of a rest.
fn resting(movies: usize) -> Browser<Fake, NoArt> {
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

fn on_bus(movies: usize, moments: Vec<Moment>) -> (Browser<Fake, NoArt>, FakeBus) {
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
fn playing(items: Vec<PlayItem>) -> (Browser<Fake, NoArt>, FakeBus) {
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
