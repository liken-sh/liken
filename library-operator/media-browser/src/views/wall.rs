// The wall: a grid of art slots with the focused one drawn larger and
// named. Only the slots inside the viewport become geometry, so a wall of
// five thousand titles builds a couple of dozen slots a frame.
//
// The slot ratio is a parameter because a wall of posters is 2:3 and a
// wall of episode stills is 16:9, and the two walls are one primitive.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle};

use super::{Card, artwork, label, mark, scroll};
use crate::look;
use crate::posters::Posters;

/// The wall's column count, fixed so focus movement is a function of
/// the index alone and never of the window size.
pub const COLUMNS: usize = 6;

/// The height of a poster slot as a share of its width: the 2:3 portrait
/// that a movie's and a series' primary art is.
pub const POSTER: f32 = 1.5;

// The poster's share of its cell; the rest is the gutter.
const POSTER_SHARE: f32 = 0.84;

// The band under each row of posters, where the focused title's line
// is drawn.
const LINE: f32 = 56.0;

// How much larger the focused slot draws than its neighbors.
const FOCUS_GROWTH: f32 = 1.12;

// How wide the focused line may run, in cells, so a title with a year, a
// runtime, and a rating fits on one line.
const LINE_CELLS: f32 = 2.5;

/// The wall's cell measures, derived from the viewport width alone.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Cells {
    /// One cell's width, a column of the viewport.
    pub width: f32,
    /// One cell's height, the poster and the name band.
    pub height: f32,
    /// The poster slot's width inside the cell.
    pub poster_width: f32,
    /// The poster slot's height: the width at the ratio the caller asked
    /// for.
    pub poster_height: f32,
}

/// The cell measures for a viewport of this width, at this slot ratio.
pub fn cells(width: f32, ratio: f32) -> Cells {
    let width = width / COLUMNS as f32;
    let poster_width = width * POSTER_SHARE;
    let poster_height = poster_width * ratio;
    Cells {
        width,
        height: poster_height + LINE,
        poster_width,
        poster_height,
    }
}

/// The poster slot of one index, in viewport space after the scroll.
pub fn slot(cells: &Cells, index: usize, offset: f32) -> Rectangle {
    let column = (index % COLUMNS) as f32;
    let row = (index / COLUMNS) as f32;
    Rectangle {
        x: column * cells.width + (cells.width - cells.poster_width) / 2.0,
        y: row * cells.height - offset,
        width: cells.poster_width,
        height: cells.poster_height,
    }
}

/// The same rectangle grown around its center, for the focused slot.
pub fn grown(slot: Rectangle, growth: f32) -> Rectangle {
    let width = slot.width * growth;
    let height = slot.height * growth;
    Rectangle {
        x: slot.x - (width - slot.width) / 2.0,
        y: slot.y - (height - slot.height) / 2.0,
        width,
        height,
    }
}

/// One wall to draw: the items, the focus, the region under the band,
/// and the ratio of a slot.
pub struct Grid<'a, T> {
    /// The items in draw order.
    pub items: &'a [T],
    /// The focused item's index.
    pub focus: usize,
    /// Whether the focused slot carries the mark. It does not while the
    /// band above holds focus.
    pub marked: bool,
    /// The library the art paths resolve against.
    pub library: &'a str,
    /// The height of a slot as a share of its width.
    pub ratio: f32,
    /// The part of the frame the grid draws in, under the band.
    pub region: Rectangle,
}

/// The space over the first row. It keeps the grown focused slot off the
/// band.
pub const HEAD: f32 = 28.0;

/// Draw one wall. The store is asked for the slots this frame draws and
/// for one row past them, so a scroll's next posters decode before they
/// appear.
pub fn draw<T: Card, P: Posters>(
    frame: &mut canvas::Frame<Renderer>,
    posters: &mut P,
    grid: &Grid<'_, T>,
) {
    let cells = cells(grid.region.width, grid.ratio);
    let top = grid.region.y + HEAD;
    let offset = scroll::offset(
        grid.focus / COLUMNS,
        scroll::rows(grid.items.len(), COLUMNS),
        cells.height,
        grid.region.height - HEAD,
    );
    let range = scroll::visible(
        offset,
        grid.region.height - HEAD,
        cells.height,
        grid.items.len(),
        COLUMNS,
    );

    for index in range.clone() {
        if index == grid.focus {
            continue;
        }
        let item = &grid.items[index];
        artwork(
            frame,
            posters,
            grid.library,
            item.art(),
            lowered(slot(&cells, index, offset), top),
            item.name(),
        );
    }

    // One row past the viewport is asked for and not drawn, so a
    // scroll's next posters decode before they appear.
    for index in range.end..(range.end + COLUMNS).min(grid.items.len()) {
        let item = &grid.items[index];
        let ahead = slot(&cells, index, offset);
        if !item.art().is_empty() {
            let _ = posters.poster(
                grid.library,
                item.art(),
                ahead.width as u32,
                ahead.height as u32,
            );
        }
    }

    // The focused slot draws last so its grown edges cover its
    // neighbors and never the other way around.
    if let Some(item) = grid.items.get(grid.focus) {
        let focused = lowered(grown(slot(&cells, grid.focus, offset), FOCUS_GROWTH), top);
        artwork(
            frame,
            posters,
            grid.library,
            item.art(),
            focused,
            item.name(),
        );
        if grid.marked {
            mark(frame, focused);
        }
        // The line is centered under its poster, held inside the frame,
        // and clipped to one line, so a long title never runs off the
        // screen or over the row below.
        let width = cells.width * LINE_CELLS;
        let line = super::area(
            (focused.center_x() - width / 2.0).clamp(0.0, grid.region.width - width),
            focused.y + focused.height + 10.0,
            width,
            LINE,
        );
        frame.with_clip(line, |frame| {
            frame.fill_text(label(
                item.line(),
                Point::new(line.center_x(), line.y),
                look::NAME,
                look::text(),
                Alignment::Center,
                Vertical::Top,
                width,
            ));
        });
    }
}

// One slot in frame space: its place in the grid, moved down by the top
// of the grid's region.
fn lowered(slot: Rectangle, top: f32) -> Rectangle {
    Rectangle {
        y: slot.y + top,
        ..slot
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cells_keep_the_two_three_poster_ratio() {
        let cells = cells(1920.0, POSTER);
        assert_eq!(cells.width, 320.0);
        assert_eq!(cells.poster_height, cells.poster_width * 1.5);
        assert_eq!(cells.height, cells.poster_height + 56.0);
    }

    #[test]
    fn a_wider_ratio_gives_a_shorter_slot() {
        let stills = cells(1920.0, 9.0 / 16.0);
        assert_eq!(stills.width, 320.0);
        assert_eq!(stills.poster_height, stills.poster_width * 9.0 / 16.0);
        assert!(stills.height < cells(1920.0, POSTER).height);
    }

    #[test]
    fn slots_land_in_their_column_and_row() {
        let cells = cells(1920.0, POSTER);
        let first = slot(&cells, 0, 0.0);
        assert_eq!(first.y, 0.0);
        let below = slot(&cells, COLUMNS, 0.0);
        assert_eq!(below.x, first.x);
        assert_eq!(below.y, cells.height);
        let beside = slot(&cells, 1, 0.0);
        assert_eq!(beside.x, first.x + cells.width);
    }

    #[test]
    fn the_scroll_lifts_every_slot() {
        let cells = cells(1920.0, POSTER);
        assert_eq!(slot(&cells, 0, 200.0).y, -200.0);
    }

    #[test]
    fn the_region_lowers_every_slot_by_its_top() {
        let cells = cells(1920.0, POSTER);
        assert_eq!(lowered(slot(&cells, 0, 0.0), 78.0).y, 78.0);
    }

    #[test]
    fn a_grown_slot_keeps_its_center() {
        let grown = grown(
            Rectangle {
                x: 100.0,
                y: 200.0,
                width: 50.0,
                height: 80.0,
            },
            2.0,
        );
        assert_eq!(grown.center_x(), 125.0);
        assert_eq!(grown.center_y(), 240.0);
        assert_eq!(grown.width, 100.0);
        assert_eq!(grown.height, 160.0);
    }
}
