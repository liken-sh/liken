// The culling math the wall and the lists share: where the viewport
// scrolls to, and which slots fall inside it. The functions are pure
// over numbers, so the tests prove the head-to-head's floor of five
// thousand titles without building a row.

use std::ops::Range;

/// The pixel offset that keeps the focused row centered, clamped to
/// the edges of the content, so focus never leaves the viewport.
pub fn offset(focus_row: usize, rows: usize, row_height: f32, height: f32) -> f32 {
    let content = rows as f32 * row_height;
    if content <= height {
        return 0.0;
    }
    let centered = (focus_row as f32 + 0.5) * row_height - height / 2.0;
    centered.clamp(0.0, content - height)
}

/// How many rows `count` slots fill at `columns` a row.
pub fn rows(count: usize, columns: usize) -> usize {
    count.div_ceil(columns)
}

/// The slot indices inside the scrolled viewport, partial rows
/// included; the views build nothing outside this range.
pub fn visible(
    offset: f32,
    height: f32,
    row_height: f32,
    count: usize,
    columns: usize,
) -> Range<usize> {
    if count == 0 || row_height <= 0.0 {
        return 0..0;
    }
    let first_row = (offset / row_height).floor().max(0.0) as usize;
    let past_row = ((offset + height) / row_height).ceil().max(0.0) as usize;
    let start = (first_row * columns).min(count);
    let end = (past_row * columns).min(count);
    start..end
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn content_that_fits_never_scrolls() {
        assert_eq!(offset(1, 2, 100.0, 500.0), 0.0);
    }

    #[test]
    fn the_focused_row_sits_centered() {
        assert_eq!(offset(10, 100, 100.0, 500.0), 800.0);
    }

    #[test]
    fn the_scroll_clamps_at_the_top_and_the_bottom() {
        assert_eq!(offset(0, 100, 100.0, 500.0), 0.0);
        assert_eq!(offset(99, 100, 100.0, 500.0), 9500.0);
    }

    #[test]
    fn slots_fill_rows_with_a_remainder() {
        assert_eq!(rows(10, 3), 4);
        assert_eq!(rows(9, 3), 3);
        assert_eq!(rows(0, 3), 0);
    }

    #[test]
    fn the_viewport_culls_a_wall_of_thousands() {
        let all = 5000;
        let range = visible(0.0, 1080.0, 458.0, all, 6);
        assert_eq!(range, 0..18);
    }

    #[test]
    fn a_scrolled_viewport_starts_past_the_hidden_rows() {
        let range = visible(950.0, 1080.0, 458.0, 5000, 6);
        assert_eq!(range, 12..30);
    }

    #[test]
    fn the_last_rows_end_at_the_count() {
        let range = visible(300.0, 500.0, 100.0, 25, 3);
        assert_eq!(range, 9..24);
        let range = visible(9500.0, 500.0, 100.0, 299, 3);
        assert_eq!(range, 285..299);
    }

    #[test]
    fn nothing_is_visible_in_an_empty_level() {
        assert_eq!(visible(0.0, 1080.0, 100.0, 0, 6), 0..0);
    }
}
