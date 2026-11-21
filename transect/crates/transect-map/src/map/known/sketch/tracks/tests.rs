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

/// The footprint of each role of `joins`, whose parts keep their own
/// width.
fn footprint(name: &str) -> Option<Footprint> {
    joins(name).map(|joins| Footprint {
        joins,
        narrowest: 0.0,
    })
}

/// `slab` joins on every edge, `rail` on its bottom, `cart` and `peg`
/// on their tops, `link` on its left and right, `hook` on its left,
/// `plate` on its right, and the rest on none. `ghost` is not drawn.
fn joins(name: &str) -> Option<Joins> {
    let [top, bottom, left, right] = match name {
        "ghost" => return None,
        "slab" => [true, true, true, true],
        "rail" => [false, true, false, false],
        "cart" | "peg" => [true, false, false, false],
        "link" => [false, false, true, true],
        "hook" => [false, false, true, false],
        "plate" => [false, false, false, true],
        _ => [false; 4],
    };
    Some(Joins {
        top,
        bottom,
        left,
        right,
    })
}

const GAP: &str = "var(--sketch-gap)";
const COLUMN_GAP: &str = "var(--sketch-column-gap)";
const FR: &str = "minmax(0, 1fr)";

#[rstest]
#[case::no_contact(
    "face face\nbox hand",
    r#""face face face" ". . ." "box . hand""#,
    format!("auto {GAP} auto"),
    format!("{FR} {COLUMN_GAP} {FR}")
)]
#[case::a_stack_of_parts_that_join(
    "rail .\nslab slab\npeg .",
    r#""rail . ." ". . ." "slab slab slab" ". . ." "peg . .""#,
    "auto 0px auto 0px auto".into(),
    format!("{FR} {COLUMN_GAP} {FR}")
)]
#[case::a_part_that_joins_over_one_that_does_not(
    "slab\nbox",
    r#""slab" "." "box""#,
    format!("auto {GAP} auto"),
    FR.into()
)]
#[case::areas_in_other_columns(
    "slab .\n. peg",
    r#""slab . ." ". . ." ". . peg""#,
    format!("auto {GAP} auto"),
    format!("{FR} {COLUMN_GAP} {FR}")
)]
#[case::a_pair_that_joins_beside_a_pair_that_does_not(
    "rail box\ncart crate",
    r#""rail . box" ". . ." "cart . crate""#,
    format!("auto {GAP} auto"),
    format!("{FR} {COLUMN_GAP} {FR}")
)]
#[case::a_pair_that_joins_beside_an_empty_cell(
    "rail .\ncart crate",
    r#""rail . ." ". . ." "cart . crate""#,
    "auto 0px auto".into(),
    format!("{FR} {COLUMN_GAP} {FR}")
)]
#[case::a_pair_that_joins_beside_a_role_not_drawn(
    "rail ghost\ncart box",
    r#""rail . ghost" ". . ." "cart . box""#,
    "auto 0px auto".into(),
    format!("{FR} {COLUMN_GAP} {FR}")
)]
#[case::an_area_across_two_rows_and_two_columns(
    "face face hand\nface face box",
    r#""face face face . hand" "face face face . ." "face face face . box""#,
    format!("auto {GAP} auto"),
    format!("{FR} {COLUMN_GAP} {FR} {COLUMN_GAP} {FR}")
)]
#[case::neighbors_that_join_across_a_column(
    "plate slab link hook box",
    r#""plate . slab . link . hook . box""#,
    "auto".into(),
    format!("{FR} 0px {FR} 0px {FR} 0px {FR} {COLUMN_GAP} {FR}")
)]
#[case::a_column_of_roles_not_drawn(
    "slab ghost hook",
    r#""slab . ghost . hook""#,
    "auto".into(),
    format!("{FR} 0px 0px 0px {FR}")
)]
#[case::a_column_with_an_empty_cell(
    "slab ghost hook\n. . .",
    r#""slab . ghost . hook" ". . . . ." ". . . . .""#,
    format!("auto {GAP} auto"),
    format!("{FR} {COLUMN_GAP} {FR} {COLUMN_GAP} {FR}")
)]
fn writes_gap_tracks_and_closes_them_where_parts_join(
    #[case] sketch: &str,
    #[case] areas: &str,
    #[case] rows: String,
    #[case] columns: String,
) {
    let found = tracks(&grid(sketch), footprint, 20.0);
    assert_eq!(
        (found.areas.as_str(), found.rows, found.columns),
        (areas, rows, columns)
    );
}

/// Which neighbors an area touches: above, below, left, and right.
type Touches = [bool; 4];

#[rstest]
#[case::a_stack_of_parts_that_join("rail .\nslab slab\npeg .", "peg", [true, false, false, false])]
#[case::the_upper_of_a_stack("rail .\nslab slab\npeg .", "rail", [false, true, false, false])]
#[case::a_pair_that_does_not_join("rail box\ncart crate", "cart", [false; 4])]
#[case::between_two_neighbors("plate slab hook", "slab", [false, false, true, true])]
#[case::after_a_neighbor("plate slab hook", "hook", [false, false, true, false])]
#[case::before_a_role_not_drawn("slab ghost hook", "slab", [false, false, false, true])]
fn says_which_neighbors_each_area_touches(
    #[case] sketch: &str,
    #[case] area: &str,
    #[case] expected: Touches,
) {
    let found = tracks(&grid(sketch), footprint, 20.0);
    let c = found.contact[area];
    assert_eq!([c.above, c.below, c.left, c.right], expected);
}

#[rstest]
#[case::the_top_row("rail .\nslab slab\npeg .", "rail", (true, false))]
#[case::the_bottom_row("rail .\nslab slab\npeg .", "peg", (false, true))]
#[case::between("rail .\nslab slab\npeg .", "slab", (false, false))]
#[case::one_row("plate slab hook", "slab", (true, true))]
fn says_whether_an_area_is_at_the_top_or_the_bottom_of_the_grid(
    #[case] sketch: &str,
    #[case] area: &str,
    #[case] expected: (bool, bool),
) {
    let c = tracks(&grid(sketch), footprint, 20.0).contact[area];
    assert_eq!((c.top_row, c.bottom_row), expected);
}

/// In a row of `slab`, `link`, and `box`, the slab joins the row's top
/// and bottom, so the link and the box add nothing to the row there.
/// The slab's row is on one axis, and the row of the rail is not.
#[rstest]
#[case::the_part_that_joins_the_edges("slab", (true, false, false))]
#[case::a_part_that_joins_only_its_sides("link", (true, true, true))]
#[case::a_part_that_joins_nothing("box", (true, true, true))]
#[case::a_row_with_no_contact_across_a_column("rail", (false, false, false))]
fn puts_a_row_whose_areas_touch_across_a_column_on_one_axis(
    #[case] area: &str,
    #[case] expected: (bool, bool, bool),
) {
    let found = tracks(&grid("rail . .\nslab link box"), footprint, 20.0);
    let contact = found.contact[area];
    assert_eq!(
        (contact.axis, contact.free_top, contact.free_bottom),
        expected
    );
}

#[rstest]
#[case::every_edge("slab", Joins { top: true, bottom: true, left: true, right: true })]
#[case::the_edges_of_parts_at_them(
    "rail hook\nslab box",
    Joins { top: false, bottom: true, left: true, right: false }
)]
#[case::past_a_column_of_roles_not_drawn("plate ghost", Joins { right: true, ..Joins::default() })]
#[case::none_for_a_part_that_joins_no_edge("box", Joins::default())]
fn says_which_edges_of_the_grid_join(#[case] sketch: &str, #[case] expected: Joins) {
    assert_eq!(tracks(&grid(sketch), footprint, 20.0).joins, expected);
}

/// Each column keeps the narrowest width of the area in it, and an area
/// across two columns shares what its gap does not hold. The narrowest
/// width of the grid adds the open gaps.
#[rstest]
#[case::a_row_on_an_axis(
    "slab slab link box",
    "minmax(50px, 1fr) var(--sketch-column-gap) minmax(50px, 1fr) 0px minmax(45px, 1fr) var(--sketch-column-gap) minmax(160px, 1fr)",
    344.2
)]
#[case::a_row_with_no_contact(
    "box box rail",
    "minmax(70px, 1fr) var(--sketch-column-gap) minmax(70px, 1fr) var(--sketch-column-gap) minmax(160px, 1fr)",
    340.0
)]
fn reserves_the_narrowest_width_of_each_area(
    #[case] sketch: &str,
    #[case] columns: &str,
    #[case] narrowest: f64,
) {
    let least = |name: &str| match name {
        "slab" => 120.0,
        "link" => 44.2,
        _ => 160.0,
    };
    let found = tracks(
        &grid(sketch),
        |name| {
            joins(name).map(|joins| Footprint {
                joins,
                narrowest: least(name),
            })
        },
        20.0,
    );
    assert_eq!(
        (found.columns.as_str(), found.narrowest),
        (columns, narrowest)
    );
}
