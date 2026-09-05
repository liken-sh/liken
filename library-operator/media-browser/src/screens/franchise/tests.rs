// The franchise page over an invented order: the rows it packs, the
// rail it derives, and where a press takes focus.

use super::*;
use crate::catalog::franchise::{Calendar, Entry, Era, Held, MOVIE, Membership, SERIES};
use crate::catalog::{
    Answer, Credits, Episode, FileFacts, GenreEntry, LibraryEntry, MovieDetails, MovieSet, Person,
    PlayItem, Query, Selection, SeriesDetails, pool,
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
        tagline: String::new(),
        plot: String::new(),
        duration: 0,
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
            held: Some(held(5, "series")),
            ..film(5, (2.0, 3.0), &[])
        },
        film(6, (10.0, 12.0), &[COPPICE, FEN, MARSH]),
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
            entries: order(),
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

    fn episodes(&mut self, _library: &str, _series: &str) -> Vec<Episode> {
        Vec::new()
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

    fn changed(&mut self) -> bool {
        false
    }

    fn wake_by(&mut self, _wake: Waker) {}
}

fn page() -> Franchise {
    Franchise::open(ORDERS, CYCLE, &mut Orders::default()).expect("the fake holds the order")
}

#[test]
fn a_franchise_no_library_holds_opens_no_page() {
    assert!(Franchise::open(ORDERS, CYCLE, &mut Orders { empty: true }).is_none());
    assert!(Franchise::open(ORDERS, "franchise:name:none", &mut Orders::default()).is_none());
}

#[test]
fn the_page_opens_on_the_first_row_in_story_order() {
    let page = page();
    assert_eq!(page.title, "The Cycle");
    assert_eq!(page.focus, Focus::Row(0));
    assert_eq!(page.universes, [COPPICE, FEN, MARSH]);
    assert_eq!(page.rows.len(), 6);
}

#[test]
fn the_eras_become_the_bars_of_the_rail_widest_first() {
    let page = page();
    let bars: Vec<(&str, usize, usize, usize)> = page
        .eras
        .iter()
        .map(|bar| (bar.label.as_str(), bar.first, bar.last, bar.lane))
        .collect();
    assert_eq!(
        bars,
        [("The Long Survey", 0, 5, 0), ("The Coppice Years", 3, 4, 1),]
    );
}

#[test]
fn up_and_down_move_a_row_and_up_from_the_first_row_holds_it() {
    let mut page = page();
    let mut source = Orders::default();
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Row(1));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Row(2));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Row(1));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Row(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Row(0));
}

#[test]
fn down_from_the_last_row_holds_focus() {
    let mut page = page();
    let mut source = Orders::default();
    page.focus = Focus::Row(5);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Row(5));
}

#[test]
fn right_moves_nothing_on_a_row() {
    let mut page = page();
    let mut source = Orders::default();
    page.focus = Focus::Row(1);
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Row(1));
}

#[test]
fn left_from_the_first_column_lands_on_the_rail() {
    let mut page = page();
    let mut source = Orders::default();
    page.key("left", &mut source);
    assert_eq!(page.focus, Focus::Rail(0));
}

#[test]
fn the_rail_moves_an_era_at_a_time_and_returns_to_its_first_row() {
    let mut page = page();
    let mut source = Orders::default();
    page.focus = Focus::Rail(0);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Rail(1));
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Rail(1));
    page.key("right", &mut source);
    assert_eq!(page.focus, Focus::Row(3));

    page.focus = Focus::Rail(1);
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Rail(0));
    page.key("enter", &mut source);
    assert_eq!(page.focus, Focus::Row(0));
    page.focus = Focus::Rail(0);
    page.key("left", &mut source);
    assert_eq!(page.focus, Focus::Rail(0));
}

#[test]
fn a_press_on_an_entry_opens_the_film_in_the_place_of_this_page() {
    let mut page = page();
    let mut source = Orders::default();
    let step = page.key("enter", &mut source);
    assert!(matches!(step, Step::Replace(Screen::Movie(_))));
}

#[test]
fn a_press_on_a_series_entry_opens_the_series() {
    let mut page = page();
    let mut source = Orders::default();
    page.focus = Focus::Row(4);
    let step = page.key("enter", &mut source);
    assert!(matches!(step, Step::Replace(Screen::Series(_))));
}

#[test]
fn a_press_on_a_gap_opens_nothing() {
    let mut page = page();
    let mut source = Orders::default();
    page.focus = Focus::Row(3);
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
    page.focus = Focus::Row(9);
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}

#[test]
fn a_reread_holds_the_rung_focus_was_on() {
    let mut page = page();
    let mut source = Orders::default();
    page.focus = Focus::Row(2);
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Row(2));

    page.focus = Focus::Row(99);
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Row(5));

    page.focus = Focus::Rail(9);
    page.reread(&mut source);
    assert_eq!(page.focus, Focus::Rail(1));
}

#[test]
fn a_reread_that_finds_nothing_leaves_the_page_as_it_was() {
    let mut page = page();
    page.reread(&mut Orders { empty: true });
    assert_eq!(page.rows.len(), 6);
}

#[test]
fn a_rail_or_a_wall_that_went_away_leaves_focus_on_the_first_row() {
    let mut page = page();
    page.eras.clear();
    assert_eq!(page.hold(Focus::Rail(0)), Focus::Row(0));
    page.rows.clear();
    assert_eq!(page.hold(Focus::Row(3)), Focus::Row(0));
}

#[test]
fn a_page_with_no_rows_moves_nowhere() {
    let mut page = page();
    let mut source = Orders::default();
    page.rows.clear();
    page.eras.clear();
    page.focus = Focus::Row(0);
    page.key("down", &mut source);
    assert_eq!(page.focus, Focus::Row(0));
    page.key("left", &mut source);
    assert_eq!(page.focus, Focus::Row(0));
    page.key("up", &mut source);
    assert_eq!(page.focus, Focus::Row(0));
    assert!(matches!(page.key("enter", &mut source), Step::Stay));
}
