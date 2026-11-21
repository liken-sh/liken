//! The text of a part, the same block on every shape: the role's label,
//! the state as a word for a screen reader, the name that the part
//! links to, the reason of its health, its readings, what the page's
//! own object shows, the note under it, and the field it was read
//! from. A sketch fills it from a role, and `/shapes` fills it with the
//! same samples on every shape. `parts.html` writes it, and `text.css`
//! lays it out.

use transect_graph::links::Reference;

use super::Drawing;

/// The parts of one role, drawn in one shape, and what an expected role
/// with no part shows.
#[derive(Debug, PartialEq)]
pub struct Drawn {
    pub drawing: Drawing,
    /// The role's label, such as "shows on".
    pub label: String,
    /// Where the parts were read, such as "from status.screen.node".
    pub source: String,
    pub parts: Vec<Part>,
    pub nothing: Option<Nothing>,
}

/// The value that a part names: a link, the note under it, and what
/// it resolves to. The ring of boxes shows the same value in a box.
#[derive(Debug, PartialEq)]
pub struct End {
    pub reference: Reference,
    /// A note under the value: what it resolves to, such as the
    /// workload behind an address, or why it resolves to nothing.
    pub under: Option<String>,
    /// What the value resolves to, as `under` lists it, each linked to
    /// its page.
    pub reaches: Vec<Reference>,
}

/// One value that a role's reference reads.
#[derive(Debug, PartialEq)]
pub struct Part {
    pub end: End,
    /// Each printer column of the role's `reads` that has a value, with
    /// the column's name in lower case.
    pub readings: Vec<Reading>,
    /// Each entry of the role's `shows` that has a value, read from the
    /// page's own object, with the column's name as its CRD declares
    /// it. A shape with a display draws these as its title.
    pub shows: Vec<Shown>,
    pub state: State,
    /// The part names an address or a value and not an object, so its
    /// text breaks after a separator, as a reading's value does.
    pub literal: bool,
    /// Why the object is failing, warning, or at rest.
    pub why: Option<Why>,
}

/// One printer column of an object that has a value.
#[derive(Clone, Debug, PartialEq)]
pub struct Reading {
    pub name: String,
    /// The value that `kubectl get` prints.
    pub value: String,
    /// The object at the end of a reference that the value was read
    /// from, when it is not the part's own: its page, and a sentence
    /// such as "Charge of Pad aa-bb, through status.pad".
    pub from: Option<Reference>,
}

/// One entry of a role's `shows` that has a value.
#[derive(Clone, Debug, PartialEq)]
pub struct Shown {
    pub reading: Reading,
    /// The value is from a column after the first of its entry, such as
    /// the idle screen in place of the play. A display draws it quieter,
    /// so a display at rest looks different from a display that plays.
    pub fallback: bool,
}

/// The reason that the health condition of a part's object gives.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Why {
    /// A short line on the part: the condition's reason, such as
    /// `Unreachable`, or its type and status when it has no reason.
    pub reason: String,
    /// The condition's message, which the part shows when the reader
    /// points at the reason.
    pub message: Option<String>,
}

/// What an expected role with no part shows in its shape.
#[derive(Debug, PartialEq, Eq)]
pub struct Nothing {
    /// "no <Kind>" when the reference names a kind, and "nothing" when
    /// it does not.
    pub text: String,
    /// "<path> names nothing now".
    pub note: String,
    /// `Empty`, or `Elsewhere` for a role that is expected in another
    /// state of its object.
    pub state: State,
}

/// How a part is drawn.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum State {
    Plain,
    Failing,
    Warning,
    /// The object's health condition is `False` with a reason that its
    /// kind's knowledge lists as calm, so it is dimmed.
    Resting,
    /// The reference reads a value that is not an object, so the part
    /// has an outline and no fill.
    Value,
    /// The reference names an object of a kind that `transect` watches,
    /// and the cluster has no such object.
    NoObject,
    /// An expected role with no part, drawn dashed.
    Empty,
    /// A role with no part that is expected in another state of its
    /// object, drawn dashed and quieter.
    Elsewhere,
}

impl State {
    /// The state as a word for a screen reader. The page draws the
    /// state with the part's outline, so the word is not shown. A plain
    /// part has no word.
    pub fn spoken(self) -> Option<&'static str> {
        match self {
            Self::Plain => None,
            Self::Failing => Some("failing"),
            Self::Warning => Some("warning"),
            Self::Resting => Some("at rest"),
            Self::Value => Some("not an object"),
            Self::NoObject => Some("no such object"),
            Self::Empty => Some("empty"),
            Self::Elsewhere => Some("empty, expected in another state"),
        }
    }

    /// The class of a part in the state, which `shapes.css` draws the
    /// same way on every shape.
    pub fn class(self) -> &'static str {
        match self {
            Self::Plain => "shape-plain",
            Self::Failing => "shape-failing",
            Self::Warning => "shape-warning",
            Self::Resting => "shape-resting",
            Self::Value => "shape-value",
            Self::NoObject => "shape-no-object",
            Self::Empty => "shape-empty",
            Self::Elsewhere => "shape-elsewhere",
        }
    }
}
