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
use super::row::{self, Row};
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

/// The row of posters at these bounds: under the axis, or under the head
/// where the page draws no axis, across the screen's content region and
/// down to the foot of the frame. The art of the first poster at rest is
/// on the content region's left edge, under the headshot.
pub fn row(bounds: Rectangle, axis: bool) -> Row {
    let top = match axis {
        true => ROW,
        false => ROW_ALONE,
    };
    let content = screen::region(bounds);
    Row::new(area(
        content.x,
        bounds.y + top,
        content.width,
        (bounds.height - top).max(0.0),
    ))
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
        let row = row(bounds, person.axis.is_some());
        let offset = person.row_at(bounds, self.at);
        let region = row.region;
        let clip = area(region.x - 1.0, region.y, region.width + 2.0, region.height);
        frame.with_clip(clip, |frame| {
            row::draw(frame, store, &person.credits, &row, offset, self.faces);
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
        let (with, without) = (row(frame(), true).region, row(frame(), false).region);
        assert!(with.y > line(frame()).y);
        assert!(without.y > head());
        assert!(without.y < with.y);
    }

    // The first poster of the row at rest and the headshot share one left
    // edge: the left edge of the screen's content region.
    #[test]
    fn the_first_poster_stands_under_the_headshot() {
        let first = row::slot(&row(frame(), true), 0, 0.0);
        let content = screen::region(frame());
        assert!((first.x - content.x).abs() < 0.01, "{first:?}");
        assert_eq!(line(frame()).x, content.x);
    }

    // The frames of a 16:9 screen, two ultrawide screens, a 16:10
    // screen, and a 4:3 screen, with and without an axis.
    const FRAMES: [(f32, f32, bool); 10] = [
        (1920.0, 1080.0, true),
        (1920.0, 1080.0, false),
        (2560.0, 1080.0, true),
        (2560.0, 1080.0, false),
        (3440.0, 1440.0, true),
        (5120.0, 1440.0, true),
        (1920.0, 1200.0, true),
        (1920.0, 1200.0, false),
        (1440.0, 1080.0, true),
        (1440.0, 1080.0, false),
    ];

    #[test]
    fn both_lines_of_a_card_end_inside_the_frame() {
        for (width, height, axis) in FRAMES {
            let row = row(area(0.0, 0.0, width, height), axis);
            let slot = row::slot(&row, 0, 0.0);
            let under = wall::under(&row.cells, slot);
            assert!(
                under.y + under.height <= height,
                "{width}x{height} {under:?}"
            );
        }
    }

    // The first and the last poster of the row at rest stand on the two
    // edges of the content region, at every frame.
    #[test]
    fn the_row_at_rest_fills_the_content_region() {
        for (width, height, axis) in FRAMES {
            let bounds = area(0.0, 0.0, width, height);
            let content = screen::region(bounds);
            let row = row(bounds, axis);
            let first = row::slot(&row, 0, 0.0);
            let last = row::slot(&row, row.across() - 1, 0.0);
            assert!(
                (first.x - content.x).abs() < 0.01,
                "{width}x{height} {first:?}"
            );
            let right = content.x + content.width;
            assert!(
                (last.x + last.width - right).abs() < 0.01,
                "{width}x{height} {last:?}"
            );
        }
    }

    // A 16:9 screen holds six posters of the wall's size, with or without
    // an axis. A screen wider than 16:9 holds more posters, as tall as the
    // height under the axis allows, and a screen squarer than 16:9 holds
    // six smaller ones.
    #[test]
    fn the_row_holds_as_many_posters_as_fit_across() {
        let cases = [
            (1920.0, 1080.0, true, 6),
            (1920.0, 1080.0, false, 6),
            (2560.0, 1080.0, true, 8),
            (3440.0, 1440.0, true, 6),
            (3440.0, 1080.0, true, 11),
            (1920.0, 1200.0, true, 6),
            (1440.0, 1080.0, true, 6),
        ];
        for (width, height, axis, across) in cases {
            let row = row(area(0.0, 0.0, width, height), axis);
            assert_eq!(row.across(), across, "{width}x{height} {row:?}");
        }
    }

    #[test]
    fn a_16_9_poster_is_the_size_of_a_wall_poster() {
        for axis in [true, false] {
            let cells = row(frame(), axis).cells;
            assert!((cells.poster_width - 248.55).abs() < 0.05, "{cells:?}");
            assert!((cells.poster_height - 372.82).abs() < 0.05, "{cells:?}");
        }
    }
}
