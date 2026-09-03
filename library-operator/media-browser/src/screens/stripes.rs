// The credited people of one title, as the three stripes a movie's
// page and a series' page both draw, and the focus ladder those stripes
// are the last rungs of. Every function here is pure over the rows, so
// the two pages share one model and the tests need no window.

use crate::catalog::{CreditSlot, Credits};
use crate::focus;
use crate::views::Card;

// The file every contributor entry holds their headshot in.
const HEADSHOT: &str = "headshot.jpg";

/// One slot of a stripe: the person, what they did on this title,
/// and where their entry lives.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Face {
    /// The name a person reads.
    pub name: String,
    /// The character an actor played, empty for the crew.
    pub role: String,
    /// The person's directory relative to the library volume, empty
    /// where the library's store holds no entry for them.
    pub contributor: String,
    /// The path of the headshot file, empty where the entry holds
    /// none.
    pub art: String,
}

impl Face {
    // One credit row as a slot. The headshot sits inside the
    // person's own entry, so its path is built once here, at the read.
    fn of(slot: CreditSlot) -> Self {
        let art = match slot.headshot && !slot.contributor.is_empty() {
            true => format!("{}/{HEADSHOT}", slot.contributor),
            false => String::new(),
        };
        Self {
            name: slot.name,
            role: slot.role,
            contributor: slot.contributor,
            art,
        }
    }
}

impl Card for Face {
    fn art(&self) -> &str {
        &self.art
    }

    fn name(&self) -> &str {
        &self.name
    }

    fn detail(&self) -> &str {
        &self.role
    }
}

// The crew as one list: the directors first, then the writers the directors
// do not already name, each with the parts they hold joined under the name.
// A person is the same person by their entry, or by their name where the
// store has no entry for them.
fn crew(directors: Vec<CreditSlot>, writers: Vec<CreditSlot>) -> Vec<CreditSlot> {
    let mut crew: Vec<CreditSlot> = Vec::with_capacity(directors.len() + writers.len());
    for (part, slots) in [("Director", directors), ("Writer", writers)] {
        for slot in slots {
            match crew.iter_mut().find(|held| same_person(held, &slot)) {
                Some(held) => held.role = format!("{}, {part}", held.role),
                None => crew.push(CreditSlot {
                    role: part.to_string(),
                    ..slot
                }),
            }
        }
    }
    crew
}

fn same_person(a: &CreditSlot, b: &CreditSlot) -> bool {
    match (a.contributor.is_empty(), b.contributor.is_empty()) {
        (false, false) => a.contributor == b.contributor,
        _ => a.name.eq_ignore_ascii_case(&b.name),
    }
}

/// One stripe: the part its heading names, and the people in it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Stripe {
    /// The heading drawn over the headshots.
    pub heading: &'static str,
    /// The people, in the order the catalog answered them.
    pub faces: Vec<Face>,
}

/// Where focus is inside the stripes: which stripe, and which slot
/// of it.
pub type Rung = (usize, usize);

/// The stripes of one title, in the order the pages draw them,
/// with the parts the title credits nobody in left out.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Stripes {
    bands: Vec<Stripe>,
}

impl Stripes {
    /// The stripes of one title's credits: the crew, and the cast. A person
    /// who both directed and wrote is one face of the crew, with both parts
    /// under their name where an actor's character goes.
    pub fn of(credits: Credits) -> Self {
        let bands = [
            ("Crew", crew(credits.directors, credits.writers)),
            ("Cast", credits.cast),
        ]
        .into_iter()
        .filter(|(_, slots)| !slots.is_empty())
        .map(|(heading, slots)| Stripe {
            heading,
            faces: slots.into_iter().map(Face::of).collect(),
        })
        .collect();
        Self { bands }
    }

    /// The stripes in draw order.
    pub fn bands(&self) -> &[Stripe] {
        &self.bands
    }

    /// Whether the title credits nobody at all.
    pub fn is_empty(&self) -> bool {
        self.bands.is_empty()
    }

    /// The rung a move down from the rung above the stripes lands
    /// on, or nothing where the title credits nobody.
    pub fn first(&self) -> Option<Rung> {
        (!self.bands.is_empty()).then_some((0, 0))
    }

    /// The person at one rung, or nothing where the rung is past
    /// what the title credits.
    pub fn face(&self, (stripe, slot): Rung) -> Option<&Face> {
        self.bands.get(stripe)?.faces.get(slot)
    }

    /// The rung this press moves to, or nothing where up leaves the
    /// stripes for the rung above them.
    pub fn key(&self, (stripe, slot): Rung, key: &str) -> Option<Rung> {
        let count = self.bands.get(stripe).map_or(0, |band| band.faces.len());
        match key {
            "up" if stripe == 0 => None,
            "up" => Some((stripe - 1, 0)),
            "down" if stripe + 1 < self.bands.len() => Some((stripe + 1, 0)),
            "down" => Some((stripe, slot)),
            _ => Some((stripe, focus::row(slot, count, key))),
        }
    }

    /// The rung focus holds after a re-read: the same one, unless
    /// the stripe it was on grew shorter or went away.
    pub fn held(&self, (stripe, slot): Rung) -> Option<Rung> {
        let stripe = stripe.min(self.bands.len().checked_sub(1)?);
        let count = self.bands[stripe].faces.len();
        Some((stripe, slot.min(count.checked_sub(1)?)))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn slot(name: &str, role: &str, entry: bool) -> CreditSlot {
        CreditSlot {
            name: name.to_string(),
            role: role.to_string(),
            contributor: match entry {
                true => format!(".contributors/{name}"),
                false => String::new(),
            },
            headshot: entry,
        }
    }

    fn credits() -> Credits {
        Credits {
            directors: vec![slot("A Director", "", true)],
            writers: Vec::new(),
            cast: vec![
                slot("A Player", "The Part", true),
                slot("Another", "A Walk-on", false),
            ],
        }
    }

    #[test]
    fn a_title_draws_only_the_parts_it_credits() {
        let stripes = Stripes::of(credits());
        let headings: Vec<&str> = stripes.bands().iter().map(|band| band.heading).collect();
        assert_eq!(headings, ["Crew", "Cast"]);
        assert_eq!(stripes.bands()[0].faces[0].role, "Director");
        assert_eq!(stripes.bands()[1].faces.len(), 2);
    }

    #[test]
    fn a_person_who_directed_and_wrote_is_one_face_with_both_parts() {
        let stripes = Stripes::of(Credits {
            directors: vec![slot("A Director", "", true), slot("Both", "", true)],
            writers: vec![slot("Both", "", true), slot("A Writer", "", false)],
            cast: Vec::new(),
        });
        let crew: Vec<(&str, &str)> = stripes.bands()[0]
            .faces
            .iter()
            .map(|face| (face.name.as_str(), face.role.as_str()))
            .collect();
        assert_eq!(
            crew,
            [
                ("A Director", "Director"),
                ("Both", "Director, Writer"),
                ("A Writer", "Writer")
            ]
        );
    }

    #[test]
    fn a_title_that_credits_nobody_draws_no_stripe() {
        let stripes = Stripes::of(Credits::default());
        assert!(stripes.is_empty());
        assert_eq!(stripes.first(), None);
    }

    #[test]
    fn a_headshot_sits_inside_the_persons_own_entry() {
        let stripes = Stripes::of(credits());
        let face = stripes.face((0, 0)).expect("the title has a director");
        assert_eq!(face.art, ".contributors/A Director/headshot.jpg");
        assert_eq!(face.name, "A Director");
        assert_eq!(face.detail(), "Director");
    }

    #[test]
    fn a_name_with_no_entry_carries_no_art_and_no_path() {
        let stripes = Stripes::of(credits());
        let face = stripes.face((1, 1)).expect("the title has a second player");
        assert_eq!(face.art, "");
        assert_eq!(face.contributor, "");
        assert_eq!(face.detail(), "A Walk-on");
    }

    #[test]
    fn left_and_right_move_inside_one_stripe() {
        let stripes = Stripes::of(credits());
        assert_eq!(stripes.key((1, 0), "right"), Some((1, 1)));
        assert_eq!(stripes.key((1, 1), "right"), Some((1, 1)));
        assert_eq!(stripes.key((1, 1), "left"), Some((1, 0)));
    }

    #[test]
    fn down_and_up_move_between_stripes_and_up_leaves_the_first() {
        let stripes = Stripes::of(credits());
        assert_eq!(stripes.first(), Some((0, 0)));
        assert_eq!(stripes.key((0, 0), "down"), Some((1, 0)));
        assert_eq!(stripes.key((1, 1), "down"), Some((1, 1)));
        assert_eq!(stripes.key((1, 1), "up"), Some((0, 0)));
        assert_eq!(stripes.key((0, 0), "up"), None);
    }

    #[test]
    fn a_reread_holds_the_rung_inside_what_the_title_still_credits() {
        let stripes = Stripes::of(credits());
        assert_eq!(stripes.held((1, 1)), Some((1, 1)));
        assert_eq!(stripes.held((1, 9)), Some((1, 1)));
        assert_eq!(stripes.held((9, 9)), Some((1, 1)));
        assert_eq!(Stripes::default().held((0, 0)), None);
    }
}
