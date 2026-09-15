// The franchise page over an invented order: the rows it packs, the
// headings it derives, and where a press takes focus.

use super::*;
use crate::catalog::franchise::{Calendar, Entry, Era, Held, MOVIE, Membership, SERIES};
use crate::catalog::progress::finished;
use crate::catalog::{
    Answer, Change, Credits, Episode, FileFacts, GenreEntry, LibraryEntry, MovieDetails, MovieSet,
    Person, PlayItem, Progress, Query, Resume, Selection, SeriesDetails, pool,
};
use crate::harness::Waker;

/// The `Library` of kind franchises the fake holds.
pub const ORDERS: &str = "screening/orders";

/// The one franchise in it.
pub const CYCLE: &str = "franchise:name:the-cycle";

const FILMS: &str = "screening/films";
const COPPICE: &str = "The Coppice";
const FEN: &str = "The Fen";
const MARSH: &str = "The Marsh";

// A catalog of one order across two libraries, with a calendar, two
// eras that nest, three universes, two entries the story tells at once,
// a banner, a series run, and a gap.
#[derive(Default)]
pub struct Orders {
    /// Whether the `Library` holds the order at all.
    pub empty: bool,
    // Whether the fake answers the split order in place of the cycle: two
    // films and one series cut into two members.
    pub split: bool,
    // The plays the fake's progress store holds.
    pub nights: Vec<Night>,
}

// How long every work of the fake runs, in seconds.
pub const RUNTIME: i64 = 1_000;

// The seasons of the one series the fake's orders hold, and how many
// episodes each one aired.
pub const SEASONS: i64 = 2;
pub const AIRED: i64 = 5;

// The id of that series, and of the two films of the split order.
pub const SHOW: &str = "series:5";
pub const FIRST: &str = "movies:1";
pub const SECOND: &str = "movies:3";

// One play in the fake's store: the work it names, its aired numbers,
// the people as one word of their names, the second it was recorded,
// and how far into the work it reached.
#[derive(Debug, Clone, Copy)]
pub struct Night {
    pub work: &'static str,
    pub numbers: (i64, i64),
    pub people: &'static str,
    pub recorded: i64,
    pub position: i64,
}

impl Night {
    // Whether the play names every one of these people, and if so
    // whether it names exactly them, which is what the store's read
    // answers.
    fn names(&self, people: &[String]) -> Option<bool> {
        let every = people
            .iter()
            .all(|person| self.people.contains(person.as_str()));
        every.then(|| self.people.chars().count() == people.len())
    }

    fn progress(&self) -> Progress {
        Progress {
            play: format!("{}-{}", self.people, self.recorded),
            position: self.position,
            duration: RUNTIME,
            finished: finished(self.position, RUNTIME),
            recorded: self.recorded,
            season: self.numbers.0,
            episode: self.numbers.1,
            ..Progress::default()
        }
    }
}

fn held(number: i64, kind: &str) -> Held {
    Held {
        arts: Vec::new(),
        library: match kind {
            "movies" => FILMS.into(),
            _ => "screening/shows".into(),
        },
        id: format!("{kind}:{number}"),
        kind: kind.into(),
        title: format!("Title {number}"),
        art: format!("{number}.jpg"),
        released: format!("{}", 1970 + number),
        slug: format!("title-{number}"),
        // The third title carries no tagline, so a card of it falls back
        // to the name.
        tagline: match number {
            3 => String::new(),
            number => format!("Title {number}, in one line."),
        },
        plot: String::new(),
        duration: match kind {
            "movies" => 7_620,
            _ => 0,
        },
    }
}

fn film(position: i64, span: (f64, f64), universes: &[&str]) -> Entry {
    Entry {
        position,
        kind: MOVIE.into(),
        alias: format!("movie:tmdb:{position}"),
        title: format!("Title {position}"),
        released: format!("{}", 1970 + position),
        release_year: 1970 + position,
        timed: true,
        from: span.0,
        to: span.1,
        universes: universes.iter().map(|name| name.to_string()).collect(),
        held: Some(held(position, "movies")),
        episodes: 0,
        runs: Vec::new(),
    }
}

// The order the tests read: five rows, because the two entries at
// positions two and three pack onto one.
pub fn order() -> Vec<Entry> {
    vec![
        film(1, (-32.0, -32.0), &[]),
        film(2, (-30.0, -28.0), &[FEN]),
        film(3, (-29.0, -27.0), &[MARSH]),
        Entry {
            held: None,
            released: "2099".into(),
            release_year: 2099,
            title: "A Later Title".into(),
            ..film(4, (0.0, 0.0), &[])
        },
        Entry {
            kind: SERIES.into(),
            alias: "series:tvdb:1".into(),
            episodes: 9,
            runs: vec![(2, 0), (3, 4)],
            held: Some(held(5, "series")),
            ..film(5, (2.0, 3.0), &[])
        },
        film(6, (10.0, 12.0), &[COPPICE, FEN, MARSH]),
    ]
}

// The split order the progress tests read: a film, the first season of
// a series, a second film, and the second season of that same series.
pub fn split() -> Vec<Entry> {
    let run = |position: i64, season: i64| Entry {
        kind: SERIES.into(),
        alias: "series:tvdb:1".into(),
        episodes: AIRED,
        runs: vec![(season, 0)],
        held: Some(held(5, "series")),
        ..film(position, (position as f64, position as f64), &[])
    };
    vec![
        film(1, (1.0, 1.0), &[]),
        run(2, 1),
        film(3, (3.0, 3.0), &[]),
        run(4, 2),
    ]
}

impl Orders {
    fn page(&self) -> Option<crate::catalog::Franchise> {
        if self.empty {
            return None;
        }
        Some(crate::catalog::Franchise {
            library: ORDERS.into(),
            id: CYCLE.into(),
            title: "The Cycle".into(),
            art: "cycle.jpg".into(),
            universe: COPPICE.into(),
            calendar: Some(Calendar {
                unit: "years".into(),
                zero: "the Survey".into(),
                before: "BS".into(),
                after: "AS".into(),
            }),
            eras: vec![
                Era {
                    name: "The Coppice Years".into(),
                    from: -5.0,
                    to: 5.0,
                },
                Era {
                    name: "The Long Survey".into(),
                    from: -40.0,
                    to: 40.0,
                },
            ],
            entries: match self.split {
                true => split(),
                false => order(),
            },
        })
    }
}

impl Source for Orders {
    // The page fake answers the one order it holds, which the home page
    // never reads.
    fn franchises(&mut self) -> Vec<crate::catalog::FranchiseEntry> {
        Vec::new()
    }

    fn libraries(&mut self) -> Vec<LibraryEntry> {
        Vec::new()
    }

    fn genres(&mut self) -> Vec<GenreEntry> {
        Vec::new()
    }

    fn wall(&mut self, query: &Query) -> Answer {
        match query {
            Query::Franchise { library, id } => {
                crate::catalog::franchise::answer(self.franchise(library, id))
            }
            _ => Answer::default(),
        }
    }

    fn pool(&mut self) -> Vec<pool::Candidate> {
        Vec::new()
    }

    fn movie(&mut self, _library: &str, id: &str) -> Option<MovieDetails> {
        id.starts_with("movies:").then(|| MovieDetails {
            title: format!("Film {id}"),
            released: "1980".into(),
            ..MovieDetails::default()
        })
    }

    fn series(&mut self, _library: &str, id: &str) -> Option<SeriesDetails> {
        id.starts_with("series:").then(|| SeriesDetails {
            title: format!("Serial {id}"),
            released: "1980".into(),
            ..SeriesDetails::default()
        })
    }

    fn episodes(&mut self, _library: &str, series: &str) -> Vec<Episode> {
        if !series.starts_with("series:") {
            return Vec::new();
        }
        (1..=SEASONS)
            .flat_map(|season| {
                (1..=AIRED).map(move |episode| Episode {
                    id: format!("{series}:{season}:{episode}"),
                    season,
                    episode,
                    title: format!("S{season}E{episode}"),
                    duration: RUNTIME,
                    ..Episode::default()
                })
            })
            .collect()
    }

    fn plays_of(&mut self, library: &str, id: &str, people: &[String]) -> Vec<Resume> {
        self.nights
            .iter()
            .filter(|night| night.work == id)
            .filter_map(|night| {
                Some(Resume {
                    library: library.to_string(),
                    id: id.to_string(),
                    progress: night.progress(),
                    exact: night.names(people)?,
                    ..Resume::default()
                })
            })
            .collect()
    }

    fn episode_progress(
        &mut self,
        _library: &str,
        series: &str,
        people: &[String],
    ) -> Vec<Progress> {
        let aired = self.episodes("", series);
        aired
            .iter()
            .filter_map(|episode| {
                self.nights
                    .iter()
                    .filter(|night| night.work == series)
                    .filter(|night| night.numbers == (episode.season, episode.episode))
                    .filter(|night| night.names(people).is_some())
                    .max_by_key(|night| night.recorded)
                    .map(Night::progress)
            })
            .collect()
    }

    fn set(&mut self, _library: &str, _id: &str) -> Option<MovieSet> {
        None
    }

    fn franchises_of(&mut self, _library: &str, _id: &str) -> Vec<Membership> {
        let Some(page) = self.page() else {
            return Vec::new();
        };
        vec![Membership {
            movies: page
                .entries
                .iter()
                .filter(|entry| entry.kind == MOVIE)
                .count() as i64,
            series: page
                .entries
                .iter()
                .filter(|entry| entry.kind == SERIES)
                .count() as i64,
            library: page.library,
            id: page.id,
            title: page.title,
            members: page
                .entries
                .into_iter()
                .filter(|entry| entry.held.is_some())
                .collect(),
        }]
    }

    fn franchise(&mut self, library: &str, id: &str) -> Option<crate::catalog::Franchise> {
        match library == ORDERS && id == CYCLE {
            true => self.page(),
            false => None,
        }
    }

    fn play(&mut self, _library: &str, _selection: &Selection) -> Vec<PlayItem> {
        Vec::new()
    }

    fn credits(&mut self, _library: &str, _id: &str) -> Credits {
        Credits::default()
    }

    fn files(&mut self, _library: &str, _item: &str) -> Vec<FileFacts> {
        Vec::new()
    }

    fn person(&mut self, _library: &str, _path: &str) -> Option<Person> {
        None
    }

    fn changed(&mut self) -> Change {
        Change::None
    }

    fn wake_by(&mut self, _wake: Waker) {}
}

fn page() -> Franchise {
    Franchise::open(ORDERS, CYCLE, &mut Orders::default()).expect("the fake holds the order")
}

#[test]
fn a_franchise_no_library_holds_opens_no_page() {
    assert!(
        Franchise::open(
            ORDERS,
            CYCLE,
            &mut Orders {
                empty: true,
                ..Orders::default()
            }
        )
        .is_none()
    );
    assert!(Franchise::open(ORDERS, "franchise:name:none", &mut Orders::default()).is_none());
}

#[test]
fn the_page_opens_on_the_row_of_the_entry_at_a_position() {
    let page = Franchise::open_at(ORDERS, CYCLE, 5, &mut Orders::default())
        .expect("the fake holds the order");
    assert_eq!(page.focus, 4);
    assert_eq!(page.rows[4].cell.kind, "series");
}

#[test]
fn a_position_the_order_does_not_hold_opens_on_the_first_row() {
    let page = Franchise::open_at(ORDERS, CYCLE, 9, &mut Orders::default())
        .expect("the fake holds the order");
    assert_eq!(page.focus, 0);
    assert!(
        Franchise::open_at(
            ORDERS,
            CYCLE,
            1,
            &mut Orders {
                empty: true,
                ..Orders::default()
            }
        )
        .is_none()
    );
}

#[test]
fn the_page_opens_on_the_first_row_in_story_order() {
    let page = page();
    assert_eq!(page.title, "The Cycle");
    assert_eq!(page.focus, 0);
    assert_eq!(page.universes, [COPPICE, FEN, MARSH]);
    assert_eq!(page.rows.len(), 6);
}

#[test]
fn the_eras_become_headings_over_their_first_rows() {
    let page = page();
    let headings: Vec<(String, usize, usize)> = page
        .headings
        .iter()
        .map(|heading| (heading.label(), heading.first, heading.last))
        .collect();
    assert_eq!(
        headings,
        [
            ("The Long Survey · 81 years".to_string(), 0, 5),
            ("The Coppice Years · 11 years".to_string(), 3, 4)
        ]
    );
}

#[test]
fn up_and_down_move_a_row_and_up_from_the_first_row_holds_it() {
    let mut page = page();
    let mut source = Orders::default();
    page.key("down", &mut source);
    assert_eq!(page.focus, 1);
    page.key("down", &mut source);
    assert_eq!(page.focus, 2);
    page.key("up", &mut source);
    assert_eq!(page.focus, 1);
    page.key("up", &mut source);
    assert_eq!(page.focus, 0);
    page.key("up", &mut source);
    assert_eq!(page.focus, 0);
}

#[test]
fn down_from_the_last_row_holds_focus() {
    let mut page = page();
    let mut source = Orders::default();
    page.focus = 5;
    page.key("down", &mut source);
    assert_eq!(page.focus, 5);
}

#[test]
fn left_and_right_step_an_era_at_a_time_and_hold_at_the_ends() {
    let mut page = page();
    let mut source = Orders::default();
    page.key("right", &mut source);
    assert_eq!(page.focus, 3);
    page.key("right", &mut source);
    assert_eq!(page.focus, 3);
    page.key("left", &mut source);
    assert_eq!(page.focus, 0);
    page.key("left", &mut source);
    assert_eq!(page.focus, 0);

    page.focus = 5;
    page.key("left", &mut source);
    assert_eq!(page.focus, 3);
    page.focus = 2;
    page.key("left", &mut source);
    assert_eq!(page.focus, 0);
    page.focus = 1;
    page.key("right", &mut source);
    assert_eq!(page.focus, 3);
}

#[test]
fn a_press_on_an_entry_opens_the_film_over_this_page() {
    let mut page = page();
    let mut source = Orders::default();
    let step = page.key("enter", &mut source);
    assert!(matches!(step, Step::Open(Screen::Movie(_))));
}

#[test]
fn a_press_on_a_series_entry_opens_the_series_on_the_member_s_runs() {
    let mut page = page();
    let mut source = Orders::default();
    page.focus = 4;
    let Step::Open(Screen::Series(opened)) = page.key("enter", &mut source) else {
        panic!("a press on a series entry opens the series");
    };
    let via = opened
        .via
        .expect("the page carries the member it opened on");

    assert_eq!(via.position, 5);
    assert_eq!(via.runs, [(2, 0), (3, 4)]);
}

#[test]
fn a_press_on_a_gap_opens_nothing() {
    let mut page = page();
    let mut source = Orders::default();
    page.focus = 3;
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
    page.focus = 9;
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

#[test]
fn a_reread_holds_the_rung_focus_was_on() {
    let mut page = page();
    let mut source = Orders::default();
    page.focus = 2;
    page.reread(&mut source);
    assert_eq!(page.focus, 2);

    page.focus = 99;
    page.reread(&mut source);
    assert_eq!(page.focus, 5);
}

#[test]
fn a_reread_that_finds_nothing_leaves_the_page_as_it_was() {
    let mut page = page();
    page.reread(&mut Orders {
        empty: true,
        ..Orders::default()
    });
    assert_eq!(page.rows.len(), 6);
}

#[test]
fn a_wall_that_went_away_leaves_focus_on_the_first_row() {
    let mut page = page();
    page.rows.clear();
    assert_eq!(page.hold(3), 0);
}

#[test]
fn a_page_with_no_rows_moves_nowhere() {
    let mut page = page();
    let mut source = Orders::default();
    page.rows.clear();
    page.headings.clear();
    page.focus = 0;
    page.key("down", &mut source);
    assert_eq!(page.focus, 0);
    page.key("left", &mut source);
    assert_eq!(page.focus, 0);
    page.key("up", &mut source);
    assert_eq!(page.focus, 0);
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}
