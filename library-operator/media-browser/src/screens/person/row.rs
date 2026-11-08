// The row of posters on a person's page: one poster for each credit, at
// the size of a wall poster, oldest at the left, scrolled across. The row
// rests in one of two places. While a poster holds focus, it centers that
// poster by the rule every strip scrolls by. After a move on the axis, it
// puts the year's first credit at the left edge, so a busy year shows as
// many of its credits as the row holds.

use std::ops::Range;

use iced_wgpu::Renderer;
use iced_widget::canvas::Frame;
use iced_winit::core::Rectangle;

use super::credit::Credit;
use super::page::LINES;
use super::walk::Rest;
use crate::art::Art;
use crate::audience::faces::Faces;
use crate::views::{Card, Tone, area, artwork, card, progress, strip, wall};

// How far a band may reach past the edge of the row and still count as
// inside it: the rounding of a scroll that rests a poster on the edge.
const EDGE: f32 = 0.5;

/// The measures of one cell of a row this wide: a column of a six-column
/// wall.
pub fn cells(region: Rectangle) -> wall::Cells {
    wall::lined(region.width, wall::POSTER, wall::COLUMNS, LINES)
}

/// How far the row has scrolled at this rest, for this many posters.
pub fn offset(rest: Rest, count: usize, region: Rectangle) -> f32 {
    let width = cells(region).width;
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
pub fn slot(region: Rectangle, index: usize, offset: f32) -> Rectangle {
    let cells = cells(region);
    area(
        region.x + index as f32 * cells.width + (cells.width - cells.poster_width) / 2.0 - offset,
        region.y,
        cells.poster_width,
        cells.poster_height,
    )
}

/// The posters the row shows whole at this scroll, which the bright
/// part of the axis spans the years of.
pub fn shown(region: Rectangle, count: usize, offset: f32) -> Range<usize> {
    let whole = |index: &usize| {
        let slot = slot(region, *index, offset);
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
    region: Rectangle,
    offset: f32,
    faces: &Faces,
) {
    let cells = cells(region);
    let right = region.x + region.width;
    for (index, credit) in credits.iter().enumerate() {
        let item = &credit.item;
        let slot = slot(region, index, offset);
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

    // The row of the final boards: the content region widened by the
    // cell's gutter, under the axis.
    const REGION: Rectangle = Rectangle {
        x: 72.33,
        y: 528.0,
        width: 1775.34,
        height: 552.0,
    };

    fn near(one: f32, other: f32) -> bool {
        (one - other).abs() < 0.05
    }

    #[test]
    fn a_cell_is_a_column_of_a_six_column_wall() {
        let cells = cells(REGION);
        assert!(near(cells.width, 295.89), "{cells:?}");
        assert!(near(cells.poster_width, 248.55), "{cells:?}");
        assert!(near(cells.poster_height, 372.82), "{cells:?}");
    }

    #[test]
    fn a_focused_poster_rests_in_the_middle_of_the_row() {
        let offset = offset(Rest::Centered(10), 31, REGION);
        let poster = slot(REGION, 10, offset);
        assert!(near(poster.center_x(), 960.0), "{poster:?}");
        assert_eq!(shown(REGION, 31, offset).len(), 5);
    }

    #[test]
    fn a_year_on_the_axis_puts_its_first_credit_at_the_left_edge() {
        let offset = offset(Rest::Leading(13), 31, REGION);
        let poster = slot(REGION, 13, offset);
        assert!(near(poster.x, 96.0), "{poster:?}");
        assert_eq!(shown(REGION, 31, offset), 13..19);
    }

    #[test]
    fn the_row_never_scrolls_past_either_end() {
        let cases = [
            (Rest::Centered(0), 31, 0.0),
            (Rest::Leading(0), 31, 0.0),
            (Rest::Leading(30), 31, 25.0 * cells(REGION).width),
            (Rest::Centered(2), 3, 0.0),
            (Rest::Leading(2), 3, 0.0),
        ];
        for (rest, count, held) in cases {
            assert!(near(offset(rest, count, REGION), held), "{rest:?} {count}");
        }
        assert_eq!(shown(REGION, 3, 0.0), 0..3);
        assert_eq!(shown(REGION, 0, 0.0), 0..0);
    }
}
