// The attached progress file as the source treats it: absent, present and
// never written, and shared with the second source.

use super::*;

#[test]
fn a_source_with_no_progress_store_reads_no_progress() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    a_film(&catalog);
    let mut source = SidecarSource::new(&catalog, NO_AGENT);

    assert_eq!(watching(&mut source, &["first"]), []);
    assert_eq!(source.plays_of(FILMS, FILM, &names(&["first"])), []);
    assert_eq!(source.episode_progress(SHOWS, SHOW, &names(&["first"])), []);
}

#[test]
fn a_progress_file_that_is_not_there_leaves_the_reads_empty() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    a_film(&catalog);
    let mut source = source_over(&catalog, dir.path().join("absent.db"));

    assert_eq!(watching(&mut source, &["first"]), []);
}

#[test]
fn the_reads_never_write_the_progress_file() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    a_show(&catalog);
    film_play(&store, "one", &["first"], (600, 6000, 10));
    let before = fs::read(&store).unwrap();
    let mut source = source_over(&catalog, store.clone());

    watching(&mut source, &["first"]);
    source.plays_of(FILMS, FILM, &names(&["first"]));
    source.episode_progress(SHOWS, SHOW, &names(&["first"]));

    assert_eq!(fs::read(&store).unwrap(), before);
}

#[test]
fn the_reader_source_reads_the_progress_store_too() {
    let dir = TempDir::new().unwrap();
    let catalog = fixture(&dir);
    let store = progress_fixture(&dir);
    a_film(&catalog);
    film_play(&store, "one", &["first"], (600, 6000, 10));
    let mut source = source_over(&catalog, store);

    let mut reader = source.reader().expect("the sidecar gives a second source");
    assert_eq!(watching(reader.as_mut(), &["first"]).len(), 1);
}
