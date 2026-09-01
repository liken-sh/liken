// The wall: a grid of 2:3 poster slots with the focused one drawn
// larger and named. Only the slots inside the viewport become geometry, so
// a wall of five thousand titles builds a couple of dozen slots a frame.

use std::cell::RefCell;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use super::{artwork, label, scroll};
use crate::levels::Row;
use crate::look;
use crate::posters::Posters;

/// The wall's column count, fixed so focus movement is a function of
/// the index alone and never of the window size.
pub const COLUMNS: usize = 6;

// The poster's share of its cell; the rest is the gutter.
const POSTER_SHARE: f32 = 0.84;

// The band under each row of posters, where the focused title's name
// is drawn.
const BAND: f32 = 56.0;

// How much larger the focused slot draws than its neighbors.
const FOCUS_GROWTH: f32 = 1.12;

/// The wall's cell measures, derived from the viewport width alone.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Cells {
    /// One cell's width, a column of the viewport.
    pub width: f32,
    /// One cell's height, the poster and the name band.
    pub height: f32,
    /// The poster slot's width inside the cell.
    pub poster_width: f32,
    /// The poster slot's height, one and a half times its width, so
    /// the slot is a 2:3 portrait.
    pub poster_height: f32,
}

/// The cell measures for a viewport of this width.
pub fn cells(width: f32) -> Cells {
    let width = width / COLUMNS as f32;
    let poster_width = width * POSTER_SHARE;
    let poster_height = poster_width * 1.5;
    Cells {
        width,
        height: poster_height + BAND,
        poster_width,
        poster_height,
    }
}

/// The poster slot of one index, in viewport space after the scroll.
pub fn slot(cells: &Cells, index: usize, offset: f32) -> Rectangle {
    let column = (index % COLUMNS) as f32;
    let row = (index / COLUMNS) as f32;
    Rectangle {
        x: column * cells.width + (cells.width - cells.poster_width) / 2.0,
        y: row * cells.height - offset,
        width: cells.poster_width,
        height: cells.poster_height,
    }
}

/// The same rectangle grown around its center, for the focused slot.
pub fn grown(slot: Rectangle, growth: f32) -> Rectangle {
    let width = slot.width * growth;
    let height = slot.height * growth;
    Rectangle {
        x: slot.x - (width - slot.width) / 2.0,
        y: slot.y - (height - slot.height) / 2.0,
        width,
        height,
    }
}

/// The wall program: the rows, the focus, and the poster store it
/// draws from.
pub struct Wall<'a, P> {
    /// The rows in draw order.
    pub rows: &'a [Row],
    /// The focused row's index.
    pub focus: usize,
    /// The library the art paths resolve against.
    pub library: &'a str,
    /// The poster store, behind a RefCell because a canvas draws
    /// through a shared reference and the store mutates its cache.
    pub posters: &'a RefCell<P>,
}

impl<Message, P: Posters> canvas::Program<Message, Theme, Renderer> for Wall<'_, P> {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let cells = cells(bounds.width);
        let offset = scroll::offset(
            self.focus / COLUMNS,
            scroll::rows(self.rows.len(), COLUMNS),
            cells.height,
            bounds.height,
        );
        let range = scroll::visible(
            offset,
            bounds.height,
            cells.height,
            self.rows.len(),
            COLUMNS,
        );

        let mut posters = self.posters.borrow_mut();
        for index in range.clone() {
            if index == self.focus {
                continue;
            }
            let row = &self.rows[index];
            artwork(
                &mut frame,
                &mut *posters,
                self.library,
                &row.art,
                slot(&cells, index, offset),
                &row.name,
            );
        }

        // One row past the viewport is asked for and not drawn, so a
        // scroll's next posters decode before they appear.
        for index in range.end..(range.end + COLUMNS).min(self.rows.len()) {
            let row = &self.rows[index];
            let ahead = slot(&cells, index, offset);
            if !row.art.is_empty() {
                let _ = posters.poster(
                    self.library,
                    &row.art,
                    ahead.width as u32,
                    ahead.height as u32,
                );
            }
        }

        // The focused slot draws last so its grown edges cover its
        // neighbors and never the other way around.
        if let Some(row) = self.rows.get(self.focus) {
            let focused = grown(slot(&cells, self.focus, offset), FOCUS_GROWTH);
            artwork(
                &mut frame,
                &mut *posters,
                self.library,
                &row.art,
                focused,
                &row.name,
            );
            frame.stroke_rectangle(
                focused.position(),
                focused.size(),
                canvas::Stroke::default()
                    .with_color(look::accent())
                    .with_width(4.0),
            );
            frame.fill_text(label(
                &row.name,
                Point::new(focused.center_x(), focused.y + focused.height + 10.0),
                look::NAME,
                look::text(),
                Alignment::Center,
                Vertical::Top,
                cells.width * 1.5,
            ));
        }

        vec![frame.into_geometry()]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cells_keep_the_two_three_poster_ratio() {
        let cells = cells(1920.0);
        assert_eq!(cells.width, 320.0);
        assert_eq!(cells.poster_height, cells.poster_width * 1.5);
        assert_eq!(cells.height, cells.poster_height + 56.0);
    }

    #[test]
    fn slots_land_in_their_column_and_row() {
        let cells = cells(1920.0);
        let first = slot(&cells, 0, 0.0);
        assert_eq!(first.y, 0.0);
        let below = slot(&cells, COLUMNS, 0.0);
        assert_eq!(below.x, first.x);
        assert_eq!(below.y, cells.height);
        let beside = slot(&cells, 1, 0.0);
        assert_eq!(beside.x, first.x + cells.width);
    }

    #[test]
    fn the_scroll_lifts_every_slot() {
        let cells = cells(1920.0);
        assert_eq!(slot(&cells, 0, 200.0).y, -200.0);
    }

    #[test]
    fn a_grown_slot_keeps_its_center() {
        let grown = grown(
            Rectangle {
                x: 100.0,
                y: 200.0,
                width: 50.0,
                height: 80.0,
            },
            2.0,
        );
        assert_eq!(grown.center_x(), 125.0);
        assert_eq!(grown.center_y(), 240.0);
        assert_eq!(grown.width, 100.0);
        assert_eq!(grown.height, 160.0);
    }
}
