// The two franchise reads against the schema every agent loads: the
// strip on a title's page, and the wall on the franchise's own page.

use super::*;
use crate::catalog::franchise::{MOVIE, SERIES, Standing};

// The Library of kind franchises, which holds the order and no member.
const ORDERS: &str = "screening/orders";
const CYCLE: &str = "franchise:name:the-cycle";

// The body a franchise row carries: the home universe, the calendar,
// and the eras.
const BODY: &str = r#"{"universe":"The Fen","sources":["https://example.invalid/order"],
    "calendar":{"unit":"years","zero":"The Coppice","before":"BC","after":"AC"},
    "eras":[{"name":"The Long Survey","from":-40,"to":40},
            {"name":"The Coppice Years","from":-5,"to":5}]}"#;

fn insert_franchise(path: &Path, id: &str, title: &str, body: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO franchises (library, id, kind, path, title, sort_key, art, body, slug) \
             VALUES (?, ?, 'franchises', ?, ?, ?, ?, ?, ?)",
            (
                ORDERS,
                id,
                title.to_lowercase(),
                title,
                title.to_lowercase(),
                format!("{id}.jpg"),
                body,
                title.to_lowercase().replace(' ', "-"),
            ),
        )
        .unwrap();
}

// One entry of the order. `member` is the kind, the alias, the file's
// title, and the release year the file gives it. `time` is the span,
// and nothing where the file gave the entry none.
fn insert_member(
    path: &Path,
    franchise: &str,
    position: i64,
    member: (&str, &str, &str, i64),
    time: Option<(f64, f64)>,
    universes: &str,
) {
    let (kind, alias, title, release_year) = member;
    let (timed, from, to) = match time {
        Some((from, to)) => (1, from, to),
        None => (0, 0.0, 0.0),
    };
    let released = match release_year > 0 {
        true => release_year.to_string(),
        false => String::new(),
    };
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO franchise_members \
             (library, franchise, position, kind, alias, title, release_year, released, \
              timed, time_from, time_to, universes) \
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (
                ORDERS,
                franchise,
                position,
                kind,
                alias,
                title,
                release_year,
                released,
                timed,
                from,
                to,
                universes,
            ),
        )
        .unwrap();
}

// The release date one member carries, at whatever precision the file
// gave it.
fn date_member(path: &Path, franchise: &str, position: i64, released: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "UPDATE franchise_members SET released = ? \
             WHERE library = ? AND franchise = ? AND position = ?",
            (released, ORDERS, franchise, position),
        )
        .unwrap();
}

// One season, or one episode, of one series run.
fn insert_run(path: &Path, franchise: &str, position: i64, season: i64, episode: i64) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO franchise_runs (library, franchise, position, season, episode) \
             VALUES (?, ?, ?, ?, ?)",
            (ORDERS, franchise, position, season, episode),
        )
        .unwrap();
}

fn insert_alias(path: &Path, library: &str, alias: &str, item: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO aliases (library, alias, item, source) VALUES (?, ?, ?, 'nfo')",
            (library, alias, item),
        )
        .unwrap();
}

// The order the reads below are made of: two films the lab holds, a
// series run of one season, a film no library holds, and one entry with
// no time.
fn an_order(path: &Path) {
    insert_franchise(path, CYCLE, "The Cycle", BODY);
    insert_movie(path, "screening/films", "movie:path:one", "One", "one");
    insert_movie(path, "screening/films", "movie:path:two", "Two", "two");
    insert_series(
        path,
        "screening/shows",
        "series:path:one",
        "A Serial",
        "serial",
    );
    for episode in 1..=4 {
        insert_episode(
            path,
            "screening/shows",
            &format!("episode:path:{episode}"),
            "series:path:one",
            1 + episode / 3,
            episode,
        );
    }
    insert_alias(path, "screening/films", "movie:tmdb:1", "movie:path:one");
    insert_alias(path, "screening/films", "movie:tmdb:2", "movie:path:two");
    insert_alias(path, "screening/shows", "series:tvdb:1", "series:path:one");

    insert_member(
        path,
        CYCLE,
        1,
        (MOVIE, "movie:tmdb:1", "One", 1977),
        Some((-32.0, -32.0)),
        "[]",
    );
    insert_member(
        path,
        CYCLE,
        2,
        (SERIES, "series:tvdb:1", "A Serial", 2004),
        Some((-22.0, -20.0)),
        r#"["The Marsh"]"#,
    );
    insert_member(
        path,
        CYCLE,
        3,
        (MOVIE, "movie:tmdb:9", "Nine", 2031),
        Some((0.0, 0.0)),
        "[]",
    );
    insert_member(
        path,
        CYCLE,
        4,
        (MOVIE, "movie:tmdb:2", "Two", 1980),
        None,
        r#"["The Fen","The Marsh"]"#,
    );
}

#[test]
fn a_titles_strip_holds_the_whole_order_the_libraries_hold() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let strips = source.franchises_of("screening/films", "movie:path:one");

    assert_eq!(strips.len(), 1);
    assert_eq!(strips[0].library, ORDERS);
    assert_eq!(strips[0].id, CYCLE);
    assert_eq!(strips[0].title, "The Cycle");
    let held: Vec<(i64, &str)> = strips[0]
        .members
        .iter()
        .map(|member| (member.position, member.name()))
        .collect();
    assert_eq!(held, [(1, "One"), (2, "A Serial"), (4, "Two")]);
}

#[test]
fn a_strip_carries_the_library_and_the_kind_a_press_opens() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let strips = source.franchises_of("screening/shows", "series:path:one");

    let serial = strips[0].members[1].held.clone().expect("the lab holds it");
    assert_eq!(serial.library, "screening/shows");
    assert_eq!(serial.kind, "series");
    assert_eq!(serial.id, "series:path:one");
    assert_eq!(serial.art, "series:path:one.jpg");
}

#[test]
fn a_strip_carries_the_count_of_every_entry_by_kind() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let strips = source.franchises_of("screening/films", "movie:path:one");

    // The order holds three films and one serial, and the gap at
    // position three counts with them.
    assert_eq!((strips[0].movies, strips[0].series), (3, 1));
    assert_eq!(strips[0].members.len(), 3);
}

#[test]
fn the_home_page_reads_every_franchise_in_sort_order() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    insert_franchise(&path, "franchise:name:another", "Another Order", "{}");
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let entries = source.franchises();

    let named: Vec<(&str, &str, &str)> = entries
        .iter()
        .map(|entry| {
            (
                entry.library.as_str(),
                entry.id.as_str(),
                entry.title.as_str(),
            )
        })
        .collect();
    assert_eq!(
        named,
        [
            (ORDERS, "franchise:name:another", "Another Order"),
            (ORDERS, CYCLE, "The Cycle"),
        ]
    );
    assert_eq!(entries[1].art, format!("{CYCLE}.jpg"));
    assert_eq!(entries[1].slug, "the-cycle");
}

#[test]
fn a_franchise_with_no_art_draws_the_poster_of_its_first_held_member() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    let connection = Connection::open(&path).unwrap();
    connection
        .execute("UPDATE franchises SET art = '' WHERE id = ?", (CYCLE,))
        .unwrap();
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let entries = source.franchises();

    // The first entry of the order is the film at position one, and its
    // poster resolves against the library that holds it.
    assert_eq!(entries[0].art, "movie:path:one.jpg");
    assert_eq!(entries[0].art_library, "screening/films");
}

#[test]
fn a_franchise_with_art_of_its_own_keeps_it() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let entries = source.franchises();

    assert_eq!(entries[0].art, format!("{CYCLE}.jpg"));
    assert_eq!(entries[0].art_library, ORDERS);
}

#[test]
fn a_franchise_no_library_holds_a_member_of_draws_no_art() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_franchise(&path, "franchise:name:lone", "Lone Order", "{}");
    let connection = Connection::open(&path).unwrap();
    connection
        .execute(
            "UPDATE franchises SET art = '' WHERE id = 'franchise:name:lone'",
            (),
        )
        .unwrap();
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let entries = source.franchises();

    assert_eq!(entries[0].art, "");
    assert_eq!(entries[0].art_library, "");
}

#[test]
fn a_catalog_with_no_franchises_reads_none() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    assert!(source.franchises().is_empty());
}

#[test]
fn a_title_in_no_franchise_draws_no_strip() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    insert_movie(&path, "screening/films", "movie:path:lone", "Lone", "lone");
    let mut source = SidecarSource::new(&path, NO_AGENT);

    assert!(
        source
            .franchises_of("screening/films", "movie:path:lone")
            .is_empty()
    );
}

#[test]
fn the_page_holds_every_entry_in_story_order_with_its_gaps() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let page = source.franchise(ORDERS, CYCLE).expect("the order is there");

    assert_eq!(page.title, "The Cycle");
    assert_eq!(page.universe, "The Fen");
    assert_eq!(page.art, format!("{CYCLE}.jpg"));
    let order: Vec<(i64, &str, bool)> = page
        .entries
        .iter()
        .map(|entry| (entry.position, entry.name(), entry.held.is_some()))
        .collect();
    assert_eq!(
        order,
        [
            (1, "One", true),
            (2, "A Serial", true),
            (3, "Nine", false),
            (4, "Two", true),
        ]
    );
    assert_eq!(page.entries[2].release_year, 2031);
    assert_eq!(page.entries[2].released, "2031");
    assert_eq!(page.entries[2].standing("2026-09-04"), Standing::Coming);
}

#[test]
fn the_page_carries_a_release_date_at_the_precision_the_file_gave() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    date_member(&path, CYCLE, 3, "2026-10-12");
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let page = source.franchise(ORDERS, CYCLE).expect("the order is there");
    assert_eq!(page.entries[2].released, "2026-10-12");
    assert_eq!(page.entries[2].standing("2026-10-11"), Standing::Coming);
    assert_eq!(page.entries[2].standing("2026-10-12"), Standing::Missing);
    assert_eq!(page.entries[0].released, "1977");
}

#[test]
fn the_page_carries_the_tagline_and_the_plot_of_a_held_member() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_franchise(&path, CYCLE, "The Cycle", BODY);
    insert_page(
        &path,
        "screening/films",
        "movie:path:one",
        "1999",
        "",
        r#"{"plot":"A plot.","tagline":"One line."}"#,
    );
    insert_alias(&path, "screening/films", "movie:tmdb:1", "movie:path:one");
    insert_member(
        &path,
        CYCLE,
        1,
        (MOVIE, "movie:tmdb:1", "One", 1977),
        Some((-32.0, -32.0)),
        "[]",
    );
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let page = source.franchise(ORDERS, CYCLE).expect("the order is there");
    let held = page.entries[0].held.as_ref().expect("the film is held");
    assert_eq!(held.tagline, "One line.");
    assert_eq!(held.plot, "A plot.");
    assert_eq!(held.duration, 6720);

    let strip = source.franchises_of("screening/films", "movie:path:one");
    let member = strip[0].members[0].held.as_ref().expect("held");
    assert_eq!(member.tagline, "");
}

#[test]
fn the_page_carries_the_calendar_the_eras_and_the_spans() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let page = source.franchise(ORDERS, CYCLE).expect("the order is there");

    let calendar = page.calendar.expect("the file names a calendar");
    assert_eq!(calendar.label(-32.0, -32.0), "-32 BC");
    let eras: Vec<(&str, f64, f64)> = page
        .eras
        .iter()
        .map(|era| (era.name.as_str(), era.from, era.to))
        .collect();
    assert_eq!(
        eras,
        [
            ("The Long Survey", -40.0, 40.0),
            ("The Coppice Years", -5.0, 5.0)
        ]
    );
    assert!(page.entries[0].timed);
    assert_eq!((page.entries[1].from, page.entries[1].to), (-22.0, -20.0));
    assert!(!page.entries[3].timed);
    assert_eq!(page.entries[3].universes, ["The Fen", "The Marsh"]);
}

#[test]
fn a_series_run_counts_the_episodes_the_catalog_holds() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    // A run with no rows is the whole show.
    let whole = source.franchise(ORDERS, CYCLE).expect("the order is there");
    assert_eq!(whole.entries[1].episodes, 4);

    // A run that names one season counts that season alone, and one
    // that names episodes counts those.
    insert_run(&path, CYCLE, 2, 1, 0);
    let season = source.franchise(ORDERS, CYCLE).expect("the order is there");
    assert_eq!(season.entries[1].episodes, 2);
}

#[test]
fn a_run_that_names_episodes_counts_the_ones_it_names() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    insert_run(&path, CYCLE, 2, 2, 3);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let page = source.franchise(ORDERS, CYCLE).expect("the order is there");

    assert_eq!(page.entries[1].episodes, 1);
}

#[test]
fn a_franchise_no_library_holds_has_no_page_and_no_answer() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    assert_eq!(source.franchise(ORDERS, "franchise:name:none"), None);
    assert_eq!(
        source.wall(&Query::Franchise {
            library: ORDERS.into(),
            id: "franchise:name:none".into(),
        }),
        Answer::default()
    );
}

#[test]
fn the_franchise_query_answers_the_held_members_as_slots() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let answer = source.wall(&Query::Franchise {
        library: ORDERS.into(),
        id: CYCLE.into(),
    });

    assert_eq!(answer.name, "The Cycle");
    let slots: Vec<(&str, &str, &str)> = answer
        .slots
        .iter()
        .map(|slot| (slot.id.as_str(), slot.kind.as_str(), slot.library.as_str()))
        .collect();
    assert_eq!(
        slots,
        [
            ("movie:path:one", "movies", "screening/films"),
            ("series:path:one", "series", "screening/shows"),
            ("movie:path:two", "movies", "screening/films"),
        ]
    );
}

#[test]
fn two_libraries_that_hold_one_member_draw_the_first_by_name() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    an_order(&path);
    insert_movie(&path, "screening/copies", "movie:path:one", "One", "one");
    insert_alias(&path, "screening/copies", "movie:tmdb:1", "movie:path:one");
    let mut source = SidecarSource::new(&path, NO_AGENT);

    let page = source.franchise(ORDERS, CYCLE).expect("the order is there");

    let held = page.entries[0].held.clone().expect("two libraries hold it");
    assert_eq!(held.library, "screening/copies");
}
