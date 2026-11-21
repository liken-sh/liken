//! The grid of a sketch: the lines of names that an author writes, in
//! which each name is a role and its cells are the role's area. The
//! author can read the arrangement in the YAML, and the page writes the
//! grid as `grid-template-areas`, so every area is one filled
//! rectangle.

use std::ops::Range;
use std::sync::Arc;

use super::role::valid_name;
use crate::shapes::{Edge, Shape};

/// The cells of a sketch, row by row. A cell is the name of a role, or
/// `None` for an empty cell, which the sketch writes as `.`. The parser
/// guarantees that every row has the same number of cells and that the
/// cells of each name form one filled rectangle.
#[derive(Clone, Debug, PartialEq)]
pub struct Grid {
    pub rows: Vec<Vec<Option<String>>>,
}

impl Grid {
    /// The number of cells in each row.
    pub fn columns(&self) -> usize {
        self.rows.first().map_or(0, Vec::len)
    }

    /// The columns that the area of `name` spans, from its left column
    /// to the column after its right one, or `None` when no cell has
    /// the name.
    pub fn columns_of(&self, name: &str) -> Option<Range<usize>> {
        let cells = self.rows.iter().flat_map(|row| row.iter().enumerate());
        let named = cells.filter(|(_, cell)| cell.as_deref() == Some(name));
        let columns: Vec<usize> = named.map(|(column, _)| column).collect();
        Some(*columns.iter().min()?..columns.iter().max()? + 1)
    }

    /// The rows that the area of `name` spans, from its top row to the
    /// row after its bottom one, or `None` when no cell has the name.
    pub fn rows_of(&self, name: &str) -> Option<Range<usize>> {
        let rows = self.rows.iter().enumerate();
        let named = rows.filter(|(_, cells)| cells.iter().any(|c| c.as_deref() == Some(name)));
        let rows: Vec<usize> = named.map(|(row, _)| row).collect();
        Some(*rows.first()?..rows.last()? + 1)
    }

    /// Each name in the grid once, in the order that its first cell
    /// appears.
    pub fn names(&self) -> Vec<&str> {
        let mut names: Vec<&str> = Vec::new();
        for name in self.rows.iter().flatten().flatten() {
            if !names.contains(&name.as_str()) {
                names.push(name);
            }
        }
        names
    }

    /// `shape`, as a role is drawn in the area of `name`: on its side
    /// when the shape turns and the area spans more columns than rows.
    pub fn posed(&self, name: &str, shape: &Arc<Shape>) -> Shape {
        let columns = self.columns_of(name).unwrap_or_default();
        let rows = self.rows_of(name).unwrap_or_default();
        match shape.turns && columns.len() > rows.len() {
            true => shape.turned(),
            false => shape.as_ref().clone(),
        }
    }

    /// The edges of the areas of `from` and `to` that face each other. Two
    /// areas in other columns face across the columns, and two areas that
    /// share a column face across the rows.
    pub fn facing(&self, from: &str, to: &str) -> Option<(Edge, Edge)> {
        let (from_columns, to_columns) = (self.columns_of(from)?, self.columns_of(to)?);
        if from_columns.end <= to_columns.start {
            return Some((Edge::Right, Edge::Left));
        }
        if to_columns.end <= from_columns.start {
            return Some((Edge::Left, Edge::Right));
        }
        let (from_rows, to_rows) = (self.rows_of(from)?, self.rows_of(to)?);
        match from_rows.end <= to_rows.start {
            true => Some((Edge::Bottom, Edge::Top)),
            false => Some((Edge::Top, Edge::Bottom)),
        }
    }
}

/// Reads the text of a sketch into a grid. Blank lines are skipped, so
/// a block scalar's final newline adds no row.
pub fn read(sketch: &str) -> Result<Grid, String> {
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
