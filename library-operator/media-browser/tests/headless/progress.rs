// The --print-progress run, which a drill reads the progress store with.
// It opens no window, so the test runs the binary directly and not under
// cage.

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
