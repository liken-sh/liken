// A wall in motion. The grid scrolls down to keep the slot the wall
// stands at in view, and the scroll eases from where it is drawn to where
// a press puts it. The browser's focus outline travels to the slot at the
// place the scroll comes to rest, so the row and the outline land
// together. On a search wall with its keyboard grid shown, the outline
// goes around the focused cell of the grid.

use iced_winit::core::Rectangle;

use super::search::grid_at;
use super::{Focus, Wall, rail, region};
use crate::look;
use crate::screens::Item;
use crate::views::{self, card, wall};

impl Wall {
    /// The grid of slots at these bounds with its scroll at rest, and that
    /// scroll. The grid draws no mark of its own.
    pub(super) fn slotted(&self, bounds: Rectangle) -> (wall::Grid<'_, Item>, f32) {
        let whole = region(bounds, self.under_band());
        let region = rail::beside(whole, &self.bars);
        let cells = wall::lined(region.width, wall::POSTER, wall::COLUMNS, card::LINES);
        let standing = self.standing();
        let offset = wall::scrolled(
            standing,
            self.slots.items.len(),
            wall::COLUMNS,
            &cells,
            region.height,
        );
        let grid = wall::Grid {
            items: &self.slots.items,
            focus: Some(standing),
            library: "",
            ratio: wall::POSTER,
            columns: wall::COLUMNS,
            lines: card::LINES,
            region,
            offset,
        };
        (grid, offset)
    }

    /// The rectangle the focus outline goes to on this wall at these
    /// bounds: around the focused cell of the keyboard grid where it is
    /// shown, the focused cell of the rail, or the focused slot at the
    /// place the scroll comes to rest. Nothing on a wall with no slots.
    pub fn marked(&self, bounds: Rectangle) -> Option<Rectangle> {
        let around = match (self.grid(), self.focus) {
            (Some(keys), _) => Some(keys.focused(grid_at(bounds.width))),
            (None, Focus::Slots) => wall::focused(&self.slotted(bounds).0),
            (None, Focus::Rail(cell)) => rail::focused(
                region(bounds, self.under_band()),
                &self.bars,
                &self.slots.query,
                cell,
            ),
        };
        around.map(views::marked)
    }

    /// Follow the place the grid scrolls to at these bounds: ease there on
    /// the pace of a press, or snap there where nothing pressed. The answer
    /// is the second the scroll comes to rest.
    pub fn follow(&mut self, bounds: Rectangle, at: f64, pace: Option<f64>) -> f64 {
        let (_, settled) = self.slotted(bounds);
        self.scroll.follow(settled, at, pace, look::SCROLL)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::catalog::{Query, Sort};
    use crate::sample::Catalog;
    use crate::screens::wall::search::Search;

    const FRAME: Rectangle = Rectangle {
        x: 0.0,
        y: 0.0,
        width: 1920.0,
        height: 1080.0,
    };

    // The wall of the sample's features, scrolled to rest on its fourth
    // row, ready for a press down.
    fn on_the_fourth_row() -> Wall {
        let query = Query::Library {
            library: "sample/features".into(),
            sort: Sort::Title,
        };
        let mut wall = Wall::open(query, &mut Catalog);
        wall.slots.focus = 3 * wall::COLUMNS;
        wall.follow(FRAME, 0.0, None);
        wall
    }

    #[test]
    fn a_press_down_eases_the_wall_to_the_next_row() {
        let mut wall = on_the_fourth_row();
        let (_, from) = wall.slotted(FRAME);
        wall.key("down", &mut Catalog);
        let (_, to) = wall.slotted(FRAME);
        assert!(to > from, "{from} {to}");

        assert_eq!(wall.follow(FRAME, 1.0, Some(1.0)), 1.0 + look::SCROLL);

        let half = wall.scroll.toward(to, 1.0 + look::SCROLL / 2.0);
        assert!(from < half && half < to, "{half}");
        assert_eq!(wall.scroll.toward(to, 1.0 + look::SCROLL), to);
    }

    #[test]
    fn the_outline_goes_around_the_focused_cell_while_the_keyboard_grid_shows() {
        let mut wall = on_the_fourth_row();
        let slot = wall.marked(FRAME).expect("the wall holds slots");
        wall.search = Some(Search::default());
        wall.show_grid();

        let cell = wall.marked(FRAME).expect("the grid has a focused cell");

        assert_ne!(cell, slot);
        assert!(cell.y < slot.y, "{cell:?} {slot:?}");
    }
}
