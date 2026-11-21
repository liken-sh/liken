//! The grid of one elevation of an object, with its roles and its wires:
//! the drawing of the page's own object, and of each part that a role
//! draws as an elevation of the part's kind. Both are built here and
//! written by `plan.html`, so a nested elevation is drawn the same way as
//! the page's.
//!
//! An object is a part of at most one role in an elevation, because a
//! sketch is a picture of one thing, and an object drawn twice reads as
//! two things. The roles are taken in the order of their first cells,
//! row by row from the top and from left to right, and the first role
//! that reads an object draws it. A nested elevation leaves out a part
//! that is the page's own object, because the page around it is that
//! object.

use askama::Template;

use super::super::board::Board;
use super::super::board::Subject;
use super::area::{self, Fills, SketchRole};
use super::fill::{self, Found};
use super::tracks::{Footprint, Joins, tracks};
use super::wires::{self, Placed, Presence, SketchWire};
use transect_graph::edges::Object;
use transect_graph::links::Reference;
use transect_resources::knowledge::Elevation;

/// The gap between two columns of a grid with no wires and of a grid
/// with wires, in pixels, as `sketch.css` draws them when a grid stands
/// at its narrowest width in the wide layout.
const COLUMN_GAP: f64 = 20.0;
const WIRED_COLUMN_GAP: f64 = 64.0;

/// The drawing of one elevation's grid.
#[derive(Debug, PartialEq, Template)]
#[template(path = "map/known/plan.html")]
pub struct Plan {
    /// The elevation's name, which names its wires for a screen reader.
    pub name: String,
    /// The value of the grid's `grid-template-columns`.
    pub columns: String,
    /// The value of the grid's `grid-template-areas`.
    pub areas: String,
    /// The value of the grid's `grid-template-rows`.
    pub rows: String,
    /// The roles that the page draws, in the order of the sketch. A
    /// role that is not expected and has no part is left out, so its
    /// area stays blank.
    pub roles: Vec<SketchRole>,
    /// The wires between the roles that the page draws.
    pub wires: Vec<SketchWire>,
    /// The edges of the grid on which a part at that edge joins, so the
    /// area of a role whose parts are drawn as this elevation joins
    /// there.
    pub joins: Joins,
    /// The narrowest width of the grid, in pixels, which the area of a
    /// role whose parts are drawn as this elevation keeps.
    pub narrowest: f64,
}

impl Plan {
    /// The grid of `elevation` for `subject`: the page's own object, or,
    /// when `nested`, an object that a part of the page's sketch draws.
    pub fn build(board: &Board, subject: &Subject, elevation: &Elevation, nested: bool) -> Self {
        let grid = &elevation.grid;
        let page = &board.subject.object;
        let wired = |name: &str| {
            let mut pairs = elevation.wires.iter().flat_map(|w| w.pairs());
            pairs.any(|(a, b)| a == name || b == name)
        };
        let mut drawn_by: Vec<(Object, String)> = Vec::new();
        let mut drawn: Vec<(SketchRole, Footprint)> = Vec::new();
        for name in grid.names() {
            let roles = &subject.knowledge.roles;
            let Some(role) = roles.iter().find(|role| role.name == name) else {
                continue;
            };
            let found = fill::found(board, subject, role);
            let the_page = |found: &Found| nested && found.object() == Some(page);
            let only_the_page = !found.is_empty() && found.iter().all(the_page);
            let mut parts = Vec::new();
            let mut elsewhere = Vec::new();
            for one in found.into_iter().filter(|found| !the_page(found)) {
                let earlier = one.object().and_then(|object| {
                    let mut drawn = drawn_by.iter();
                    drawn.find(|(other, _)| other == object).cloned()
                });
                match earlier {
                    Some(earlier) => elsewhere.push(earlier),
                    None => parts.push(one),
                }
            }
            let label = role.label(&subject.knowledge.references);
            let objects = parts.iter().filter_map(Found::object);
            drawn_by.extend(objects.map(|object| (object.clone(), label.clone())));
            let fills = Fills {
                parts: &parts,
                elsewhere: &elsewhere,
                only_the_page,
                wired: wired(name),
                nested,
            };
            drawn.extend(area::draw(board, subject, grid, role, fills));
        }
        let placed = |name: &str| placed(subject, &drawn, name);
        let wires = wires::wires(elevation, &subject.knowledge.name, placed);
        let footprint = |name: &str| {
            let found = drawn.iter().find(|(role, _)| role.area == name);
            found.map(|(_, footprint)| *footprint)
        };
        let gap = match elevation.wires.is_empty() {
            true => COLUMN_GAP,
            false => WIRED_COLUMN_GAP,
        };
        let tracks = tracks(grid, footprint, gap);
        let roles = drawn.into_iter().map(|(role, _)| SketchRole {
            contact: tracks.contact.get(&role.area).copied().unwrap_or_default(),
            ..role
        });
        Self {
            name: elevation.name.clone(),
            columns: tracks.columns,
            areas: tracks.areas,
            rows: tracks.rows,
            roles: roles.collect(),
            wires,
            joins: tracks.joins,
            narrowest: tracks.narrowest,
        }
    }

    /// Each object or value that the plan draws, as the drawing names
    /// it: the parts of each role, and each part that is drawn as an
    /// elevation with the parts of that elevation.
    pub fn references(&self) -> Vec<&Reference> {
        let mut found = Vec::new();
        for role in &self.roles {
            found.extend(role.drawn.parts.iter().map(|part| &part.end.reference));
            for nested in &role.nested {
                found.push(&nested.name);
                found.extend(nested.plan.references());
            }
        }
        found
    }
}

/// How the page draws the role `name`, as a wire names it.
fn placed(subject: &Subject, drawn: &[(SketchRole, Footprint)], name: &str) -> Placed {
    let roles = &subject.knowledge.roles;
    let role = roles.iter().find(|role| role.name == name);
    let path = role.map(|role| role.fill.fields()).unwrap_or_default();
    let Some((role, _)) = drawn.iter().find(|(role, _)| role.area == name) else {
        return Placed {
            presence: Presence::Absent,
            names: Vec::new(),
            path,
        };
    };
    let parts = role
        .drawn
        .parts
        .iter()
        .map(|part| part.end.reference.clone());
    let nested = role.nested.iter().map(|nested| nested.name.clone());
    let nothing = role.drawn.nothing.iter().map(|nothing| Reference {
        text: nothing.text.clone(),
        href: None,
    });
    let has_parts = !role.drawn.parts.is_empty() || !role.nested.is_empty();
    Placed {
        presence: match has_parts {
            true => Presence::Parts,
            false => Presence::Empty,
        },
        names: parts.chain(nested).chain(nothing).collect(),
        path,
    }
}
