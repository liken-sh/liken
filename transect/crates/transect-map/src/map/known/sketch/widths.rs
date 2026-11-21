//! The narrowest width of each column of a sketch's grid. Each drawn
//! area keeps the narrowest width of its part, so a part is never drawn
//! narrower than its shape allows and never draws over its neighbor.
//! The grid reserves it in its columns, as CSS sizes a track for an item
//! that spans several: the areas of one column are taken first, and an
//! area that spans more columns adds what it still needs to each of its
//! columns in equal shares.

use std::ops::Range;

/// The narrowest width of each of `count` columns, in pixels, where
/// `areas` gives the columns and the narrowest width of each drawn area,
/// `visible` says which columns take width, and `gap` gives the width
/// in pixels of the gap track after a column, zero when it is closed.
pub fn columns(
    count: usize,
    areas: &[(Range<usize>, f64)],
    visible: &[bool],
    gap: impl Fn(usize) -> f64,
) -> Vec<f64> {
    let mut widths = vec![0.0_f64; count];
    let mut by_span: Vec<&(Range<usize>, f64)> = areas.iter().collect();
    by_span.sort_by_key(|(columns, _)| columns.len());
    for (columns, narrowest) in by_span {
        let own: Vec<usize> = columns.clone().filter(|c| visible[*c]).collect();
        if own.is_empty() {
            continue;
        }
        let gaps: f64 = (columns.start..columns.end - 1).map(&gap).sum();
        let held: f64 = own.iter().map(|c| widths[*c]).sum::<f64>() + gaps;
        let share = (narrowest - held).max(0.0) / own.len() as f64;
        for column in own {
            widths[column] += share;
        }
    }
    widths
}

/// The narrowest width of the whole grid: its columns and its open gaps.
pub fn total(widths: &[f64], gap: impl Fn(usize) -> f64) -> f64 {
    let gaps: f64 = (0..widths.len().saturating_sub(1)).map(gap).sum();
    widths.iter().sum::<f64>() + gaps
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    /// Three columns with a gap of 20 pixels after each, every column
    /// visible.
    #[rstest]
    #[case::one_area_in_each_column(&[(0..1, 100.0), (1..2, 50.0), (2..3, 0.0)], vec![100.0, 50.0, 0.0])]
    #[case::the_widest_of_one_column(&[(0..1, 100.0), (0..1, 140.0)], vec![140.0, 0.0, 0.0])]
    #[case::an_area_across_two_columns_and_their_gap(&[(0..2, 220.0)], vec![100.0, 100.0, 0.0])]
    #[case::only_what_a_wider_area_still_needs(&[(0..1, 150.0), (0..3, 250.0)], vec![170.0, 20.0, 20.0])]
    #[case::nothing_for_an_area_that_its_columns_hold(&[(0..1, 150.0), (1..2, 150.0), (0..2, 200.0)], vec![150.0, 150.0, 0.0])]
    fn reserves_the_narrowest_width_of_each_area_in_its_columns(
        #[case] areas: &[(Range<usize>, f64)],
        #[case] expected: Vec<f64>,
    ) {
        assert_eq!(columns(3, areas, &[true; 3], |_| 20.0), expected);
    }

    #[rstest]
    fn shares_an_area_only_among_the_columns_that_take_width() {
        let found = columns(3, &[(0..3, 120.0)], &[true, false, true], |_| 0.0);
        assert_eq!(found, vec![60.0, 0.0, 60.0]);
    }

    #[rstest]
    fn adds_the_open_gaps_to_the_width_of_the_grid() {
        assert_eq!(total(&[100.0, 50.0, 0.0], |c| [20.0, 0.0][c]), 170.0);
    }
}
