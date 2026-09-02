// The lists the first screens read: the libraries, one library's titles,
// and a series' seasons and episodes.

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
            },
            LibraryEntry {
                library: "default/shows".into(),
                kind: "series".into(),
                items: 1,
            },
        ]
    );
}

#[test]
fn titles_come_back_in_sort_key_order() {
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
    let titles = source.titles("default/films", "movies");
    let ids: Vec<&str> = titles.iter().map(|title| title.id.as_str()).collect();
    assert_eq!(ids, ["movie:tmdb:603", "movie:tmdb:604"]);
    assert_eq!(titles[0].title, "The Matrix");
    assert_eq!(titles[0].released, "1999");
    assert_eq!(titles[0].art, "movie:tmdb:603.jpg");
}

#[test]
fn titles_read_the_table_the_kind_names() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_movie(
        &path,
        "default/mixed",
        "movie:tmdb:603",
        "The Matrix",
        "matrix",
    );
    insert_series(&path, "default/mixed", "series:tvdb:73739", "Lost", "lost");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let series = source.titles("default/mixed", "series");
    assert_eq!(series.len(), 1);
    assert_eq!(series[0].id, "series:tvdb:73739");
}

#[test]
fn titles_refuse_a_kind_that_names_no_item_table() {
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
    assert!(source.titles("default/films", "episodes").is_empty());
    assert!(
        source
            .titles("default/films", "movies; DROP TABLE movies")
            .is_empty()
    );
    assert!(source.titles("default/films", "").is_empty());
}

#[test]
fn seasons_come_back_distinct_and_ordered() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let series = "series:tvdb:73739";
    insert_episode(&path, "default/shows", "episode:tvdb:2", series, 2, 1);
    insert_episode(&path, "default/shows", "episode:tvdb:1a", series, 1, 1);
    insert_episode(&path, "default/shows", "episode:tvdb:1b", series, 1, 2);
    insert_episode(
        &path,
        "default/shows",
        "episode:tvdb:other",
        "series:tvdb:999",
        5,
        1,
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(source.seasons("default/shows", series), vec![1, 2]);
    assert!(source.seasons("default/empty", series).is_empty());
}

#[test]
fn episodes_come_back_in_episode_order_for_one_season() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    let series = "series:tvdb:73739";
    insert_episode(&path, "default/shows", "episode:tvdb:1b", series, 1, 2);
    insert_episode(&path, "default/shows", "episode:tvdb:1a", series, 1, 1);
    insert_episode(&path, "default/shows", "episode:tvdb:2", series, 2, 1);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let episodes = source.episodes("default/shows", series, 1);
    let ids: Vec<&str> = episodes.iter().map(|episode| episode.id.as_str()).collect();
    assert_eq!(ids, ["episode:tvdb:1a", "episode:tvdb:1b"]);
    assert_eq!(episodes[0].season, 1);
    assert_eq!(episodes[0].episode, 1);
    assert_eq!(episodes[0].title, "Episode 1");
}

#[test]
fn a_missing_file_reads_as_empty_until_the_sidecar_writes_it() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("catalog.db");

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(source.libraries().is_empty());
    assert!(source.titles("default/films", "movies").is_empty());
    assert!(
        source
            .seasons("default/shows", "series:tvdb:73739")
            .is_empty()
    );
    assert!(
        source
            .episodes("default/shows", "series:tvdb:73739", 1)
            .is_empty()
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
    assert!(source.titles("default/films", "movies").is_empty());
}

#[test]
fn a_fresh_source_reports_no_change() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(!source.changed());
}
