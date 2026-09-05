// The invented people of the sample catalog: the stripes of a title, one
// person's page, and the wall of what they are credited in.

use super::{movie, trailing};
use crate::catalog::{CreditSlot, Credits, Person, Slot};

// The directory every invented person's entry sits under, the way a
// library's contributor store names them.
const CONTRIBUTORS: &str = ".contributors/";

// The one invented person credited in more works than the pool's
// floor: the second writer of every movie, whose wall holds the first
// six.
pub const PROLIFIC_NAME: &str = "A Second Writer";
pub const PROLIFIC: &str = ".contributors/A Second Writer";
const PROLIFIC_WORKS: i64 = 6;

// One invented slot of a stripe. Every sample person has an entry and a
// headshot, so every slot draws one.
fn slot(name: &str, role: &str) -> CreditSlot {
    CreditSlot {
        name: name.to_string(),
        role: role.to_string(),
        contributor: format!("{CONTRIBUTORS}{name}"),
        headshot: true,
    }
}

// The invented credits of one title, so a run with no catalog opens a
// page with all three stripes.
pub fn credits(id: &str) -> Credits {
    let number = trailing(id);
    if number == 0 {
        return Credits::default();
    }
    Credits {
        directors: vec![slot(&format!("Director {number:04}"), "")],
        writers: vec![slot(&format!("Writer {number:04}"), "")],
        cast: (1..=6)
            .map(|part| {
                slot(
                    &format!("Player {number:04}-{part}"),
                    &format!("Part {part}"),
                )
            })
            .collect(),
    }
}

// Every invented person has an entry, both files, and the same dates.
pub fn person(library: &str, path: &str) -> Option<Person> {
    let name = path.strip_prefix(CONTRIBUTORS)?;
    Some(Person {
        library: library.to_string(),
        path: path.to_string(),
        name: name.to_string(),
        born: "1950-01-02".into(),
        died: String::new(),
        biography: true,
        headshot: true,
        biography_library: library.to_string(),
        biography_path: path.to_string(),
        headshot_library: library.to_string(),
        headshot_path: path.to_string(),
    })
}

/// Every invented person acts in the first three movies, so a person's
/// page opens on a wall of three, and the prolific writer's wall holds
/// the first `PROLIFIC_WORKS` movies as their writer, so the sample's pool
/// has one person over the floor.
/// The slots carry the duration and the rating, as the sidecar's works
/// read does, so a card under a one-role heading draws the facts line
/// every other strip draws.
pub fn works(library: &str, path: &str) -> Vec<Slot> {
    if !path.starts_with(CONTRIBUTORS) {
        return Vec::new();
    }
    let (count, parts) = match path == PROLIFIC {
        true => (PROLIFIC_WORKS, "Writer"),
        false => (3, "as Part 1"),
    };
    let mut works: Vec<Slot> = (1..=count)
        .map(movie)
        .map(|title| Slot {
            parts: parts.into(),
            ..Slot::of(library, "movies", title)
        })
        .collect();
    works.sort_by(|one, other| other.released.cmp(&one.released));
    works
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_title_carries_all_three_stripes() {
        let credits = credits("movie:sample:0001");
        assert_eq!(credits.directors.len(), 1);
        assert_eq!(credits.writers.len(), 1);
        assert_eq!(credits.cast.len(), 6);
        assert_eq!(credits.cast[0].contributor, ".contributors/Player 0001-1");
        assert!(credits.cast[0].headshot);
    }

    #[test]
    fn a_person_carries_a_page_and_a_wall_in_release_order() {
        let person = person("sample/features", ".contributors/Player 0001-1")
            .expect("the sample invents every person");
        assert_eq!(person.name, "Player 0001-1");
        assert!(person.headshot);

        let works = works("sample/features", ".contributors/Player 0001-1");
        let released: Vec<&str> = works.iter().map(|work| work.released.as_str()).collect();
        assert_eq!(released, ["2011", "1974", "1937"]);
        assert_eq!(works[0].parts, "as Part 1");
    }

    #[test]
    fn the_prolific_writer_wrote_more_than_three() {
        let works = works("sample/features", PROLIFIC);
        assert_eq!(works.len() as i64, PROLIFIC_WORKS);
        assert!(works.iter().all(|work| work.parts == "Writer"));
        assert_eq!(
            person("sample/features", PROLIFIC).map(|person| person.name),
            Some(PROLIFIC_NAME.to_string())
        );
    }

    #[test]
    fn a_person_the_sample_never_invented_has_no_page() {
        assert_eq!(person("sample/features", "nonsense"), None);
        assert!(works("sample/features", "nonsense").is_empty());
    }
}
