//! The layout of an object's page. The object is a box in the middle
//! with its conditions and its own facts. Boxes to its left show what it
//! belongs to and what refers to it, and boxes to its right show what it
//! refers to. A section with a table spans the page below them. A page
//! that draws the object elsewhere, as the diagram of a known kind does,
//! has no middle box and lays the other boxes in one row.

use super::Section;
use super::section::Side;
use crate::map::known::Chip;
use transect_graph::links::Reference;
use transect_observe::health::Level;

#[derive(Debug, PartialEq)]
pub struct Center {
    pub kind: String,
    pub name: String,
    pub level: Option<Level>,
    /// Why the object is at rest, when its condition is `False` with a
    /// reason that kind knowledge marks as calm.
    pub resting: Option<String>,
    /// A note on how the page read the object, for a kind that no store
    /// holds, or on something the object lacks, such as a claim with no
    /// volume.
    pub note: Option<transect_resources::resources::OffLine>,
    pub chips: Vec<Chip>,
    /// The message of each unhealthy condition, shown in the box so the
    /// reader does not have to hover over the condition's chip.
    pub messages: Vec<String>,
}

#[derive(Debug, PartialEq)]
pub struct ObjectBoard {
    pub center: Option<Center>,
    pub left: Vec<Section>,
    pub own: Vec<Section>,
    pub right: Vec<Section>,
    pub wide: Vec<Section>,
    pub nesting: Nesting,
}

/// What the object is part of and what is part of it, from the `part
/// of` references in kind knowledge.
#[derive(Debug, Default, PartialEq)]
pub struct Nesting {
    /// The first thing the object is part of, drawn as a frame around the
    /// board.
    pub frame: Option<Reference>,
    /// Anything else the object is part of, each as a line in the
    /// object's box.
    pub also: Vec<Reference>,
    /// The object's parts, each drawn as a box inside the object's box.
    pub parts: Vec<Part>,
}

#[derive(Debug, PartialEq)]
pub struct Part {
    pub reference: Reference,
    pub parts: Vec<Reference>,
}

impl Nesting {
    /// The nesting without what a drawing of the object already shows:
    /// a part's own part goes when `shown` says the drawing shows it, and
    /// a part goes when the drawing shows it and it has no own part
    /// left. A part keeps the box of its whole while one of its own
    /// parts stays. What the object is part of stays, because a drawing
    /// of the object shows what it is made of.
    pub fn without(self, shown: impl Fn(&Reference) -> bool) -> Self {
        let parts = self.parts.into_iter().filter_map(|part| {
            let own: Vec<Reference> = part.parts.into_iter().filter(|p| !shown(p)).collect();
            match own.is_empty() && shown(&part.reference) {
                true => None,
                false => Some(Part {
                    reference: part.reference,
                    parts: own,
                }),
            }
        });
        Self {
            parts: parts.collect(),
            ..self
        }
    }
}

impl ObjectBoard {
    /// The board for `sections` around `center`. A section with nothing
    /// to show is left out, and the `BelongsTo` sections come first.
    pub fn build(center: Option<Center>, sections: Vec<Section>) -> Self {
        let mut board = Self {
            center,
            left: Vec::new(),
            own: Vec::new(),
            right: Vec::new(),
            wide: Vec::new(),
            nesting: Nesting::default(),
        };
        let shown = sections.into_iter().filter(Section::shown);
        let (belongs, rest): (Vec<Section>, Vec<Section>) =
            shown.partition(|section| section.side == Side::BelongsTo);
        for section in belongs.into_iter().chain(rest) {
            let into = match (&section.table, section.side) {
                (Some(_), _) => &mut board.wide,
                (None, Side::BelongsTo | Side::Into) => &mut board.left,
                (None, Side::Own) => &mut board.own,
                (None, Side::Out) => &mut board.right,
            };
            into.push(section);
        }
        board
    }

    pub fn with_nesting(self, nesting: Nesting) -> Self {
        Self { nesting, ..self }
    }

    /// The boxes in one row, left to right, for a board with no middle
    /// box.
    pub fn row(&self) -> Vec<&Section> {
        self.left
            .iter()
            .chain(&self.own)
            .chain(&self.right)
            .collect()
    }
}

impl Center {
    /// The CSS class of the dot in the title: gray at rest, and `None` for
    /// an object that reports no condition.
    pub fn dot_class(&self) -> Option<&'static str> {
        let level = self.level?;
        Some(match self.resting {
            Some(_) => "dot dot-gray",
            None => super::dot_class(level),
        })
    }

    /// The CSS class of the box: a failure fill or a warning outline, as
    /// on a workload's box on the namespace page. An object at rest gets
    /// neither.
    pub fn class(&self) -> &'static str {
        match self.level.filter(|_| self.resting.is_none()) {
            Some(Level::Failing) => "board-object board-failing",
            Some(Level::Warning) => "board-object board-warning",
            _ => "board-object",
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use crate::objects::{Table, words};

    fn titles(sections: &[Section]) -> Vec<&str> {
        sections
            .iter()
            .map(|section| section.title.as_str())
            .collect()
    }

    fn board() -> ObjectBoard {
        let named = |title: &str, side| Section::new(title, side).fact("names", vec![words("a")]);
        let table = Table {
            head: vec!["verbs".into()],
            rows: vec![vec!["get".into()]],
        };
        ObjectBoard::build(
            None,
            vec![
                named("Referred to by", Side::Into),
                named("Status", Side::Own),
                Section::new("Rules", Side::Own).with_table(table),
                named("Refers to", Side::Out),
                named("Belongs to", Side::BelongsTo),
                Section::new("Empty", Side::Out),
            ],
        )
    }

    #[rstest]
    fn puts_each_section_on_its_side_with_what_it_belongs_to_first() {
        let board = board();
        assert_eq!(titles(&board.left), ["Belongs to", "Referred to by"]);
        assert_eq!(titles(&board.own), ["Status"]);
        assert_eq!(titles(&board.right), ["Refers to"]);
        assert_eq!(titles(&board.wide), ["Rules"]);
    }

    #[rstest]
    fn lays_the_boxes_in_one_row_from_left_to_right() {
        let board = board();
        let row: Vec<&str> = board.row().iter().map(|s| s.title.as_str()).collect();
        assert_eq!(row, ["Belongs to", "Referred to by", "Status", "Refers to"]);
    }

    fn named(text: &str) -> Reference {
        Reference {
            text: text.into(),
            href: Some(format!("/{text}")),
        }
    }

    /// A room is part of a store, and has a shelf with a bin and a
    /// lamp, and a ladder with no parts.
    fn nesting() -> Nesting {
        let part = |name: &str, own: &[&str]| Part {
            reference: named(name),
            parts: own.iter().map(|o| named(o)).collect(),
        };
        Nesting {
            frame: Some(named("store")),
            also: vec![named("mall")],
            parts: vec![part("shelf", &["bin", "lamp"]), part("ladder", &[])],
        }
    }

    type Nested = (Option<String>, Vec<String>, Vec<(String, Vec<String>)>);

    fn nested(nesting: Nesting) -> Nested {
        let text = |r: Reference| r.text;
        let parts = nesting.parts.into_iter();
        let parts = parts.map(|p| (p.reference.text, p.parts.into_iter().map(text).collect()));
        (
            nesting.frame.map(text),
            nesting.also.into_iter().map(text).collect(),
            parts.collect(),
        )
    }

    fn own(entries: &[(&str, &[&str])]) -> Vec<(String, Vec<String>)> {
        let entries = entries.iter();
        let entries = entries.map(|(name, own)| {
            (
                name.to_string(),
                own.iter().map(|o| o.to_string()).collect(),
            )
        });
        entries.collect()
    }

    #[rstest]
    #[case::nothing_shown(&[], own(&[("shelf", &["bin", "lamp"]), ("ladder", &[])]))]
    #[case::an_own_part(&["bin"], own(&[("shelf", &["lamp"]), ("ladder", &[])]))]
    #[case::a_part_with_an_own_part_left(&["shelf", "bin"], own(&[("shelf", &["lamp"]), ("ladder", &[])]))]
    #[case::a_part_and_all_its_own_parts(&["shelf", "bin", "lamp"], own(&[("ladder", &[])]))]
    #[case::a_part_with_no_own_parts(&["ladder"], own(&[("shelf", &["bin", "lamp"])]))]
    #[case::what_the_object_is_part_of(&["store", "mall"], own(&[("shelf", &["bin", "lamp"]), ("ladder", &[])]))]
    fn leaves_out_of_the_nesting_each_part_that_a_drawing_shows(
        #[case] shown: &[&str],
        #[case] parts: Vec<(String, Vec<String>)>,
    ) {
        let found = nesting().without(|value| shown.contains(&value.text.as_str()));
        let expected = (Some("store".to_string()), vec!["mall".to_string()], parts);
        assert_eq!(nested(found), expected);
    }
}
