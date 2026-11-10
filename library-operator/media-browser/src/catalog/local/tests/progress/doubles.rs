// A file of several episodes through the read: the store holds one row for
// each play of the file, with a position in the whole file, and every
// screen reads one row for each episode the play covered.

use super::*;

// The double S01E01-E02 of 2,640 seconds, and the single S01E03 after it.
const DOUBLE: &str = "Some Show/S01E01-E02.mkv";

// One file with the probe's duration, and the link of each item it holds
// as the scanner writes it: the item, its part, and the file's parts.
fn insert_parted_file(catalog: &Path, file: &str, duration_ms: i64, items: &[(&str, i64, i64)]) {
    let connection = Connection::open(catalog).unwrap();
    connection
        .execute(
            "INSERT INTO files (library, path, type, role, duration_ms) \
             VALUES (?, ?, 'video', 'primary', ?)",
            (SHOWS, file, duration_ms),
        )
        .unwrap();
    for (item, part, parts) in items {
        connection
            .execute(
                "INSERT INTO file_items (library, path, item, part, parts) VALUES (?, ?, ?, ?, ?)",
                (SHOWS, file, item, part, parts),
            )
            .unwrap();
    }
}

// The show with the double and the single after it.
fn a_show_with_a_double(catalog: &Path, duration_ms: i64) {
    a_show(catalog);
    for episode in 1..=3 {
        insert_episode(
            catalog,
            SHOWS,
            &format!("episode:{episode}"),
            SHOW,
            1,
            episode,
        );
    }
    insert_parted_file(
        catalog,
        DOUBLE,
        duration_ms,
        &[("episode:1", 1, 2), ("episode:2", 2, 2)],
    );
    insert_parted_file(
        catalog,
        "Some Show/S01E03.mkv",
        1_320_000,
        &[("episode:3", 0, 0)],
    );
}

// Write a row the way a row from outside the cluster or a mark lands: with
// no library.
fn from_outside(store: &Path, play: &str) {
    Connection::open(store)
        .unwrap()
        .execute("UPDATE plays SET library = '' WHERE play = ?", [play])
        .unwrap();
}

// What the series page reads for each episode: its number, its position,
// its duration, and whether it finished.
fn episodes(catalog: &Path, store: PathBuf) -> Vec<(i64, i64, i64, bool)> {
    source_over(catalog, store)
        .episode_progress(SHOWS, SHOW, &names(&["first"]))
        .into_iter()
        .map(|row| (row.episode, row.position, row.duration, row.finished))
        .collect()
}

// The stages of the plan's table, each from one row of the store.
#[test]
fn each_stage_of_a_play_of_the_double_reads_one_row_for_each_episode_it_covers() {
    let cases = [
        ("play on E01 at 600", 1, 600, vec![(1, 600, 1_320, false)]),
        ("at 1,300", 1, 1_300, vec![(1, 1_300, 1_320, true)]),
        (
            "at 1,800",
            1,
            1_800,
            vec![(1, 1_320, 1_320, true), (2, 480, 1_320, false)],
        ),
        (
            "to the end",
            1,
            2_640,
            vec![(1, 1_320, 1_320, true), (2, 1_320, 1_320, true)],
        ),
        (
            "play on E02 alone at 1,800",
            2,
            1_800,
            vec![(2, 480, 1_320, false)],
        ),
    ];
    for (case, named, position, want) in cases {
        let dir = TempDir::new().unwrap();
        let catalog = fixture(&dir);
        let store = progress_fixture(&dir);
        a_show_with_a_double(&catalog, 2_640_000);
        show_play(&store, "den", (position, 2_640, 10), (1, named));
        assert_eq!(episodes(&catalog, store), want, "{case}");
    }
}

#[test]
fn a_jellyfin_row_of_the_double_reads_for_both_episodes() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show_with_a_double(&catalog, 2_640_000);
    show_play(&store, "jellyfin-user-item", (1_800, 2_640, 10), (1, 1));
    from_outside(&store, "jellyfin-user-item");
    let mut source = source_over(&catalog, store);

    let rows: Vec<(String, i64, Origin)> = watching(&mut source, &["first"])
        .into_iter()
        .map(|resume| {
            let progress = resume.progress;
            (progress.play, progress.episode, progress.origin)
        })
        .collect();
    assert_eq!(
        rows,
        [
            ("jellyfin-user-item#01".to_string(), 1, Origin::Outside),
            ("jellyfin-user-item#02".to_string(), 2, Origin::Outside),
        ]
    );
}

// A mark already holds one episode's seconds, so the read leaves it alone.
#[test]
fn a_mark_of_the_second_episode_reads_unchanged() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show_with_a_double(&catalog, 2_640_000);
    show_play(&store, "mark-den-1794107779", (1_320, 1_320, 10), (1, 2));
    from_outside(&store, "mark-den-1794107779");

    assert_eq!(episodes(&catalog, store), [(2, 1_320, 1_320, true)]);
}

// A file the probe found no duration for splits on the row's own duration,
// which is mpv's duration of the file.
#[test]
fn a_double_the_probe_could_not_read_splits_on_the_rows_duration() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show_with_a_double(&catalog, 0);
    show_play(&store, "den", (1_800, 2_640, 10), (1, 1));

    assert_eq!(
        episodes(&catalog, store),
        [(1, 1_320, 1_320, true), (2, 480, 1_320, false)]
    );
}

// The single after the double reads as it always did.
#[test]
fn an_episode_alone_in_its_file_reads_as_its_row() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show_with_a_double(&catalog, 2_640_000);
    show_play(&store, "den", (600, 1_320, 10), (1, 3));

    assert_eq!(episodes(&catalog, store), [(3, 600, 1_320, false)]);
}

// `--print-progress` prints the continue-watching rows, so a drill reads
// each episode's state after the split.
#[test]
fn the_print_of_the_store_carries_one_line_for_each_episode() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show_with_a_double(&catalog, 2_640_000);
    show_play(&store, "den", (1_800, 2_640, 10), (1, 1));
    let mut source = source_over(&catalog, store);

    let lines: Vec<String> = watching(&mut source, &["first"])
        .iter()
        .map(crate::catalog::progress::line)
        .collect();
    assert!(
        lines[0].contains("\tS1E1\t1320/1320\tfinished\t"),
        "{lines:?}"
    );
    assert!(
        lines[1].contains("\tS1E2\t480/1320\trunning\t"),
        "{lines:?}"
    );
}

// A Jellyfin row as the jellyfin role writes it: no library, ended, at the
// end of a double whose probe duration has an odd number of seconds. The
// read resolves the work by the series' alias and the file by the alias's
// library, so the empty library of the row does not matter.
#[test]
fn an_ended_jellyfin_row_at_the_end_of_an_odd_double_finishes_both_episodes() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_show(&catalog);
    insert_episode(&catalog, SHOWS, "episode:tvdb:72974:s01e01", SHOW, 1, 1);
    insert_episode(&catalog, SHOWS, "episode:tvdb:72974:s01e02", SHOW, 1, 2);
    insert_parted_file(
        &catalog,
        "Tenacious D/Season 01/S01E01-E02 a b.mkv",
        1_321_283,
        &[
            ("episode:tvdb:72974:s01e01", 1, 2),
            ("episode:tvdb:72974:s01e02", 2, 2),
        ],
    );
    show_play(&store, "jellyfin-u-i", (1_321, 1_321, 10), (1, 1));
    from_outside(&store, "jellyfin-u-i");
    end_play(&store, "jellyfin-u-i", 11);
    assert_eq!(
        episodes(&catalog, store),
        [(1, 660, 660, true), (2, 661, 661, true)]
    );
}
