// The row of posters on a person's page: one poster for each credit,
// oldest at the left, scrolled across. The row rests in one of two
// places. While a poster holds focus, it centers that poster by the rule
// every strip scrolls by. After a move on the axis, it puts the year's
// first credit at the left edge, so a busy year shows as many of its
// credits as the row holds.
//
// The head and the axis over the row are a fixed height, so the posters
// take their size from the height left under the axis, and the row holds
// as many of them as fit across. A poster is never larger than a column
// of a six-column wall of the same width, so a 16:9 screen draws the
// wall's posters six across, and a wider screen draws more of them, not
// larger ones that run off the foot of the screen.

use std::ops::Range;

use iced_wgpu::Renderer;
use iced_widget::canvas::Frame;
use iced_winit::core::Rectangle;

use super::credit::Credit;
use super::page::LINES;
use super::walk::Rest;
use crate::art::Art;
use crate::audience::faces::Faces;
use crate::views::{Card, Tone, area, artwork, card, progress, screen, strip, wall};

// How far a band may reach past the edge of the row and still count as
// inside it: the rounding of a scroll that rests a poster on the edge.
const EDGE: f32 = 0.5;

// The space under the cards at the foot of the screen: the margin the
// sides keep.
const BOTTOM: f32 = screen::MARGIN_X;

/// The measures of the row at one frame: the region its cells fill and
/// the size of one cell.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Row {
    /// The region the cells fill: the content region widened by a gutter
    /// at each side, so the art of the first poster at rest is on the
    /// content region's left edge and the art of the last poster that
    /// fits is on its right edge.
    pub region: Rectangle,
    /// One cell of the row.
    pub cells: wall::Cells,
}

impl Row {
    /// The row in this band: the content region's `x` and width, from the
    /// row's top to the foot of the frame.
    pub fn new(band: Rectangle) -> Row {
        let wide = wall::lined(
            wall::columned(band, wall::COLUMNS).width,
            wall::POSTER,
            wall::COLUMNS,
            LINES,
        );
        let fits = band.height - BOTTOM - wall::below(LINES);
        let tallest = wide.poster_height.min(fits).max(0.0);
        // The posters across: the first, and one more for each cell of the
        // narrowest width that the rest of the band holds, rounded to the
        // nearest whole poster. Where the count rounds up, every poster
        // narrows a little so the last one ends on the right edge. Where
        // it rounds down, every gutter widens a little instead. Either
        // way, the change is less than half a cell.
        let share = |poster_width: f32| poster_width / wall::POSTER_SHARE;
        let across = match tallest > 0.0 {
            true => {
                let poster_width = tallest / wall::POSTER;
                ((band.width - poster_width) / share(poster_width)).round() as usize + 1
            }
            false => 1,
        };
        let narrowed = band.width / (1.0 + (across - 1) as f32 / wall::POSTER_SHARE);
        let poster_width = (tallest / wall::POSTER).min(narrowed);
        let poster_height = poster_width * wall::POSTER;
        let width = match across {
            1 => share(poster_width),
            across => (band.width - poster_width) / (across - 1) as f32,
        };
        let gutter = (width - poster_width) / 2.0;
        Row {
            region: area(band.x - gutter, band.y, across as f32 * width, band.height),
            cells: wall::Cells {
                width,
                height: poster_height + wall::below(LINES),
                poster_width,
                poster_height,
            },
        }
    }

    /// The posters the row holds across at rest.
    #[cfg(test)]
    pub fn across(&self) -> usize {
        (self.region.width / self.cells.width).round() as usize
    }
}

/// How far the row has scrolled at this rest, for this many posters.
pub fn offset(rest: Rest, count: usize, row: &Row) -> f32 {
    let width = row.cells.width;
    let region = row.region;
    match rest {
        Rest::Centered(index) => {
            let slots: Vec<(f32, f32)> = (0..count)
                .map(|index| (index as f32 * width, width))
                .collect();
            strip::offset(&slots, Some(index), region.width)
        }
        Rest::Leading(index) => {
            let most = (count as f32 * width - region.width).max(0.0);
            (index as f32 * width).clamp(0.0, most)
        }
    }
}

/// The poster of one credit, in frame space, at this scroll.
pub fn slot(row: &Row, index: usize, offset: f32) -> Rectangle {
    let cells = row.cells;
    area(
        row.region.x + index as f32 * cells.width + (cells.width - cells.poster_width) / 2.0
            - offset,
        row.region.y,
        cells.poster_width,
        cells.poster_height,
    )
}

/// The posters the row shows whole at this scroll, which the bright
/// part of the axis spans the years of.
pub fn shown(row: &Row, count: usize, offset: f32) -> Range<usize> {
    let region = row.region;
    let whole = |index: &usize| {
        let slot = slot(row, *index, offset);
        slot.x >= region.x && slot.x + slot.width <= region.x + region.width
    };
    let first = (0..count).find(whole).unwrap_or(count);
    let end = (first..count).find(|index| !whole(index)).unwrap_or(count);
    first..end
}

/// Draw the row at this scroll. Only the posters that reach into the
/// region become geometry, and the store is asked for one more on each
/// side, so a scroll's next art decodes before it appears.
pub fn draw<A: Art>(
    frame: &mut Frame<Renderer>,
    store: &mut A,
    credits: &[Credit],
    row: &Row,
    offset: f32,
    faces: &Faces,
) {
    let cells = row.cells;
    let region = row.region;
    let right = region.x + region.width;
    for (index, credit) in credits.iter().enumerate() {
        let item = &credit.item;
        let slot = slot(row, index, offset);
        let ahead = slot.x + slot.width + cells.width < region.x || slot.x - cells.width > right;
        if ahead {
            continue;
        }
        if slot.x + slot.width < region.x || slot.x > right {
            if !item.art().is_empty() {
                let _ = store.covered(
                    item.library(),
                    item.art(),
                    slot.width as u32,
                    slot.height as u32,
                );
            }
            continue;
        }
        artwork(
            frame,
            store,
            item.library(),
            item.art(),
            slot,
            item.name(),
            Tone::Full,
        );
        progress::draw(frame, item, slot);
        // The letters of the faces and a card's lines clip to their own
        // bands, and those clips do not inherit the row's, so they would
        // draw past the edge of the row. The faces stand at the left of the
        // poster's foot and draw where that edge is inside the row, and
        // only a poster the row shows whole draws its card.
        if slot.x >= region.x - EDGE {
            progress::faces(frame, item, slot, faces);
        }
        let band = wall::caption(&cells, slot);
        if band.x >= region.x - EDGE && band.x + band.width <= right + EDGE {
            card::draw(frame, item, band);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // The row of the final boards on a 1920 by 1080 frame: the content
    // region from the row's top to the foot of the frame.
    fn row() -> Row {
        Row::new(area(96.0, 528.0, 1728.0, 552.0))
    }

    fn near(one: f32, other: f32) -> bool {
        (one - other).abs() < 0.05
    }

    #[test]
    fn a_cell_of_a_16_9_row_is_a_column_of_a_six_column_wall() {
        let row = row();
        let cells = row.cells;
        assert_eq!(row.across(), 6);
        assert!(near(row.region.x, 72.33), "{row:?}");
        assert!(near(row.region.width, 1775.34), "{row:?}");
        assert!(near(cells.width, 295.89), "{cells:?}");
        assert!(near(cells.poster_width, 248.55), "{cells:?}");
        assert!(near(cells.poster_height, 372.82), "{cells:?}");
    }

    #[test]
    fn a_focused_poster_rests_in_the_middle_of_the_row() {
        let offset = offset(Rest::Centered(10), 31, &row());
        let poster = slot(&row(), 10, offset);
        assert!(near(poster.center_x(), 960.0), "{poster:?}");
        assert_eq!(shown(&row(), 31, offset).len(), 5);
    }

    #[test]
    fn a_year_on_the_axis_puts_its_first_credit_at_the_left_edge() {
        let offset = offset(Rest::Leading(13), 31, &row());
        let poster = slot(&row(), 13, offset);
        assert!(near(poster.x, 96.0), "{poster:?}");
        assert_eq!(shown(&row(), 31, offset), 13..19);
    }

    #[test]
    fn the_row_never_scrolls_past_either_end() {
        let cases = [
            (Rest::Centered(0), 31, 0.0),
            (Rest::Leading(0), 31, 0.0),
            (Rest::Leading(30), 31, 25.0 * row().cells.width),
            (Rest::Centered(2), 3, 0.0),
            (Rest::Leading(2), 3, 0.0),
        ];
        for (rest, count, held) in cases {
            assert!(near(offset(rest, count, &row()), held), "{rest:?} {count}");
        }
        assert_eq!(shown(&row(), 3, 0.0), 0..3);
        assert_eq!(shown(&row(), 0, 0.0), 0..0);
    }
}
