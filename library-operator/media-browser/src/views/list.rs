// The list: full-width rows with an art thumbnail, a name, and a
// detail line, culled to the viewport the same way the wall is.

use std::cell::RefCell;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle, Size, Theme, mouse};

use super::{artwork, label, scroll};
use crate::levels::Row;
use crate::look;
use crate::posters::Posters;

/// One row's height in logical pixels, sized for a couch.
pub const ROW_HEIGHT: f32 = 132.0;

// The margin at both sides of a row's content.
const PAD: f32 = 32.0;

// The inset of the thumbnail inside its row.
const INSET: f32 = 12.0;

// The stripe that marks the focused row.
const STRIPE: f32 = 6.0;

/// The thumbnail slot of one row, a 2:3 portrait against the left
/// margin.
pub fn thumb(index: usize, offset: f32) -> Rectangle {
    let height = ROW_HEIGHT - 2.0 * INSET;
    Rectangle {
        x: PAD,
        y: index as f32 * ROW_HEIGHT - offset + INSET,
        width: height * 2.0 / 3.0,
        height,
    }
}

/// The list program: the rows, the focus, and the poster store its
/// thumbnails come from.
pub struct List<'a, P> {
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

impl<Message, P: Posters> canvas::Program<Message, Theme, Renderer> for List<'_, P> {
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
        let offset = scroll::offset(self.focus, self.rows.len(), ROW_HEIGHT, bounds.height);
        let range = scroll::visible(offset, bounds.height, ROW_HEIGHT, self.rows.len(), 1);

        let mut posters = self.posters.borrow_mut();
        for index in range.clone() {
            let row = &self.rows[index];
            let top = index as f32 * ROW_HEIGHT - offset;

            if index == self.focus {
                frame.fill_rectangle(
                    Point::new(0.0, top),
                    Size::new(bounds.width, ROW_HEIGHT),
                    look::slot(),
                );
                frame.fill_rectangle(
                    Point::new(0.0, top),
                    Size::new(STRIPE, ROW_HEIGHT),
                    look::accent(),
                );
            }

            let thumb = thumb(index, offset);
            artwork(&mut frame, &mut *posters, self.library, &row.art, thumb, "");

            frame.fill_text(label(
                &row.name,
                Point::new(thumb.x + thumb.width + PAD, top + ROW_HEIGHT / 2.0),
                look::ROW_NAME,
                look::text(),
                Alignment::Left,
                Vertical::Center,
                bounds.width * 0.6,
            ));
            if !row.detail.is_empty() {
                frame.fill_text(label(
                    &row.detail,
                    Point::new(bounds.width - PAD, top + ROW_HEIGHT / 2.0),
                    look::DETAIL,
                    look::muted(),
                    Alignment::Right,
                    Vertical::Center,
                    bounds.width * 0.3,
                ));
            }
        }

        // One row past the viewport is asked for and not drawn, so a
        // scroll's next thumbnail decodes before it appears.
        for index in range.end..(range.end + 1).min(self.rows.len()) {
            let row = &self.rows[index];
            let ahead = thumb(index, offset);
            if !row.art.is_empty() {
                let _ = posters.poster(
                    self.library,
                    &row.art,
                    ahead.width as u32,
                    ahead.height as u32,
                );
            }
        }

        vec![frame.into_geometry()]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_thumbnail_is_a_two_three_portrait_inside_its_row() {
        let thumb = thumb(0, 0.0);
        assert_eq!(thumb.height, ROW_HEIGHT - 24.0);
        assert_eq!(thumb.width, thumb.height * 2.0 / 3.0);
        assert_eq!(thumb.y, 12.0);
    }

    #[test]
    fn the_scroll_lifts_every_thumbnail() {
        assert_eq!(thumb(3, 100.0).y, 3.0 * ROW_HEIGHT - 100.0 + 12.0);
    }
}
