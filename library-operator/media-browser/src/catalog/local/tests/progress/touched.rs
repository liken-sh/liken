// A play the progress stream named, resolved against the real catalog and
// the real progress file to the works and the people it names.

use super::*;
use crate::catalog::Touched;

// The source over one film, one show, and the plays a case writes, with
// the plays the stream named since the last ask.
fn over(dir: &TempDir, plays: impl FnOnce(&Path)) -> LocalCatalog {
    let catalog = fixture(dir);
    let store = progress_fixture(dir);
    a_film(&catalog);
    a_show(&catalog);
    plays(&store);
    source_over(&catalog, store)
}

#[test]
fn a_play_of_a_film_names_the_film_and_its_people() {
    let dir = TempDir::new().unwrap();
    let mut source = over(&dir, |store| {
        film_play(store, "one", &["second", "first"], (600, 6000, 10));
    });
    source.shared.touch("one".into(), None);

    assert_eq!(
        source.touched(),
        [Touched {
            works: vec![(FILMS.into(), FILM.into())],
            people: names(&["first", "second"]),
        }]
    );
    assert_eq!(source.touched(), []);
}

#[test]
fn a_play_of_an_episode_names_its_series() {
    let dir = TempDir::new().unwrap();
    let mut source = over(&dir, |store| {
        show_play(store, "one", (600, 2760, 10), (1, 2));
    });
    source.shared.touch("one".into(), None);

    assert_eq!(
        source.touched(),
        [Touched {
            works: vec![(SHOWS.into(), SHOW.into())],
            people: names(&["first"]),
        }]
    );
}

#[test]
fn a_person_a_delete_took_off_the_play_is_named_beside_the_rest() {
    let dir = TempDir::new().unwrap();
    let mut source = over(&dir, |store| {
        film_play(store, "one", &["first"], (600, 6000, 10));
    });
    source.shared.touch("one".into(), Some("second".into()));

    assert_eq!(source.touched()[0].people, names(&["first", "second"]));
}

#[test]
fn a_play_no_work_of_the_catalog_answers_to_touches_nothing() {
    let dir = TempDir::new().unwrap();
    let mut source = over(&dir, |store| {
        insert_play(
            store,
            "one",
            &[("tmdb", "9999")],
            &["first"],
            (600, 6000, 10),
            (0, 0),
        );
    });
    source.shared.touch("one".into(), None);
    source.shared.touch("absent".into(), None);

    assert_eq!(source.touched(), []);
}
