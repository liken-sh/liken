// The strip: one row of posters under a heading, with one of them marked
// as the item the page is about. The rest draw at a lower opacity, so the
// strip reads as a place inside a set and not as another wall.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle};

use super::{Card, Tone, area, artwork, label, mark, scroll, underline, wall};
use crate::look;
use crate::posters::Posters;

/// The height of a poster in the strip.
pub const POSTER: f32 = 210.0;

/// The height the heading and the posters take together.
pub const HEIGHT: f32 = HEADING + POSTER;

// The height the heading takes over the posters.
const HEADING: f32 = 46.0;

// The gap between two posters.
const GAP: f32 = 26.0;

/// The width of one poster: the height at the wall's own ratio.
pub fn poster_width() -> f32 {
    POSTER / wall::POSTER
}

// The distance from one poster to the next.
fn pitch() -> f32 {
    poster_width() + GAP
}

/// One strip to draw: the members, the one the page is about, the focus,
/// and the region under the buttons.
pub struct Strip<'a, T> {
    /// The members in the order the catalog answered them.
    pub members: &'a [T],
    /// The index of the member the page is about. It draws at full
    /// brightness.
    pub current: usize,
    /// The member that holds focus, or nothing while another row of the
    /// page holds it.
    pub focus: Option<usize>,
    /// The set's own title, drawn over the posters.
    pub heading: &'a str,
    /// The library the art paths resolve against.
    pub library: &'a str,
    /// The part of the frame the strip draws in.
    pub region: Rectangle,
}

/// Draw the strip. Only the posters inside the region become geometry,
/// so a set of any length costs one row of slots.
pub fn draw<T: Card, P: Posters>(
    frame: &mut canvas::Frame<Renderer>,
    posters: &mut P,
    strip: &Strip<'_, T>,
) {
    frame.fill_text(label(
        strip.heading,
        Point::new(strip.region.x, strip.region.y),
        look::HEADING,
        look::muted(),
        Alignment::Left,
        Vertical::Top,
        strip.region.width,
    ));

    let top = strip.region.y + HEADING;
    let held = strip.focus.unwrap_or(strip.current);
    let offset = scroll::offset(held, strip.members.len(), pitch(), strip.region.width);
    let range = scroll::visible(offset, strip.region.width, pitch(), strip.members.len(), 1);

    for index in range {
        let member = &strip.members[index];
        let slot = area(
            strip.region.x + index as f32 * pitch() - offset,
            top,
            poster_width(),
            POSTER,
        );
        artwork(
            frame,
            posters,
            strip.library,
            member.art(),
            slot,
            member.name(),
            tone(strip, index),
        );
        if index == strip.current {
            underline(frame, slot);
        }
        if strip.focus == Some(index) {
            mark(frame, slot);
        }
    }
}

/// How bright one member of the strip draws: the film the page is about
/// and the one that holds focus at full, and every sibling under it.
pub fn tone<T>(strip: &Strip<'_, T>, index: usize) -> Tone {
    match index == strip.current || strip.focus == Some(index) {
        true => Tone::Full,
        false => Tone::Dimmed,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_poster_keeps_the_walls_ratio() {
        assert_eq!(POSTER / poster_width(), wall::POSTER);
    }

    #[test]
    fn the_heading_sits_over_the_posters() {
        assert_eq!(HEIGHT, HEADING + POSTER);
        assert!(pitch() > poster_width());
    }

    #[test]
    fn a_poster_has_room_beside_it_for_the_mark() {
        const { assert!(GAP / 2.0 > super::super::REACH) };
    }

    #[test]
    fn the_current_film_and_the_focused_one_draw_over_their_siblings() {
        struct Member;
        impl Card for Member {
            fn name(&self) -> &str {
                "Film one"
            }
        }
        let members = [Member, Member, Member];
        let strip = Strip {
            members: &members,
            current: 1,
            focus: Some(2),
            heading: "The Set",
            library: "screening/films",
            region: area(0.0, 0.0, 1000.0, HEIGHT),
        };
        assert_eq!(tone(&strip, 0), Tone::Dimmed);
        assert_eq!(tone(&strip, 1), Tone::Full);
        assert_eq!(tone(&strip, 2), Tone::Full);
    }
}
