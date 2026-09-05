// What a person's works say about the person, and what one work's card
// says about the part they took in it. A person's strip on the home page
// and a person's own page read the same works, so both credit them here:
// the roles head the strip, and every work's parts line loses what the
// heading already says.

use crate::catalog::{Query, Slot};

/// The person's roles across these works as role words in lower case,
/// comma separated, most frequent first. A work's parts line is what the
/// works read wrote: the role words and `as <character>` runs, comma
/// separated, and an `as` run is the actor role, never the character's
/// name. A role counts once per work even where a work credits it twice.
/// A tie in frequency keeps the order the roles first came in, so the
/// heading is stable between frames.
pub fn roles(slots: &[Slot]) -> String {
    let mut counted: Vec<(String, usize)> = Vec::new();
    for slot in slots {
        let mut seen: Vec<String> = Vec::new();
        for part in slot.parts.split(", ").filter(|part| !part.is_empty()) {
            let role = match part.starts_with(AS) {
                true => "actor".to_string(),
                false => part.to_lowercase(),
            };
            if seen.contains(&role) {
                continue;
            }
            seen.push(role.clone());
            match counted.iter_mut().find(|(named, _)| *named == role) {
                Some((_, count)) => *count += 1,
                None => counted.push((role, 1)),
            }
        }
    }
    counted.sort_by_key(|(_, count)| std::cmp::Reverse(*count));
    counted
        .into_iter()
        .map(|(role, _)| role)
        .collect::<Vec<String>>()
        .join(", ")
}

/// Credit every work of a person as its card draws it, and answer the
/// roles those works give the person, which head the strip. A query about
/// anything but a person leaves every slot as the read answered it and
/// names no roles.
pub fn credit(query: &Query, slots: &mut [Slot]) -> String {
    if !matches!(query, Query::Person { .. }) {
        return String::new();
    }
    let roles = roles(slots);
    for slot in slots.iter_mut() {
        slot.parts = credited(&slot.parts, &roles);
    }
    roles
}

// A work's parts as its card draws them: the "as <character>" runs alone
// where the heading over the strip names one role, and every part where
// the heading names more than one, because then the heading cannot say
// which work was which.
fn credited(parts: &str, roles: &str) -> String {
    match one_role(roles) {
        true => characters(parts),
        false => parts.to_string(),
    }
}

// Whether the strip's heading names one role. Every card would repeat
// that one word.
fn one_role(roles: &str) -> bool {
    roles.split(", ").filter(|role| !role.is_empty()).count() == 1
}

// The run that names a character in a parts line, and never a role word.
const AS: &str = "as ";

// The "as <character>" runs of a parts line, without the role words the
// heading over the strip names. The character is per work, so it stays.
fn characters(parts: &str) -> String {
    parts
        .split(", ")
        .filter(|part| part.starts_with(AS))
        .collect::<Vec<&str>>()
        .join(", ")
}

#[cfg(test)]
mod tests {
    use super::*;

    const LIBRARY: &str = "sample/features";

    fn work(parts: &str) -> Slot {
        Slot {
            parts: parts.into(),
            ..Slot::default()
        }
    }

    fn person() -> Query {
        Query::Person {
            library: LIBRARY.into(),
            path: ".contributors/A Player".into(),
        }
    }

    #[test]
    fn a_persons_roles_read_most_frequent_first_in_lower_case() {
        let works = [
            work("as Ripley"),
            work("Writer"),
            work("as Dallas"),
            work("as Kane"),
            work("as Ash"),
            work("as Parker"),
        ];
        assert_eq!(roles(&works), "actor, writer");
    }

    #[test]
    fn roles_of_one_frequency_keep_the_order_they_first_came_in() {
        let works = [work("Writer"), work("Director"), work("as Someone")];
        assert_eq!(roles(&works), "writer, director, actor");
        let works = [work("Director"), work("Writer")];
        assert_eq!(roles(&works), "director, writer");
    }

    #[test]
    fn a_work_that_credits_a_person_twice_counts_once_per_role() {
        let works = [
            work("as One, as Two"),
            work("Writer, Director"),
            work("Writer"),
        ];
        assert_eq!(roles(&works), "writer, actor, director");
    }

    #[test]
    fn one_role_reads_as_one_word_and_no_work_reads_as_nothing() {
        assert_eq!(roles(&[work("Writer"), work("Writer")]), "writer");
        assert_eq!(roles(&[work("as The Part")]), "actor");
        assert_eq!(roles(&[]), "");
        assert_eq!(roles(&[work("")]), "");
    }

    #[test]
    fn a_strip_of_one_role_leaves_every_card_its_characters_alone() {
        let mut works = [work("as One"), work("Actor"), work("as Two, as Three")];
        assert_eq!(credit(&person(), &mut works), "actor");
        assert_eq!(works[0].parts, "as One");
        assert_eq!(works[1].parts, "");
        assert_eq!(works[2].parts, "as Two, as Three");
    }

    #[test]
    fn a_strip_of_two_roles_leaves_every_card_all_of_its_parts() {
        let mut works = [work("Director, Writer"), work("Writer")];
        assert_eq!(credit(&person(), &mut works), "writer, director");
        assert_eq!(works[0].parts, "Director, Writer");
        assert_eq!(works[1].parts, "Writer");
    }

    #[test]
    fn a_query_about_no_person_credits_nothing() {
        let mut slots = [work("Director")];
        let library = Query::Library {
            library: LIBRARY.into(),
        };
        assert_eq!(credit(&library, &mut slots), "");
        assert_eq!(slots[0].parts, "Director");
    }
}
