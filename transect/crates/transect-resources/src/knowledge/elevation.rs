//! An elevation is an arrangement of roles that the author of a kind
//! states in its `KindKnowledge`, and that the page of an object of the
//! kind draws in place of the ring of boxes. The sketch places each role
//! in a rectangle of a grid, so the author can read the arrangement in
//! the YAML, and each role draws the parts that one of the kind's
//! references reads.

use std::collections::BTreeMap;
use std::ops::Range;

use serde::Deserialize;

use super::Reference;

/// One arrangement of roles, such as "the room".
#[derive(Clone, Debug, PartialEq)]
pub struct Elevation {
    /// What the page calls the elevation.
    pub name: String,
    /// The question that the elevation answers, as one sentence.
    pub question: String,
    pub grid: Grid,
    /// The roles in the order that their first cell appears in the
    /// sketch, from left to right and from top to bottom.
    pub roles: Vec<Role>,
}

/// The cells of a sketch, row by row. A cell is the name of a role, or
/// `None` for an empty cell, which the sketch writes as `.`. The parser
/// guarantees that every row has the same number of cells and that the
/// cells of each name form one filled rectangle.
#[derive(Clone, Debug, PartialEq)]
pub struct Grid {
    pub rows: Vec<Vec<Option<String>>>,
}

/// One place in an elevation, and the reference whose values fill it.
#[derive(Clone, Debug, PartialEq)]
pub struct Role {
    /// The name of the role in the sketch.
    pub name: String,
    /// The `path` of the entry of `references` whose values are the
    /// role's parts.
    pub reference: String,
    pub shape: Shape,
    /// Whether the page draws the role when its reference reads no
    /// value.
    pub expected: bool,
    /// The names of the printer columns of a part's kind that the part
    /// shows.
    pub reads: Vec<String>,
    /// The printer columns of the page's own object that each part of
    /// the role shows, such as what a player plays on the part that is
    /// its screen.
    pub shows: Vec<Show>,
}

/// One entry of a role's `shows`: the names of printer columns of the
/// page's own object, in order of preference. The part draws the first
/// of them that has a value, so an author can write "the play when one
/// plays, and the idle screen when none does". The parser guarantees
/// that the list is not empty.
#[derive(Clone, Debug, PartialEq)]
pub struct Show {
    pub columns: Vec<String>,
}

/// The shapes that `transect` draws for a part.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Shape {
    Screen,
    #[default]
    Box,
    Speaker,
    Handheld,
    /// A thing that is in the cluster and not in the picture, such as
    /// a message bus or a claim, drawn smaller and quieter than the
    /// things that the picture shows.
    Tag,
}

impl Shape {
    /// The shape that `name` writes in a role, or `None` when
    /// `transect` draws no such shape.
    fn named(name: &str) -> Option<Self> {
        Some(match name {
            "screen" => Self::Screen,
            "box" => Self::Box,
            "speaker" => Self::Speaker,
            "handheld" => Self::Handheld,
            "tag" => Self::Tag,
            _ => return None,
        })
    }

    /// The word that a document writes for the shape, which is also the
    /// end of its CSS class.
    pub fn word(self) -> &'static str {
        match self {
            Self::Screen => "screen",
            Self::Box => "box",
            Self::Speaker => "speaker",
            Self::Handheld => "handheld",
            Self::Tag => "tag",
        }
    }
}

impl Grid {
    /// The number of cells in each row.
    pub fn columns(&self) -> usize {
        self.rows.first().map_or(0, Vec::len)
    }

    /// The value of the CSS property `grid-template-areas`: one quoted
    /// string for each row, with `.` for an empty cell. Every name in
    /// the grid passed the parser's check of a role name, so the value
    /// contains no quote and no character that CSS reads as syntax.
    pub fn template_areas(&self) -> String {
        let rows = self.rows.iter().map(|row| {
            let cells: Vec<&str> = row
                .iter()
                .map(|cell| cell.as_deref().unwrap_or("."))
                .collect();
            format!("\"{}\"", cells.join(" "))
        });
        rows.collect::<Vec<_>>().join(" ")
    }

    /// The columns that the area of `name` spans, from its left column
    /// to the column after its right one, or `None` when no cell has
    /// the name.
    pub fn span(&self, name: &str) -> Option<Range<usize>> {
        let cells = self.rows.iter().flat_map(|row| row.iter().enumerate());
        let named = cells.filter(|(_, cell)| cell.as_deref() == Some(name));
        let columns: Vec<usize> = named.map(|(column, _)| column).collect();
        Some(*columns.iter().min()?..columns.iter().max()? + 1)
    }

    /// Each name in the grid once, in the order that its first cell
    /// appears.
    fn names(&self) -> Vec<&str> {
        let mut names: Vec<&str> = Vec::new();
        for name in self.rows.iter().flatten().flatten() {
            if !names.contains(&name.as_str()) {
                names.push(name);
            }
        }
        names
    }
}

/// One `elevations` entry, as written in the document.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Written {
    name: String,
    question: String,
    sketch: String,
    #[serde(default)]
    roles: BTreeMap<String, WrittenRole>,
}

/// One entry of an elevation's `roles`, as written in the document.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct WrittenRole {
    reference: String,
    shape: Option<String>,
    #[serde(default)]
    expected: bool,
    #[serde(default)]
    reads: Vec<String>,
    #[serde(default)]
    shows: Vec<WrittenShow>,
}

/// One entry of a role's `shows`, as written: one column's name, or a
/// list of names of which the first with a value is drawn.
#[derive(Deserialize)]
#[serde(untagged)]
enum WrittenShow {
    One(String),
    First(Vec<String>),
}

/// The elevations of `written`, checked against the kind's
/// `references`. The error names the first fault and the elevation it
/// is in.
pub fn read(written: Vec<Written>, references: &[Reference]) -> Result<Vec<Elevation>, String> {
    let mut elevations: Vec<Elevation> = Vec::new();
    for (at, one) in written.into_iter().enumerate() {
        let elevation = elevation(one, references).map_err(|e| format!("elevations[{at}]: {e}"))?;
        if elevations.iter().any(|e| e.name == elevation.name) {
            return Err(format!(
                "elevations[{at}]: the name {:?} is the name of an earlier elevation",
                elevation.name
            ));
        }
        elevations.push(elevation);
    }
    Ok(elevations)
}

fn elevation(written: Written, references: &[Reference]) -> Result<Elevation, String> {
    let grid = grid(&written.sketch)?;
    let names = grid.names();
    if let Some(name) = names
        .iter()
        .find(|name| !written.roles.contains_key(**name))
    {
        return Err(format!(
            "the sketch has the name {name}, and roles has no {name}"
        ));
    }
    if let Some(name) = written
        .roles
        .keys()
        .find(|name| !names.contains(&name.as_str()))
    {
        return Err(format!(
            "roles has {name}, and the sketch has no cell named {name}"
        ));
    }
    let mut roles = written.roles;
    let roles = names
        .iter()
        .filter_map(|name| roles.remove_entry(*name))
        .map(|(name, written)| role(name, written, references))
        .collect::<Result<_, _>>()?;
    Ok(Elevation {
        name: written.name,
        question: written.question,
        grid,
        roles,
    })
}

fn role(name: String, written: WrittenRole, references: &[Reference]) -> Result<Role, String> {
    if !references.iter().any(|r| r.path == written.reference) {
        return Err(format!(
            "roles.{name}: reference {} is the path of no entry of references",
            written.reference
        ));
    }
    let shape = match written.shape.as_deref() {
        None => Shape::default(),
        Some(shape) => Shape::named(shape).ok_or_else(|| {
            format!(
                "roles.{name}: shape {shape:?} is not one of screen, box, speaker, handheld, or tag"
            )
        })?,
    };
    let shows = written.shows.into_iter().enumerate().map(|(at, show)| {
        let columns = match show {
            WrittenShow::One(column) => vec![column],
            WrittenShow::First(columns) => columns,
        };
        if columns.is_empty() {
            return Err(format!(
                "roles.{name}: shows[{at}] is an empty list, and an entry of shows is a column's name or a list of one or more names"
            ));
        }
        Ok(Show { columns })
    });
    let shows = shows.collect::<Result<_, _>>()?;
    Ok(Role {
        name,
        reference: written.reference,
        shape,
        expected: written.expected,
        reads: written.reads,
        shows,
    })
}

/// Reads the text of a sketch into a grid. Blank lines are skipped, so
/// a block scalar's final newline adds no row.
fn grid(sketch: &str) -> Result<Grid, String> {
    let rows: Vec<Vec<Option<String>>> = sketch
        .lines()
        .filter(|line| !line.trim().is_empty())
        .map(|line| {
            let cells = line.split_whitespace();
            cells
                .map(|cell| (cell != ".").then(|| cell.to_string()))
                .collect()
        })
        .collect();
    let Some(first) = rows.first() else {
        return Err("the sketch has no rows".into());
    };
    let columns = first.len();
    for (at, row) in rows.iter().enumerate() {
        if row.len() != columns {
            return Err(format!(
                "row {} of the sketch has {} cells, and the first row has {columns}",
                at + 1,
                row.len()
            ));
        }
    }
    let grid = Grid { rows };
    for name in grid.names() {
        if !valid_name(name) {
            return Err(format!(
                "the name {name:?} in the sketch is not a lower-case letter followed by lower-case letters, digits, and hyphens"
            ));
        }
        if !rectangle(&grid, name) {
            return Err(format!(
                "the cells of {name} in the sketch do not form one filled rectangle"
            ));
        }
    }
    Ok(grid)
}

/// Whether `name` can name a role. The rule keeps a name a valid CSS
/// identifier, because the page writes it into `grid-template-areas`
/// and `grid-area`.
fn valid_name(name: &str) -> bool {
    let mut chars = name.chars();
    let first = chars.next().is_some_and(|c| c.is_ascii_lowercase());
    first && chars.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-')
}

/// Whether the cells named `name` fill the smallest rectangle that
/// contains them all. CSS refuses a `grid-template-areas` value whose
/// area is any other shape.
fn rectangle(grid: &Grid, name: &str) -> bool {
    let at: Vec<(usize, usize)> = grid
        .rows
        .iter()
        .enumerate()
        .flat_map(|(row, cells)| {
            let named = cells.iter().enumerate();
            let named = named.filter(|(_, cell)| cell.as_deref() == Some(name));
            named.map(move |(column, _)| (row, column))
        })
        .collect();
    let rows = at.iter().map(|(row, _)| *row);
    let columns = at.iter().map(|(_, column)| *column);
    let (Some(top), Some(bottom)) = (rows.clone().min(), rows.max()) else {
        return false;
    };
    let (Some(left), Some(right)) = (columns.clone().min(), columns.max()) else {
        return false;
    };
    at.len() == (bottom - top + 1) * (right - left + 1)
}

#[cfg(test)]
mod tests;
