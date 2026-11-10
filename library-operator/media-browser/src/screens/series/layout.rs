// The series page's geometry: a header of a fixed height at the top, and
// under it the region the wall of stills scrolls in. The header stays
// while focus is on the wall, because it carries the focused episode's
// line and its plot, and a person reads them while moving across the
// wall. Focus on a row under the seasons lifts the header out of the
// frame, because it shows no episode then. Every measure here is pure over
// numbers, so the fit at 1080 and the scroll are tested without a window.

use super::{COLUMNS, Focus, Season};
use crate::look;
use crate::views::{
    REACH, area, band, card, clip_marked, divider, people, ratings, screen, scroll, stack, strip,
    text, wall,
};
use iced_winit::core::Rectangle;

/// The line a page scrolled to its rows shows them under: the foot of
/// the browser's strip, where a movie's page stops its rows too.
pub const VIEWPORT_TOP: f32 = band::HEIGHT;

/// The space between the foot of the header and the first divider.
pub const HEAD: f32 = 20.0;

/// The space over the header's first block.
pub const TOP: f32 = 28.0;

/// The space between the plot's last line and the first row of stills,
/// so the header's text never touches the wall. It is narrower than
/// `TOP`, because the first divider adds `HEAD` and the empty top of its
/// own band over its heading. The header is short enough that at 1080
/// the region under it holds a divider, a row of stills, and the art of
/// the next row, and a rail with one bar for each of nine seasons.
pub const FOOT: f32 = 16.0;

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
/// place on every series. The header draws on the screen's content
/// region, which `views::screen` states.
pub fn header(bounds: Rectangle) -> Rectangle {
    let content = screen::region(bounds);
    area(
        content.x,
        bounds.y,
        content.width,
        head().min(bounds.height),
    )
}

// The region the rail's bars are counted against at the read. The bars
// are built at the read, before any frame exists, and the header is one
// height on every series, so this is the region drawn on
// `screen::FRAME`. A shorter window draws the same bars in a shorter
// rail.
pub fn rail_region() -> Rectangle {
    region(screen::FRAME)
}

// The part of the frame the rail draws in. The mark on a focused bar
// draws outside the bar's box, so a clip of the region alone cuts the
// stroke on the top bar and on the bar at the foot.
pub fn rail_clip(region: Rectangle) -> Rectangle {
    clip_marked(region)
}

/// The part of the frame the wall scrolls in, under the header. The grid
/// is the screen's content region widened by the cell gutter, so the
/// first and the last column's stills land on the region's own edges.
pub fn region(bounds: Rectangle) -> Rectangle {
    let header = header(bounds);
    let grid = wall::columned(screen::region(bounds), COLUMNS);
    area(
        grid.x,
        bounds.y + header.height,
        grid.width,
        bounds.height - header.height,
    )
}

/// A region of the page moved up by the header's lift: its top rises
/// with the foot of the header, and its foot stays at the foot of the
/// frame.
pub fn lifted(region: Rectangle, lift: f32) -> Rectangle {
    area(
        region.x,
        region.y - lift,
        region.width,
        region.height + lift,
    )
}

/// The part of the frame the header still draws in at this lift: from
/// the line the page shows its blocks under, which falls from the top of
/// the frame to `VIEWPORT_TOP` as the header rises, down to the foot of
/// the header. A header lifted wholly past that line draws nothing.
pub fn head_shown(bounds: Rectangle, lift: f32) -> Rectangle {
    let top = lift.clamp(0.0, VIEWPORT_TOP);
    let foot = head() - lift;
    area(
        bounds.x,
        bounds.y + top,
        bounds.width,
        (foot - top).max(0.0),
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

/// The top of an episode's open row, from the top of the header: level
/// with the ratings line, so the row, its bar, and its status line fit
/// beside the text column inside the header's fixed height.
pub fn episode_row() -> f32 {
    TOP + LOGO_HEIGHT + GAP + text::height(1, look::FACTS) + GAP
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

/// Where the page rests: how far the header has scrolled up out of the
/// frame, and how far the wall has scrolled in the region under it.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Rest {
    /// How far the header has scrolled up. It is zero while focus is on
    /// the wall, and at most the height of the header over `VIEWPORT_TOP`.
    pub lift: f32,
    /// How far the wall has scrolled down inside its region.
    pub offset: f32,
}

// Whether focus is on a row under the seasons: a franchise strip, a
// stripe of people, the More from strip, or a studio of the foot. The
// header shows no episode while one of them holds focus, so the page
// scrolls the header away to give the rows the frame.
fn under_the_seasons(focus: Focus) -> bool {
    matches!(
        focus,
        Focus::Franchise(..) | Focus::Stripe(..) | Focus::More(..) | Focus::Studio(..)
    )
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
    /// The top of the More from strip, in the wall's own space, and
    /// nothing on a page that draws none.
    pub more: Option<f32>,
    /// The top of the foot block, in the wall's own space.
    pub foot: f32,
    /// The top of the line that says the series holds no episodes, in
    /// the wall's own space, and nothing on a series that holds some.
    pub empty: Option<f32>,
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
        more: bool,
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
        // A series the scan found no episodes of keeps its header, its
        // stripes, and its foot, and the line says why the wall under
        // the header is empty.
        let empty = seasons.is_empty().then_some(top);
        if empty.is_some() {
            top += text::height(1, look::FACTS);
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
        let more = more.then(|| {
            top += STRIPE_GAP;
            let at = top;
            top += strip_height();
            at
        });
        let rows = stripes > 0 || franchises > 0 || more.is_some();
        if rows {
            top += STRIPE_GAP;
        }
        let mut block = top;
        if foot > 0.0 {
            if !rows {
                block += STRIPE_GAP;
            }
            top = block + foot + STRIPE_GAP;
        }
        Self {
            cells,
            bands,
            franchises: strips,
            stripes: tops,
            more,
            foot: block,
            empty,
            content: top,
        }
    }

    /// Where the page rests with focus here, in a frame this tall. While
    /// focus is on the wall, the header holds and the wall scrolls in the
    /// region under it. While focus is on a row under the seasons, the
    /// page scrolls as one stack, the way a movie's page does: the header
    /// leaves the top of the frame first, and the wall scrolls once the
    /// foot of the header has reached `VIEWPORT_TOP`.
    pub fn rest(&self, focus: Focus, seasons: &[Season], height: f32) -> Rest {
        let Some((block, tail)) = self.block(focus, seasons) else {
            return Rest::default();
        };
        if !under_the_seasons(focus) {
            let offset = stack::offset(block, tail, self.content, height - head());
            return Rest { lift: 0.0, offset };
        }
        // The page's own space starts at the top of the header, and the
        // part of the frame it scrolls in starts at the strip's line, so
        // a block never rests above that line.
        let over = head() - VIEWPORT_TOP;
        let page = stack::offset(
            area(0.0, over + block.y, 0.0, block.height),
            tail,
            over + self.content,
            height - VIEWPORT_TOP,
        );
        let lift = page.min(over);
        Rest {
            lift,
            offset: page - lift,
        }
    }

    // The block focus is on, in the wall's own space, and the height of
    // what follows it that the scroll keeps in view with it. A focus that
    // stands in the header, or on the rail, keeps the wall at its top.
    fn block(&self, focus: Focus, seasons: &[Season]) -> Option<(Rectangle, f32)> {
        match focus {
            // The page passes the still a bar's select lands on instead
            // of the bar, because the layout does not read the rail.
            Focus::Rail(..) => None,
            // The button row is in the header, which holds while it has
            // focus, so the wall stays at its top.
            Focus::Buttons(..) => None,
            Focus::Franchise(strip, _) => {
                let top = self.franchises.get(strip)?;
                // A franchise strip is never the last block, because the
                // stripes and the foot follow it, so it pulls the gap
                // under it into view and no more.
                let below = self.content - top - strip_height();
                Some((area(0.0, *top, 0.0, strip_height()), STRIPE_GAP.min(below)))
            }
            Focus::Stripe(stripe, _) => {
                let top = self.stripes.get(stripe)?;
                // The last stripe pulls everything under it into view, so
                // the studios under it show before focus reaches them.
                let below = self.content - top - people::HEIGHT;
                let tail = match stripe + 1 == self.stripes.len() {
                    true => below,
                    false => STRIPE_GAP.min(below),
                };
                Some((area(0.0, *top, 0.0, people::HEIGHT), tail))
            }
            // The More from strip is the last row, so it pulls the foot
            // under it into view, as the last stripe does.
            Focus::More(..) => {
                let top = self.more?;
                Some((
                    area(0.0, top, 0.0, strip_height()),
                    self.content - top - strip_height(),
                ))
            }
            // The foot is the last block, so a studio pulls the page's end
            // into view.
            Focus::Studio(..) => Some((area(0.0, self.foot, 0.0, 0.0), self.content - self.foot)),
            // The episode's own row and its marks are in the header too,
            // and the wall stands at the still they were opened from.
            Focus::Still(index) | Focus::Episode(index, _) | Focus::EpisodeMark(index, _) => {
                let band = self.band(index, seasons)?;
                let row = (index - seasons[band].run.first) / COLUMNS;
                Some((
                    area(
                        0.0,
                        self.bands[band].rows_top + row as f32 * self.cells.height,
                        0.0,
                        self.cells.height,
                    ),
                    TRAIL * self.cells.height,
                ))
            }
        }
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
