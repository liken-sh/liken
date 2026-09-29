// The progress store under a run: the --print-progress run, which a drill
// reads the store with, and a film's page drawn for an audience that is in
// the middle of the film. The print opens no window, so it runs the binary
// directly and not under cage.

use std::process::Output;

use super::*;

// A progress file beside the catalog fixture, with one play of the
// fixture's own movie and the alias that resolves it.
fn store(dir: &Path, database: &Path) -> PathBuf {
    rusqlite::Connection::open(database)
        .expect("open the fixture catalog")
        .execute(
            "INSERT INTO aliases (library, alias, item, source) \
             VALUES ('drill/films', 'movie:path:one', 'movie:path:one', 'folder')",
            (),
        )
        .expect("insert the fixture alias");

    let path = dir.join("progress.db");
    let connection = rusqlite::Connection::open(&path).expect("open the fixture store");
    let schema = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../corrosion/progress-schema/progress.sql"
    );
    connection
        .execute_batch(&std::fs::read_to_string(schema).expect("read the progress schema"))
        .expect("apply the progress schema");
    connection
        .execute(
            "INSERT INTO plays (play, player, library, position, duration, recorded) \
             VALUES ('one', 'den', 'films', 600, 5400, 1757000000)",
            (),
        )
        .expect("insert the fixture play");
    connection
        .execute(
            "INSERT INTO play_aliases (play, provider, id) VALUES ('one', 'path', 'one')",
            (),
        )
        .expect("insert the fixture play alias");
    connection
        .execute(
            "INSERT INTO play_people (play, person) VALUES ('one', 'first')",
            (),
        )
        .expect("insert the fixture play person");

    path
}

fn printed(flags: &[&str]) -> Output {
    Command::new(BINARY)
        .args(flags)
        .output()
        .expect("the binary prints the progress")
}

#[test]
fn the_print_writes_one_line_per_play_and_ends() {
    let dir = workspace("print-progress");
    let (database, _volume) = fixture(&dir);
    let progress = store(&dir, &database);

    let run = printed(&[
        "--catalog",
        &text(&database),
        "--progress",
        &text(&progress),
        "--audience",
        "first",
        "--print-progress",
    ]);

    assert_eq!(run.status.code(), Some(0));
    assert_eq!(
        String::from_utf8_lossy(&run.stdout),
        "movie\tdrill/films\tmovie:path:one\tVespera Coppice\t-\t600/5400\trunning\t\
         2025-09-04T15:33:20Z\texact\n"
    );
}

#[test]
fn a_print_with_no_progress_store_writes_nothing() {
    let dir = workspace("print-progress-none");
    let (database, _volume) = fixture(&dir);

    let run = printed(&["--catalog", &text(&database), "--print-progress"]);

    assert_eq!(run.status.code(), Some(0));
    assert_eq!(String::from_utf8_lossy(&run.stdout), "");
}

#[test]
fn a_print_with_no_catalog_stops_the_run() {
    let run = printed(&["--print-progress"]);

    assert_eq!(run.status.code(), Some(2));
    assert_eq!(
        String::from_utf8_lossy(&run.stderr).trim(),
        "media-browser: --print-progress needs --catalog"
    );
}

// The film's page for an audience in the middle of it. The play puts the
// film on the continue-watching row, which holds focus on the home page,
// so select opens the page. Down puts focus on the first mark of the watch
// line and right on the second, so the frame draws the held bar, the
// status, and both marks.
#[test]
fn a_page_in_the_middle_of_a_film_draws_its_status_line() {
    let dir = workspace("status-line");
    let frames = dir.join("frames");
    let (database, _volume) = fixture(&dir);
    let progress = store(&dir, &database);

    let run = headless(
        &dir,
        &[
            "--catalog",
            &text(&database),
            "--updates",
            "http://127.0.0.1:1",
            "--progress",
            &text(&progress),
            "--audience",
            "first",
            "--script",
            "0.5:enter,1.2:down,1.6:right",
            "--capture",
            &text(&frames),
            "--capture-at",
            "2.0",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("002.00.png"), &run);
}

// The page of a film the audience finished. A finished film leaves the
// continue-watching row, so the way in is the library's wall, as on a
// page no play names. Down puts focus on Clear progress, the one mark a
// finished film offers, beside the watched status and its check.
#[test]
fn a_page_of_a_finished_film_draws_its_check() {
    let dir = workspace("status-line-finished");
    let frames = dir.join("frames");
    let (database, _volume) = fixture(&dir);
    let progress = store(&dir, &database);
    rusqlite::Connection::open(&progress)
        .expect("open the fixture store")
        .execute(
            "UPDATE plays SET position = duration WHERE play = 'one'",
            (),
        )
        .expect("finish the fixture play");

    let run = headless(
        &dir,
        &[
            "--catalog",
            &text(&database),
            "--updates",
            "http://127.0.0.1:1",
            "--progress",
            &text(&progress),
            "--audience",
            "first",
            "--script",
            "0.5:enter,1.5:enter,2.0:down",
            "--capture",
            &text(&frames),
            "--capture-at",
            "2.4",
            "--size",
            "1920x1080",
            "--quit-after",
            "25",
        ],
    );

    assert_eq!(run.exit, "0", "{}", run.log);
    drawn(&frames.join("002.40.png"), &run);
}
