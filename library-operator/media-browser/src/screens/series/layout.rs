// The series page's geometry: a header of a fixed height at the top, and
// under it the region the wall of stills scrolls in. The header stays
// because it carries the focused episode's line and its plot, and a person
// reads them while moving across the wall. Every measure here is pure over
// numbers, so the fit at 1080 and the scroll are tested without a window.

use super::{COLUMNS, Season};
use crate::look;
use crate::views::{REACH, area, divider, scroll, stack, text, wall};
use iced_winit::core::Rectangle;

/// The space between the foot of the header and the first divider.
pub const HEAD: f32 = 20.0;

/// The space over the header's first block.
pub const TOP: f32 = 28.0;

/// The space under the header's last block, before the wall's region.
pub const FOOT: f32 = 12.0;

/// The space between two blocks of the header.
pub const GAP: f32 = 14.0;

/// The box a logo draws in, at the proportions the metadata tools write a
/// logo file in.
pub const LOGO_WIDTH: f32 = 460.0;
pub const LOGO_HEIGHT: f32 = 96.0;

/// The lines the header cuts the episode's plot and the cast to.
pub const PLOT_LINES: usize = 2;
pub const CAST_LINES: usize = 1;

// How much of the row under the focused one the scroll keeps in view, as
// a share of a row, so a person sees that there is more below.
const TRAIL: f32 = 0.25;

/// The part of the frame the header draws in: the height its blocks take,
/// whatever this series carries, so the wall under it starts at the same
/// place on every series.
pub fn header(bounds: Rectangle) -> Rectangle {
    area(bounds.x, bounds.y, bounds.width, head().min(bounds.height))
}

/// The part of the frame the wall scrolls in, under the header.
pub fn region(bounds: Rectangle) -> Rectangle {
    let header = header(bounds);
    area(
        bounds.x,
        bounds.y + header.height,
        bounds.width,
        bounds.height - header.height,
    )
}

/// The height the header's blocks take with every one of them present.
/// Each block is cut to its own lines, so this is the height of any
/// series' header and not of one series'.
pub fn head() -> f32 {
    TOP + LOGO_HEIGHT
        + GAP
        + text::height(1, look::FACTS)
        + GAP
        + text::height(1, look::FACTS)
        + GAP
        + text::height(PLOT_LINES, look::PLOT)
        + GAP
        + text::height(CAST_LINES, look::CREDITS)
        + FOOT
}

/// One season's place in the wall: the divider, the rows of stills under
/// it, and the height of both.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Band {
    /// The top of the divider, in the wall's own space.
    pub top: f32,
    /// The top of the season's first row of stills.
    pub rows_top: f32,
    /// How many rows the season's episodes fill.
    pub rows: usize,
    /// The height of the divider and the rows together.
    pub height: f32,
}

impl Band {
    /// Whether any part of this band falls inside a region this tall
    /// at this scroll.
    pub fn shows(&self, offset: f32, height: f32) -> bool {
        self.top < offset + height && self.top + self.height > offset
    }
}

/// The whole wall: the cell the stills draw in, one band for each season,
/// and how long the wall is.
#[derive(Debug, Clone, PartialEq)]
pub struct Layout {
    /// The measures of one still's cell.
    pub cells: wall::Cells,
    /// One band for each season, in aired order.
    pub bands: Vec<Band>,
    /// The length of the wall, the gap under the header included.
    pub content: f32,
}

impl Layout {
    /// The wall for these seasons, with stills of this cell. The first
    /// divider starts a gap below the header, and the gap scrolls away
    /// with the rows.
    pub fn of(seasons: &[Season], cells: wall::Cells) -> Self {
        let mut bands = Vec::with_capacity(seasons.len());
        let mut top = HEAD;
        for season in seasons {
            // The room under the divider is what the mark of a focused
            // slot in the first row reaches into.
            let rows = scroll::rows(season.run.count, COLUMNS);
            let head = divider::HEIGHT + REACH;
            let height = head + rows as f32 * cells.height;
            bands.push(Band {
                top,
                rows_top: top + head,
                rows,
                height,
            });
            top += height;
        }
        Self {
            cells,
            bands,
            content: top,
        }
    }

    /// How far the wall has scrolled with focus on this still. The wall
    /// stands at its top until the focused row would leave the foot of the
    /// region, and the header above the region never moves.
    pub fn scroll(&self, focus: usize, seasons: &[Season], height: f32) -> f32 {
        let Some(band) = self.band(focus, seasons) else {
            return 0.0;
        };
        let row = (focus - seasons[band].run.first) / COLUMNS;
        let region = Rectangle {
            x: 0.0,
            y: self.bands[band].rows_top + row as f32 * self.cells.height,
            width: 0.0,
            height: self.cells.height,
        };
        stack::offset(region, TRAIL * self.cells.height, self.content, height)
    }

    // The band that holds this still, or nothing on a page with no
    // episodes.
    fn band(&self, focus: usize, seasons: &[Season]) -> Option<usize> {
        seasons.iter().position(|season| {
            season.run.count > 0
                && focus >= season.run.first
                && focus < season.run.first + season.run.count
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::focus::Run;

    // A frame of the size the screens this browser draws on hold.
    const WIDTH: f32 = 1920.0;
    const HEIGHT: f32 = 1080.0;

    // Three seasons of eight, nine, and ten episodes.
    fn seasons() -> Vec<Season> {
        [(1, 0, 8), (2, 8, 9), (3, 17, 10)]
            .iter()
            .map(|(number, first, count)| Season {
                number: *number,
                name: format!("Season {number}"),
                year: "2004".into(),
                run: Run {
                    first: *first,
                    count: *count,
                },
            })
            .collect()
    }

    fn frame() -> Rectangle {
        area(0.0, 0.0, WIDTH, HEIGHT)
    }

    fn cells() -> wall::Cells {
        wall::cells(WIDTH, wall::STILL, COLUMNS)
    }

    fn layout() -> Layout {
        Layout::of(&seasons(), cells())
    }

    #[test]
    fn the_header_takes_the_top_of_the_frame_and_the_wall_takes_the_rest() {
        let header = header(frame());
        let region = region(frame());
        assert_eq!(header.y, 0.0);
        assert_eq!(region.y, header.height);
        assert_eq!(header.height + region.height, HEIGHT);
    }

    #[test]
    fn the_header_is_as_tall_as_the_blocks_it_draws() {
        assert_eq!(header(frame()).height, head());
        assert!(head() < HEIGHT / 2.0, "{}", head());
    }

    #[test]
    fn a_divider_and_two_rows_of_stills_fit_under_the_header() {
        let cells = cells();
        let takes = HEAD + divider::HEIGHT + REACH + 2.0 * cells.height;
        assert!(takes <= region(frame()).height, "{takes}");
    }

    #[test]
    fn the_wall_starts_a_clear_gap_under_the_header() {
        const { assert!(HEAD > REACH) };
        assert_eq!(layout().bands[0].top, HEAD);
    }

    #[test]
    fn every_season_takes_a_divider_and_its_rows() {
        let layout = layout();
        assert_eq!(layout.bands.len(), 3);
        assert_eq!(layout.bands[0].rows, 2);
        assert_eq!(layout.bands[1].rows, 3);
        assert_eq!(layout.bands[1].top, HEAD + layout.bands[0].height);
        assert_eq!(layout.content, layout.bands[2].top + layout.bands[2].height);
    }

    #[test]
    fn the_rows_start_under_the_divider() {
        let layout = layout();
        assert_eq!(
            layout.bands[0].rows_top,
            layout.bands[0].top + divider::HEIGHT + REACH
        );
    }

    #[test]
    fn the_header_stands_still_while_the_wall_scrolls_under_it() {
        let layout = layout();
        let region = region(frame());
        assert_eq!(layout.scroll(0, &seasons(), region.height), 0.0);
        let deep = layout.scroll(20, &seasons(), region.height);
        assert!(deep > 0.0);
        assert_eq!(header(frame()), header(frame()));
        assert_eq!(super::region(frame()), region);
    }

    #[test]
    fn the_wall_scrolls_as_focus_moves_down() {
        let layout = layout();
        let height = region(frame()).height;
        assert_eq!(layout.scroll(0, &seasons(), height), 0.0);
        let deep = layout.scroll(20, &seasons(), height);
        let deeper = layout.scroll(26, &seasons(), height);
        assert!(deep > 0.0);
        assert!(deeper > deep);
    }

    #[test]
    fn the_scroll_stops_at_the_foot_of_the_wall() {
        let layout = layout();
        let height = region(frame()).height;
        assert_eq!(
            layout.scroll(26, &seasons(), height),
            layout.content - height
        );
    }

    #[test]
    fn a_wall_shorter_than_its_region_never_scrolls() {
        let layout = Layout::of(&seasons()[..1], cells());
        assert_eq!(layout.scroll(7, &seasons()[..1], 2000.0), 0.0);
    }

    #[test]
    fn a_series_with_no_episodes_never_scrolls() {
        let layout = Layout::of(&[], cells());
        assert_eq!(layout.content, HEAD);
        assert_eq!(layout.scroll(0, &[], 1080.0), 0.0);
    }

    #[test]
    fn a_band_shows_only_where_the_region_reaches_it() {
        let layout = layout();
        let height = region(frame()).height;
        assert!(layout.bands[0].shows(0.0, height));
        assert!(!layout.bands[2].shows(0.0, height));
        assert!(layout.bands[2].shows(layout.content - height, height));
    }
}
