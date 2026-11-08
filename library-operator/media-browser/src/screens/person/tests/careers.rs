// Invented careers for the tests of a person's page that need no
// catalog: the axis, the walk across it, and the facts line. Each credit is
// a film with a release year, or none, and a seen state.

use crate::catalog::{Query, Slot};
use crate::screens::Item;
use crate::screens::person::credit::{Credit, Seen};

fn query() -> Query {
    Query::Person {
        library: "screening/films".into(),
        path: ".contributors/A Player".into(),
    }
}

/// One film credit, the `number`th of its career, released in this year
/// or undated.
pub fn film(number: usize, year: Option<i64>, seen: Seen) -> Credit {
    let slot = Slot {
        library: "screening/films".into(),
        kind: "movies".into(),
        id: format!("film:{number}"),
        title: format!("Film {number}"),
        released: year.map(|year| year.to_string()).unwrap_or_default(),
        ..Slot::default()
    };
    Credit {
        item: Item::of(&query(), slot),
        year,
        seen,
        episodes: 0,
        run: None,
    }
}

/// A career of one film for each of these years, in this order, seen by
/// nobody.
pub fn career(years: &[i64]) -> Vec<Credit> {
    years
        .iter()
        .enumerate()
        .map(|(number, year)| film(number, Some(*year), Seen::Nobody))
        .collect()
}

/// A career of one film for each year, with the seen state each one
/// names.
pub fn seen(years: &[(i64, Seen)]) -> Vec<Credit> {
    years
        .iter()
        .enumerate()
        .map(|(number, (year, seen))| film(number, Some(*year), *seen))
        .collect()
}
