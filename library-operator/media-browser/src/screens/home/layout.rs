// The page is measured before anything draws, because the scroll needs
// every row's place, and the two layers of the view must agree on it.
// This module answers where every row that holds anything lands under
// the band, how tall the page is, and how far it has scrolled.

use iced_winit::core::Rectangle;

use super::{Block, Home};
use crate::views::{area, banner, stack, strip, wall};

// The margin at both sides of the strips, which is the band's own inset,
// so the headings line up.
pub const MARGIN: f32 = 32.0;

// The space between two strips.
const GAP: f32 = 28.0;

// How much of the strip under the focused one the scroll keeps in
// view, so a person sees that there is more below.
const TRAIL: f32 = 70.0;

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

    /// How far the page has scrolled: enough to hold the focused row and the
    /// head of the row under it, and none while the band holds focus.
    pub fn scroll(&self, home: &Home, page: f32, viewport: f32) -> f32 {
        if home.control.is_some() {
            return 0.0;
        }
        let Some(top) = self.tops.get(home.focus).copied().flatten() else {
            return 0.0;
        };
        let block = area(0.0, top, 0.0, height(&home.blocks[home.focus], page));
        let tail = TRAIL.min(self.content - block.y - block.height);
        stack::offset(block, tail, self.content, viewport)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::sample::Catalog;
    use crate::views::band;

    const PAGE: f32 = 1080.0;

    fn home() -> Home {
        Home::open(&mut Catalog)
    }

    #[test]
    fn the_trail_keeps_the_next_rows_heading_in_view() {
        const { assert!(TRAIL > 46.0) };
        const { assert!(TRAIL < strip::POSTER) };
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
