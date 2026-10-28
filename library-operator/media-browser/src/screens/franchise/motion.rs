// A franchise's page in motion. The wall of rows scrolls down when focus
// leaves the view, and the scroll eases from where it is drawn to where a
// press puts it. The time labels, the metro strip, the circles of the
// room, and the era headings scroll with the rows, so they ease with
// them. The browser's focus outline travels to the focused row at the
// place the scroll comes to rest, so the wall and the outline land
// together.

use iced_winit::core::Rectangle;

use super::page::placed;
use super::{Franchise, wall};
use crate::look;
use crate::views;

impl Franchise {
    /// The rectangle the focus outline goes to on this page at these
    /// bounds: around the focused row, at the place the scroll comes to
    /// rest. Nothing on a page with no rows.
    pub fn marked(&self, bounds: Rectangle) -> Option<Rectangle> {
        if self.focus >= self.rows.len() {
            return None;
        }
        let placed = placed(self, bounds);
        let row = wall::cell_box(
            placed.lane.cards,
            self.focus,
            &self.headings,
            &placed.tops,
            placed.settled,
        );
        Some(views::marked(row))
    }

    /// Follow the place the wall scrolls to at these bounds: ease there on
    /// the pace of a press, or snap there where nothing pressed. The
    /// answer is the second the scroll comes to rest.
    pub fn follow(&mut self, bounds: Rectangle, at: f64, pace: Option<f64>) -> f64 {
        let settled = placed(self, bounds).settled;
        self.scroll.follow(settled, at, pace, look::SCROLL)
    }
}

#[cfg(test)]
mod tests;
