//! An object's board: the object in a box in the middle with its
//! conditions and its own facts, what it is part of and what names it
//! in boxes to its left, what it names in boxes to its right, and each
//! table across the page under them.  A page that draws the object
//! elsewhere, as a known kind's diagram draws it, leaves the middle out
//! and lays the same boxes in one row.

use super::Section;
use super::section::Side;
use crate::map::known::Chip;
use transect_graph::links::Reference;
use transect_observe::health::Level;

/// The object's own box.
#[derive(Debug, PartialEq)]
pub struct Center {
    pub kind: String,
    pub name: String,
    pub level: Option<Level>,
    /// Why it is at rest, when its condition reports it at rest.
    pub resting: Option<String>,
    /// A line on how the page read the object, for a kind that no store
    /// holds and that the page reads once, or on what the object lacks,
    /// such as a claim with no volume.
    pub note: Option<transect_resources::resources::OffLine>,
    pub chips: Vec<Chip>,
    /// The message of each condition that is not healthy, so a reader
    /// sees the reason without hovering its chip.
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

/// What the object is part of and what is part of it, as kind
/// knowledge's `part of` references give it: the frame around the board,
/// and boxes inside the object's own.
#[derive(Debug, Default, PartialEq)]
pub struct Nesting {
    /// The first thing it is part of, which frames the board.
    pub frame: Option<Reference>,
    /// Anything else it is part of, each a line in its box.
    pub also: Vec<Reference>,
    /// Its parts, each with its own parts.
    pub parts: Vec<Part>,
}

/// One part of the object, as a box inside the object's own.
#[derive(Debug, PartialEq)]
pub struct Part {
    pub reference: Reference,
    pub parts: Vec<Reference>,
}

impl ObjectBoard {
    /// The board of `sections` around `center`, each on its side.  A
    /// section with nothing to show is left out.
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

    /// The board nested in what its object is part of, around its parts.
    pub fn with_nesting(self, nesting: Nesting) -> Self {
        Self { nesting, ..self }
    }

    /// The boxes in one row, for a board with no middle: what it is
    /// part of and what names it, its own facts, then what it names.
    pub fn row(&self) -> Vec<&Section> {
        self.left
            .iter()
            .chain(&self.own)
            .chain(&self.right)
            .collect()
    }
}

impl Center {
    /// The title's dot: gray at rest, clear when healthy, warm when not,
    /// and none for an object that reports no condition.
    pub fn dot_class(&self) -> Option<&'static str> {
        let level = self.level?;
        Some(match self.resting {
            Some(_) => "dot dot-gray",
            None => super::dot_class(level),
        })
    }

    /// The box takes the failure fill or the warning outline, as a box
    /// on the namespace page does.
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
}
