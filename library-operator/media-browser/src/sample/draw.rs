// The invented genres and the invented pool of the sample catalog, so
// a run with no catalog opens a home page with drawn strips.

use super::{
    ARRIVALS_FROM, DAY, FEATURES, IMPORT_STEP_DAYS, IN_SETS, MOVIES, PER_SET, SERIALS,
    SERIALS_LIBRARY, movie, movie_arrival, people, serial,
};
use crate::catalog::pool::Candidate;
use crate::catalog::{Order, Query, Slot};

// The invented genres. Every movie carries one or two of them by its
// number, so every genre has titles that lead with it and titles that
// trail with it.
const GENRES: [&str; 5] = ["Drama", "Mystery", "Western", "Comedy", "Thriller"];

/// The genres of one invented movie in the sidecar's order: the lead by
/// the number, and a second where it differs from the lead.
pub fn movie_genres(number: i64) -> Vec<String> {
    let lead = GENRES[(number % 5) as usize];
    let second = GENRES[((number / 5) % 5) as usize];
    let mut genres = vec![lead.to_string()];
    if second != lead {
        genres.push(second.to_string());
    }
    genres
}

/// Every invented serial carries the same two genres, as its page
/// says.
pub fn serial_genres() -> Vec<String> {
    vec!["Drama".into(), "Mystery".into()]
}

// The invented arrival of one serial: its import day.
fn serial_arrival(number: i64) -> i64 {
    ARRIVALS_FROM + number * IMPORT_STEP_DAYS * DAY
}

/// The genre query as the sample answers it: every movie and serial
/// that carries the genre, the titles that lead with it first, then
/// newest by the order's column, then by id, which is the sidecar's own
/// order.
pub fn titles(name: &str, order: Order) -> Vec<Slot> {
    let mut found: Vec<(usize, i64, Slot)> = Vec::new();
    for number in 1..=MOVIES {
        if let Some(rank) = movie_genres(number).iter().position(|genre| genre == name) {
            let slot = Slot::of(FEATURES, "movies", movie(number));
            found.push((rank, movie_arrival(number), slot));
        }
    }
    for number in 1..=SERIALS {
        if let Some(rank) = serial_genres().iter().position(|genre| genre == name) {
            let slot = Slot::of(SERIALS_LIBRARY, "series", serial(number));
            found.push((rank, serial_arrival(number), slot));
        }
    }
    found.sort_by(|(rank, added, slot), (other_rank, other_added, other)| {
        rank.cmp(other_rank)
            .then_with(|| match order {
                Order::Released => other.released.cmp(&slot.released),
                Order::Added => other_added.cmp(added),
            })
            .then_with(|| slot.library.cmp(&other.library))
            .then_with(|| slot.id.cmp(&other.id))
    });
    found.into_iter().map(|(_, _, slot)| slot).collect()
}

/// The invented pool: every genre weighed as the catalog weighs it, the
/// one invented person over the works floor, and every invented set with
/// its members.
pub fn pool() -> Vec<Candidate> {
    let mut pool: Vec<Candidate> = GENRES
        .iter()
        .map(|name| {
            let mut weight = 0;
            for number in 1..=MOVIES {
                weight += weight_of(&movie_genres(number), name);
            }
            weight += SERIALS as u64 * weight_of(&serial_genres(), name);
            Candidate {
                query: Query::Genre {
                    name: name.to_string(),
                    order: Order::Released,
                },
                name: name.to_string(),
                weight,
            }
        })
        .collect();
    pool.push(Candidate {
        query: Query::Person {
            library: FEATURES.into(),
            path: people::PROLIFIC.to_string(),
        },
        name: people::PROLIFIC_NAME.into(),
        weight: people::works(FEATURES, people::PROLIFIC).len() as u64,
    });
    for set in 1..=IN_SETS / PER_SET {
        pool.push(Candidate {
            query: Query::Set {
                library: FEATURES.into(),
                id: format!("set:sample:{set:02}"),
            },
            name: format!("The Specimen Cycle {set:02}"),
            weight: PER_SET as u64,
        });
    }
    pool
}

// What one title adds to a genre's weight: two where it leads with the
// genre, one where it trails with it, none otherwise.
fn weight_of(genres: &[String], name: &str) -> u64 {
    match genres.iter().position(|genre| genre == name) {
        Some(0) => 2,
        Some(_) => 1,
        None => 0,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::Source;
    use crate::catalog::pool::Kind;
    use crate::catalog::recency::WORKS_FLOOR;
    use crate::sample::Catalog;

    #[test]
    fn a_movies_genres_lead_by_its_number_and_never_repeat() {
        assert_eq!(movie_genres(1), ["Mystery", "Drama"]);
        assert_eq!(movie_genres(5), ["Drama", "Mystery"]);
        assert_eq!(movie_genres(6), ["Mystery"]);
        assert_eq!(movie_genres(12), ["Western"]);
    }

    #[test]
    fn a_genre_wall_leads_with_the_titles_whose_first_genre_it_is() {
        let mut catalog = Catalog;
        let answer = catalog.wall(&Query::Genre {
            name: "Western".into(),
            order: Order::Released,
        });
        assert_eq!(answer.name, "Western");
        assert!(!answer.slots.is_empty());
        let ranks: Vec<usize> = answer
            .slots
            .iter()
            .map(|slot| {
                movie_genres(crate::sample::trailing(&slot.id))
                    .iter()
                    .position(|genre| genre == "Western")
                    .expect("every slot carries the genre")
            })
            .collect();
        assert!(ranks.windows(2).all(|pair| pair[0] <= pair[1]));
        assert_eq!(ranks[0], 0);
        assert_eq!(*ranks.last().unwrap(), 1);
        assert!(answer.slots.iter().all(|slot| slot.kind == "movies"));
    }

    #[test]
    fn drama_reads_the_serials_beside_the_movies_and_by_arrival_on_request() {
        let released = titles("Drama", Order::Released);
        assert!(released.iter().any(|slot| slot.kind == "series"));
        let leading: Vec<&Slot> = released
            .iter()
            .take_while(|slot| {
                slot.kind == "series"
                    || movie_genres(crate::sample::trailing(&slot.id))[0] == "Drama"
            })
            .collect();
        assert!(
            leading
                .windows(2)
                .all(|pair| pair[0].released >= pair[1].released)
        );
        let added = titles("Drama", Order::Added);
        assert_eq!(added.len(), released.len());
        assert_ne!(added[0].id, released[0].id);
        assert!(titles("Musical", Order::Released).is_empty());
    }

    #[test]
    fn the_pool_holds_every_genre_the_prolific_writer_and_every_set() {
        let pool = Catalog.pool();
        let genres: Vec<&str> = pool
            .iter()
            .filter(|candidate| candidate.kind() == Kind::Genre)
            .map(|candidate| candidate.name.as_str())
            .collect();
        assert_eq!(genres, GENRES);
        assert!(pool.iter().all(|candidate| candidate.weight > 0));

        let people: Vec<&Candidate> = pool
            .iter()
            .filter(|candidate| candidate.kind() == Kind::Person)
            .collect();
        assert_eq!(people.len(), 1);
        assert_eq!(people[0].name, people::PROLIFIC_NAME);
        assert!(people[0].weight > WORKS_FLOOR);
        assert_eq!(
            Catalog.wall(&people[0].query).slots.len() as u64,
            people[0].weight
        );

        let sets: Vec<&Candidate> = pool
            .iter()
            .filter(|candidate| candidate.kind() == Kind::Set)
            .collect();
        assert_eq!(sets.len() as i64, IN_SETS / PER_SET);
        assert_eq!(sets[0].name, "The Specimen Cycle 01");
        assert_eq!(Catalog.wall(&sets[0].query).name, sets[0].name);
    }

    #[test]
    fn a_genres_weight_counts_a_leading_title_twice() {
        let western = Catalog
            .pool()
            .into_iter()
            .find(|candidate| candidate.name == "Western")
            .expect("the pool holds Western");
        let leading = (1..=MOVIES)
            .filter(|number| movie_genres(*number)[0] == "Western")
            .count() as u64;
        let carrying = titles("Western", Order::Released).len() as u64;
        assert_eq!(western.weight, carrying + leading);
    }
}
