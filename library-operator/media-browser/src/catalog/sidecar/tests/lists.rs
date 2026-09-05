// The lists the first screens read: the libraries and their art, one
// library's titles, and every episode of a series.

use super::*;

#[test]
fn libraries_come_back_counted_and_ordered_by_name() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_series(&path, "default/shows", "series:tvdb:73739", "Lost", "lost");
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:604",
        "The Matrix Reloaded",
        "matrix reloaded",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(
        source.libraries(),
        vec![
            LibraryEntry {
                library: "default/films".into(),
                kind: "movies".into(),
                items: 2,
                art: vec!["movie:tmdb:603.jpg".into(), "movie:tmdb:604.jpg".into()],
            },
            LibraryEntry {
                library: "default/shows".into(),
                kind: "series".into(),
                items: 1,
                art: vec!["series:tvdb:73739.jpg".into()],
            },
        ]
    );
}

#[test]
fn a_library_draws_no_more_posters_than_its_tile_holds() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    for number in 1..=6 {
        insert_added_movie(
            &path,
            "default/films",
            &format!("movie:tmdb:{number}"),
            "1999",
            number * 100,
        );
    }

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(
        source.libraries()[0].art,
        [
            "movie:tmdb:6.jpg",
            "movie:tmdb:5.jpg",
            "movie:tmdb:4.jpg",
            "movie:tmdb:3.jpg",
        ]
    );
}

#[test]
fn a_library_draws_as_the_posters_of_its_newest_added_titles_with_art() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_added_movie(&path, "default/films", "movie:tmdb:1", "1999", 100);
    insert_added_movie(&path, "default/films", "movie:tmdb:2", "2004", 300);
    insert_added_movie(&path, "default/films", "movie:tmdb:3", "2010", 200);
    let connection = Connection::open(&path).unwrap();
    connection
        .execute("UPDATE movies SET art = '' WHERE id = 'movie:tmdb:2'", [])
        .unwrap();
    connection
        .execute(
            "INSERT INTO movies (library, id, kind, title, sort_key) \
             VALUES ('default/bare', 'movie:tmdb:9', 'movies', 'Bare', 'bare')",
            [],
        )
        .unwrap();

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let libraries = source.libraries();
    assert_eq!(libraries[0].library, "default/bare");
    assert!(libraries[0].art.is_empty());
    assert_eq!(libraries[1].library, "default/films");
    assert_eq!(libraries[1].art, ["movie:tmdb:3.jpg", "movie:tmdb:1.jpg"]);
}

#[test]
fn a_library_wall_comes_back_in_sort_key_order() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:604",
        "The Matrix Reloaded",
        "matrix reloaded",
    );
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_movie(
        &path,
        "default/other",
        "movie:tmdb:1",
        "Elsewhere",
        "elsewhere",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let answer = source.wall(&library("default/films"));
    assert_eq!(answer.name, "films");
    let ids: Vec<&str> = answer.slots.iter().map(|slot| slot.id.as_str()).collect();
    assert_eq!(ids, ["movie:tmdb:603", "movie:tmdb:604"]);
    assert_eq!(
        answer.slots[0],
        Slot {
            library: "default/films".into(),
            kind: "movies".into(),
            id: "movie:tmdb:603".into(),
            title: "The Matrix".into(),
            released: "1999".into(),
            art: "movie:tmdb:603.jpg".into(),
            ..Slot::default()
        }
    );
}

#[test]
fn a_slot_carries_the_tagline_its_card_leads_with() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_page(&path, "default/films", "one", "1999", "", BODY);
    insert_page(&path, "default/films", "two", "1999", "", "{}");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let slots = source.wall(&library("default/films")).slots;
    assert_eq!(slots[0].tagline, "One line.");
    assert_eq!(slots[1].tagline, "");
}

#[test]
fn a_series_library_wall_stamps_every_slot_with_its_kind() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_series(&path, "default/shows", "series:tvdb:73739", "Lost", "lost");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let slots = source.wall(&library("default/shows")).slots;
    assert_eq!(slots.len(), 1);
    assert_eq!(slots[0].id, "series:tvdb:73739");
    assert_eq!(slots[0].kind, "series");
    assert_eq!(slots[0].library, "default/shows");
    assert!(source.wall(&library("default/empty")).slots.is_empty());
}

#[test]
fn a_series_slot_counts_the_seasons_its_episodes_fall_into_and_a_movie_counts_none() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let series = "series:tvdb:73739";
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_series(&path, "default/shows", series, "Lost", "lost");
    insert_series(&path, "default/shows", "series:tvdb:1", "Alias", "alias");
    for (season, episode) in [(1, 1), (1, 2), (2, 1), (4, 7)] {
        insert_episode(
            &path,
            "default/shows",
            &format!("episode:tvdb:{season}-{episode}"),
            series,
            season,
            episode,
        );
    }

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let shows = source.wall(&library("default/shows")).slots;
    let counted: Vec<(&str, i64)> = shows
        .iter()
        .map(|slot| (slot.id.as_str(), slot.seasons))
        .collect();
    assert_eq!(counted, [("series:tvdb:1", 0), (series, 3)]);
    let films = source.wall(&library("default/films")).slots;
    assert_eq!(films[0].seasons, 0);
}

#[test]
fn episodes_come_back_by_season_and_then_by_episode() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let series = "series:tvdb:73739";
    insert_episode(&path, "default/shows", "episode:tvdb:2", series, 2, 1);
    insert_episode(&path, "default/shows", "episode:tvdb:1b", series, 1, 2);
    insert_episode(&path, "default/shows", "episode:tvdb:1a", series, 1, 1);
    insert_episode(
        &path,
        "default/shows",
        "episode:tvdb:other",
        "series:tvdb:999",
        5,
        1,
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let episodes = source.episodes("default/shows", series);
    let numbers: Vec<(i64, i64)> = episodes
        .iter()
        .map(|episode| (episode.season, episode.episode))
        .collect();
    assert_eq!(numbers, [(1, 1), (1, 2), (2, 1)]);
    assert_eq!(episodes[0].title, "Episode 1");
    assert_eq!(episodes[0].art, "episode:tvdb:1a.jpg");
    assert!(source.episodes("default/empty", series).is_empty());
}

#[test]
fn a_missing_file_reads_as_empty_until_the_sidecar_writes_it() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("catalog.db");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.libraries().is_empty());
    assert!(source.wall(&library("default/films")).slots.is_empty());
    assert!(
        source
            .episodes("default/shows", "series:tvdb:73739")
            .is_empty()
    );
    assert!(
        source
            .series("default/shows", "series:tvdb:73739")
            .is_none()
    );

    assert!(source.play("default/films", &movie_chosen()).is_empty());
    assert!(source.play("default/shows", &episode_chosen(1)).is_empty());
    assert!(source.movie("default/films", "movie:tmdb:603").is_none());
    assert!(source.set("default/films", "set:one").is_none());

    fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    assert_eq!(source.libraries().len(), 1);
}

#[test]
fn a_file_without_the_schema_reads_as_empty() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("catalog.db");
    Connection::open(&path).unwrap();

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.libraries().is_empty());
    assert!(source.wall(&library("default/films")).slots.is_empty());
}

#[test]
fn a_fresh_source_reports_no_change() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(!source.changed());
}

#[test]
fn a_reader_reads_the_same_file_and_leaves_the_streams_running() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/films",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let mut reader = source.reader().expect("the sidecar answers a reader");
    reader.wake_by(Arc::new(|| {}));

    assert_eq!(reader.libraries(), source.libraries());
    assert!(!reader.changed());
    assert_eq!(reader.wall(&library("default/films")).slots.len(), 1);

    let shared = source.shared.clone();
    drop(reader);
    assert!(!shared.stop.load(Ordering::Acquire));
    drop(source);
    assert!(shared.stop.load(Ordering::Acquire));
}
