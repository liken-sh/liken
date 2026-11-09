// A studio's wall as the local catalog answers it: every title whose body
// names the studio, across the libraries of both kinds, in each of the
// three orders.

use super::*;

const FILMS: &str = "default/films";
const SHOWS: &str = "default/shows";

// One title of one item table, with the studios its body names.
fn insert(path: &Path, table: &str, id: &str, sort_key: &str, released: &str, studios: &[&str]) {
    let library = match table {
        "movies" => FILMS,
        _ => SHOWS,
    };
    let body = serde_json::json!({ "studios": studios }).to_string();
    Connection::open(path)
        .unwrap()
        .execute(
            &format!(
                "INSERT INTO {table} (library, id, kind, title, sort_key, released, body) \
                 VALUES (?, ?, ?, ?, ?, ?, ?)"
            ),
            (library, id, table, id, sort_key, released, body),
        )
        .unwrap();
}

// Three titles of Gullwing Pictures across two libraries, one of them
// shared with a second studio, and two titles it did not make: one of
// another studio whose name holds Gullwing's, and one with no studio.
fn gullwing(path: &Path) {
    insert(path, "movies", "a", "amber", "1999", &["Gullwing Pictures"]);
    insert(
        path,
        "movies",
        "b",
        "brine",
        "2004",
        &["Lantern Works", "Gullwing Pictures"],
    );
    insert(path, "series", "c", "cusp", "1994", &["Gullwing Pictures"]);
    insert(
        path,
        "movies",
        "d",
        "delta",
        "2010",
        &["Gullwing Pictures Two"],
    );
    insert(path, "movies", "e", "ember", "2001", &[]);
}

fn ids(answer: &Answer) -> Vec<String> {
    answer.slots.iter().map(|slot| slot.id.clone()).collect()
}

#[test]
fn a_studio_wall_answers_each_of_the_three_orders() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    gullwing(&path);

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    let mut wall = |sort| {
        source.wall(&Query::Studio {
            name: "Gullwing Pictures".into(),
            sort,
        })
    };
    assert_eq!(ids(&wall(Sort::Newest)), ["b", "a", "c"]);
    assert_eq!(ids(&wall(Sort::Oldest)), ["c", "a", "b"]);
    assert_eq!(ids(&wall(Sort::Title)), ["a", "b", "c"]);
}

#[test]
fn a_studio_wall_is_headed_by_its_name_and_the_counts_by_kind() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    gullwing(&path);

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    let query = Query::studio("Gullwing Pictures");
    let answer = source.wall(&query);
    let counts = Counts::of(answer.slots.iter().map(|slot| slot.kind.as_str()));
    assert_eq!(
        query.heading(&answer.name, counts),
        "Gullwing Pictures · 2 movies, 1 series"
    );
}

#[test]
fn a_studio_no_title_names_answers_an_empty_wall() {
    let dir = TempDir::new().unwrap();
    let path = fixture(&dir);
    gullwing(&path);

    let mut source = LocalCatalog::new(&path, NO_AGENT);
    let answer = source.wall(&Query::studio("Gullwing"));
    assert!(answer.slots.is_empty());
}
