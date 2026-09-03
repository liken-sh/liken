// The reads behind the stripes on a title's page and behind a person's
// own page: one title's credits, one person, and every title that person
// is credited in across the libraries that share their ids.

use super::*;
use crate::catalog::{CreditSlot, Credits, Work};

// One person's entry in one library's store. `files` is the biography and
// the headshot, in that order, as the contributor facts write them.
fn insert_contributor(
    path: &Path,
    library: &str,
    contributor: &str,
    name: &str,
    files: (i64, i64),
) {
    let (biography, headshot) = files;
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO contributors (library, path, name, born, died, biography, headshot) \
             VALUES (?, ?, ?, '1950-01-02', '', ?, ?)",
            (library, contributor, name, biography, headshot),
        )
        .unwrap();
}

// One id of one person, which is the only thing that joins two libraries'
// copies of them.
fn insert_alias(path: &Path, library: &str, scheme: &str, id: &str, contributor: &str) {
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO contributor_aliases (library, scheme, id, path) VALUES (?, ?, ?, ?)",
            (library, scheme, id, contributor),
        )
        .unwrap();
}

// One credited person on one title. `billing` is the key beside the item,
// and the crew hold the billing after the cast.
fn insert_credit(
    path: &Path,
    library: &str,
    item: &str,
    billing: i64,
    person: (&str, &str),
    part: (&str, &str),
) {
    let (contributor, name) = person;
    let (part, role) = part;
    let connection = Connection::open(path).unwrap();
    connection
        .execute(
            "INSERT INTO credits (library, item, billing, contributor, name, part, role) \
             VALUES (?, ?, ?, ?, ?, ?, ?)",
            (library, item, billing, contributor, name, part, role),
        )
        .unwrap();
}

// A movie with two players, a director, and a writer, where the director
// is also the second player, and only the first player has a headshot.
fn a_credited_film(path: &Path) {
    insert_page(path, "default/films", "one", "1994", "", BODY);
    insert_contributor(
        path,
        "default/films",
        ".contributors/first",
        "A First",
        (0, 1),
    );
    insert_contributor(
        path,
        "default/films",
        ".contributors/second",
        "A Second",
        (0, 0),
    );
    insert_credit(
        path,
        "default/films",
        "one",
        0,
        (".contributors/first", "A First"),
        ("actor", "The Part"),
    );
    insert_credit(
        path,
        "default/films",
        "one",
        1,
        (".contributors/second", "A Second"),
        ("actor", "The Other Part"),
    );
    insert_credit(
        path,
        "default/films",
        "one",
        2,
        (".contributors/second", "A Second"),
        ("director", ""),
    );
    insert_credit(
        path,
        "default/films",
        "one",
        3,
        ("", "An Unresolved Writer"),
        ("writer", ""),
    );
}

#[test]
fn a_title_splits_its_credits_by_part_in_billing_order() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_credited_film(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let credits = source.credits("default/films", "one");
    assert_eq!(
        credits.cast,
        [
            CreditSlot {
                name: "A First".into(),
                role: "The Part".into(),
                contributor: ".contributors/first".into(),
                headshot: true,
            },
            CreditSlot {
                name: "A Second".into(),
                role: "The Other Part".into(),
                contributor: ".contributors/second".into(),
                headshot: false,
            },
        ]
    );
    assert_eq!(credits.directors.len(), 1);
    assert_eq!(credits.directors[0].name, "A Second");
    assert_eq!(credits.writers.len(), 1);
}

#[test]
fn a_credit_the_store_holds_no_entry_for_carries_no_headshot() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_credited_film(&path);
    // A person with no entry keys on the empty path, which no entry's own
    // path ever equals.
    insert_contributor(&path, "default/films", "", "Nobody", (1, 1));

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let credits = source.credits("default/films", "one");
    assert_eq!(credits.writers[0].contributor, "");
    assert!(!credits.writers[0].headshot);
}

#[test]
fn a_title_with_no_credits_carries_three_empty_stripes() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_page(&path, "default/films", "one", "1994", "", BODY);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(source.credits("default/films", "one"), Credits::default());
}

#[test]
fn a_person_comes_back_with_the_dates_and_the_files_their_entry_holds() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_contributor(
        &path,
        "default/films",
        ".contributors/first",
        "A First",
        (1, 1),
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let person = source
        .person("default/films", ".contributors/first")
        .expect("the store holds this person");
    assert_eq!(person.name, "A First");
    assert_eq!(person.born, "1950-01-02");
    assert_eq!(person.died, "");
    assert!(person.biography);
    assert!(person.headshot);
    assert_eq!(person.headshot_library, "default/films");
    assert_eq!(person.headshot_path, ".contributors/first");
    assert_eq!(person.biography_library, "default/films");
}

#[test]
fn a_person_no_library_holds_comes_back_as_nothing() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_contributor(
        &path,
        "default/films",
        ".contributors/first",
        "A First",
        (1, 1),
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert_eq!(source.person("default/films", ".contributors/other"), None);
    assert_eq!(source.person("default/shows", ".contributors/first"), None);
}

// The same person in two libraries, joined by one shared id. The films
// entry holds neither file, and the shows entry holds both, under a
// directory of its own.
fn a_person_in_two_libraries(path: &Path) {
    insert_contributor(
        path,
        "default/films",
        ".contributors/first",
        "A First",
        (0, 0),
    );
    insert_alias(path, "default/films", "tmdb", "31", ".contributors/first");
    insert_contributor(
        path,
        "default/shows",
        ".contributors/a-first",
        "A First",
        (1, 1),
    );
    insert_alias(path, "default/shows", "tmdb", "31", ".contributors/a-first");
}

#[test]
fn a_persons_files_come_from_whichever_library_holds_them() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_person_in_two_libraries(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let person = source
        .person("default/films", ".contributors/first")
        .expect("the store holds this person");
    assert_eq!(person.library, "default/films");
    assert_eq!(person.path, ".contributors/first");
    assert!(person.headshot);
    assert!(person.biography);
    assert_eq!(person.headshot_library, "default/shows");
    assert_eq!(person.headshot_path, ".contributors/a-first");
    assert_eq!(person.biography_library, "default/shows");
    assert_eq!(person.biography_path, ".contributors/a-first");
}

#[test]
fn a_person_no_library_holds_a_headshot_for_names_no_library_for_it() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    insert_contributor(
        &path,
        "default/films",
        ".contributors/first",
        "A First",
        (0, 0),
    );

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let person = source
        .person("default/films", ".contributors/first")
        .expect("the store holds this person");
    assert!(!person.headshot);
    assert_eq!(person.headshot_library, "");
    assert_eq!(person.headshot_path, "");
}

// One person credited in three titles across two libraries: a film they
// wrote and directed, a film with no release, and a series they acted in.
fn a_worked_person(path: &Path) {
    a_person_in_two_libraries(path);
    insert_page(path, "default/films", "one", "1994", "", BODY);
    insert_page(path, "default/films", "two", "", "", BODY);
    insert_series_page(path, "default/shows", "three", "2004", BODY);
    insert_credit(
        path,
        "default/films",
        "one",
        0,
        (".contributors/first", "A First"),
        ("director", ""),
    );
    insert_credit(
        path,
        "default/films",
        "one",
        1,
        (".contributors/first", "A First"),
        ("writer", ""),
    );
    insert_credit(
        path,
        "default/films",
        "two",
        0,
        (".contributors/first", "A First"),
        ("actor", "Tony"),
    );
    insert_credit(
        path,
        "default/shows",
        "three",
        0,
        (".contributors/a-first", "A First"),
        ("actor", ""),
    );
}

#[test]
fn a_persons_works_gather_every_library_newest_first() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_worked_person(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    let works = source.works("default/films", ".contributors/first");
    assert_eq!(
        works,
        [
            Work {
                library: "default/shows".into(),
                kind: "series".into(),
                id: "three".into(),
                title: "Serial three".into(),
                released: "2004".into(),
                art: "three.jpg".into(),
                parts: "Actor".into(),
            },
            Work {
                library: "default/films".into(),
                kind: "movies".into(),
                id: "one".into(),
                title: "Film one".into(),
                released: "1994".into(),
                art: "one.jpg".into(),
                parts: "Director, Writer".into(),
            },
            Work {
                library: "default/films".into(),
                kind: "movies".into(),
                id: "two".into(),
                title: "Film two".into(),
                released: String::new(),
                art: "two.jpg".into(),
                parts: "as Tony".into(),
            },
        ]
    );
}

#[test]
fn a_person_credited_in_nothing_has_an_empty_wall() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    a_person_in_two_libraries(&path);

    let mut source = SidecarSource::new(&path, NO_AGENT);
    assert!(
        source
            .works("default/films", ".contributors/first")
            .is_empty()
    );
}
