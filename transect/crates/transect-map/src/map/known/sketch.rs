//! The sketch of a known kind: the first elevation of its
//! `KindKnowledge`, drawn in place of the ring of boxes. The author
//! states only where each role is, its shape, and which printer columns
//! its parts show. `transect` draws the state of each part the same way
//! in every sketch, so a failing part looks the same on every kind's
//! page.

mod lean;

pub use lean::Lean;

use super::{Board, End, end};
use transect_graph::edges::Object;
use transect_resources::knowledge::elevation::{Role, Show};
use transect_resources::knowledge::{Elevation, Grid, Names, Reached, Relation, Shape, Target};

/// The drawing of one elevation of the page's object.
#[derive(Debug, PartialEq)]
pub struct Sketch {
    /// The elevation's name, such as "the room".
    pub name: String,
    pub question: String,
    /// The number of columns of the grid.
    pub columns: usize,
    /// The value of the grid's `grid-template-areas`.
    pub areas: String,
    /// The roles that the page draws, in the order of the sketch. A
    /// role that is not expected and has no part is left out, so its
    /// area stays blank.
    pub roles: Vec<SketchRole>,
    /// The name of the `KindKnowledge` that states the elevation.
    pub knowledge: String,
}

/// One role and its parts, in the role's area of the grid.
#[derive(Debug, PartialEq)]
pub struct SketchRole {
    /// The role's name, which is also its `grid-area`.
    pub area: String,
    pub lean: Lean,
    pub shape: Shape,
    /// The reference's label, as the ring's box shows it.
    pub label: String,
    /// Where the parts were read, such as "from status.screen.node".
    pub source: String,
    pub parts: Vec<Part>,
    /// What an expected role with no part shows.
    pub nothing: Option<Nothing>,
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
    /// it. A screen draws these on its glass.
    pub shows: Vec<Shown>,
    pub state: State,
    /// The part names an address or a value and not an object, so its
    /// text breaks after a separator, as a reading's value does.
    pub literal: bool,
    /// Why the object is failing, warning, or at rest.
    pub why: Option<Why>,
}

/// One printer column of an object that has a value.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Reading {
    pub name: String,
    /// The value that `kubectl get` prints.
    pub value: String,
}

/// One entry of a role's `shows` that has a value.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Shown {
    pub name: String,
    pub value: String,
    /// The value is from a column after the first of its entry, such as
    /// the idle screen in place of the play. A screen draws it quieter,
    /// so a screen at rest looks different from a screen that plays.
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
}

/// What the cluster says about an object that a part names. Only the
/// binary crate reads the stores, so the page asks through
/// `Board::standing`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Standing {
    /// The object is drawn plainly: it is healthy, its kind has no
    /// knowledge that gives it a health, or `transect` does not watch
    /// its kind and so cannot say whether the object exists.
    Plain,
    /// `transect` watches the kind, and the cluster has no such object.
    Missing,
    Failing(Why),
    Warning(Why),
    /// The object's health condition is `False` with a calm reason.
    Resting(Why),
}

impl Sketch {
    /// The sketch of `elevation` for `board`'s object. Each role's
    /// parts are the relations whose path is the role's reference.
    pub fn build(board: &Board, elevation: &Elevation) -> Self {
        let roles = elevation.roles.iter();
        let grid = &elevation.grid;
        Self {
            name: elevation.name.clone(),
            question: elevation.question.clone(),
            columns: grid.columns(),
            areas: grid.template_areas(),
            roles: roles.filter_map(|role| drawn(board, grid, role)).collect(),
            knowledge: board.knowledge.name.clone(),
        }
    }

    /// The line under the sketch.
    pub fn caption(&self) -> String {
        format!(
            "Hover a part for the field it was read from. The places and shapes are from the KindKnowledge {}.",
            self.knowledge
        )
    }
}

impl SketchRole {
    pub fn shape_class(&self) -> String {
        format!("sketch-{}", self.shape.word())
    }
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
        }
    }

    pub fn class(self) -> &'static str {
        match self {
            Self::Plain => "sketch-plain",
            Self::Failing => "sketch-failing",
            Self::Warning => "sketch-warning",
            Self::Resting => "sketch-resting",
            Self::Value => "sketch-value",
            Self::NoObject => "sketch-no-object",
        }
    }
}

/// `role` as the page draws it, or `None` when it is not expected and
/// its reference reads no value.
fn drawn(board: &Board, grid: &Grid, role: &Role) -> Option<SketchRole> {
    let mut references = board.knowledge.references.iter();
    let reference = references.find(|r| r.path == role.reference)?;
    let shows = shows(board, &role.shows);
    let parts: Vec<Part> = board
        .reached
        .iter()
        .filter(|(relation, _)| relation.path == role.reference)
        .map(|(relation, reached)| part(board, role, &shows, relation, reached))
        .collect();
    if parts.is_empty() && !role.expected {
        return None;
    }
    let nothing = parts.is_empty().then(|| Nothing {
        text: match &reference.target {
            Target::Object { kind, .. } => format!("no {kind}"),
            Target::Address | Target::Value => "nothing".into(),
        },
        note: format!("{} names nothing now", role.reference),
    });
    let label = reference.label.as_deref().unwrap_or(reference.edge.words());
    let span = grid.span(&role.name).unwrap_or_default();
    Some(SketchRole {
        area: role.name.clone(),
        lean: Lean::of(span, grid.columns()),
        shape: role.shape,
        label: label.into(),
        source: format!("from {}", role.reference),
        parts,
        nothing,
    })
}

/// The part for one value of `role`'s reference. It links and notes the
/// value as the ring's box does, and takes its state from the object it
/// names. Only a part that names an object shows `shows`, so a role
/// whose reference reads nothing draws nothing of the page's object.
fn part(
    board: &Board,
    role: &Role,
    shows: &[Shown],
    relation: &Relation,
    reached: &Reached,
) -> Part {
    let here = board.object.namespace.as_deref();
    let mut end = end(relation, reached, here, board);
    let object = match &relation.names {
        Names::Object(object) => object,
        Names::Value => return quiet(end, State::Value, true),
        Names::Address | Names::Ambiguous(_) => return quiet(end, State::Plain, true),
    };
    let (state, why) = match (board.standing)(object) {
        Standing::Missing => {
            end.under = Some(format!("the cluster has no such {}", object.kind));
            end.reaches.clear();
            return quiet(end, State::NoObject, false);
        }
        Standing::Failing(why) => (State::Failing, Some(why)),
        Standing::Warning(why) => (State::Warning, Some(why)),
        Standing::Resting(why) => (State::Resting, Some(why)),
        Standing::Plain => (State::Plain, None),
    };
    let readings = read(board, object, &role.reads).into_iter();
    let readings = readings.map(|reading| Reading {
        name: reading.name.to_lowercase(),
        value: reading.value,
    });
    Part {
        readings: readings.collect(),
        shows: shows.to_vec(),
        end,
        state,
        why,
        literal: false,
    }
}

/// The first column of each entry of `shows` that has a value on the
/// page's own object.
fn shows(board: &Board, shows: &[Show]) -> Vec<Shown> {
    let names: Vec<String> = shows.iter().flat_map(|s| s.columns.clone()).collect();
    let read = read(board, board.object, &names);
    let first = |show: &Show| {
        let mut columns = show.columns.iter().enumerate();
        columns.find_map(|(at, column)| {
            let reading = read.iter().find(|reading| &reading.name == column)?;
            Some(Shown {
                name: reading.name.clone(),
                value: reading.value.clone(),
                fallback: at > 0,
            })
        })
    };
    shows.iter().filter_map(first).collect()
}

fn read(board: &Board, object: &Object, columns: &[String]) -> Vec<Reading> {
    if columns.is_empty() {
        return Vec::new();
    }
    (board.readings)(object, columns)
}

/// A part with no readings.
fn quiet(end: End, state: State, literal: bool) -> Part {
    Part {
        end,
        readings: Vec::new(),
        shows: Vec::new(),
        state,
        why: None,
        literal,
    }
}

#[cfg(test)]
mod tests;
