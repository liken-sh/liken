// The home page in motion. The page scrolls down to keep the focused row
// in view, and each strip scrolls across to keep its focused slot in
// view. Both scrolls ease from where they are drawn to where a press puts
// them, and the browser's focus outline travels to the slot at the place
// those scrolls come to rest, so the row and the outline land together.

use iced_winit::core::Rectangle;

use super::layers::viewed;
use super::layout::Layout;
use super::{Block, Home};
use crate::audience::faces::Faces;
use crate::look;
use crate::motion::Glide;
use crate::views::{self, strip};

impl Home {
    /// The layout, the scroll as this second draws it, and the clip both
    /// layers draw inside.
    pub(super) fn drawn(&self, bounds: Rectangle, at: f64) -> (Layout, f32, Rectangle) {
        let (layout, offset, clip) = self.placed(bounds);
        (layout, self.scroll.toward(offset, at), clip)
    }

    /// The rectangle the focus outline goes to on this page at these
    /// bounds: around the banner, the circles of the room, or the focused
    /// slot, at the place the scrolls come to rest. Nothing where the page
    /// holds no row.
    pub fn marked(&self, bounds: Rectangle) -> Option<Rectangle> {
        let (layout, offset, _) = self.placed(bounds);
        let region = layout.region(self, self.focus, offset, bounds)?;
        let around = match self.blocks.get(self.focus)? {
            Block::Banner(banner) => banner.focused().map(|_| region),
            Block::Strip(row) => strip::focused(&viewed(row, region, true, &Faces::default())),
        };
        around.map(views::marked)
    }

    /// How far one row has scrolled across at this second, where it comes
    /// to rest at `settled`. Only the row that holds focus eases.
    pub(super) fn slid(&self, index: usize, settled: f32, at: f64) -> f32 {
        match self.slide {
            (row, glide) if row == index => glide.toward(settled, at),
            _ => settled,
        }
    }

    /// Follow the places the page and the focused row scroll to at these
    /// bounds: ease there on the pace of a press, or snap there where
    /// nothing pressed. The answer is the second the last scroll comes to
    /// rest.
    pub fn follow(&mut self, bounds: Rectangle, at: f64, pace: Option<f64>) -> f64 {
        let (layout, offset, _) = self.placed(bounds);
        let across = self.blocks.get(self.focus).and_then(|block| {
            let row = block.strip()?;
            let region = layout.region(self, self.focus, offset, bounds)?;
            Some(strip::scrolled(&viewed(
                row,
                region,
                false,
                &Faces::default(),
            )))
        });
        let rests = self.scroll.follow(offset, at, pace, look::SCROLL);
        let Some(across) = across else {
            return rests;
        };
        // A row that just took focus starts at rest where it stands, so a
        // press that moves between rows scrolls the page and no row.
        if self.slide.0 != self.focus {
            self.slide = (self.focus, Glide::still(across));
        }
        rests.max(self.slide.1.follow(across, at, pace, look::SCROLL))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::sample::Catalog;

    const FRAME: Rectangle = Rectangle {
        x: 0.0,
        y: 0.0,
        width: 1920.0,
        height: 1080.0,
    };

    // The page scrolled to rest with focus on the second strip, ready for
    // a press down.
    fn on_the_second_strip() -> Home {
        let mut home = Home::open(&mut Catalog, &[], &[]);
        home.focus = 2;
        home.follow(FRAME, 0.0, None);
        home
    }

    #[test]
    fn a_press_down_eases_the_page_to_the_next_row() {
        let mut home = on_the_second_strip();
        let (_, from, _) = home.placed(FRAME);
        home.key("down", &mut Catalog);
        let (_, to, _) = home.placed(FRAME);
        assert!(to > from, "{from} {to}");

        let rests = home.follow(FRAME, 1.0, Some(1.0));

        assert_eq!(rests, 1.0 + look::SCROLL);
        let (_, half, _) = home.drawn(FRAME, 1.0 + look::SCROLL / 2.0);
        assert!(from < half && half < to, "{half}");
        assert_eq!(home.drawn(FRAME, 1.0 + look::SCROLL).1, to);
    }

    // The row's scroll across where it comes to rest, as the page draws it.
    fn across(home: &Home) -> f32 {
        let (layout, offset, _) = home.placed(FRAME);
        let region = layout
            .region(home, home.focus, offset, FRAME)
            .expect("the strip has a region");
        let Block::Strip(row) = &home.blocks[home.focus] else {
            panic!("focus is on a strip");
        };
        strip::scrolled(&viewed(row, region, true, &Faces::default()))
    }

    #[test]
    fn a_press_across_a_long_row_eases_the_row_and_no_other() {
        let mut home = on_the_second_strip();
        if let Block::Strip(row) = &mut home.blocks[home.focus] {
            row.focus = row.items.len() / 2;
        }
        home.follow(FRAME, 0.0, None);
        let from = across(&home);
        home.key("right", &mut Catalog);
        let to = across(&home);
        assert!(to > from, "{from} {to}");

        assert_eq!(home.follow(FRAME, 1.0, Some(1.0)), 1.0 + look::SCROLL);

        let half = home.slid(home.focus, to, 1.0 + look::SCROLL / 2.0);
        assert!(from < half && half < to, "{half}");
        assert_eq!(home.slid(home.focus, to, 1.0 + look::SCROLL), to);
        assert_eq!(home.slid(home.focus + 1, 7.0, 1.0), 7.0);
    }

    #[test]
    fn a_page_that_moved_with_no_press_draws_where_it_rests() {
        let mut home = on_the_second_strip();
        home.key("down", &mut Catalog);
        let (_, to, _) = home.placed(FRAME);

        home.follow(FRAME, 1.0, None);

        assert_eq!(home.drawn(FRAME, 1.0).1, to);
    }

    #[test]
    fn the_outline_goes_to_the_focused_slot_where_the_page_comes_to_rest() {
        let mut home = on_the_second_strip();
        home.key("down", &mut Catalog);
        home.follow(FRAME, 1.0, Some(1.0));

        // The page is still on its way, and the outline already goes to
        // the strip's place at the end of the scroll.
        let marked = home.marked(FRAME).expect("the strip holds a slot");
        let (layout, to, _) = home.placed(FRAME);
        let (_, drawn, _) = home.drawn(FRAME, 1.0 + look::SCROLL / 2.0);
        assert_ne!(drawn, to);
        let region = layout
            .region(&home, home.focus, to, FRAME)
            .expect("the strip has a region");
        assert!(marked.y > region.y, "{marked:?} {region:?}");
        assert!(
            marked.y + marked.height < region.y + region.height,
            "{marked:?} {region:?}"
        );
    }
}
