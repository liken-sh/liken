use std::panic::{AssertUnwindSafe, catch_unwind};

use super::*;
use crate::catalog::draw::Date;
use crate::catalog::{
    Credits, Episode, Franchise, FranchiseEntry, GenreEntry, Membership, MovieDetails, MovieSet,
    Person, PlayItem,
};
use crate::harness::Waker;
use crate::screens::home::{self, Row};

const FILMS: &str = "default/films";
const SHOWS: &str = "default/shows";
const PERSON: &str = ".contributors/first";
const OTHER: &str = ".contributors/a-first";

fn insert_alias(path: &Path, library: &str, scheme: &str, id: &str, contributor: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO contributor_aliases (library, scheme, id, path) VALUES (?, ?, ?, ?)",
            (library, scheme, id, contributor),
        )
        .unwrap();
}

fn add_other_entry(path: &Path, scheme: &str, id: &str) {
    insert_alias(path, FILMS, scheme, id, PERSON);
    insert_contributor(path, SHOWS, OTHER, "A First", (1, 1));
    insert_alias(path, SHOWS, scheme, id, OTHER);
}

fn incomplete_person(path: &Path) {
    insert_contributor(path, FILMS, PERSON, "A First", (0, 0));
}

fn person_wall() -> Query {
    Query::Person {
        library: FILMS.into(),
        path: PERSON.into(),
    }
}

fn person(source: &mut dyn Source) -> Person {
    source
        .person(FILMS, PERSON)
        .expect("the catalog holds the opening entry")
}

#[test]
fn one_page_reuses_the_aliases_from_a_persons_wall_for_the_about_slot() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    incomplete_person(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    source.begin_page_read();
    source.wall(&person_wall());
    add_other_entry(&path, "tmdb", "31");

    assert!(!person(&mut source).headshot);
    source.end_page_read();
}

#[test]
fn a_successful_empty_alias_read_is_held_until_the_next_page() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    incomplete_person(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    source.begin_page_read();
    assert!(!person(&mut source).headshot);
    add_other_entry(&path, "tmdb", "31");
    assert!(!person(&mut source).headshot);
    source.end_page_read();

    source.begin_page_read();
    assert!(person(&mut source).headshot);
    source.end_page_read();
}

#[test]
fn standalone_person_reads_resolve_aliases_each_time() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    incomplete_person(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    assert!(!person(&mut source).headshot);
    add_other_entry(&path, "tmdb", "31");
    assert!(person(&mut source).headshot);
}

#[test]
fn a_complete_person_does_not_resolve_aliases() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_contributor(&path, FILMS, PERSON, "A First", (1, 1));
    Connection::open(&path)
        .unwrap()
        .execute_batch("DROP TABLE contributor_aliases")
        .unwrap();
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let person = person(&mut source);
    assert!(person.biography);
    assert!(person.headshot);
}

fn person_strip_headshot(page: &home::Page) -> &str {
    page.blocks
        .iter()
        .filter_map(|block| block.strip())
        .find(|strip| matches!(strip.row, Row::Query(Query::Person { .. })))
        .and_then(|strip| strip.last.as_ref())
        .map(|last| last.art.as_str())
        .expect("the page draws the person's about slot")
}

fn person_with_four_works(path: &Path) {
    incomplete_person(path);
    for number in 1..=4 {
        let id = format!("film-{number}");
        insert_page(path, FILMS, &id, "1994", "", BODY);
        insert_credit(
            path,
            FILMS,
            &id,
            0,
            (PERSON, "A First"),
            ("actor", "A Part"),
        );
    }
}

#[test]
fn a_reader_without_streams_starts_a_fresh_scope_for_each_page() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    person_with_four_works(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);
    let mut reader = source.reader().expect("the sidecar answers a reader");
    let today = Date {
        year: 2026,
        month: 9,
        day: 3,
    };

    let first = home::read(&mut *reader, today);
    assert_eq!(person_strip_headshot(&first), "");
    add_other_entry(&path, "tmdb", "31");

    let second = home::read(&mut *reader, today);
    assert_eq!(
        person_strip_headshot(&second),
        format!("{OTHER}/headshot.jpg")
    );
    assert!(!reader.changed());
}

#[test]
fn a_failed_alias_read_does_not_cache_an_empty_answer() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    incomplete_person(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);
    source.begin_page_read();

    Connection::open(&path)
        .unwrap()
        .execute_batch("DROP TABLE contributor_aliases")
        .unwrap();
    assert_eq!(source.person(FILMS, PERSON), None);

    Connection::open(&path)
        .unwrap()
        .execute_batch(
            "CREATE TABLE contributor_aliases (\
                 library TEXT NOT NULL DEFAULT '', scheme TEXT NOT NULL DEFAULT '', \
                 id TEXT NOT NULL DEFAULT '', path TEXT NOT NULL DEFAULT '', \
                 PRIMARY KEY (library, scheme, id));\
             CREATE INDEX contributor_aliases_library_path \
                 ON contributor_aliases (library, path);\
             CREATE INDEX contributor_aliases_scheme_id \
                 ON contributor_aliases (scheme, id);",
        )
        .unwrap();
    add_other_entry(&path, "tmdb", "31");

    assert!(person(&mut source).headshot);
    source.end_page_read();
}

fn add_series_genre(path: &Path) {
    insert_series(path, SHOWS, "series:1", "A Serial", "a serial");
    Connection::open(path)
        .unwrap()
        .execute(
            "INSERT INTO genres (library, item, rank, genre) VALUES (?, 'series:1', 0, 'Drama')",
            [SHOWS],
        )
        .unwrap();
}

#[test]
fn one_page_reuses_library_kinds_between_people_and_genres() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    person_with_four_works(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    source.begin_page_read();
    source.wall(&person_wall());
    add_series_genre(&path);
    assert!(source.genres().is_empty());
    source.end_page_read();

    assert_eq!(source.genres()[0].name, "Drama");
}

#[test]
fn a_reader_releases_an_empty_library_kind_answer_after_the_page() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let mut source = SidecarSource::new(&path, NO_AGENT);
    let mut reader = source.reader().unwrap();

    reader.begin_page_read();
    assert!(reader.genres().is_empty());
    add_series_genre(&path);
    assert!(reader.genres().is_empty());
    reader.end_page_read();

    reader.begin_page_read();
    assert_eq!(reader.genres()[0].name, "Drama");
    reader.end_page_read();
}

#[test]
fn a_failed_library_kind_read_does_not_cache_an_empty_answer() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let connection = Connection::open(&path).unwrap();
    let mut source = SidecarSource::new(&path, NO_AGENT);
    source.begin_page_read();

    connection
        .execute_batch("ALTER TABLE series RENAME TO unavailable_series")
        .unwrap();
    assert!(source.genres().is_empty());

    connection
        .execute_batch("ALTER TABLE unavailable_series RENAME TO series")
        .unwrap();
    add_series_genre(&path);
    assert_eq!(source.genres()[0].name, "Drama");
    source.end_page_read();
}

struct PanickingSource {
    reading: bool,
}

impl Source for PanickingSource {
    fn begin_page_read(&mut self) {
        self.reading = true;
    }

    fn end_page_read(&mut self) {
        self.reading = false;
    }

    fn libraries(&mut self) -> Vec<LibraryEntry> {
        unreachable!()
    }

    fn genres(&mut self) -> Vec<GenreEntry> {
        unreachable!()
    }

    fn franchises(&mut self) -> Vec<FranchiseEntry> {
        unreachable!()
    }

    fn wall(&mut self, _query: &Query) -> Answer {
        unreachable!()
    }

    fn pool(&mut self) -> Vec<crate::catalog::pool::Candidate> {
        panic!("the pool read failed")
    }

    fn movie(&mut self, _library: &str, _id: &str) -> Option<MovieDetails> {
        unreachable!()
    }

    fn series(&mut self, _library: &str, _id: &str) -> Option<SeriesDetails> {
        unreachable!()
    }

    fn episodes(&mut self, _library: &str, _series: &str) -> Vec<Episode> {
        unreachable!()
    }

    fn set(&mut self, _library: &str, _id: &str) -> Option<MovieSet> {
        unreachable!()
    }

    fn franchises_of(&mut self, _library: &str, _id: &str) -> Vec<Membership> {
        unreachable!()
    }

    fn franchise(&mut self, _library: &str, _id: &str) -> Option<Franchise> {
        unreachable!()
    }

    fn play(&mut self, _library: &str, _selection: &Selection) -> Vec<PlayItem> {
        unreachable!()
    }

    fn credits(&mut self, _library: &str, _id: &str) -> Credits {
        unreachable!()
    }

    fn files(&mut self, _library: &str, _item: &str) -> Vec<FileFacts> {
        unreachable!()
    }

    fn person(&mut self, _library: &str, _path: &str) -> Option<Person> {
        unreachable!()
    }

    fn changed(&mut self) -> bool {
        false
    }

    fn wake_by(&mut self, _wake: Waker) {}
}

#[test]
fn a_page_read_ends_its_scope_when_the_source_panics() {
    let mut source = PanickingSource { reading: false };
    let result = catch_unwind(AssertUnwindSafe(|| {
        home::read(
            &mut source,
            Date {
                year: 2026,
                month: 9,
                day: 3,
            },
        )
    }));

    assert!(result.is_err());
    assert!(!source.reading);
}
