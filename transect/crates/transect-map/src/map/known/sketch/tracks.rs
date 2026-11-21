//! The tracks of a sketch's grid. The rows of the sketch alternate with
//! gap tracks, and so do its columns, so the gap between two rows or two
//! columns can be zero where their parts touch. The arrangement needs no
//! field for it: the shapes state where they join.
//!
//! A gap track runs across the whole grid, so it closes only when every
//! pair of areas that face each other across it touch: the facing edges
//! of both shapes join. A cell that is empty, or whose role the page
//! leaves out, does not keep the gap open, and neither does one area
//! that spans both sides. When one pair joins and another does not,
//! every part keeps its gap, so no part touches a part that does not
//! join it. A column in which every cell belongs to a role that the page
//! leaves out takes no width, so the areas on either side of it face
//! each other, and a shelf with no lamp draws its label against its
//! bins. A column with an empty cell keeps its width. Each column keeps
//! the narrowest width of the parts in it, which `widths` reserves.
//!
//! A row in which two areas touch across a column is on one axis: its
//! parts align on their axes, as the parts of a row of bins stand on one
//! line. On an edge of such a row that a part joins, each
//! other area of the row adds nothing to the row's height, so the
//! joining part's edge is the row's edge, and a taller part stands past
//! it.
//!
//! Contact is a fact of the wide layout. When the areas leave the grid
//! and wrap, below the width at which `sketch.css` lets them, nothing
//! joins, and every part keeps its gap.

use std::collections::BTreeMap;

use super::widths;
use transect_resources::knowledge::Grid;

/// The grid's tracks and how each area meets its neighbors.
#[derive(Debug, PartialEq)]
pub struct Tracks {
    /// The value of `grid-template-areas`, with a row for each gap
    /// between rows and a column for each gap between columns.
    pub areas: String,
    /// The value of `grid-template-rows`.
    pub rows: String,
    /// The value of `grid-template-columns`.
    pub columns: String,
    /// How each drawn area meets its neighbors, by its name.
    pub contact: BTreeMap<String, Contact>,
    /// The edges of the whole grid that a part at that edge joins, so a
    /// grid drawn in the area of another grid joins there.
    pub joins: Joins,
    /// The narrowest width of the whole grid, in pixels.
    pub narrowest: f64,
}

/// The edges on which the shape of a role joins a neighbor.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Joins {
    pub top: bool,
    pub bottom: bool,
    pub left: bool,
    pub right: bool,
}

/// How one area meets its neighbors in the grid.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Contact {
    /// The area touches the area above it.
    pub above: bool,
    /// The area touches the area below it.
    pub below: bool,
    /// The area starts in the grid's top row.
    pub top_row: bool,
    /// The area ends in the grid's bottom row.
    pub bottom_row: bool,
    /// The area touches the area on its left.
    pub left: bool,
    /// The area touches the area on its right.
    pub right: bool,
    /// The area is in a row on one axis, so its parts align on it.
    pub axis: bool,
    /// Another area of its row on one axis joins the row's top edge, and
    /// this one does not, so it adds nothing to the row above the axis.
    /// It is set only where the row touches the row above, or at the top
    /// of the grid, where the area around a nested grid decides.
    pub free_top: bool,
    /// The same for the row's bottom edge.
    pub free_bottom: bool,
}

/// What the grid reads of a drawn role.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Footprint {
    pub joins: Joins,
    /// The narrowest width of one of the role's parts, in pixels, or
    /// zero for a role whose parts keep their own width.
    pub narrowest: f64,
}

/// The tracks of `grid`, where `footprint` gives what each drawn role
/// needs, and none for a role that the page leaves out, whose area is
/// empty. `gap` is the width in pixels of an open gap between columns.
pub fn tracks(grid: &Grid, footprint: impl Fn(&str) -> Option<Footprint>, gap: f64) -> Tracks {
    let count = grid.columns();
    let cell = |row: usize, column: usize| grid.rows[row][column].as_deref();
    let footprint = &footprint;
    let drawn = |name: &str| footprint(name).map(|found| found.joins);
    let collapsed: Vec<bool> = (0..count)
        .map(|column| {
            let cells = grid.rows.iter().map(|row| row[column].as_deref());
            cells
                .into_iter()
                .all(|c| c.is_some_and(|name| drawn(name).is_none()))
        })
        .collect();
    let visible: Vec<usize> = (0..count).filter(|c| !collapsed[*c]).collect();
    let mut contact: BTreeMap<String, Contact> = BTreeMap::new();
    let mut touch = |name: &str, set: &dyn Fn(&mut Contact)| {
        set(contact.entry(name.to_string()).or_default());
    };
    for name in grid
        .names()
        .into_iter()
        .filter(|name| drawn(name).is_some())
    {
        let rows = grid.rows_of(name).unwrap_or_default();
        let last = grid.rows.len();
        touch(name, &|c| {
            c.top_row = rows.start == 0;
            c.bottom_row = rows.end == last;
        });
    }
    // The gap after each row, closed when every pair across it joins.
    let row_gaps: Vec<bool> = (0..grid.rows.len().saturating_sub(1))
        .map(|at| {
            let pairs = (0..count).map(|column| (cell(at, column), cell(at + 1, column)));
            let joined = touching(pairs, |upper, lower| upper.bottom && lower.top, drawn);
            for (upper, lower) in joined.iter().flatten() {
                touch(upper, &|c| c.below = true);
                touch(lower, &|c| c.above = true);
            }
            joined.is_some_and(|pairs| !pairs.is_empty())
        })
        .collect();
    // The gap between each column and the next visible one, closed when
    // every pair across it joins, and the rows that it puts on an axis.
    let mut column_gaps: BTreeMap<usize, bool> = BTreeMap::new();
    let mut axes: Vec<usize> = Vec::new();
    for pair in visible.windows(2) {
        let (left, right) = (pair[0], pair[1]);
        let pairs = (0..grid.rows.len()).map(|row| (cell(row, left), cell(row, right)));
        let joined = touching(pairs, |a, b| a.right && b.left, drawn);
        let closed = joined.as_ref().is_some_and(|pairs| !pairs.is_empty());
        if closed {
            for (a, b) in joined.into_iter().flatten() {
                touch(a, &|c| c.right = true);
                touch(b, &|c| c.left = true);
                let single = [a, b].map(|name| grid.rows_of(name).filter(|rows| rows.len() == 1));
                axes.extend(single.into_iter().flatten().map(|rows| rows.start));
            }
        }
        column_gaps.insert(left, closed);
    }
    for row in axes {
        let members: Vec<&str> = grid
            .names()
            .into_iter()
            .filter(|name| grid.rows_of(name) == Some(row..row + 1))
            .filter(|name| drawn(name).is_some())
            .collect();
        // A row adds nothing past an edge only where it touches the row
        // beyond, so a part that stands past the edge of a grid that
        // touches nothing is inside the sketch's frame.
        let touches_above = row == 0 || row_gaps[row - 1];
        let touches_below = row + 1 == grid.rows.len() || row_gaps[row];
        let joins = |edge: fn(Joins) -> bool| members.iter().any(|m| drawn(m).is_some_and(edge));
        let joins_top = touches_above && joins(|j| j.top);
        let joins_bottom = touches_below && joins(|j| j.bottom);
        for member in members {
            let own = drawn(member).unwrap_or_default();
            touch(member, &|c| {
                c.axis = true;
                c.free_top = joins_top && !own.top;
                c.free_bottom = joins_bottom && !own.bottom;
            });
        }
    }
    let open = |column: usize| match column_gaps.get(&column) {
        Some(false) => gap,
        _ => 0.0,
    };
    let drawn_areas: Vec<_> = grid
        .names()
        .into_iter()
        .filter_map(|name| Some((grid.columns_of(name)?, footprint(name)?.narrowest)))
        .collect();
    let shown: Vec<bool> = collapsed.iter().map(|c| !c).collect();
    let narrowest = widths::columns(count, &drawn_areas, &shown, open);
    let rows = row_gaps.iter().map(|closed| match closed {
        true => "auto 0px",
        false => "auto var(--sketch-gap)",
    });
    let columns = collapsed.iter().enumerate().map(|(column, collapsed)| {
        let width = match (collapsed, narrowest[column]) {
            (true, _) => "0px".to_string(),
            (false, least) if least > 0.0 => format!("minmax({}px, 1fr)", least.ceil()),
            (false, _) => "minmax(0, 1fr)".to_string(),
        };
        let gap = match (column + 1 == count, column_gaps.get(&column)) {
            (true, _) => "",
            (false, Some(false)) => " var(--sketch-column-gap)",
            (false, _) => " 0px",
        };
        format!("{width}{gap}")
    });
    Tracks {
        areas: areas(grid),
        rows: rows.chain(["auto"]).collect::<Vec<_>>().join(" "),
        columns: columns.collect::<Vec<_>>().join(" "),
        contact,
        joins: edges(grid, &visible, drawn),
        narrowest: widths::total(&narrowest, open),
    }
}

/// The value of `grid-template-areas` for `grid`, with a row for each
/// gap between rows and a column for each gap between columns. An area
/// that spans two rows or two columns spans the gap between them too.
/// Every name passed the parser's check of a role's name, so it contains
/// no quote and no character that CSS reads as syntax.
fn areas(grid: &Grid) -> String {
    let count = grid.columns();
    let cell = |row: usize, column: usize| grid.rows[row][column].as_deref();
    let mut lines = Vec::new();
    for at in 0..grid.rows.len() {
        let name = |c: usize| cell(at, c).unwrap_or(".").to_string();
        lines.push(line(count, name, |c| between(cell(at, c), cell(at, c + 1))));
        if at + 1 == grid.rows.len() {
            continue;
        }
        let across = |c: usize| between(cell(at, c), cell(at + 1, c));
        let corner = |c: usize| {
            let four = [
                cell(at, c),
                cell(at, c + 1),
                cell(at + 1, c),
                cell(at + 1, c + 1),
            ];
            match four[0] {
                Some(name) if four.iter().all(|other| *other == Some(name)) => name.to_string(),
                _ => ".".into(),
            }
        };
        lines.push(line(count, across, corner));
    }
    let lines = lines.iter().map(|line| format!("\"{}\"", line.join(" ")));
    lines.collect::<Vec<_>>().join(" ")
}

/// One line of `grid-template-areas`: each of `count` cells, with the
/// cell of the gap after each but the last between them.
fn line(
    count: usize,
    cell: impl Fn(usize) -> String,
    gap: impl Fn(usize) -> String,
) -> Vec<String> {
    let mut line = Vec::new();
    for column in 0..count {
        line.push(cell(column));
        if column + 1 < count {
            line.push(gap(column));
        }
    }
    line
}

/// The cell between two cells: the area's name when one area spans
/// both, and empty otherwise.
fn between(a: Option<&str>, b: Option<&str>) -> String {
    match (a, b) {
        (Some(a), Some(b)) if a == b => a.to_string(),
        _ => ".".into(),
    }
}

/// The pairs of areas that face each other across a gap and join, or
/// none when a pair faces and does not join. `pairs` gives the two cells
/// of each line across the gap, and `join` whether the first area's
/// facing edge and the second's both join.
fn touching<'a>(
    pairs: impl Iterator<Item = (Option<&'a str>, Option<&'a str>)>,
    join: impl Fn(Joins, Joins) -> bool,
    drawn: impl Fn(&str) -> Option<Joins>,
) -> Option<Vec<(&'a str, &'a str)>> {
    let mut touch = Vec::new();
    for (a, b) in pairs {
        let (Some(a), Some(b)) = (a, b) else {
            continue;
        };
        let (Some(first), Some(second)) = (drawn(a), drawn(b)) else {
            continue;
        };
        if a == b {
            continue;
        }
        if !join(first, second) {
            return None;
        }
        if !touch.contains(&(a, b)) {
            touch.push((a, b));
        }
    }
    Some(touch)
}

/// The edges of the grid that a drawn area at that edge joins: the top
/// row, the bottom row, and the first and the last column that takes
/// width.
fn edges(grid: &Grid, visible: &[usize], drawn: impl Fn(&str) -> Option<Joins>) -> Joins {
    let last_row = grid.rows.len().saturating_sub(1);
    let (first, last) = (visible.first().copied(), visible.last().copied());
    let mut joins = Joins::default();
    for name in grid.names() {
        let (Some(own), Some(rows), Some(columns)) =
            (drawn(name), grid.rows_of(name), grid.columns_of(name))
        else {
            continue;
        };
        joins.top |= rows.start == 0 && own.top;
        joins.bottom |= rows.end == last_row + 1 && own.bottom;
        joins.left |= first.is_some_and(|f| columns.contains(&f)) && own.left;
        joins.right |= last.is_some_and(|l| columns.contains(&l)) && own.right;
    }
    joins
}

#[cfg(test)]
mod tests;
