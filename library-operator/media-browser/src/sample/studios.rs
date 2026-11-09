// The invented studios. Every movie names a studio of its own and one
// that every movie shares, and every serial names a studio of its own, so
// a run with no catalog opens a studio's wall of one title and one of
// every movie.

use super::{FEATURES, SERIALS_LIBRARY, sorted, titles, trailing};
use crate::catalog::{Slot, Sort};

/// The studio every invented movie names after its own.
pub const SHARED: &str = "A Second Studio";

/// The studios one invented movie names.
pub fn of_movie(number: i64) -> Vec<String> {
    vec![format!("Studio {number:04}"), SHARED.into()]
}

/// The studio one invented serial names.
pub fn of_serial(number: i64) -> Vec<String> {
    vec![format!("Studio {number:02}")]
}

/// Every invented title that names the studio, in the sort's order.
pub fn wall(name: &str, sort: Sort) -> Vec<Slot> {
    let mut slots: Vec<Slot> = titles(FEATURES, sort)
        .into_iter()
        .chain(titles(SERIALS_LIBRARY, sort))
        .filter(|slot| {
            let number = trailing(&slot.id);
            let named = match slot.kind.as_str() {
                "movies" => of_movie(number),
                _ => of_serial(number),
            };
            named.iter().any(|studio| studio == name)
        })
        .collect();
    slots.sort_by(|one, other| sorted(sort, one, other));
    slots
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::{Query, Source};
    use crate::sample::Catalog;

    #[test]
    fn a_movie_s_own_studio_holds_that_movie_alone() {
        let answer = Catalog.wall(&Query::studio("Studio 0042"));
        assert_eq!(answer.name, "Studio 0042");
        let ids: Vec<&str> = answer.slots.iter().map(|slot| slot.id.as_str()).collect();
        assert_eq!(ids, ["movie:sample:0042"]);
    }

    #[test]
    fn the_shared_studio_holds_every_movie_and_no_serial() {
        let answer = Catalog.wall(&Query::studio(SHARED));
        assert_eq!(answer.slots.len(), super::super::MOVIES as usize);
        assert!(answer.slots.iter().all(|slot| slot.kind == "movies"));
    }

    #[test]
    fn a_serial_s_studio_holds_that_serial() {
        let answer = Catalog.wall(&Query::studio("Studio 07"));
        let ids: Vec<&str> = answer.slots.iter().map(|slot| slot.id.as_str()).collect();
        assert_eq!(ids, ["series:sample:07"]);
    }
}
