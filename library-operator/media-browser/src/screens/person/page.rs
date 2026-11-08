// The person's page as one canvas: a head of a fixed height at the top,
// holding the headshot and the words beside it, the career axis under the
// head, and the row of posters under the axis. The head and the axis
// stand still, because they carry the person and not the work focus is
// on; only the row scrolls.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use super::Person;
use super::axis::draw::{self, Drawn};
use super::row;
use crate::art::Art;
use crate::audience::faces::Faces;
use crate::look;
use crate::views::stack::Stack;
use crate::views::{area, card, extent, screen, text, wall};

// The space over the headshot.
const TOP: f32 = 56.0;

// The height of the headshot, and the space between it and the
// words beside it.
const HEADSHOT: f32 = 240.0;
const BESIDE: f32 = 40.0;

// The space between two blocks of the words beside the headshot.
const GAP: f32 = 12.0;

// The lines the biography is cut to.
const BIOGRAPHY_LINES: usize = 3;

// The `y` of the axis line, and the top of the row of posters under it.
// A page with no axis has its row right under the head.
const LINE: f32 = 457.0;
const ROW: f32 = 528.0;
const ROW_ALONE: f32 = 358.0;

/// The lines under each poster: the card's own two.
pub const LINES: usize = card::LINES;

/// The width of the headshot: the height at the wall's poster
/// ratio.
pub fn headshot_width() -> f32 {
    HEADSHOT / wall::POSTER
}

/// The axis line at these bounds: one pixel tall, across the screen's
/// content region.
pub fn line(bounds: Rectangle) -> Rectangle {
    let content = screen::region(bounds);
    area(content.x, bounds.y + LINE, content.width, 1.0)
}

/// The part of the frame the row of posters draws in: under the axis,
/// or under the head where the page draws no axis. The row is the
/// screen's content region widened by the cell gutter, so the art of
/// the first poster at rest is on the content region's left edge, under
/// the headshot.
pub fn region(bounds: Rectangle, axis: bool) -> Rectangle {
    let top = match axis {
        true => ROW,
        false => ROW_ALONE,
    };
    let grid = wall::columned(screen::region(bounds), wall::COLUMNS);
    area(
        grid.x,
        bounds.y + top,
        grid.width,
        (bounds.height - top).max(0.0),
    )
}

/// The page as one canvas.
pub struct Page<'a, A> {
    /// The person the page is about.
    pub person: &'a Person,
    /// The store the headshot and the posters come from.
    pub store: &'a RefCell<A>,
    /// The faces a film's poster draws along its foot.
    pub faces: &'a Faces,
    /// Whether the page holds focus. The label over the axis shows only
    /// while it does and the axis holds focus.
    pub held: bool,
    /// The second this frame draws, which every glide eases on.
    pub at: f64,
}

impl<A: Art> canvas::Program<Infallible, Theme, Renderer> for Page<'_, A> {
    type State = ();

    fn draw(
        &self,
        _state: &Self::State,
        renderer: &Renderer,
        _theme: &Theme,
        bounds: Rectangle,
        _cursor: mouse::Cursor,
    ) -> Vec<canvas::Geometry<Renderer>> {
        let person = self.person;
        let mut frame = canvas::Frame::new(renderer, bounds.size());
        let store = &mut *self.store.borrow_mut();

        let content = screen::region(bounds);
        let headshot = area(content.x, TOP, headshot_width(), HEADSHOT);
        drawn(&mut frame, store, person, headshot);

        let left = headshot.x + headshot.width + BESIDE;
        let column = content.x + content.width - left;
        let mut words = Stack::new(Point::new(left, TOP), GAP);
        for (content, size, color, cap) in [
            (&person.name, look::TITLE, look::text(), 1),
            (&person.facts, look::FACTS, look::muted(), 1),
            (&person.biography, look::PLOT, look::text(), BIOGRAPHY_LINES),
        ] {
            let taken = text::block(&mut frame, content, words.at(), size, color, column, cap);
            words.add(taken);
        }

        if let Some(axis) = &person.axis {
            draw::draw(
                &mut frame,
                &Drawn {
                    axis,
                    credits: &person.credits,
                    line: line(bounds),
                    window: person.window_at(bounds, self.at),
                    label: person.label_at(bounds, self.at).filter(|_| self.held),
                    accented: person.accented().filter(|_| self.held),
                },
            );
        }

        // The row clips at its own sides, so a poster cut by the edge of
        // the row stops there and does not run into the margin. The clip
        // reaches a pixel past each side, because the renderer drops a
        // line of text whose own clip reaches outside the row's, and a
        // card at rest on the edge reaches outside it by a rounding.
        let region = region(bounds, person.axis.is_some());
        let offset = person.row_at(bounds, self.at);
        let clip = area(region.x - 1.0, region.y, region.width + 2.0, region.height);
        frame.with_clip(clip, |frame| {
            row::draw(frame, store, &person.credits, region, offset, self.faces);
        });

        vec![frame.into_geometry()]
    }
}

// The headshot in its box, and the ground under it until the
// decode lands. The art draws band by band, because the renderer uploads
// a large image on a later frame and this client draws no later frame
// until an event.
fn drawn<A: Art>(
    frame: &mut canvas::Frame<Renderer>,
    store: &mut A,
    person: &Person,
    slot: Rectangle,
) {
    if !person.headshot.is_empty()
        && let Some(art) = store.covered(
            &person.headshot_library,
            &person.headshot,
            slot.width as u32,
            slot.height as u32,
        )
    {
        for (band, handle) in art.bands(slot) {
            frame.draw_image(band, canvas::Image::new(handle));
        }
        return;
    }
    frame.fill_rectangle(slot.position(), extent(slot), look::slot());
}

#[cfg(test)]
mod tests {
    use super::*;

    const WIDTH: f32 = 1920.0;
    const HEIGHT: f32 = 1080.0;

    fn frame() -> Rectangle {
        area(0.0, 0.0, WIDTH, HEIGHT)
    }

    // The foot of the head: the bottom of the headshot.
    fn head() -> f32 {
        TOP + HEADSHOT
    }

    #[test]
    fn the_headshot_keeps_the_walls_poster_ratio() {
        assert_eq!(HEADSHOT / headshot_width(), wall::POSTER);
        assert_eq!(headshot_width(), 160.0);
    }

    #[test]
    fn the_axis_and_the_row_stand_under_the_head() {
        assert!(line(frame()).y > head());
        assert!(region(frame(), true).y > line(frame()).y);
        assert!(region(frame(), false).y > head());
        assert!(region(frame(), false).y < region(frame(), true).y);
    }

    // The first poster of the row at rest and the headshot share one left
    // edge: the left edge of the screen's content region.
    #[test]
    fn the_first_poster_stands_under_the_headshot() {
        let region = region(frame(), true);
        let first = row::slot(region, 0, 0.0);
        let content = screen::region(frame());
        assert!((first.x - content.x).abs() < 0.01, "{first:?}");
        assert_eq!(line(frame()).x, content.x);
    }

    #[test]
    fn both_lines_of_a_card_end_inside_the_frame() {
        for axis in [true, false] {
            let region = region(frame(), axis);
            let cells = row::cells(region);
            let slot = row::slot(region, 0, 0.0);
            let under = wall::under(&cells, slot);
            assert!(under.y + under.height <= HEIGHT, "{under:?}");
        }
    }
}
