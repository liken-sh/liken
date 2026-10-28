// The scroll across of every row of a title's page: the set strip, the
// franchise strips, and the stripes of people. A row scrolls to keep its
// anchor in view, and the anchor moves on a press that moves focus into
// the row, along it, or out of it, because a row that loses focus scrolls
// back to the slot it keeps. So every row eases on its own, and a press
// that moves focus from one row to the next eases both of them.

use super::Glide;
use crate::look;

/// One scroll across for each row of a page, in the page's own order of
/// its rows.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Rows(Vec<Glide>);

impl Rows {
    /// How far this row has scrolled at this second, where it comes to
    /// rest at `settled`. A row the page has not followed yet draws at
    /// rest.
    pub fn at(&self, row: usize, settled: f32, clock: f64) -> f32 {
        self.0
            .get(row)
            .map_or(settled, |glide| glide.toward(settled, clock))
    }

    /// Follow the places every row scrolls to, in the page's order: ease
    /// there on the pace of a press, or snap there where nothing pressed.
    /// A row the page did not have before starts at rest where it stands,
    /// because a re-read that adds a row is not a press. The answer is
    /// the second the last row comes to rest.
    pub fn follow(&mut self, settled: &[f32], clock: f64, pace: Option<f64>) -> f64 {
        self.0.truncate(settled.len());
        let held = self.0.len();
        self.0
            .extend(settled[held..].iter().map(|place| Glide::still(*place)));
        settled
            .iter()
            .zip(&mut self.0)
            .map(|(place, glide)| glide.follow(*place, clock, pace, look::SCROLL))
            .fold(f64::NEG_INFINITY, f64::max)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_press_eases_every_row_whose_place_moved() {
        let mut rows = Rows::default();
        rows.follow(&[0.0, 300.0, 50.0], 0.0, None);

        let rests = rows.follow(&[100.0, 0.0, 50.0], 1.0, Some(1.0));

        assert_eq!(rests, 1.0 + look::SCROLL);
        let half = 1.0 + look::SCROLL / 2.0;
        assert_eq!(rows.at(0, 100.0, half), 50.0);
        assert_eq!(rows.at(1, 0.0, half), 150.0);
        assert_eq!(rows.at(2, 50.0, half), 50.0);
        assert_eq!(rows.at(1, 0.0, 1.0 + look::SCROLL), 0.0);
    }

    #[test]
    fn a_row_the_page_did_not_have_draws_at_rest() {
        let mut rows = Rows::default();
        rows.follow(&[10.0], 0.0, None);

        rows.follow(&[10.0, 80.0], 1.0, Some(1.0));

        assert_eq!(rows.at(1, 80.0, 1.0), 80.0);
        assert_eq!(rows.at(5, 33.0, 1.0), 33.0);
    }

    #[test]
    fn a_place_moved_with_no_press_draws_at_once() {
        let mut rows = Rows::default();
        rows.follow(&[0.0, 0.0], 0.0, None);

        let rests = rows.follow(&[200.0], 1.0, None);

        assert_eq!(rests, 0.0);
        assert_eq!(rows.at(0, 200.0, 1.0), 200.0);
        assert_eq!(rows.at(1, 0.0, 1.0), 0.0);
    }
}
