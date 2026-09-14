// The person's page as one canvas: a head of a fixed height at the
// top, holding the headshot and the words beside it, and under it the
// region the wall of works scrolls in. The head stands still, because it
// carries the person and not the work focus is on.

use std::cell::RefCell;
use std::convert::Infallible;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle, Theme, mouse};

use super::Person;
use crate::art::Art;
use crate::look;
use crate::views::stack::Stack;
use crate::views::{area, card, extent, screen, text, wall};

// The space over the headshot, and the space under it before the
// wall's region.
const TOP: f32 = 56.0;
const FOOT: f32 = 36.0;

// The height of the headshot, and the space between it and the
// words beside it.
const HEADSHOT: f32 = 300.0;
const BESIDE: f32 = 40.0;

// The space between two blocks of the words beside the headshot.
const GAP: f32 = 12.0;

// The lines the biography is cut to.
const BIOGRAPHY_LINES: usize = 4;

// The lines under each work: the card's own two.
const LINES: usize = card::LINES;

/// The width of the headshot: the height at the wall's poster
/// ratio.
pub fn headshot_width() -> f32 {
    HEADSHOT / wall::POSTER
}

/// The height the head takes, whatever the entry holds, so the
/// wall under it starts at the same place on every person.
pub fn head() -> f32 {
    TOP + HEADSHOT + FOOT
}

/// The part of the frame the wall scrolls in: under the head, and under
/// the space that keeps the first row's mark off it. The scroll clamps
/// to this region, so the last row's lines end inside it. The grid is
/// the screen's content region widened by the cell gutter, so the first
/// column's poster is directly under the headshot.
pub fn region(bounds: Rectangle) -> Rectangle {
    let top = (head() + wall::HEAD).min(bounds.height);
    let grid = wall::columned(screen::region(bounds), wall::COLUMNS);
    area(grid.x, bounds.y + top, grid.width, bounds.height - top)
}

/// The page as one canvas.
pub struct Page<'a, A> {
    /// The person the page is about.
    pub person: &'a Person,
    /// The store the headshot and the posters come from.
    pub store: &'a RefCell<A>,
    /// Whether the page holds focus, or the browser's strip over it does.
    pub held: bool,
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
            (&person.dates, look::FACTS, look::muted(), 1),
            (&person.biography, look::PLOT, look::text(), BIOGRAPHY_LINES),
        ] {
            let taken = text::block(&mut frame, content, words.at(), size, color, column, cap);
            words.add(taken);
        }

        let region = region(bounds);
        // The clip reaches up into the space over the first row, so the
        // mark of a focused slot there draws whole and the head stays
        // clear.
        let clip = area(
            region.x,
            region.y - wall::HEAD,
            region.width,
            region.height + wall::HEAD,
        );
        frame.with_clip(clip, |frame| {
            person.works.draw(frame, store, region, self.held, LINES);
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

    #[test]
    fn the_head_takes_the_top_of_the_frame_and_the_wall_takes_the_rest() {
        let region = region(frame());
        assert_eq!(region.y, head() + wall::HEAD);
        assert_eq!(region.y + region.height, HEIGHT);
        assert!(head() < HEIGHT / 2.0, "{}", head());
    }

    // The headshot and the first column of the wall under it share one
    // left edge: the left edge of the screen's content region.
    #[test]
    fn the_first_poster_stands_under_the_headshot() {
        let region = region(frame());
        let cells = wall::lined(region.width, wall::POSTER, wall::COLUMNS, LINES);
        let first = wall::slot(&cells, 0, 0.0, wall::COLUMNS);
        let content = screen::region(frame());

        assert!((region.x + first.x - content.x).abs() < 0.01, "{first:?}");
    }

    #[test]
    fn the_headshot_keeps_the_walls_poster_ratio() {
        assert_eq!(HEADSHOT / headshot_width(), wall::POSTER);
    }

    #[test]
    fn a_row_of_posters_fits_under_the_head() {
        let cells = wall::lined(WIDTH, wall::POSTER, wall::COLUMNS, LINES);
        assert!(cells.height <= region(frame()).height);
    }

    #[test]
    fn the_last_rows_second_line_ends_inside_the_region() {
        let region = region(frame());
        let cells = wall::lined(region.width, wall::POSTER, wall::COLUMNS, LINES);
        let count = 40;
        let last = count - 1;
        let offset = wall::scrolled(last, count, wall::COLUMNS, &cells, region.height);
        let slot = wall::slot(&cells, last, offset, wall::COLUMNS);
        let under = wall::under(&cells, slot);
        assert!(under.y + under.height <= region.height, "{under:?}");
    }
}
