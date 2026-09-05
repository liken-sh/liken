// The series page's geometry: a header of a fixed height at the top, and
// under it the region the wall of stills scrolls in. The header stays
// because it carries the focused episode's line and its plot, and a person
// reads them while moving across the wall. Every measure here is pure over
// numbers, so the fit at 1080 and the scroll are tested without a window.

use super::{COLUMNS, Focus, Season};
use crate::look;
use crate::views::{REACH, area, card, divider, people, ratings, scroll, stack, strip, text, wall};
use iced_winit::core::Rectangle;

/// The space between the foot of the header and the first divider.
pub const HEAD: f32 = 20.0;

/// The space over the header's first block.
pub const TOP: f32 = 28.0;

/// The space between the plot's last line and the first row of stills,
/// so the header's text never touches the wall.
pub const FOOT: f32 = 28.0;

/// The space between two blocks of the header.
pub const GAP: f32 = 14.0;

/// The box a logo draws in, at the proportions the metadata tools write a
/// logo file in.
pub const LOGO_WIDTH: f32 = 460.0;
pub const LOGO_HEIGHT: f32 = 96.0;

/// The lines the header cuts the episode's plot to.
pub const PLOT_LINES: usize = 2;

/// The space between the foot of the wall and the first stripe,
/// and between two stripes.
pub const STRIPE_GAP: f32 = 24.0;

/// The height one franchise strip takes on the page. Its slots draw the
/// card every other strip draws, which is two lines.
pub fn strip_height() -> f32 {
    strip::height(card::LINES)
}

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
        + ratings::HEIGHT
        + GAP
        + text::height(2, look::FACTS)
        + GAP
        + text::height(PLOT_LINES, look::PLOT)
        + FOOT
}

/// The box one season's divider draws in, in frame space after the
/// scroll. The divider stands at the top of its own band while that top
/// is in view, holds at the top of the region while the season's rows
/// scroll under it, and the next season's band pushes it off: the rule
/// the jump rail's era labels follow.
pub fn divider_box(region: Rectangle, band: &Band, offset: f32) -> Rectangle {
    stack::held(
        area(
            region.x,
            region.y + band.top - offset,
            region.width,
            band.height,
        ),
        region,
        divider::HEIGHT,
    )
}

/// The part of the region the stills draw in: under the band a held
/// divider keeps at the top. A still that scrolls into that band draws
/// nothing, so art never crosses the divider, which the renderer would
/// otherwise draw the art over.
pub fn stills(region: Rectangle) -> Rectangle {
    area(
        region.x,
        region.y + divider::HEIGHT,
        region.width,
        (region.height - divider::HEIGHT).max(0.0),
    )
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
    /// The top of each franchise strip, in the wall's own space.
    pub franchises: Vec<f32>,
    /// The top of each stripe, in the wall's own space.
    pub stripes: Vec<f32>,
    /// The top of the foot block, in the wall's own space.
    pub foot: f32,
    /// The length of the wall, the gap under the header included.
    pub content: f32,
}

impl Layout {
    /// The wall for these seasons, with stills of this cell. The first
    /// divider starts a gap below the header, and the gap scrolls away
    /// with the rows.
    pub fn of(
        seasons: &[Season],
        cells: wall::Cells,
        franchises: usize,
        stripes: usize,
        foot: f32,
    ) -> Self {
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
        let mut strips = Vec::with_capacity(franchises);
        for _ in 0..franchises {
            top += STRIPE_GAP;
            strips.push(top);
            top += strip_height();
        }
        let mut tops = Vec::with_capacity(stripes);
        for _ in 0..stripes {
            top += STRIPE_GAP;
            tops.push(top);
            top += people::HEIGHT;
        }
        if stripes > 0 || franchises > 0 {
            top += STRIPE_GAP;
        }
        let mut block = top;
        if foot > 0.0 {
            if stripes == 0 && franchises == 0 {
                block += STRIPE_GAP;
            }
            top = block + foot + STRIPE_GAP;
        }
        Self {
            cells,
            bands,
            franchises: strips,
            stripes: tops,
            foot: block,
            content: top,
        }
    }

    /// How far the wall has scrolled with focus on this still. The wall
    /// stands at its top until the focused row would leave the foot of the
    /// region, and the header above the region never moves.
    pub fn scroll(&self, focus: Focus, seasons: &[Season], height: f32) -> f32 {
        let (region, tail) = match focus {
            Focus::Franchise(strip, _) => match self.franchises.get(strip) {
                Some(top) => {
                    // A franchise strip is never the last block, because
                    // the stripes and the foot follow it, so it pulls
                    // the gap under it into view and no more.
                    let below = self.content - top - strip_height();
                    (area(0.0, *top, 0.0, strip_height()), STRIPE_GAP.min(below))
                }
                None => return 0.0,
            },
            Focus::Stripe(stripe, _) => match self.stripes.get(stripe) {
                Some(top) => {
                    // The last stripe pulls everything under it into view,
                    // because the foot takes no focus of its own.
                    let below = self.content - top - people::HEIGHT;
                    let tail = match stripe + 1 == self.stripes.len() {
                        true => below,
                        false => STRIPE_GAP.min(below),
                    };
                    (area(0.0, *top, 0.0, people::HEIGHT), tail)
                }
                None => return 0.0,
            },
            Focus::Still(index) => {
                let Some(band) = self.band(index, seasons) else {
                    return 0.0;
                };
                let row = (index - seasons[band].run.first) / COLUMNS;
                (
                    area(
                        0.0,
                        self.bands[band].rows_top + row as f32 * self.cells.height,
                        0.0,
                        self.cells.height,
                    ),
                    TRAIL * self.cells.height,
                )
            }
        };
        stack::offset(region, tail, self.content, height)
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
    use crate::screens::franchise::strips::Place;

    // A frame of the size the screens this browser draws on hold.
    const WIDTH: f32 = 1920.0;
    const HEIGHT: f32 = 1080.0;

    // Three seasons of eight, nine, and ten episodes.
    fn seasons() -> Vec<Season> {
        [(1, 0, 8), (2, 8, 9), (3, 17, 10)]
            .iter()
            .map(|(number, first, count)| Season {
                number: *number,
                name: format!("Season {number} (2004)"),
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
        wall::lined(WIDTH, wall::STILL, COLUMNS, card::LINES)
    }

    fn layout() -> Layout {
        Layout::of(&seasons(), cells(), 0, 0, 0.0)
    }

    // The same wall with the three stripes of a title's credits
    // after it.
    fn with_stripes() -> Layout {
        Layout::of(&seasons(), cells(), 0, 3, 0.0)
    }

    // The same wall, its stripes, and a foot of two lines under them.
    fn with_foot() -> Layout {
        Layout::of(&seasons(), cells(), 0, 3, FOOT_LINES)
    }

    // The height of a foot of two lines.
    const FOOT_LINES: f32 = 48.0;

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
    fn a_still_carries_the_two_lines_of_a_card_under_it() {
        assert_eq!(
            cells().height,
            wall::cells(WIDTH, wall::STILL, COLUMNS).height + text::height(1, look::FACE)
        );
    }

    #[test]
    fn a_divider_stands_at_the_top_of_its_own_season_while_that_top_is_in_view() {
        let layout = layout();
        let region = region(frame());
        let first = divider_box(region, &layout.bands[0], 0.0);
        assert_eq!(first.y, region.y + layout.bands[0].top);
        assert_eq!(first.x, region.x);
        assert_eq!(first.width, region.width);
        assert_eq!(first.height, divider::HEIGHT);
    }

    #[test]
    fn a_divider_holds_at_the_top_of_the_wall_while_its_rows_scroll_under_it() {
        let layout = layout();
        let region = region(frame());
        let seasons = seasons();
        let offset = layout.scroll(Focus::Still(7), &seasons, region.height);
        assert!(offset > layout.bands[0].top);
        assert_eq!(divider_box(region, &layout.bands[0], offset).y, region.y);
    }

    #[test]
    fn the_next_season_pushes_the_divider_over_it_off() {
        let layout = layout();
        let region = region(frame());
        let band = layout.bands[0];
        // The scroll that leaves the first season a divider short of its
        // foot, where the second season's rows reach the top.
        let offset = band.top + band.height - divider::HEIGHT / 2.0;
        let held = divider_box(region, &band, offset);
        assert!(held.y < region.y);
        assert_eq!(held.y + held.height, region.y + divider::HEIGHT / 2.0);
    }

    #[test]
    fn the_stills_draw_under_the_band_a_held_divider_keeps() {
        let region = region(frame());
        let stills = stills(region);
        assert_eq!(stills.y, region.y + divider::HEIGHT);
        assert_eq!(stills.y + stills.height, region.y + region.height);
        assert_eq!(stills.x, region.x);
        assert_eq!(stills.width, region.width);
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
    fn the_stripes_follow_the_last_season_of_the_wall() {
        let plain = layout();
        let layout = with_stripes();
        assert_eq!(layout.bands, plain.bands);
        assert_eq!(layout.stripes.len(), 3);
        assert_eq!(layout.stripes[0], plain.content + STRIPE_GAP);
        assert_eq!(
            layout.stripes[1],
            layout.stripes[0] + people::HEIGHT + STRIPE_GAP
        );
        assert_eq!(
            layout.content,
            layout.stripes[2] + people::HEIGHT + STRIPE_GAP
        );
    }

    // The same wall with one franchise strip between the seasons and
    // the stripes.
    fn with_franchise() -> Layout {
        Layout::of(&seasons(), cells(), 1, 3, 0.0)
    }

    #[test]
    fn a_franchise_strip_stands_as_tall_as_the_card_its_slots_draw() {
        assert_eq!(strip_height(), strip::height(card::LINES));
        assert!(strip_height() > strip::height(1));
    }

    #[test]
    fn a_franchise_strip_stands_between_the_last_season_and_the_stripes() {
        let plain = with_stripes();
        let layout = with_franchise();
        assert_eq!(layout.bands, plain.bands);
        assert_eq!(layout.franchises.len(), 1);
        assert_eq!(layout.franchises[0], plain.stripes[0]);
        assert_eq!(
            layout.stripes[0],
            layout.franchises[0] + strip_height() + STRIPE_GAP
        );
        assert!((layout.content - plain.content - strip_height() - STRIPE_GAP).abs() < 1e-3);
    }

    #[test]
    fn the_wall_scrolls_the_focused_franchise_strip_into_view() {
        let layout = with_franchise();
        let height = region(frame()).height;
        let offset = layout.scroll(Focus::Franchise(0, Place::Heading), &seasons(), height);
        assert!(layout.franchises[0] - offset >= 0.0);
        assert!(layout.franchises[0] + strip_height() - offset <= height);
        assert_eq!(
            layout.scroll(Focus::Franchise(9, Place::Heading), &seasons(), height),
            0.0
        );
    }

    #[test]
    fn the_foot_follows_the_last_stripe_and_ends_the_wall() {
        let plain = with_stripes();
        let layout = with_foot();
        assert_eq!(layout.stripes, plain.stripes);
        assert_eq!(layout.foot, plain.content);
        assert_eq!(layout.content, layout.foot + FOOT_LINES + STRIPE_GAP);
    }

    #[test]
    fn a_wall_with_no_stripe_still_leaves_a_gap_over_its_foot() {
        let layout = Layout::of(&seasons(), cells(), 0, 0, FOOT_LINES);
        assert_eq!(
            layout.foot,
            layout.bands[2].top + layout.bands[2].height + STRIPE_GAP
        );
    }

    #[test]
    fn the_last_stripe_pulls_the_foot_into_view() {
        let layout = with_foot();
        let height = region(frame()).height;
        let offset = layout.scroll(Focus::Stripe(2, 0), &seasons(), height);
        assert!(layout.foot + FOOT_LINES - offset <= height);
    }

    #[test]
    fn the_wall_scrolls_the_focused_stripe_into_view() {
        let layout = with_stripes();
        let height = region(frame()).height;
        let mut offsets = Vec::new();
        for stripe in 0..3 {
            let offset = layout.scroll(Focus::Stripe(stripe, 0), &seasons(), height);
            assert!(layout.stripes[stripe] - offset >= 0.0);
            assert!(layout.stripes[stripe] + people::HEIGHT - offset <= height);
            offsets.push(offset);
        }
        assert!(offsets[0] > layout.scroll(Focus::Still(26), &seasons(), height));
        assert!(offsets[2] > offsets[1]);
        assert_eq!(offsets[2], layout.content - height);
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
        assert_eq!(
            layout.scroll(Focus::Still(0), &seasons(), region.height),
            0.0
        );
        let deep = layout.scroll(Focus::Still(20), &seasons(), region.height);
        assert!(deep > 0.0);
        assert_eq!(header(frame()), header(frame()));
        assert_eq!(super::region(frame()), region);
    }

    #[test]
    fn the_wall_scrolls_as_focus_moves_down() {
        let layout = layout();
        let height = region(frame()).height;
        assert_eq!(layout.scroll(Focus::Still(0), &seasons(), height), 0.0);
        let deep = layout.scroll(Focus::Still(20), &seasons(), height);
        let deeper = layout.scroll(Focus::Still(26), &seasons(), height);
        assert!(deep > 0.0);
        assert!(deeper > deep);
    }

    #[test]
    fn the_scroll_stops_at_the_foot_of_the_wall() {
        let layout = layout();
        let height = region(frame()).height;
        assert_eq!(
            layout.scroll(Focus::Still(26), &seasons(), height),
            layout.content - height
        );
    }

    #[test]
    fn a_wall_shorter_than_its_region_never_scrolls() {
        let layout = Layout::of(&seasons()[..1], cells(), 0, 0, 0.0);
        assert_eq!(layout.scroll(Focus::Still(7), &seasons()[..1], 2000.0), 0.0);
    }

    #[test]
    fn a_series_with_no_episodes_never_scrolls() {
        let layout = Layout::of(&[], cells(), 0, 0, 0.0);
        assert_eq!(layout.content, HEAD);
        assert_eq!(layout.scroll(Focus::Still(0), &[], 1080.0), 0.0);
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
