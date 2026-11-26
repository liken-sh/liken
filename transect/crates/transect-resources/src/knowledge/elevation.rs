//! An elevation is one arrangement of a kind's roles, such as "the
//! room" or "the signal path", that the page of an object of the kind
//! draws in place of the ring of boxes. It has one of three forms. A
//! sketch places each role in a rectangle of a grid, so the author can
//! read the arrangement in the YAML, and its wires say what passes from
//! one role to another. A sequence puts each role in a lane and states
//! the interactions between the lanes in order, for a system whose
//! parts take turns. Rows draw a table from a list in the object's
//! status. Every elevation places roles from the same `spec.roles`, so
//! a role is drawn the same way in each.

use serde::Deserialize;

use super::grid::{self, Grid};
use super::rows::{self, Rows};
use super::sequence::{self, Sequence};
use super::wire::{self, Wire};

/// One arrangement of roles, such as "the room".
#[derive(Clone, Debug, PartialEq)]
pub struct Elevation {
    /// What the page calls the elevation.
    pub name: String,
    /// The last part of the elevation's URL, made from its name.
    pub slug: String,
    /// The question that the elevation answers, as one sentence.
    pub question: String,
    pub form: Form,
}

/// How an elevation arranges its roles.
#[derive(Clone, Debug, PartialEq)]
pub enum Form {
    Sketch(Sketch),
    Sequence(Sequence),
    Rows(Rows),
}

/// The grid of a sketch, with its wires.
#[derive(Clone, Debug, PartialEq)]
pub struct Sketch {
    pub grid: Grid,
    pub wires: Vec<Wire>,
    /// The edge of its area that each part stands against.
    pub align: Align,
}

impl Elevation {
    /// The elevation's sketch, when its form is one.
    pub fn sketch(&self) -> Option<&Sketch> {
        match &self.form {
            Form::Sketch(sketch) => Some(sketch),
            Form::Sequence(_) | Form::Rows(_) => None,
        }
    }

    /// The names of the roles that the elevation places: the names in a
    /// sketch's grid, in the order of its cells, or a sequence's lanes in
    /// their order. Rows place no role.
    pub fn names(&self) -> Vec<&str> {
        match &self.form {
            Form::Sketch(sketch) => sketch.grid.names(),
            Form::Sequence(sequence) => sequence.lanes.iter().map(String::as_str).collect(),
            Form::Rows(_) => Vec::new(),
        }
    }
}

/// The edge of its area that a part stands against. A room's parts
/// stand on its floor, at the bottom of their areas, and the parts of a
/// row of pictures that a wire joins can face each other at the top.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Align {
    Top,
    #[default]
    Bottom,
}

impl Align {
    pub fn word(self) -> &'static str {
        match self {
            Self::Top => "top",
            Self::Bottom => "bottom",
        }
    }
}

/// One `elevations` entry, as written in the document. `roles` is
/// read only to refuse it, because roles are stated once.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Written {
    name: String,
    question: String,
    sketch: Option<String>,
    sequence: Option<sequence::Written>,
    rows: Option<rows::Written>,
    #[serde(default)]
    wires: Vec<wire::Written>,
    align: Option<String>,
    roles: Option<serde_yaml_ng::Value>,
}

/// The elevations of `written`, which place the roles named in
/// `roles`. The error names the first fault and the elevation it is in.
pub fn read(written: Vec<Written>, roles: &[&str]) -> Result<Vec<Elevation>, String> {
    let mut elevations: Vec<Elevation> = Vec::new();
    for (at, one) in written.into_iter().enumerate() {
        let elevation = elevation(one, roles).map_err(|e| format!("elevations[{at}]: {e}"))?;
        if let Some(earlier) = elevations.iter().position(|e| e.slug == elevation.slug) {
            return Err(format!(
                "elevations[{at}]: the name {:?} makes the slug {}, which elevations[{earlier}] has",
                elevation.name, elevation.slug
            ));
        }
        elevations.push(elevation);
    }
    Ok(elevations)
}

fn elevation(written: Written, roles: &[&str]) -> Result<Elevation, String> {
    if written.roles.is_some() {
        return Err(
            "the elevation has roles, and roles are stated once, in spec.roles, for every elevation to place".into(),
        );
    }
    let slug = slug(&written.name);
    if slug.is_empty() {
        return Err(format!(
            "the name {:?} has no letter or digit, so it makes no slug for the elevation's URL",
            written.name
        ));
    }
    let form = match (written.sketch, written.sequence, written.rows) {
        (Some(sketch), None, None) => {
            Form::Sketch(sketch_form(&sketch, written.wires, written.align, roles)?)
        }
        (None, Some(sequence), None) => {
            only_a_sketch(&written.wires, &written.align)?;
            Form::Sequence(sequence::read(sequence, roles)?)
        }
        (None, None, Some(rows)) => {
            only_a_sketch(&written.wires, &written.align)?;
            Form::Rows(rows::read(rows)?)
        }
        (None, None, None) => {
            return Err(
                "the elevation has none of sketch, sequence, and rows, so it draws nothing".into(),
            );
        }
        _ => {
            return Err(
                "the elevation has more than one of sketch, sequence, and rows, and it is drawn in one form".into(),
            );
        }
    };
    Ok(Elevation {
        name: written.name,
        slug,
        question: written.question,
        form,
    })
}

/// The sketch whose grid `written` writes, with its wires and the edge
/// that its parts stand against.
fn sketch_form(
    written: &str,
    wires: Vec<wire::Written>,
    align: Option<String>,
    roles: &[&str],
) -> Result<Sketch, String> {
    let grid = grid::read(written)?;
    if let Some(name) = grid.names().into_iter().find(|n| !roles.contains(n)) {
        return Err(format!(
            "the sketch has the name {name}, and spec.roles has no {name}"
        ));
    }
    let wires = wire::read(wires, &grid)?;
    let align = match align.as_deref() {
        None | Some("bottom") => Align::Bottom,
        Some("top") => Align::Top,
        Some(other) => {
            return Err(format!(
                "align is {other:?}, and it is \"top\" or \"bottom\""
            ));
        }
    };
    Ok(Sketch { grid, wires, align })
}

/// Refuses `wires` and `align` on an elevation that is not a sketch,
/// because both are about the areas of a grid.
fn only_a_sketch(wires: &[wire::Written], align: &Option<String>) -> Result<(), String> {
    match (wires.is_empty(), align) {
        (true, None) => Ok(()),
        (false, _) => Err("the elevation has wires, and only a sketch has wires".into()),
        (true, Some(_)) => {
            Err("the elevation has align, and only a sketch aligns its parts".into())
        }
    }
}

/// The slug of an elevation named `name`: the name in lower case, with
/// each run of characters other than the letters `a` to `z` and the
/// digits made one hyphen, and no hyphen at either end, so the slug needs
/// no escaping in a URL. "The signal path" makes `the-signal-path`.
pub fn slug(name: &str) -> String {
    let lower = name.to_ascii_lowercase();
    let words = lower.split(|c: char| !c.is_ascii_alphanumeric());
    let words: Vec<&str> = words.filter(|word| !word.is_empty()).collect();
    words.join("-")
}

#[cfg(test)]
mod tests;
