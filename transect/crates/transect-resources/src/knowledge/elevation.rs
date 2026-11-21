//! An elevation is one arrangement of a kind's roles, such as "the
//! room" or "the signal path", that the page of an object of the kind
//! draws in place of the ring of boxes. Its sketch places each role in a
//! rectangle of a grid, so the author can read the arrangement in the
//! YAML, and its wires say what passes from one role to another. Every
//! elevation places roles from the same `spec.roles`, so a role is drawn
//! the same way in each.

use serde::Deserialize;

use super::grid::{self, Grid};
use super::role::Role;
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
    pub grid: Grid,
    pub wires: Vec<Wire>,
}

/// One `elevations` entry, as written in the document. `roles` is
/// read only to refuse it, because roles are stated once.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Written {
    name: String,
    question: String,
    sketch: String,
    #[serde(default)]
    wires: Vec<wire::Written>,
    roles: Option<serde_yaml::Value>,
}

/// The elevations of `written`, which place `roles`. The error names
/// the first fault and the elevation it is in.
pub fn read(written: Vec<Written>, roles: &[Role]) -> Result<Vec<Elevation>, String> {
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

fn elevation(written: Written, roles: &[Role]) -> Result<Elevation, String> {
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
    let grid = grid::read(&written.sketch)?;
    if let Some(name) = grid
        .names()
        .into_iter()
        .find(|n| !roles.iter().any(|r| r.name == *n))
    {
        return Err(format!(
            "the sketch has the name {name}, and spec.roles has no {name}"
        ));
    }
    let wires = wire::read(written.wires, &grid)?;
    Ok(Elevation {
        name: written.name,
        slug,
        question: written.question,
        grid,
        wires,
    })
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
