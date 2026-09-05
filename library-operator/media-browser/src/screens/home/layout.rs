// The page is measured before anything draws, because the scroll needs
// every row's place, and the two layers of the view must agree on it.
// This module answers where every row that holds anything lands under
// the band, how tall the page is, and how far it has scrolled.

use iced_winit::core::Rectangle;

use super::{Block, Home};
use crate::views::{area, banner, strip, wall};

// The margin at both sides of the strips, which is the band's own inset,
// so the headings line up.
pub const MARGIN: f32 = 32.0;

// The space between two strips.
const GAP: f32 = 28.0;

// The height one row takes on a page this tall.
fn height(block: &Block, page: f32) -> f32 {
    match block {
        Block::Banner(_) => banner::height(page),
        Block::Strip(strip) => strip::height(strip.lines),
    }
}

pub struct Layout {
    /// The top of every row in the page's own space before the scroll, and
    /// nothing for a row that holds nothing.
    pub tops: Vec<Option<f32>>,
    /// How tall the page is.
    pub content: f32,
}

impl Layout {
    pub fn of(home: &Home, page: f32) -> Self {
        let mut at = wall::HEAD;
        let tops = home
            .blocks
            .iter()
            .map(|block| {
                if block.is_empty() {
                    return None;
                }
                let top = at;
                at += height(block, page) + GAP;
                Some(top)
            })
            .collect();
        Self {
            tops,
            content: at - GAP + wall::HEAD,
        }
    }

    /// The region one row draws in at this scroll, inside a frame this wide,
    /// on a page this tall.
    pub fn region(
        &self,
        home: &Home,
        index: usize,
        offset: f32,
        width: f32,
        page: f32,
    ) -> Option<Rectangle> {
        let top = self.tops.get(index).copied().flatten()?;
        Some(area(
            MARGIN,
            crate::views::band::HEIGHT + top - offset,
            width - 2.0 * MARGIN,
            height(&home.blocks[index], page),
        ))
    }

    /// How far the page has scrolled. None while the band or the banner
    /// holds focus. The first strip keeps as much of the banner over it as
    /// the viewport holds, and scrolls only as far as it needs to stand
    /// whole in view, because a short viewport holds less than the banner
    /// and one strip. For every later row, the row's heading sits directly
    /// under the band, so the rows under it fill the rest of the viewport:
    /// a person presses down far more than up, and what is next matters
    /// more than what was passed. The scroll stops at the foot of the
    /// page, so the last rows never leave a gap under them.
    pub fn scroll(&self, home: &Home, page: f32, viewport: f32) -> f32 {
        if home.control.is_some() {
            return 0.0;
        }
        let Some(top) = self.tops.get(home.focus).copied().flatten() else {
            return 0.0;
        };
        if home.focus == 0 {
            return 0.0;
        }
        let under_band = top - wall::HEAD;
        let foot = (self.content - viewport).max(0.0);
        if self.head() == Some(home.focus) {
            let bottom = top + height(&home.blocks[home.focus], page) + wall::HEAD;
            return (bottom - viewport).clamp(0.0, under_band.min(foot).max(0.0));
        }
        under_band.clamp(0.0, foot)
    }

    // The first row after the banner that holds anything. A row that
    // holds nothing takes no room, so it is never the first strip.
    fn head(&self) -> Option<usize> {
        self.tops
            .iter()
            .skip(1)
            .position(Option::is_some)
            .map(|index| index + 1)
    }
}

#[cfg(test)]
mod tests {
    use super::super::{Row, Strip};
    use super::*;
    use crate::sample::Catalog;
    use crate::views::band;

    const PAGE: f32 = 1080.0;

    fn home() -> Home {
        Home::open(&mut Catalog)
    }

    #[test]
    fn the_banner_is_the_first_row_and_the_first_strip_stands_under_it() {
        let home = home();
        let layout = Layout::of(&home, PAGE);
        let banner = layout.tops[0].expect("the banner holds titles");
        let first = layout.tops[1].expect("the released strip holds slots");
        assert_eq!(banner, wall::HEAD);
        assert_eq!(first, banner + banner::height(PAGE) + GAP);
    }

    #[test]
    fn focus_on_the_first_strip_shows_the_whole_banner_over_it() {
        let mut home = home();
        home.focus = 1;
        let layout = Layout::of(&home, PAGE);
        let offset = layout.scroll(&home, PAGE, PAGE - band::HEIGHT);
        assert_eq!(offset, 0.0);
        let region = layout
            .region(&home, 0, offset, 1920.0, PAGE)
            .expect("the banner has a region");
        assert_eq!(region.x, MARGIN);
        assert_eq!(region.width, 1920.0 - 2.0 * MARGIN);
        assert_eq!(region.y, band::HEIGHT + wall::HEAD);
        assert_eq!(region.height, banner::height(PAGE));
    }

    #[test]
    fn the_first_strip_that_holds_anything_stands_under_the_whole_banner() {
        let mut home = home();
        home.blocks.insert(1, Block::Strip(Strip::new(Row::Genres)));
        home.focus = 2;
        let layout = Layout::of(&home, PAGE);
        assert_eq!(layout.tops[1], None);
        assert_eq!(layout.scroll(&home, PAGE, PAGE - band::HEIGHT), 0.0);
    }

    #[test]
    fn a_short_viewport_scrolls_the_first_strip_whole_into_view_and_no_further() {
        let mut home = home();
        home.focus = 1;
        let layout = Layout::of(&home, PAGE);
        let strip = layout.tops[1].expect("the released strip holds slots");
        let bottom = strip + strip::height(2) + wall::HEAD;
        let short = bottom - 100.0;
        let offset = layout.scroll(&home, PAGE, short);
        assert_eq!(offset, 100.0);
        let region = layout
            .region(&home, 1, offset, 1920.0, PAGE)
            .expect("the released strip has a region");
        assert_eq!(region.y + region.height + wall::HEAD, band::HEIGHT + short);
        assert_eq!(
            layout.scroll(&home, PAGE, wall::HEAD + strip::height(2)),
            strip - wall::HEAD
        );
    }

    #[test]
    fn focus_on_the_second_strip_scrolls_the_banner_up_under_the_band() {
        let mut home = home();
        home.focus = 2;
        let layout = Layout::of(&home, PAGE);
        let offset = layout.scroll(&home, PAGE, PAGE - band::HEIGHT);
        assert!(offset > 0.0);
        let region = layout
            .region(&home, 0, offset, 1920.0, PAGE)
            .expect("the banner has a region");
        assert!(region.y < band::HEIGHT);
        let region = layout
            .region(&home, 2, offset, 1920.0, PAGE)
            .expect("the second strip has a region");
        assert_eq!(region.y, band::HEIGHT + wall::HEAD);
    }

    #[test]
    fn the_last_row_scrolls_no_further_than_the_foot_of_the_page() {
        let mut home = home();
        home.focus = home.blocks.len() - 1;
        let layout = Layout::of(&home, PAGE);
        let viewport = PAGE - band::HEIGHT;
        assert!(layout.tops[home.focus].is_some());
        assert_eq!(
            layout.scroll(&home, PAGE, viewport),
            layout.content - viewport
        );
        assert_eq!(layout.scroll(&home, PAGE, layout.content + 100.0), 0.0);
    }

    #[test]
    fn the_band_in_focus_scrolls_nothing() {
        let mut home = home();
        home.focus = 2;
        home.control = Some(0);
        let layout = Layout::of(&home, PAGE);
        assert_eq!(layout.scroll(&home, PAGE, PAGE - band::HEIGHT), 0.0);
    }
}
