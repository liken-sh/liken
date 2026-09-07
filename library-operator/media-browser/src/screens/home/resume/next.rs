// What follows a work the audience finished: the next member of its set,
// and the next held member of every franchise it belongs to. A person who
// finished a film is offered the film after it, not the film they just
// watched.

use super::MOVIES;
use crate::catalog::franchise;
use crate::catalog::{Entry, Slot, Source};

/// The works that follow this one, as the slots the row draws: the next
/// member of the set `set` names, in release order, then the next held
/// member of each franchise the work belongs to, in story order. `set` is
/// empty for a work in no set, such as a series.
pub fn after(source: &mut dyn Source, library: &str, id: &str, set: &str) -> Vec<Slot> {
    let mut slots: Vec<Slot> = in_set(source, library, id, set).into_iter().collect();
    slots.extend(in_franchises(source, library, id));
    slots
}

// The member of the set after this one in release order, and nothing where
// the work is the last of the set or the catalog holds no such set.
fn in_set(source: &mut dyn Source, library: &str, id: &str, set: &str) -> Option<Slot> {
    if set.is_empty() {
        return None;
    }
    let members = source.set(library, set)?.members;
    let at = members.iter().position(|member| member.id == id)?;
    let member = members.into_iter().nth(at + 1)?;
    Some(Slot::of(library, MOVIES, member))
}

// One slot for each franchise the work belongs to: the next member of that
// order some library holds. A franchise the work ends yields nothing.
fn in_franchises(source: &mut dyn Source, library: &str, id: &str) -> Vec<Slot> {
    source
        .franchises_of(library, id)
        .into_iter()
        .filter_map(|membership| in_order(&membership.members, library, id))
        .collect()
}

// The first held member of one order after this work, as the slot of its own
// kind, so a film draws as a film and a show as a show.
fn in_order(members: &[Entry], library: &str, id: &str) -> Option<Slot> {
    let at = members.iter().position(|entry| names(entry, library, id))?;
    let held = members[at + 1..]
        .iter()
        .find_map(|entry| entry.held.clone())?;
    Some(franchise::slot(held))
}

// Whether one entry of an order is the work this library and id name.
fn names(entry: &Entry, library: &str, id: &str) -> bool {
    entry
        .held
        .as_ref()
        .is_some_and(|held| held.library == library && held.id == id)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::Held;

    const FILMS: &str = "screening/films";

    fn entry(position: i64, library: &str, id: &str, kind: &str) -> Entry {
        Entry {
            position,
            held: Some(Held {
                library: library.to_string(),
                id: id.to_string(),
                kind: kind.to_string(),
                title: format!("Entry {position}"),
                ..Held::default()
            }),
            ..Entry::default()
        }
    }

    fn gap(position: i64) -> Entry {
        Entry {
            position,
            title: format!("Entry {position}"),
            ..Entry::default()
        }
    }

    fn order() -> Vec<Entry> {
        vec![
            entry(1, FILMS, "movies:1", MOVIES),
            entry(2, FILMS, "movies:2", MOVIES),
            entry(3, "screening/serials", "series:1", "series"),
        ]
    }

    #[test]
    fn the_member_after_this_one_in_story_order_follows_it() {
        let slot = in_order(&order(), FILMS, "movies:1").expect("the order holds a member after");

        assert_eq!(slot.id, "movies:2");
        assert_eq!(slot.kind, MOVIES);
    }

    #[test]
    fn a_show_that_follows_a_film_draws_as_a_show() {
        let slot = in_order(&order(), FILMS, "movies:2").expect("the order holds a member after");

        assert_eq!(slot.id, "series:1");
        assert_eq!(slot.kind, "series");
        assert_eq!(slot.episode, None);
    }

    #[test]
    fn the_last_member_of_an_order_is_followed_by_nothing() {
        assert_eq!(in_order(&order(), "screening/serials", "series:1"), None);
    }

    #[test]
    fn an_order_this_work_is_no_member_of_names_nothing_after_it() {
        assert_eq!(in_order(&order(), FILMS, "movies:9"), None);
        assert_eq!(in_order(&[], FILMS, "movies:1"), None);
    }

    #[test]
    fn a_gap_in_the_order_is_stepped_over() {
        let mut members = order();
        members.insert(1, gap(2));

        let slot = in_order(&members, FILMS, "movies:1").expect("the order holds a member after");

        assert_eq!(slot.id, "movies:2");
    }

    #[test]
    fn one_id_in_two_libraries_names_two_members() {
        let mut members = order();
        members[0] = entry(1, "screening/shorts", "movies:2", MOVIES);

        let slot = in_order(&members, "screening/shorts", "movies:2")
            .expect("the order holds a member after");

        assert_eq!(slot.library, FILMS);
        assert_eq!(slot.id, "movies:2");
    }
}
