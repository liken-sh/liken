// The wall: a grid of art slots, each with one line under it, and a
// stroke of the accent outside the one that holds focus. Only the slots
// inside the viewport become geometry, so a wall of five thousand titles
// builds a couple of dozen slots a frame.
//
// The slot ratio, the column count, and the scroll offset are parameters,
// because a wall of posters is 2:3 at six across and a wall of episode
// stills is 16:9 at four across, and the two walls are one primitive. The
// offset lets a page draw one grid for each season of a series inside one
// scrolled region.
//
// Every slot draws at one size, focused or not, so the store decodes each
// slot once and a press redraws from the cache.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Color, Point, Rectangle};

use super::{Card, Tone, area, artwork, label, mark, scroll, text};
use crate::look;
use crate::posters::Posters;

/// The wall's column count, fixed so focus movement is a function of
/// the index alone and never of the window size.
pub const COLUMNS: usize = 6;

/// The height of a poster slot as a share of its width: the 2:3 portrait
/// that a movie's and a series' primary art is.
pub const POSTER: f32 = 1.5;

/// The height of a still slot as a share of its width: the 16:9 that an
/// episode's own art is.
pub const STILL: f32 = 9.0 / 16.0;

// The poster's share of its cell; the rest is the gutter, which holds the
// mark of a focused slot.
const POSTER_SHARE: f32 = 0.84;

// The space between a slot and the line under it, and the space under
// that line before the next row. Both are wider than the mark reaches, so
// no mark ever touches a caption or the row above.
const GAP: f32 = 12.0;
const FOOT: f32 = 14.0;

/// The wall's cell measures, derived from the viewport width, the slot
/// ratio, and the column count.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Cells {
    /// One cell's width, a column of the viewport.
    pub width: f32,
    /// One cell's height: the slot, the gap, the caption, and the foot.
    pub height: f32,
    /// The poster slot's width inside the cell.
    pub poster_width: f32,
    /// The poster slot's height: the width at the ratio the caller asked
    /// for.
    pub poster_height: f32,
}

/// The cell measures for a viewport of this width, at this slot ratio,
/// with this many slots across.
pub fn cells(width: f32, ratio: f32, columns: usize) -> Cells {
    let width = width / columns as f32;
    let poster_width = width * POSTER_SHARE;
    let poster_height = poster_width * ratio;
    Cells {
        width,
        height: poster_height + GAP + text::height(1, look::CAPTION) + FOOT,
        poster_width,
        poster_height,
    }
}

/// The poster slot of one index, in viewport space after the scroll.
pub fn slot(cells: &Cells, index: usize, offset: f32, columns: usize) -> Rectangle {
    let column = (index % columns) as f32;
    let row = (index / columns) as f32;
    Rectangle {
        x: column * cells.width + (cells.width - cells.poster_width) / 2.0,
        y: row * cells.height - offset,
        width: cells.poster_width,
        height: cells.poster_height,
    }
}

/// The band one slot's caption draws in: one line, under the slot and
/// inside its own cell, so no caption ever runs under a neighbour's.
pub fn caption(cells: &Cells, slot: Rectangle) -> Rectangle {
    area(
        slot.center_x() - cells.width / 2.0,
        slot.y + slot.height + GAP,
        cells.width,
        text::height(1, look::CAPTION),
    )
}

/// The words and the color one slot's caption draws in: the slot's own
/// line, muted, and the facts of the slot that holds focus, bright. The
/// focused slot draws the whole facts that fit the band's character
/// estimate, so the caption never cuts inside a fact.
pub fn captioned<T: Card>(item: &T, focused: bool, chars: usize) -> (&str, Color) {
    match focused {
        true => (item.line_fitting(chars), look::text()),
        false => (item.caption(), look::muted()),
    }
}

/// How many characters one caption band holds.
pub fn caption_fits(cells: &Cells) -> usize {
    text::fits(look::CAPTION, cells.width)
}

/// The offset that keeps the focused row of a whole wall centered in a
/// viewport this tall.
pub fn scrolled(focus: usize, count: usize, columns: usize, cells: &Cells, height: f32) -> f32 {
    scroll::offset(
        focus / columns,
        scroll::rows(count, columns),
        cells.height,
        height,
    )
}

/// One wall to draw: the items, the focus, the region it draws in, the
/// shape of a slot, and how far its rows have scrolled.
pub struct Grid<'a, T> {
    /// The items in draw order.
    pub items: &'a [T],
    /// The focused item's index, or nothing where focus is elsewhere on
    /// the screen.
    pub focus: Option<usize>,
    /// Whether the focused slot carries the mark. It does not while the
    /// band above holds focus.
    pub marked: bool,
    /// The library the art paths resolve against.
    pub library: &'a str,
    /// The height of a slot as a share of its width.
    pub ratio: f32,
    /// How many slots a row holds.
    pub columns: usize,
    /// The part of the frame the grid draws in, under the band.
    pub region: Rectangle,
    /// How far the grid's first row has scrolled above the region's top.
    /// It is negative for a grid whose rows start below the top.
    pub offset: f32,
}

/// The space over the first row. It keeps the mark of a focused slot in
/// the first row off the band.
pub const HEAD: f32 = 20.0;

/// Draw one wall. The store is asked for the slots this frame draws and
/// for one row past them, so a scroll's next posters decode before they
/// appear.
pub fn draw<T: Card, P: Posters>(
    frame: &mut canvas::Frame<Renderer>,
    posters: &mut P,
    grid: &Grid<'_, T>,
) {
    let cells = cells(grid.region.width, grid.ratio, grid.columns);
    let chars = caption_fits(&cells);
    let range = scroll::visible(
        grid.offset,
        grid.region.height,
        cells.height,
        grid.items.len(),
        grid.columns,
    );

    for index in range.clone() {
        let item = &grid.items[index];
        let slot = lowered(
            slot(&cells, index, grid.offset, grid.columns),
            grid.region.y,
        );
        artwork(
            frame,
            posters,
            grid.library,
            item.art(),
            slot,
            item.name(),
            Tone::Full,
        );
        let focused = Some(index) == grid.focus;
        if focused && grid.marked {
            mark(frame, slot);
        }
        let (content, color) = captioned(item, focused, chars);
        written(frame, caption(&cells, slot), content, color);
    }

    // One row past the viewport is asked for and not drawn, so a
    // scroll's next posters decode before they appear.
    for index in range.end..(range.end + grid.columns).min(grid.items.len()) {
        let item = &grid.items[index];
        let ahead = slot(&cells, index, grid.offset, grid.columns);
        if !item.art().is_empty() {
            let _ = posters.poster(
                grid.library,
                item.art(),
                ahead.width as u32,
                ahead.height as u32,
            );
        }
    }
}

// One line centered in its band and clipped to it, so a long title never
// runs off the screen or over the row below.
fn written(frame: &mut canvas::Frame<Renderer>, band: Rectangle, content: &str, color: Color) {
    if content.is_empty() {
        return;
    }
    frame.with_clip(band, |frame| {
        frame.fill_text(label(
            content,
            Point::new(band.center_x(), band.y),
            look::CAPTION,
            color,
            Alignment::Center,
            Vertical::Top,
            band.width,
        ));
    });
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
    use crate::views::{REACH, marked, underlined};

    // The two walls this screen draws: posters six across, and episode
    // stills four across.
    const WALLS: [(f32, usize); 2] = [(POSTER, COLUMNS), (STILL, 4)];

    // One item of a wall, with a caption of its own and a longer line
    // under focus.
    const NAME: &str = "Specimen 0001";
    const LINE: &str = "Specimen 0001 · 1987 · 1h 37m · PG-13";

    struct Slot;

    impl Card for Slot {
        fn name(&self) -> &str {
            NAME
        }

        fn line_fitting(&self, chars: usize) -> &str {
            match LINE.chars().count() <= chars {
                true => LINE,
                false => NAME,
            }
        }
    }

    #[test]
    fn cells_keep_the_two_three_poster_ratio() {
        let cells = cells(1920.0, POSTER, COLUMNS);
        assert_eq!(cells.width, 320.0);
        assert_eq!(cells.poster_height, cells.poster_width * 1.5);
        assert!(cells.height > cells.poster_height);
    }

    #[test]
    fn a_wider_ratio_gives_a_shorter_slot() {
        let stills = cells(1920.0, STILL, COLUMNS);
        assert_eq!(stills.width, 320.0);
        assert_eq!(stills.poster_height, stills.poster_width * 9.0 / 16.0);
        assert!(stills.height < cells(1920.0, POSTER, COLUMNS).height);
    }

    #[test]
    fn fewer_columns_give_a_wider_cell() {
        let four = cells(1920.0, STILL, 4);
        assert_eq!(four.width, 480.0);
        assert!(four.poster_width > cells(1920.0, STILL, COLUMNS).poster_width);
    }

    #[test]
    fn slots_land_in_their_column_and_row() {
        let cells = cells(1920.0, POSTER, COLUMNS);
        let first = slot(&cells, 0, 0.0, COLUMNS);
        assert_eq!(first.y, 0.0);
        let below = slot(&cells, COLUMNS, 0.0, COLUMNS);
        assert_eq!(below.x, first.x);
        assert_eq!(below.y, cells.height);
        let beside = slot(&cells, 1, 0.0, COLUMNS);
        assert_eq!(beside.x, first.x + cells.width);
    }

    #[test]
    fn the_scroll_lifts_every_slot() {
        let cells = cells(1920.0, POSTER, COLUMNS);
        assert_eq!(slot(&cells, 0, 200.0, COLUMNS).y, -200.0);
    }

    #[test]
    fn a_grid_whose_rows_start_below_the_region_takes_a_negative_offset() {
        let cells = cells(1920.0, STILL, 4);
        assert_eq!(slot(&cells, 0, -300.0, 4).y, 300.0);
    }

    #[test]
    fn the_region_lowers_every_slot_by_its_top() {
        let cells = cells(1920.0, POSTER, COLUMNS);
        assert_eq!(lowered(slot(&cells, 0, 0.0, COLUMNS), 78.0).y, 78.0);
    }

    #[test]
    fn every_slot_carries_one_caption_under_it() {
        for (ratio, columns) in WALLS {
            let cells = cells(1920.0, ratio, columns);
            let slot = slot(&cells, 0, 0.0, columns);
            let band = caption(&cells, slot);
            assert_eq!(band.height, text::height(1, look::CAPTION));
            assert_eq!(band.width, cells.width);
            assert!(band.y > slot.y + slot.height);
        }
    }

    #[test]
    fn a_caption_stays_clear_of_the_mark_and_of_the_row_below() {
        for (ratio, columns) in WALLS {
            let cells = cells(1920.0, ratio, columns);
            let slot = slot(&cells, 0, 0.0, columns);
            let band = caption(&cells, slot);
            let below = super::slot(&cells, columns, 0.0, columns);
            assert!(band.y > marked(slot).y + marked(slot).height);
            assert!(band.y + band.height < marked(below).y);
        }
    }

    #[test]
    fn a_caption_stays_inside_its_own_cell() {
        let cells = cells(1920.0, POSTER, COLUMNS);
        let first = caption(&cells, slot(&cells, 0, 0.0, COLUMNS));
        assert_eq!(first.x, 0.0);
        let last = caption(&cells, slot(&cells, COLUMNS - 1, 0.0, COLUMNS));
        assert_eq!(last.x + last.width, 1920.0);
    }

    #[test]
    fn the_focused_caption_is_bright_and_carries_the_facts() {
        let (content, color) = captioned(&Slot, true, LINE.chars().count());
        assert_eq!(content, LINE);
        assert_eq!(color, look::text());
    }

    #[test]
    fn a_focused_caption_wider_than_its_band_gives_facts_up() {
        let (content, _) = captioned(&Slot, true, LINE.chars().count() - 1);
        assert_eq!(content, NAME);
    }

    #[test]
    fn every_other_caption_is_muted_and_carries_the_name() {
        let (content, color) = captioned(&Slot, false, 0);
        assert_eq!(content, NAME);
        assert_eq!(color, look::muted());
    }

    #[test]
    fn a_wider_cell_holds_more_of_the_focused_line() {
        let posters = caption_fits(&cells(1920.0, POSTER, COLUMNS));
        assert_eq!(posters, text::fits(look::CAPTION, 320.0));
        assert!(caption_fits(&cells(1920.0, STILL, 4)) > posters);
    }

    #[test]
    fn the_head_holds_the_mark_of_the_first_row_off_the_band() {
        const { assert!(HEAD > REACH) };
        const { assert!(GAP > REACH) };
        const { assert!(FOOT > REACH) };
    }

    #[test]
    fn a_whole_wall_scrolls_its_focused_row_to_the_middle() {
        let cells = cells(1920.0, POSTER, COLUMNS);
        assert_eq!(scrolled(0, 60, COLUMNS, &cells, 1080.0), 0.0);
        assert!(scrolled(59, 60, COLUMNS, &cells, 1080.0) > 0.0);
    }

    #[test]
    fn the_underline_is_the_bottom_edge_of_the_mark_and_no_more() {
        let slot = area(100.0, 100.0, 200.0, 300.0);
        let around = marked(slot);
        let bar = underlined(slot);
        assert_eq!(bar.height, look::MARK);
        assert_eq!(bar.y + bar.height / 2.0, around.y + around.height);
        assert!(bar.x < around.x && bar.x + bar.width > around.x + around.width);
        assert!(around.x < slot.x && around.y + around.height > slot.y + slot.height);
    }
}
