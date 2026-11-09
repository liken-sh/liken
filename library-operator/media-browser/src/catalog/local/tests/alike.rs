// The films the local catalog answers as alike to one film, and the score
// each one carries.

use super::*;

const FILMS: &str = "default/films";

// One film with its leading genre and its IMDb rating.
fn film(path: &Path, library: &str, id: &str, genre: &str, rating: f64) {
    let connection = Connection::open(path).unwrap();
    let body = serde_json::json!({ "ratings": { "imdb": rating } }).to_string();
    connection
        .execute(
            "INSERT INTO movies (library, id, kind, title, body) VALUES (?, ?, 'movies', ?, ?)",
            (library, id, format!("Film {id}"), body),
        )
        .unwrap();
    connection
        .execute(
            "INSERT INTO genres (library, item, rank, genre) VALUES (?, ?, 0, ?)",
            (library, id, genre),
        )
        .unwrap();
}

// One credit of one film at its billing.
fn credit(path: &Path, library: &str, id: &str, billing: i64, who: &str, part: &str) {
    Connection::open(path)
        .unwrap()
        .execute(
            "INSERT INTO credits (library, item, billing, contributor, name, part) \
             VALUES (?, ?, ?, ?, ?, ?)",
            (
                library,
                id,
                billing,
                format!(".contributors/{who}"),
                who,
                part,
            ),
        )
        .unwrap();
}

// The film the page is about, "here": a drama directed by Ada, with
// Bram, Cora, Dov, and Eli billed first to fourth and Fay fifth.
fn here(path: &Path) {
    film(path, FILMS, "here", "Drama", 7.0);
    for (billing, who) in ["Bram", "Cora", "Dov", "Eli", "Fay"].iter().enumerate() {
        credit(path, FILMS, "here", billing as i64, who, "actor");
    }
    credit(path, FILMS, "here", 9, "Ada", "director");
}

fn scores(source: &mut LocalCatalog) -> Vec<(String, i64)> {
    let mut found: Vec<(String, i64)> = source
        .alike(FILMS, "here")
        .into_iter()
        .map(|alike| (alike.slot.id, alike.score))
        .collect();
    found.sort();
    found
}

#[test]
fn a_shared_director_scores_three_and_a_shared_lead_two_and_the_genre_one() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    here(&path);
    // Ada directed it, and it is a drama too: 3 + 1.
    film(&path, FILMS, "directed", "Drama", 6.0);
    credit(&path, FILMS, "directed", 9, "Ada", "director");
    // Bram and Cora lead it, and it is a comedy: 2 + 2.
    film(&path, FILMS, "led", "Comedy", 6.0);
    credit(&path, FILMS, "led", 0, "Cora", "actor");
    credit(&path, FILMS, "led", 1, "Bram", "actor");
    // Dov leads it: 2.
    film(&path, FILMS, "once", "Comedy", 6.0);
    credit(&path, FILMS, "once", 3, "Dov", "actor");

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    assert_eq!(
        scores(&mut source),
        [
            ("directed".to_string(), 4),
            ("led".to_string(), 4),
            ("once".to_string(), 2)
        ]
    );
}

#[test]
fn a_film_that_shares_only_the_genre_or_a_minor_part_is_not_alike() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    here(&path);
    film(&path, FILMS, "genre", "Drama", 9.0);
    // Fay is fifth here, and Bram is fifth there, so neither is a lead of
    // both films.
    film(&path, FILMS, "minor", "Drama", 9.0);
    credit(&path, FILMS, "minor", 0, "Fay", "actor");
    credit(&path, FILMS, "minor", 4, "Bram", "actor");
    // Ada acted in it and directed nothing, so the parts differ.
    film(&path, FILMS, "acted", "Drama", 9.0);
    credit(&path, FILMS, "acted", 0, "Ada", "actor");

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    assert!(scores(&mut source).is_empty());
}

#[test]
fn another_library_s_films_are_not_alike() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    here(&path);
    film(&path, "default/other", "elsewhere", "Drama", 9.0);
    credit(&path, "default/other", "elsewhere", 9, "Ada", "director");

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    assert!(scores(&mut source).is_empty());
}

#[test]
fn an_alike_film_carries_its_slot_and_its_imdb_rating() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    here(&path);
    film(&path, FILMS, "directed", "Comedy", 6.4);
    credit(&path, FILMS, "directed", 9, "Ada", "director");

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    let found = source.alike(FILMS, "here");
    assert_eq!(found.len(), 1);
    assert_eq!(found[0].slot.library, FILMS);
    assert_eq!(found[0].slot.kind, "movies");
    assert_eq!(found[0].slot.title, "Film directed");
    assert_eq!(found[0].score, 3);
    assert_eq!(found[0].rating, 6.4);
}

#[test]
fn a_film_with_no_rating_reads_as_zero() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    here(&path);
    Connection::open(&path)
        .unwrap()
        .execute(
            "INSERT INTO movies (library, id, kind, title) VALUES (?, 'bare', 'movies', 'Bare')",
            [FILMS],
        )
        .unwrap();
    credit(&path, FILMS, "bare", 9, "Ada", "director");

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    let found = source.alike(FILMS, "here");
    assert_eq!(found[0].rating, 0.0);
    assert_eq!(found[0].score, 3);
}
