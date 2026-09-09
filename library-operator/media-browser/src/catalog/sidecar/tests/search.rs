// The search index over a fixture replica: what the read indexes from
// each table, and how a source answers a `Search` before and after the
// first build lands.

use std::time::{Duration, Instant};

use super::*;
use crate::catalog::Change;
use crate::catalog::search::PEOPLE;
use crate::catalog::sidecar::search::read;

const FEATURES: &str = "screening/features";
const SERIALS: &str = "screening/serials";

// A replica with one row in every table a hit can land in.
fn indexed(dir: &TempDir) -> PathBuf {
    let path = fixture(dir);
    insert_page(&path, FEATURES, "movie:1", "1989", "set:1", BODY);
    insert_set(&path, FEATURES, "set:1", "The Coppice Cycle");
    insert_series_page(&path, SERIALS, "series:1", "1996", BODY);
    insert_episode_page(
        &path,
        SERIALS,
        "episode:1",
        "series:1",
        (1, 1),
        "1996-09-22",
        r#"{"plot":"A survey party reaches the marsh."}"#,
    );
    insert_contributor(
        &path,
        FEATURES,
        ".contributors/A Player",
        "A Player",
        (1, 1),
    );
    insert_contributor(&path, FEATURES, ".contributors/No Face", "No Face", (1, 0));
    let connection = Connection::open(&path).unwrap();
    connection
        .execute(
            "INSERT INTO aliases (library, alias, item, source) \
             VALUES (?, 'the-coppice-1989', 'movie:1', 'folder')",
            (FEATURES,),
        )
        .unwrap();
    // An alias whose item is no longer in the catalog.
    connection
        .execute(
            "INSERT INTO aliases (library, alias, item, source) \
             VALUES (?, 'a-lost-title-1970', 'movie:gone', 'folder')",
            (FEATURES,),
        )
        .unwrap();
    connection
        .execute(
            "INSERT INTO franchises (library, id, kind, title, sort_key, art) \
             VALUES ('screening/orders', 'franchise:name:the-cycle', 'franchises', \
                     'The Marsh Cycle', 'marsh cycle', 'cycle.jpg')",
            (),
        )
        .unwrap();
    path
}

// The first hit's kind and id, or nothing.
fn one(index: &crate::catalog::search::Index, text: &str) -> Option<(String, String)> {
    let hits = index.find(text);
    hits.first().map(|hit| (hit.kind.clone(), hit.id.clone()))
}

#[test]
fn every_table_a_hit_can_land_in_is_indexed() {
    let dir = TempDir::new().unwrap();
    let path = indexed(&dir);
    let connection = Connection::open(&path).unwrap();
    let index = read::index(&connection).unwrap();

    let cases = [
        ("Film movie:1", "movies", "movie:1"),
        ("the-coppice-1989", "movies", "movie:1"),
        ("Coppice Cycle", "sets", "set:1"),
        ("Marsh Cycle", "franchise", "franchise:name:the-cycle"),
        ("Serial series:1", "series", "series:1"),
        ("Segment 1", "series", "series:1"),
        ("survey party", "series", "series:1"),
        ("A Player", PEOPLE, ".contributors/A Player"),
    ];
    for (asked, kind, id) in cases {
        assert_eq!(
            one(&index, asked),
            Some((kind.to_string(), id.to_string())),
            "{asked}"
        );
    }
    assert_eq!(index.size().items, 6);
}

#[test]
fn a_persons_hit_carries_the_headshot_only_where_the_entry_holds_one() {
    let dir = TempDir::new().unwrap();
    let path = indexed(&dir);
    let connection = Connection::open(&path).unwrap();
    let index = read::index(&connection).unwrap();

    let player = index.find("A Player");
    assert_eq!(player[0].art, ".contributors/A Player/headshot.jpg");
    assert_eq!(player[0].library, FEATURES);
    assert_eq!(index.find("No Face")[0].art, "");
}

#[test]
fn one_person_in_two_libraries_is_one_hit_with_the_headshot_either_holds() {
    let dir = TempDir::new().unwrap();
    let path = indexed(&dir);
    // The same entry in the serials library, with no headshot beside it.
    insert_contributor(&path, SERIALS, ".contributors/A Player", "A Player", (0, 0));
    // A second person the features library holds no headshot for, and
    // the serials library does.
    insert_contributor(&path, SERIALS, ".contributors/No Face", "No Face", (0, 1));
    let connection = Connection::open(&path).unwrap();
    let index = read::index(&connection).unwrap();

    let player = index.find("A Player");
    assert_eq!(player.len(), 1);
    assert_eq!(player[0].library, FEATURES);
    assert_eq!(player[0].art, ".contributors/A Player/headshot.jpg");

    let face = index.find("No Face");
    assert_eq!(face.len(), 1);
    assert_eq!(face[0].library, FEATURES);
    assert_eq!(face[0].art, ".contributors/No Face/headshot.jpg");
}

#[test]
fn a_film_hit_carries_what_a_card_draws() {
    let dir = TempDir::new().unwrap();
    let path = indexed(&dir);
    let connection = Connection::open(&path).unwrap();
    let hits = read::index(&connection).unwrap().find("Film movie:1");
    assert_eq!(hits[0].released, "1989");
    assert_eq!(hits[0].rating, "PG");
    assert_eq!(hits[0].tagline, "One line.");
    assert_eq!(hits[0].art, "movie:1.jpg");
    assert_eq!(hits[0].seasons, 0);
}

#[test]
fn an_episode_whose_series_left_the_catalog_folds_into_nothing() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_episode_page(
        &path,
        SERIALS,
        "episode:9",
        "series:gone",
        (1, 9),
        "1996-09-22",
        r#"{"plot":"A survey party reaches the marsh."}"#,
    );
    let connection = Connection::open(&path).unwrap();
    let index = read::index(&connection).unwrap();
    assert_eq!(index.size().items, 0);
    assert!(index.find("survey").is_empty());
}

#[test]
fn an_alias_whose_item_left_the_catalog_folds_into_nothing() {
    let dir = TempDir::new().unwrap();
    let path = indexed(&dir);
    let connection = Connection::open(&path).unwrap();
    assert!(
        read::index(&connection)
            .unwrap()
            .find("a-lost-title")
            .is_empty()
    );
}

// How long a test waits for a build to land.
const LANDS_IN: Duration = Duration::from_secs(5);

// The answer once the index holds hits, or the last empty answer at the
// deadline.
fn awaited(source: &mut dyn Source, query: &Query) -> Answer {
    let deadline = Instant::now() + LANDS_IN;
    let mut answer = source.wall(query);
    while answer.slots.is_empty() && Instant::now() < deadline {
        std::thread::sleep(Duration::from_millis(20));
        answer = source.wall(query);
    }
    answer
}

#[test]
fn a_change_on_the_feed_rebuilds_the_index_after_the_quiet_period() {
    let dir = TempDir::new().unwrap();
    let path = indexed(&dir);
    let mut source = SidecarSource::quieting(&path, NO_AGENT, Duration::from_millis(50));
    let query = Query::Search {
        text: "Coppice Cycle".into(),
    };
    assert_eq!(awaited(&mut source, &query).slots.len(), 1);

    insert_set(&path, FEATURES, "set:2", "The Coppice Cycle Again");
    source.shared.mark(Change::Catalog);
    let deadline = Instant::now() + LANDS_IN;
    let mut answer = source.wall(&query);
    while answer.slots.len() < 2 && Instant::now() < deadline {
        std::thread::sleep(Duration::from_millis(20));
        answer = source.wall(&query);
    }
    assert_eq!(answer.slots.len(), 2);
}

#[test]
fn a_source_answers_a_search_once_the_first_build_lands() {
    let dir = TempDir::new().unwrap();
    let path = indexed(&dir);
    let mut source = SidecarSource::new(&path, NO_AGENT);
    let query = Query::Search {
        text: "Coppice Cycle".into(),
    };
    let answer = awaited(&mut source, &query);
    assert_eq!(answer.name, "");
    assert_eq!(answer.slots.len(), 1);
    assert_eq!(answer.slots[0].id, "set:1");
    assert_eq!(
        query.heading(
            &answer.name,
            Counts::of(answer.slots.iter().map(|slot| slot.kind.as_str()))
        ),
        "Coppice Cycle · 1"
    );
}

#[test]
fn a_second_source_over_one_file_answers_a_search_from_the_same_index() {
    let dir = TempDir::new().unwrap();
    let path = indexed(&dir);
    let mut source = SidecarSource::new(&path, NO_AGENT);
    let mut reader = source.reader().expect("the sidecar gives a second read");
    let query = Query::Search {
        text: "Marsh".into(),
    };
    let answer = awaited(&mut *reader, &query);
    // The franchise's title carries the word, and the series only
    // reaches it through an episode's plot, so where the match landed
    // puts the franchise first though its kind ties after a series.
    assert_eq!(answer.slots.len(), 2);
    assert_eq!(answer.slots[0].id, "franchise:name:the-cycle");
    assert_eq!(answer.slots[1].id, "series:1");
}
