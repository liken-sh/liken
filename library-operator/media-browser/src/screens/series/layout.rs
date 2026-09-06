// The series page's geometry: a header of a fixed height at the top, and
// under it the region the wall of stills scrolls in. The header stays
// because it carries the focused episode's line and its plot, and a person
// reads them while moving across the wall. Every measure here is pure over
// numbers, so the fit at 1080 and the scroll are tested without a window.

use super::{COLUMNS, Focus, Season};
use crate::look;
use crate::views::{
    REACH, area, card, clip_marked, divider, people, ratings, scroll, stack, strip, text, wall,
};
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

// The height of the screen the browser is drawn for. The rail's bars
// are built at the read, before any frame exists, so the slots are
// counted at this height the way a card's lines are cut at the screen's
// width.
const SCREEN: f32 = 1080.0;

// The region the rail's bars are counted against at the read. The
// header is one height on every series, so this is the region drawn on
// a screen of SCREEN, and a shorter window draws the same bars in a
// shorter rail.
pub fn rail_region() -> Rectangle {
    region(area(0.0, 0.0, 0.0, SCREEN))
}

// The part of the frame the rail draws in. The mark on a focused bar
// draws outside the bar's box, so a clip of the region alone cuts the
// stroke on the top bar and on the bar at the foot.
pub fn rail_clip(region: Rectangle) -> Rectangle {
    clip_marked(region)
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
            // The page passes the still a bar's select lands on instead
            // of the bar, because the layout does not read the rail.
            Focus::Rail(..) => return 0.0,
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
mod tests;
