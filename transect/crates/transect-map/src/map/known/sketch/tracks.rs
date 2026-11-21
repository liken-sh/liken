//! The rows of a sketch's grid. The rows of the sketch alternate with
//! gap tracks, so the gap between two rows can be zero where their
//! parts touch. The arrangement needs no field for it: the shapes state
//! where they join.
//!
//! A gap track runs across every column, so it closes only when every
//! pair of areas that face each other across it touch: the upper
//! area's shape joins on its bottom and the lower area's on its top. A
//! column where one side is empty, or where one area spans both rows,
//! does not keep the gap open. When one pair of a row joins and another
//! does not, every part keeps its gap, so no part touches a part that
//! does not join it.
//!
//! Contact is a fact of the wide layout. When the areas leave the grid
//! and wrap, below the width at which `sketch.css` lets them, nothing
//! joins, and every part keeps its gap.

use std::collections::BTreeSet;

use transect_resources::knowledge::Grid;

/// The grid's areas and rows.
#[derive(Debug, PartialEq)]
pub struct Tracks {
    /// The value of `grid-template-areas`, with a row for each gap.
    pub areas: String,
    /// The value of `grid-template-rows`.
    pub rows: String,
    /// The areas that touch an area above them.
    pub joined: BTreeSet<String>,
}

/// Whether the shape of a role joins on its top and on its bottom.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Joins {
    pub top: bool,
    pub bottom: bool,
}

/// The tracks of `grid`, where `joins` gives the edges that each drawn
/// role's shape joins, and none for a role that is not drawn, whose
/// area is empty.
pub fn tracks(grid: &Grid, joins: impl Fn(&str) -> Option<Joins>) -> Tracks {
    let mut joined = BTreeSet::new();
    let gaps: Vec<bool> = grid
        .rows
        .windows(2)
        .map(|pair| match touching(&pair[0], &pair[1], &joins) {
            Some(lower) if !lower.is_empty() => {
                joined.extend(lower);
                true
            }
            _ => false,
        })
        .collect();
    let cell = |cell: &Option<String>| cell.as_deref().unwrap_or(".").to_string();
    let mut areas = Vec::new();
    let mut rows = Vec::new();
    for (at, row) in grid.rows.iter().enumerate() {
        areas.push(row.iter().map(cell).collect::<Vec<_>>());
        rows.push("auto");
        let Some(next) = grid.rows.get(at + 1) else {
            continue;
        };
        // An area that spans this row and the next spans the gap
        // between them too.
        let between = row.iter().zip(next).map(|(this, next)| match this == next {
            true => cell(this),
            false => ".".into(),
        });
        areas.push(between.collect());
        rows.push(match gaps[at] {
            true => "0px",
            false => "var(--sketch-gap)",
        });
    }
    // Every name passed the parser's check of a role's name, so it
    // contains no quote and no character that CSS reads as syntax.
    let areas = areas.iter().map(|row| format!("\"{}\"", row.join(" ")));
    Tracks {
        areas: areas.collect::<Vec<_>>().join(" "),
        rows: rows.join(" "),
        joined,
    }
}

/// The lower areas that touch the row above, when every pair of areas
/// that face each other across the gap between `upper` and `lower`
/// joins, or none when a pair does not.
fn touching(
    upper: &[Option<String>],
    lower: &[Option<String>],
    joins: impl Fn(&str) -> Option<Joins>,
) -> Option<BTreeSet<String>> {
    let mut touch = BTreeSet::new();
    for (above, below) in upper.iter().zip(lower) {
        let (Some(above), Some(below)) = (above.as_deref(), below.as_deref()) else {
            continue;
        };
        let (Some(upper), Some(lower)) = (joins(above), joins(below)) else {
            continue;
        };
        if above == below {
            continue;
        }
        if !(upper.bottom && lower.top) {
            return None;
        }
        touch.insert(below.to_string());
    }
    Some(touch)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn grid(sketch: &str) -> Grid {
        let rows = sketch.lines().map(|line| {
            let cells = line.split_whitespace();
            cells
                .map(|cell| (cell != ".").then(|| cell.to_string()))
                .collect()
        });
        Grid {
            rows: rows.collect(),
        }
    }

    /// `slab` joins on every edge, `rail` on its bottom, `cart` and
    /// `peg` on their tops, and the rest on none. `ghost` is not drawn.
    fn joins(name: &str) -> Option<Joins> {
        let (top, bottom) = match name {
            "ghost" => return None,
            "slab" => (true, true),
            "rail" => (false, true),
            "cart" | "peg" => (true, false),
            _ => (false, false),
        };
        Some(Joins { top, bottom })
    }

    #[rstest]
    #[case::no_contact(
        "face face\nbox hand",
        r#""face face" ". ." "box hand""#,
        "auto var(--sketch-gap) auto",
        vec![]
    )]
    #[case::a_stack_of_parts_that_join(
        "rail .\nslab slab\npeg .",
        r#""rail ." ". ." "slab slab" ". ." "peg .""#,
        "auto 0px auto 0px auto",
        vec!["peg", "slab"]
    )]
    #[case::a_part_that_joins_over_one_that_does_not("slab\nbox", r#""slab" "." "box""#, "auto var(--sketch-gap) auto", vec![])]
    #[case::areas_in_other_columns(
        "slab .\n. peg",
        r#""slab ." ". ." ". peg""#,
        "auto var(--sketch-gap) auto",
        vec![]
    )]
    #[case::a_pair_that_joins_beside_a_pair_that_does_not(
        "rail box\ncart crate",
        r#""rail box" ". ." "cart crate""#,
        "auto var(--sketch-gap) auto",
        vec![]
    )]
    #[case::a_pair_that_joins_beside_an_empty_cell(
        "rail .\ncart crate",
        r#""rail ." ". ." "cart crate""#,
        "auto 0px auto",
        vec!["cart"]
    )]
    #[case::a_pair_that_joins_beside_a_role_not_drawn(
        "rail ghost\ncart box",
        r#""rail ghost" ". ." "cart box""#,
        "auto 0px auto",
        vec!["cart"]
    )]
    #[case::an_area_across_two_rows(
        "face hand\nface box",
        r#""face hand" "face ." "face box""#,
        "auto var(--sketch-gap) auto",
        vec![]
    )]
    fn writes_a_gap_track_between_rows_and_closes_it_where_parts_join(
        #[case] sketch: &str,
        #[case] areas: &str,
        #[case] rows: &str,
        #[case] joined: Vec<&str>,
    ) {
        let found = tracks(&grid(sketch), joins);
        let found_joined: Vec<&str> = found.joined.iter().map(String::as_str).collect();
        assert_eq!(
            (found.areas.as_str(), found.rows.as_str(), found_joined),
            (areas, rows, joined)
        );
    }
}
