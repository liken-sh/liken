// The More like this strip of a movie's page: the films of the catalog
// that share a director or a lead actor with this one, for a film that
// has no set strip and no franchise strip to lead a person on from it.
//
// The order puts what the people at the screen can still watch first:
// the films nobody at the screen has finished, then the rest. Inside
// each run the film with the higher score comes first, and the higher
// IMDb rating breaks a tie. The strip holds at most `MOST` films: at
// 1920 px a strip shows about eight posters, so twelve scroll by about
// half a screen, and the films past them score lower or were seen.

use std::collections::HashMap;

use crate::catalog::{Alike, Reach, Work};
use crate::screens::{Item, Spelling, fitted_strip};

/// The heading over the strip. It names no reason, because each film is
/// in the strip for its own.
pub const HEADING: &str = "More like this";

/// The most films the strip draws.
pub const MOST: usize = 12;

/// The strip: every candidate the read answered, and the films it draws,
/// in the order above.
#[derive(Debug)]
pub struct Like {
    candidates: Vec<Alike>,
    /// The films the strip draws.
    pub members: Vec<Item>,
}

impl Like {
    /// The strip of these candidates, in the order of an audience that
    /// has finished none of them, or nothing where the read answered
    /// none.
    pub fn of(candidates: Vec<Alike>) -> Option<Self> {
        if candidates.is_empty() {
            return None;
        }
        let mut like = Self {
            candidates,
            members: Vec::new(),
        };
        like.order(&HashMap::new());
        Some(like)
    }

    /// Order the candidates again for how far the people at the screen
    /// got in each, and draw the first `MOST` of them.
    pub fn order(&mut self, reach: &HashMap<Work, Reach>) {
        let finished = |alike: &Alike| {
            reach
                .get(&(alike.slot.library.clone(), alike.slot.id.clone()))
                .is_some_and(|reach| reach.people.iter().any(|(_, share)| *share >= 1.0))
        };
        self.candidates.sort_by(|one, other| {
            finished(one)
                .cmp(&finished(other))
                .then_with(|| other.score.cmp(&one.score))
                .then_with(|| other.rating.total_cmp(&one.rating))
                .then_with(|| one.slot.id.cmp(&other.slot.id))
        });
        self.members = self
            .candidates
            .iter()
            .take(MOST)
            .map(|alike| Item::spelled(alike.slot.clone(), Spelling::Facts))
            .collect();
        fitted_strip(&mut self.members);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::{Slot, Title};

    fn alike(id: &str, score: i64, rating: f64) -> Alike {
        Alike {
            slot: Slot::of(
                "screening/films",
                "movies",
                Title {
                    id: id.into(),
                    title: format!("Film {id}"),
                    ..Title::default()
                },
            ),
            score,
            rating,
        }
    }

    fn ids(like: &Like) -> Vec<&str> {
        like.members.iter().map(|item| item.id.as_str()).collect()
    }

    // One person's share of each film, as the progress read answers it.
    fn reached(shares: &[(&str, &str, f32)]) -> HashMap<Work, Reach> {
        let mut reach: HashMap<Work, Reach> = HashMap::new();
        for (id, person, share) in shares {
            reach
                .entry(("screening/films".to_string(), id.to_string()))
                .or_default()
                .people
                .push((person.to_string(), *share));
        }
        reach
    }

    #[test]
    fn no_candidate_draws_no_strip() {
        assert!(Like::of(Vec::new()).is_none());
    }

    #[test]
    fn the_higher_score_leads_and_the_rating_breaks_a_tie() {
        let like = Like::of(vec![
            alike("low", 2, 9.0),
            alike("high", 6, 5.0),
            alike("rated", 3, 8.0),
            alike("unrated", 3, 6.0),
        ])
        .expect("four candidates");
        assert_eq!(ids(&like), ["high", "rated", "unrated", "low"]);
    }

    // A film anyone at the screen finished follows every film nobody
    // finished, whatever its score. A film only started stays in front.
    #[test]
    fn a_film_someone_at_the_screen_finished_goes_after_the_rest() {
        let mut like = Like::of(vec![
            alike("best", 6, 8.0),
            alike("started", 4, 7.0),
            alike("fresh", 2, 7.0),
        ])
        .expect("three candidates");
        like.order(&reached(&[
            ("best", "person-b", 1.0),
            ("best", "person-a", 0.2),
            ("started", "person-a", 0.5),
        ]));
        assert_eq!(ids(&like), ["started", "fresh", "best"]);
    }

    #[test]
    fn the_strip_draws_the_first_twelve() {
        let like = Like::of(
            (0..20)
                .map(|number| alike(&format!("{number:02}"), 20 - number, 0.0))
                .collect(),
        )
        .expect("twenty candidates");
        assert_eq!(like.members.len(), MOST);
        assert_eq!(like.members[0].id, "00");
        assert_eq!(like.members[MOST - 1].id, "11");
    }

    #[test]
    fn a_card_carries_the_facts_spelling() {
        let like = Like::of(vec![alike("one", 2, 0.0)]).expect("one candidate");
        assert_eq!(like.members[0].kind, "movies");
        assert_eq!(like.members[0].library, "screening/films");
    }
}
