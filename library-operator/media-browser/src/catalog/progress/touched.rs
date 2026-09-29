// One play the progress store changed, as the browser matches it against
// what a screen draws. Every writer of the store changes a row of
// `plays`, `play_people`, or `play_aliases`: a `Play`'s position, its
// final, a mark, and an outside play from Jellyfin. The agent's update
// stream names the play, and the source resolves the play to the works
// it names and the people on it. A screen then reads the progress of a
// work again only where it draws that work for people the play names.

/// The works one changed play names, and the people it names.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Touched {
    /// Each catalog work the play's aliases resolve to, as the library and
    /// the item id: the film, or the series of an episode. A work that two
    /// libraries hold resolves to both.
    pub works: Vec<(String, String)>,
    /// The people on the play, and every person the change took off it.
    /// A person the change took off was on the play before it, so the
    /// reads of that person's progress changed too.
    pub people: Vec<String>,
}

impl Touched {
    /// Whether the play names this work.
    pub fn names(&self, library: &str, item: &str) -> bool {
        self.works
            .iter()
            .any(|(held, id)| held == library && id == item)
    }

    /// Whether a read for this audience can answer differently after the
    /// change. A page reads the plays that name any person of the audience,
    /// and an empty audience reads the plays that name nobody.
    pub fn reaches(&self, audience: &[String]) -> bool {
        match audience.is_empty() {
            true => self.people.is_empty(),
            false => audience.iter().any(|person| self.people.contains(person)),
        }
    }

    /// Whether the continue-watching row of this audience can change. The
    /// row reads only the plays that name every person of the audience,
    /// and an empty audience has no row.
    pub fn covers(&self, audience: &[String]) -> bool {
        !audience.is_empty() && audience.iter().all(|person| self.people.contains(person))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn touched(people: &[&str]) -> Touched {
        Touched {
            works: vec![("default/films".into(), "movie:tmdb:7001".into())],
            people: people.iter().map(|name| (*name).to_string()).collect(),
        }
    }

    fn audience(people: &[&str]) -> Vec<String> {
        people.iter().map(|name| (*name).to_string()).collect()
    }

    #[test]
    fn a_play_names_the_works_its_aliases_resolve_to() {
        let play = touched(&["first"]);
        assert!(play.names("default/films", "movie:tmdb:7001"));
        assert!(!play.names("default/films", "movie:tmdb:7002"));
        assert!(!play.names("default/other", "movie:tmdb:7001"));
    }

    #[test]
    fn a_play_reaches_an_audience_it_shares_a_person_with() {
        let cases: &[(&[&str], &[&str], bool)] = &[
            (&["first"], &["first"], true),
            (&["first", "second"], &["second"], true),
            (&["first"], &["first", "second"], true),
            (&["other"], &["first"], false),
            (&[], &[], true),
            (&["first"], &[], false),
            (&[], &["first"], false),
        ];
        for (people, room, expected) in cases {
            assert_eq!(
                touched(people).reaches(&audience(room)),
                *expected,
                "{people:?} for {room:?}"
            );
        }
    }

    #[test]
    fn a_play_covers_an_audience_whose_every_person_it_names() {
        let cases: &[(&[&str], &[&str], bool)] = &[
            (&["first"], &["first"], true),
            (&["first", "second"], &["first"], true),
            (&["first"], &["first", "second"], false),
            (&["other"], &["first"], false),
            (&[], &[], false),
        ];
        for (people, room, expected) in cases {
            assert_eq!(
                touched(people).covers(&audience(room)),
                *expected,
                "{people:?} for {room:?}"
            );
        }
    }
}
