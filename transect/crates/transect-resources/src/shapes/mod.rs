//! A shape is a small drawing in its own units that a sketch draws a
//! part in: a body, a few pieces, a region for its text, the edges where
//! it meets a neighbor, and the spans where wires attach. A `Shape`
//! resource states geometry and the material of each piece, and nothing
//! else, so `transect` draws every shape with the same code and the same
//! stylesheet. A state, a rule for text, or a contact then works the
//! same way on every shape, including a shape that an author adds.
//!
//! The library's shapes are files in `library/`, embedded in the binary,
//! with bare names such as `box`. An author's shapes are `Shape`
//! documents under `--resources`, named `<shape>.<group>` for a group
//! that the author owns. A role of a kind's knowledge names a shape, and
//! `Shapes::resolve` finds the author's shape in the kind's own group
//! before the library's.

mod form;
pub mod length;
pub mod library;
pub mod place;
mod turn;

use std::collections::BTreeMap;
use std::sync::Arc;

pub use form::parse;
pub use length::{Corner, Length, Round, Term, Unit};
pub use place::{Edge, Place, Rect, Span};

use crate::resources::DocId;

/// A shape's definition, checked against the grammar. Every value the
/// page writes from it is a number that the parser produced or a word
/// from a fixed list.
#[derive(Clone, Debug, PartialEq)]
pub struct Shape {
    pub name: String,
    pub origin: Origin,
    /// The physical thing that the shape draws, in one sentence.
    pub summary: String,
    pub size: Size,
    /// The height, in design units from the top, of the line on which a
    /// row of parts aligns. A shape whose width follows its text has
    /// none.
    pub axis: Option<f64>,
    pub outline: Outline,
    pub text: Text,
    pub display: Option<Display>,
    pub pieces: Vec<Piece>,
    /// The edges where the shape meets a neighbor that joins the facing
    /// edge with no gap.
    pub joins: Vec<Edge>,
    /// The edges where a wire attaches, each with its span.
    pub ports: Vec<Port>,
    /// Whether the shape lies on its side in an area wider than it is
    /// tall.
    pub turns: bool,
}

/// Where a shape comes from.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Origin {
    /// A file of the library, embedded in the binary.
    Library,
    /// A document under `--resources`.
    Document(DocId),
}

#[derive(Clone, Debug, PartialEq)]
pub struct Size {
    /// The width and the height in which every length is written, or
    /// none for a shape whose width follows its text, whose lengths are
    /// pixels.
    pub design: Option<[f64; 2]>,
    /// The narrowest and the widest width of a part, in pixels.
    pub width: [f64; 2],
    /// The widest width as a percentage of the sketch's width.
    pub share: Option<f64>,
}

/// The body of a part.
#[derive(Clone, Debug, PartialEq)]
pub struct Outline {
    pub draw: Material,
    pub round: Round,
    /// Where the body is, when it does not fill the part.
    pub at: Place,
}

/// One drawn detail of a shape, such as a foot, a knob, or a cone.
#[derive(Clone, Debug, PartialEq)]
pub struct Piece {
    /// What the piece is, for a reader of the definition. The page never
    /// shows it.
    pub name: String,
    pub draw: Material,
    pub at: Place,
    pub round: Round,
    /// Whether the piece is drawn under the body.
    pub behind: bool,
    /// The piece's outline as SVG path data, when it is not a
    /// rectangle.
    pub art: Option<Art>,
    /// The edge of the text region that keeps clear of the piece.
    pub clear: Option<Clear>,
}

/// SVG path data in a box of `view` units, stretched to the piece's
/// place.
#[derive(Clone, Debug, PartialEq)]
pub struct Art {
    pub view: [f64; 2],
    pub path: Path,
    /// Whether the path is drawn a quarter turn round, for a shape that
    /// lies on its side.
    pub turned: bool,
}

/// SVG path data that the parser checked: it starts with `M` or `m`,
/// and it has only the letters of the commands `M`, `L`, `H`, `V`, `C`,
/// `Q`, `A`, and `Z` in either case, digits, `.`, `-`, commas, and
/// spaces.
#[derive(Clone, Debug, PartialEq)]
pub struct Path(String);

/// The text region keeps clear of a piece on one edge: that edge of the
/// region moves to the piece's far side.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Clear {
    pub edge: Edge,
    /// The distance from the part's edge to the piece's far side, in
    /// design units.
    pub reach: f64,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Text {
    /// The text region's distance from each edge, in design units, in
    /// the order top, right, bottom, left.
    pub inset: [f64; 4],
    pub align: Align,
    pub fit: Fit,
}

/// One edge where wires attach, and the span along it, as percentages
/// of its length from its top or its left.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Port {
    pub edge: Edge,
    pub from: f64,
    pub to: f64,
}

/// What a body or a piece is made of. Each state of a part draws each
/// material in its own way.
#[derive(Clone, Copy, Debug, Default, serde::Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Material {
    /// The stuff of the body.
    #[default]
    Solid,
    /// A body that is dark in both color schemes, such as a screen's.
    Dark,
    /// A window, dark in both color schemes.
    Glass,
    /// An edge with no fill.
    Line,
    /// A small filled detail, such as a button.
    Mark,
}

#[derive(Clone, Copy, Debug, Default, serde::Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Align {
    #[default]
    Start,
    Center,
    End,
}

/// What a part does when its text needs more room than its proportions
/// give.
#[derive(Clone, Copy, Debug, Default, serde::Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Fit {
    /// The part grows taller.
    #[default]
    Wrap,
    /// The part keeps its proportions, and a line that is too long is
    /// cut.
    Cut,
}

/// Where a part shows its readings as lit characters.
#[derive(Clone, Copy, Debug, serde::Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Display {
    /// A pane of glass in the text shows the readings.
    Window,
    /// The whole text region is a display.
    Surface,
}

impl Material {
    pub fn word(self) -> &'static str {
        match self {
            Self::Solid => "solid",
            Self::Dark => "dark",
            Self::Glass => "glass",
            Self::Line => "line",
            Self::Mark => "mark",
        }
    }
}

impl Align {
    pub fn word(self) -> &'static str {
        match self {
            Self::Start => "start",
            Self::Center => "center",
            Self::End => "end",
        }
    }
}

impl Fit {
    pub fn word(self) -> &'static str {
        match self {
            Self::Wrap => "wrap",
            Self::Cut => "cut",
        }
    }
}

impl Display {
    pub fn word(self) -> &'static str {
        match self {
            Self::Window => "window",
            Self::Surface => "surface",
        }
    }
}

impl Path {
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

/// The room, in design units, that the text region keeps from a piece
/// that it keeps clear of.
const CLEAR_ROOM: f64 = 8.0;

impl Shape {
    /// The text region's distance from each edge, top, right, bottom,
    /// and left: the stated inset, or the far side of a piece that the
    /// text keeps clear of, with room, when that is further in.
    pub fn inset(&self) -> [f64; 4] {
        let mut inset = self.text.inset;
        for clear in self.pieces.iter().filter_map(|piece| piece.clear) {
            let side = &mut inset[clear.edge.index()];
            *side = side.max(clear.reach + CLEAR_ROOM);
        }
        inset
    }

    /// The design units that a piece reaches above the top edge, which
    /// a part keeps as room above it.
    pub fn above(&self) -> f64 {
        let [width, height] = self.size.design.unwrap_or_default();
        let tops = self.pieces.iter().map(|p| p.at.rect(width, height).top);
        tops.fold(0.0, |above, top| above.max(-top))
    }

    /// The design units that pieces reach past the sides and below the
    /// bottom: the room that a part alone needs around it.
    pub fn beyond(&self) -> (f64, f64) {
        let [width, height] = self.size.design.unwrap_or_default();
        let rects = self.pieces.iter().map(|p| p.at.rect(width, height));
        rects.fold((0.0, 0.0), |(side, below), rect| {
            let side = side.max(-rect.left).max(rect.right - width);
            (side, below.max(rect.bottom - height))
        })
    }

    pub fn joins(&self, edge: Edge) -> bool {
        self.joins.contains(&edge)
    }
}

/// Every shape that `transect` has loaded: the library's, and the
/// shapes of every author that loaded.
#[derive(Clone, Debug)]
pub struct Shapes {
    by_name: BTreeMap<String, Arc<Shape>>,
}

impl Default for Shapes {
    /// The library's shapes alone.
    fn default() -> Self {
        Self::with(&[])
    }
}

impl Shapes {
    /// The library's shapes and `authors`.
    pub fn with(authors: &[Arc<Shape>]) -> Self {
        let all = library::shapes().iter().chain(authors);
        let by_name = all.map(|shape| (shape.name.clone(), shape.clone()));
        Self {
            by_name: by_name.collect(),
        }
    }

    /// The shape that a role's `shape` names in the knowledge of a kind
    /// in `group`: the author's `<shape>.<group>` first, then the
    /// library's `<shape>`. A role may write the whole name of any
    /// author's shape.
    pub fn resolve(&self, written: &str, group: &str) -> Option<&Arc<Shape>> {
        if written.contains('.') || group.is_empty() {
            return self.by_name.get(written);
        }
        let own = self.by_name.get(&format!("{written}.{group}"));
        own.or_else(|| self.by_name.get(written))
    }

    /// Every shape, the library's by name and then each author's in the
    /// order of the documents they came from.
    pub fn listed(&self) -> Vec<&Arc<Shape>> {
        let mut shapes: Vec<&Arc<Shape>> = self.by_name.values().collect();
        shapes.sort_by(|a, b| (a.origin, &a.name).cmp(&(b.origin, &b.name)));
        shapes
    }
}

#[cfg(test)]
mod tests;
