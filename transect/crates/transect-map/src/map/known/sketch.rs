//! The sketch of a known kind: the first elevation of its
//! `KindKnowledge`, drawn in place of the ring of boxes. The author
//! states only where each role is, its shape, and which printer columns
//! its parts show. `transect` draws the state of each part the same way
//! in every sketch, so a failing part looks the same on every kind's
//! page.

mod lean;
mod tracks;

pub use lean::Lean;
use tracks::{Joins, tracks};

use super::{Board, end};
use crate::shapes::text::{Drawn, End, Nothing, Part, Reading, Shown, State, Why};
use crate::shapes::{Drawing, Pose};
use transect_graph::edges::Object;
use transect_resources::knowledge::elevation::{Role, Show};
use transect_resources::knowledge::{Elevation, Grid, Names, Reached, Relation, Target};
use transect_resources::shapes::{Edge, Shape};

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
    /// The value of the grid's `grid-template-rows`.
    pub rows: String,
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
    /// Whether the role's parts touch the parts of the area above.
    pub joined: bool,
    /// Whether the role's parts touch each other, because their shape
    /// joins on its left and its right.
    pub abutting: bool,
    /// The role's parts in its shape. Their label is the reference's
    /// label, as the ring's box shows it.
    pub drawn: Drawn,
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
        let grid = &elevation.grid;
        let drawn: Vec<(SketchRole, Shape)> = elevation
            .roles
            .iter()
            .filter_map(|role| drawn(board, grid, role))
            .collect();
        let joins = |name: &str| {
            let found = drawn.iter().find(|(role, _)| role.area == name);
            found.map(|(_, shape)| Joins {
                top: shape.joins(Edge::Top),
                bottom: shape.joins(Edge::Bottom),
            })
        };
        let tracks = tracks(grid, joins);
        let roles = drawn.into_iter().map(|(role, _)| SketchRole {
            joined: tracks.joined.contains(&role.area),
            ..role
        });
        Self {
            name: elevation.name.clone(),
            question: elevation.question.clone(),
            columns: grid.columns(),
            areas: tracks.areas,
            rows: tracks.rows,
            roles: roles.collect(),
            knowledge: board.knowledge.name.clone(),
        }
    }

    /// The line under the sketch.
    pub fn caption(&self) -> String {
        format!(
            "Hover a part for the field it was read from. The places and shapes are from the KindKnowledge {}, and /shapes draws each shape.",
            self.knowledge
        )
    }
}

impl SketchRole {
    /// The class of the role's area: the edge that its parts stand
    /// against, and whether they touch the parts above.
    pub fn area_class(&self) -> String {
        let mut class = self.lean.area_class().to_string();
        if self.joined {
            class.push_str(" sketch-joined-above");
        }
        if self.abutting {
            class.push_str(" sketch-abutting");
        }
        class
    }
}

/// `role` as the page draws it, with the shape it is drawn in, or
/// `None` when it is not expected and its reference reads no value. A
/// shape that turns lies on its side in an area that spans more columns
/// than rows.
fn drawn(board: &Board, grid: &Grid, role: &Role) -> Option<(SketchRole, Shape)> {
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
        state: State::Empty,
    });
    let label = reference.label.as_deref().unwrap_or(reference.edge.words());
    let columns = grid.columns_of(&role.name).unwrap_or_default();
    let rows = grid.rows_of(&role.name).unwrap_or_default();
    let shape = match role.drawn.turns && columns.len() > rows.len() {
        true => role.drawn.turned(),
        false => role.drawn.as_ref().clone(),
    };
    let drawn = SketchRole {
        area: role.name.clone(),
        lean: Lean::of(columns, grid.columns()),
        joined: false,
        abutting: shape.joins(Edge::Left) && shape.joins(Edge::Right),
        drawn: Drawn {
            drawing: Drawing::of(&shape, Pose::default()),
            label: label.into(),
            source: format!("from {}", role.reference),
            parts,
            nothing,
        },
    };
    Some((drawn, shape))
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
