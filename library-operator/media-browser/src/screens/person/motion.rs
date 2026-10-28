// A person's page in motion. The wall of works scrolls down to keep the
// focused work in view, and the scroll eases from where it is drawn to
// where a press puts it. The browser's focus outline travels to the work
// at the place the scroll comes to rest, so the row and the outline land
// together. The head over the wall never moves.

use iced_winit::core::Rectangle;

use super::Person;
use super::page::{LINES, region};
use crate::look;
use crate::screens::Item;
use crate::views::{self, wall};

impl Person {
    /// The wall of works at these bounds, scrolled as this second draws
    /// it.
    pub(super) fn drawn(&self, bounds: Rectangle, at: f64) -> wall::Grid<'_, Item> {
        let grid = self.works.grid(region(bounds), LINES);
        wall::Grid {
            offset: self.scroll.toward(grid.offset, at),
            ..grid
        }
    }

    /// The rectangle the focus outline goes to on this page at these
    /// bounds: around the focused work, at the place the wall's scroll
    /// comes to rest. Nothing on a page with no works.
    pub fn marked(&self, bounds: Rectangle) -> Option<Rectangle> {
        wall::focused(&self.works.grid(region(bounds), LINES)).map(views::marked)
    }

    /// Follow the place the wall of works scrolls to at these bounds: ease
    /// there on the pace of a press, or snap there where nothing pressed.
    /// The answer is the second the scroll comes to rest.
    pub fn follow(&mut self, bounds: Rectangle, at: f64, pace: Option<f64>) -> f64 {
        let settled = self.works.grid(region(bounds), LINES).offset;
        self.scroll.follow(settled, at, pace, look::SCROLL)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::{Query, Sort};
    use crate::sample::{Catalog, people};
    use crate::screens::slots::Slots;

    const FRAME: Rectangle = Rectangle {
        x: 0.0,
        y: 0.0,
        width: 1920.0,
        height: 1080.0,
    };

    // A person's page over a wall long enough to scroll, at rest with
    // focus on its fourth row, ready for a press down. The sample credits
    // nobody in more than one row of works, so the wall is the sample's
    // features: the page draws and scrolls any slots the same way.
    fn on_the_fourth_row() -> Person {
        let mut page = Person::open("sample/features", people::PROLIFIC, &mut Catalog)
            .expect("the sample holds the person");
        let query = Query::Library {
            library: "sample/features".into(),
            sort: Sort::Title,
        };
        page.works = Slots::open(query, &mut Catalog);
        page.works.focus = 3 * wall::COLUMNS;
        page.follow(FRAME, 0.0, None);
        page
    }

    #[test]
    fn a_press_down_eases_the_wall_of_works_to_the_next_row() {
        let mut page = on_the_fourth_row();
        let from = page.drawn(FRAME, 0.0).offset;
        page.key("down", &mut Catalog);
        let to = page.works.grid(region(FRAME), LINES).offset;
        assert!(to > from, "{from} {to}");

        assert_eq!(page.follow(FRAME, 1.0, Some(1.0)), 1.0 + look::SCROLL);

        let half = page.drawn(FRAME, 1.0 + look::SCROLL / 2.0).offset;
        assert!(from < half && half < to, "{half}");
        assert_eq!(page.drawn(FRAME, 1.0 + look::SCROLL).offset, to);
    }

    #[test]
    fn the_outline_goes_to_the_focused_work_where_the_wall_comes_to_rest() {
        let mut page = on_the_fourth_row();
        page.key("down", &mut Catalog);
        page.follow(FRAME, 1.0, Some(1.0));

        // The wall is still on its way, and the outline already goes to
        // the work's place at the end of the scroll.
        let at = |second: f64| wall::focused(&page.drawn(FRAME, second)).map(views::marked);
        assert_ne!(page.marked(FRAME), at(1.0));
        assert_eq!(page.marked(FRAME), at(1.0 + look::SCROLL));
    }
}
