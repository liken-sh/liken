// The franchise strips a film's page and a series' page both draw, and the
// focus ladder those strips are rungs of. One strip per franchise the title
// belongs to: the whole order in story order with the members some library
// holds, centered on the title the page is about. A strip's heading is a rung
// of its own over its members, and a press on it opens the franchise's page.
// Every function here is pure over the rows, so the two pages share one
// ladder.

use crate::catalog::{Query, Source, franchise};
use crate::focus;
use crate::screens::{Item, facts};
#[cfg(test)]
use crate::views::Card;

/// Where focus is inside one strip: on the heading over it, or on one of its
/// members.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Place {
    Heading,
    Member(usize),
}

/// Which strip, and where in it.
pub type Rung = (usize, Place);

/// What a press does to focus: it moves inside the strips, or it leaves them
/// for the block over them or the block under them.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Move {
    To(Rung),
    Above,
    Below,
}

/// One franchise the title belongs to. `library` and `id` name the `Library`
/// of kind franchises and the franchise in it, which is what the heading
/// opens. `heading` is the franchise's title with the count of every entry
/// of the order and the kinds it holds. `current` is the member this page is
/// about, which draws marked, and nothing where the page's own title is not
/// among the held members.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Strip {
    pub library: String,
    pub id: String,
    pub heading: String,
    pub members: Vec<Item>,
    pub current: Option<usize>,
}

/// The words after a franchise's name on a strip heading. The count is
/// every entry of the order, held or not, because the scope of the order
/// is what tells a franchise from a set. The kinds the order holds name
/// themselves: films alone, series alone, or both. One film reads in the
/// singular, and series reads the same either way.
pub fn scope(movies: i64, series: i64) -> String {
    let all = movies + series;
    let kinds = match (movies > 0, series > 0) {
        (true, true) => "films and series",
        (false, true) => "series",
        _ => match all {
            1 => "film",
            _ => "films",
        },
    };
    format!("a franchise of {all} {kinds}")
}

/// Every franchise strip of one title, in the order the pages draw them.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Strips {
    bands: Vec<Strip>,
}

impl Strips {
    /// The strips of one title, from the franchises read. A franchise the
    /// namespace holds one member of still draws its strip, because the
    /// heading over it opens the page that holds the rest of the order as
    /// gaps.
    pub fn of(library: &str, id: &str, source: &mut dyn Source) -> Self {
        let bands = source
            .franchises_of(library, id)
            .into_iter()
            .map(|membership| {
                let query = Query::Franchise {
                    library: membership.library.clone(),
                    id: membership.id.clone(),
                };
                let members: Vec<Item> = franchise::slots(&membership.members)
                    .into_iter()
                    .map(|slot| Item::of(&query, slot))
                    .collect();
                let current = members.iter().position(|member| member.id == id);
                Strip {
                    library: membership.library,
                    id: membership.id,
                    heading: facts::joined(&[
                        &membership.title,
                        &scope(membership.movies, membership.series),
                    ]),
                    members,
                    current,
                }
            })
            .collect();
        Self { bands }
    }

    /// The strips in draw order.
    pub fn bands(&self) -> &[Strip] {
        &self.bands
    }

    /// Whether the title belongs to no franchise.
    pub fn is_empty(&self) -> bool {
        self.bands.is_empty()
    }

    /// The rung a move down from the block over the strips lands on, or
    /// nothing where the title belongs to no franchise.
    pub fn first(&self) -> Option<Rung> {
        (!self.bands.is_empty()).then_some((0, Place::Heading))
    }

    /// The rung a move up from the block under the strips lands on: the
    /// member the last strip is about.
    pub fn last(&self) -> Option<Rung> {
        let index = self.bands.len().checked_sub(1)?;
        Some((index, self.at(index)))
    }

    /// The strip at one rung, or nothing where the rung is past what
    /// the title belongs to.
    pub fn band(&self, (strip, _): Rung) -> Option<&Strip> {
        self.bands.get(strip)
    }

    /// The member at one rung, and nothing while the heading holds
    /// focus.
    pub fn member(&self, (strip, place): Rung) -> Option<&Item> {
        match place {
            Place::Heading => None,
            Place::Member(index) => self.bands.get(strip)?.members.get(index),
        }
    }

    /// The rung this press moves to. Up from a member reaches the strip's own
    /// heading, and up from the first heading leaves the strips. Down from a
    /// heading reaches the member the strip is about, and down from a member
    /// reaches the next strip's heading or leaves the strips. Left and right
    /// move across the members of one strip, and move nothing on a heading.
    pub fn key(&self, (strip, place): Rung, key: &str) -> Move {
        let Some(band) = self.bands.get(strip) else {
            return Move::Above;
        };
        match (place, key) {
            (Place::Heading, "up") if strip == 0 => Move::Above,
            (Place::Heading, "up") => Move::To((strip - 1, self.at(strip - 1))),
            (Place::Heading, "down") => Move::To((strip, self.at(strip))),
            (Place::Heading, _) => Move::To((strip, Place::Heading)),
            (Place::Member(_), "up") => Move::To((strip, Place::Heading)),
            (Place::Member(_), "down") if strip + 1 < self.bands.len() => {
                Move::To((strip + 1, Place::Heading))
            }
            (Place::Member(_), "down") => Move::Below,
            (Place::Member(index), _) => Move::To((
                strip,
                Place::Member(focus::row(index, band.members.len(), key)),
            )),
        }
    }

    /// The rung focus holds after a re-read: the same one, unless the
    /// strip it was on grew shorter or went away.
    pub fn held(&self, (strip, place): Rung) -> Option<Rung> {
        let strip = strip.min(self.bands.len().checked_sub(1)?);
        let place = match place {
            Place::Heading => Place::Heading,
            Place::Member(index) => {
                Place::Member(index.min(self.bands[strip].members.len().checked_sub(1)?))
            }
        };
        Some((strip, place))
    }

    // The place a move into one strip lands on: the member the page is
    // about, and the first member where the strip is about none.
    fn at(&self, strip: usize) -> Place {
        Place::Member(
            self.bands
                .get(strip)
                .and_then(|band| band.current)
                .unwrap_or(0),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::screens::franchise::tests::{CYCLE, ORDERS, Orders};

    // The fake's order holds five members some library holds, and the
    // page is about the second of them.
    fn strips() -> Strips {
        Strips::of("screening/films", "movies:2", &mut Orders::default())
    }

    #[test]
    fn a_strip_holds_the_order_and_marks_the_title_the_page_is_about() {
        let strips = strips();
        assert_eq!(strips.bands().len(), 1);
        assert_eq!(
            strips.bands()[0].heading,
            "The Cycle · a franchise of 6 films and series"
        );
        assert_eq!(strips.bands()[0].library, ORDERS);
        assert_eq!(strips.bands()[0].id, CYCLE);
        assert_eq!(strips.bands()[0].members.len(), 5);
        assert_eq!(strips.bands()[0].current, Some(1));
        assert_eq!(strips.bands()[0].members[0].caption(), "Title 1");
        assert_eq!(strips.bands()[0].members[0].under(), "1971");
    }

    #[test]
    fn a_heading_says_the_scope_of_the_order_and_the_kinds_it_holds() {
        let cases = [
            (3, 2, "a franchise of 5 films and series"),
            (26, 14, "a franchise of 40 films and series"),
            (10, 0, "a franchise of 10 films"),
            (1, 0, "a franchise of 1 film"),
            (0, 3, "a franchise of 3 series"),
            (0, 1, "a franchise of 1 series"),
        ];
        for (movies, series, words) in cases {
            assert_eq!(scope(movies, series), words, "{movies} and {series}");
        }
    }

    #[test]
    fn a_title_in_no_franchise_draws_no_strip() {
        let strips = Strips::of("screening/films", "movies:2", &mut Orders { empty: true });
        assert!(strips.is_empty());
        assert_eq!(strips.first(), None);
        assert_eq!(strips.last(), None);
        assert_eq!(strips.held((0, Place::Heading)), None);
        assert_eq!(strips.key((0, Place::Heading), "down"), Move::Above);
    }

    #[test]
    fn a_move_into_the_strips_lands_on_the_first_heading() {
        let strips = strips();
        assert_eq!(strips.first(), Some((0, Place::Heading)));
        assert_eq!(strips.last(), Some((0, Place::Member(1))));
    }

    #[test]
    fn down_from_a_heading_lands_on_the_title_the_page_is_about() {
        let strips = strips();
        assert_eq!(
            strips.key((0, Place::Heading), "down"),
            Move::To((0, Place::Member(1)))
        );
    }

    #[test]
    fn up_from_a_member_reaches_its_heading_and_leaves_the_first_strip() {
        let strips = strips();
        assert_eq!(
            strips.key((0, Place::Member(1)), "up"),
            Move::To((0, Place::Heading))
        );
        assert_eq!(strips.key((0, Place::Heading), "up"), Move::Above);
    }

    #[test]
    fn down_from_a_member_of_the_last_strip_leaves_the_strips() {
        let strips = strips();
        assert_eq!(strips.key((0, Place::Member(0)), "down"), Move::Below);
    }

    #[test]
    fn left_and_right_move_across_one_strips_members() {
        let strips = strips();
        assert_eq!(
            strips.key((0, Place::Member(1)), "right"),
            Move::To((0, Place::Member(2)))
        );
        assert_eq!(
            strips.key((0, Place::Member(4)), "right"),
            Move::To((0, Place::Member(4)))
        );
        assert_eq!(
            strips.key((0, Place::Member(1)), "left"),
            Move::To((0, Place::Member(0)))
        );
        assert_eq!(
            strips.key((0, Place::Heading), "right"),
            Move::To((0, Place::Heading))
        );
    }

    #[test]
    fn a_strip_about_no_member_of_its_own_lands_on_its_first() {
        let strips = Strips::of("screening/films", "movies:99", &mut Orders::default());
        assert_eq!(strips.bands()[0].current, None);
        assert_eq!(strips.last(), Some((0, Place::Member(0))));
    }

    #[test]
    fn a_rung_past_the_strips_leaves_them() {
        let strips = strips();
        assert_eq!(strips.key((9, Place::Heading), "down"), Move::Above);
        assert_eq!(strips.member((9, Place::Member(0))), None);
        assert_eq!(strips.member((0, Place::Heading)), None);
        assert_eq!(strips.band((9, Place::Heading)), None);
    }

    #[test]
    fn a_reread_holds_the_rung_inside_what_the_title_still_belongs_to() {
        let strips = strips();
        assert_eq!(
            strips.held((0, Place::Member(9))),
            Some((0, Place::Member(4)))
        );
        assert_eq!(strips.held((9, Place::Heading)), Some((0, Place::Heading)));
    }

    #[test]
    fn a_member_answers_the_item_it_draws() {
        let strips = strips();
        let member = strips
            .member((0, Place::Member(4)))
            .expect("the strip holds five");
        assert_eq!(member.id, "movies:6");
        assert_eq!(member.kind, "movies");
        assert_eq!(member.library, "screening/films");
        let serial = strips
            .member((0, Place::Member(3)))
            .expect("the strip holds a serial");
        assert_eq!(serial.kind, "series");
    }
}
