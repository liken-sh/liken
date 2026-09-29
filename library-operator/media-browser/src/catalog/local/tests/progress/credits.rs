// The credits marks of a work's main file move the finished line of a
// play to the start of the credits, where they start in the second half
// of the work. Each read that answers whole plays reads those marks.

use super::*;

// One span of one file of one library, as the walk writes it off the marks
// ledger, in milliseconds. An absent edge is a null in the table.
fn insert_mark(
    path: &Path,
    library: &str,
    file: &str,
    ordinal: i64,
    kind: &str,
    span: (Option<i64>, Option<i64>),
) {
    Connection::open(path)
        .unwrap()
        .execute(
            "INSERT INTO marks (library, path, ordinal, kind, start_ms, end_ms, source) \
             VALUES (?, ?, ?, ?, ?, ?, 'theintrodb')",
            (library, file, ordinal, kind, span.0, span.1),
        )
        .unwrap();
}

// The film's main file, whose 9 minutes of credits start at 2:21:00 of a
// 2:30:00 film, and an intro mark in the second half that is not credits.
fn a_film_with_credits(catalog: &Path) {
    a_film(catalog);
    insert_main_file(catalog, FILMS, "Some Film.mkv", FILM);
    let file = "Some Film.mkv";
    insert_mark(catalog, FILMS, file, 0, "intro", (Some(5_000_000), None));
    insert_mark(
        catalog,
        FILMS,
        file,
        1,
        "credits",
        (Some(8_460_000), Some(9_000_000)),
    );
}

// Whether each play of the film reads as finished, in the order the
// continue-watching row answers them.
fn finished_films(catalog: &Path, store: PathBuf) -> Vec<(String, bool)> {
    let mut source = source_over(catalog, store);
    watching(&mut source, &["first"])
        .into_iter()
        .map(|resume| (resume.progress.play, resume.progress.finished))
        .collect()
}

#[test]
fn a_film_stopped_in_its_credits_is_finished_with_more_than_five_minutes_left() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film_with_credits(&catalog);
    film_play(&store, "in-credits", &["first"], (8_460, 9_000, 20));
    film_play(&store, "before-credits", &["first"], (8_459, 9_000, 10));

    assert_eq!(
        finished_films(&catalog, store),
        [
            ("in-credits".to_string(), true),
            ("before-credits".to_string(), false)
        ]
    );
}

#[test]
fn a_film_whose_file_has_no_mark_keeps_the_remaining_time_rule() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    insert_main_file(&catalog, FILMS, "Some Film.mkv", FILM);
    film_play(&store, "in-credits", &["first"], (8_460, 9_000, 20));
    film_play(&store, "at-five-minutes", &["first"], (8_700, 9_000, 10));

    assert_eq!(
        finished_films(&catalog, store),
        [
            ("in-credits".to_string(), false),
            ("at-five-minutes".to_string(), true)
        ]
    );
}

#[test]
fn every_play_of_one_film_reads_its_credits() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film_with_credits(&catalog);
    film_play(&store, "in-credits", &["first"], (8_460, 9_000, 10));
    let mut source = source_over(&catalog, store);

    assert!(
        source.plays_of(FILMS, FILM, &names(&["first"]))[0]
            .progress
            .finished
    );
}

// The show's first episode and its main file, whose credits start 30
// seconds before the end of 22 minutes, and no mark on the second
// episode's file.
fn a_show_with_credits(catalog: &Path) {
    a_show(catalog);
    insert_episode(catalog, SHOWS, "episode:1", SHOW, 1, 1);
    insert_episode(catalog, SHOWS, "episode:2", SHOW, 1, 2);
    insert_main_file(catalog, SHOWS, "S01E01.mkv", "episode:1");
    insert_main_file(catalog, SHOWS, "S01E02.mkv", "episode:2");
    insert_mark(
        catalog,
        SHOWS,
        "S01E01.mkv",
        0,
        "credits",
        (Some(1_290_000), None),
    );
}

#[test]
fn an_episode_reads_the_credits_of_its_own_file() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show_with_credits(&catalog);
    show_play(&store, "s01e01", (1_254, 1_320, 10), (1, 1));
    show_play(&store, "s01e02", (1_254, 1_320, 20), (1, 2));
    let mut source = source_over(&catalog, store);

    let finished: Vec<(i64, bool)> = source
        .episode_progress(SHOWS, SHOW, &names(&["first"]))
        .iter()
        .map(|row| (row.episode, row.finished))
        .collect();
    assert_eq!(finished, [(1, false), (2, true)]);
}

#[test]
fn an_episode_play_in_its_credits_is_finished_on_the_continue_watching_row() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show_with_credits(&catalog);
    show_play(&store, "in-credits", (1_290, 1_320, 20), (1, 1));
    show_play(&store, "before-credits", (1_289, 1_320, 10), (1, 1));
    let mut source = source_over(&catalog, store);

    let finished: Vec<(String, bool)> = watching(&mut source, &["first"])
        .into_iter()
        .map(|resume| (resume.progress.play, resume.progress.finished))
        .collect();
    assert_eq!(
        finished,
        [
            ("in-credits".to_string(), true),
            ("before-credits".to_string(), false)
        ]
    );
}
