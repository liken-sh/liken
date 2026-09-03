// The stripe: one heading over a scrolled row of headshots, each with a
// name and a part under it. A title's page draws one stripe per part at
// its end, and the stripe is built the way the set strip is, so the two
// read as parts of one page.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Color, Point, Rectangle};

use super::{Card, Tone, area, artwork, label, mark, scroll, text, wall};
use crate::look;
use crate::posters::Posters;

/// The height of a headshot. It is smaller than a strip's poster, so
/// the three stripes of a page take less than the episode wall above
/// them.
pub const HEADSHOT: f32 = 195.0;

/// The height the heading, the headshots, and the two caption
/// lines take together, so a page lays its stripes out before it draws.
pub const HEIGHT: f32 = HEADING + HEADSHOT + FOOT + CAPTIONS;

// The height the heading takes over the headshots.
const HEADING: f32 = 46.0;

// The gap between two headshots, wider than the focus mark
// reaches.
const GAP: f32 = 26.0;

// The space between a headshot and the first caption line under
// it.
const FOOT: f32 = 12.0;

// The height of the two caption lines, stated from the leading
// because the height a page reserves is a constant.
const CAPTIONS: f32 = 2.0 * look::FACE * text::LEADING;

/// The width of one headshot: the height at the wall's poster
/// ratio.
pub fn headshot_width() -> f32 {
    HEADSHOT / wall::POSTER
}

/// The distance from one headshot to the next.
pub fn pitch() -> f32 {
    headshot_width() + GAP
}

/// The band the headshots draw in, under the heading, which a
/// page reads to place the row.
pub fn row(region: Rectangle) -> Rectangle {
    area(region.x, region.y + HEADING, region.width, HEADSHOT)
}

/// The headshot of one index, in frame space after the scroll
/// that keeps the focused slot in view.
pub fn slot(region: Rectangle, count: usize, focus: Option<usize>, index: usize) -> Rectangle {
    let row = row(region);
    area(
        region.x + index as f32 * pitch() - offset(region, count, focus),
        row.y,
        headshot_width(),
        HEADSHOT,
    )
}

// How far the row has scrolled: the offset that keeps the focused
// slot in view, and none where nothing holds focus.
fn offset(region: Rectangle, count: usize, focus: Option<usize>) -> f32 {
    scroll::offset(focus.unwrap_or(0), count, pitch(), region.width)
}

/// One stripe to draw: the people, the one that holds focus, the
/// part the heading names, and the region it draws in.
pub struct Stripe<'a, T> {
    /// The people in the order the catalog answered them.
    pub people: &'a [T],
    /// The person that holds focus, or nothing while another row
    /// of the page holds it.
    pub focus: Option<usize>,
    /// The part the stripe is of, drawn over the headshots.
    pub heading: &'a str,
    /// The library the art paths resolve against.
    pub library: &'a str,
    /// The part of the frame the stripe draws in.
    pub region: Rectangle,
}

/// Draw the stripe. Only the headshots inside the region become
/// geometry, so a cast of any length costs one row of slots.
pub fn draw<T: Card, P: Posters>(
    frame: &mut canvas::Frame<Renderer>,
    posters: &mut P,
    stripe: &Stripe<'_, T>,
) {
    frame.fill_text(label(
        stripe.heading,
        Point::new(stripe.region.x, stripe.region.y),
        look::HEADING,
        look::muted(),
        Alignment::Left,
        Vertical::Top,
        stripe.region.width,
    ));

    let count = stripe.people.len();
    let offset = offset(stripe.region, count, stripe.focus);
    let range = scroll::visible(offset, stripe.region.width, pitch(), count, 1);

    for index in range {
        let person = &stripe.people[index];
        let slot = slot(stripe.region, count, stripe.focus, index);
        artwork(
            frame,
            posters,
            stripe.library,
            person.art(),
            slot,
            person.name(),
            Tone::Full,
        );
        if stripe.focus == Some(index) {
            mark(frame, slot);
        }
        let name = captioned(slot);
        written(frame, name, person.name(), look::text());
        written(frame, under(name), person.detail(), look::faint());
    }
}

// The band the first caption line draws in, under the headshot
// and as wide as the pitch, so no name runs under its neighbour's.
fn captioned(slot: Rectangle) -> Rectangle {
    area(
        slot.center_x() - pitch() / 2.0,
        slot.y + slot.height + FOOT,
        pitch(),
        text::height(1, look::FACE),
    )
}

// The band the second caption line draws in, under the first.
fn under(band: Rectangle) -> Rectangle {
    area(band.x, band.y + band.height, band.width, band.height)
}

// One caption line centered in its band and clipped to it, so a
// long name never runs over its neighbour.
fn written(frame: &mut canvas::Frame<Renderer>, band: Rectangle, content: &str, color: Color) {
    if content.is_empty() {
        return;
    }
    let shown = text::cut(content, look::FACE, band.width);
    frame.with_clip(band, |frame| {
        frame.fill_text(label(
            &shown,
            Point::new(band.center_x(), band.y),
            look::FACE,
            color,
            Alignment::Center,
            Vertical::Top,
            f32::INFINITY,
        ));
    });
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::views::{REACH, strip};

    // The region a page gives one stripe on a 1920 screen.
    fn region() -> Rectangle {
        area(120.0, 400.0, 1680.0, HEIGHT)
    }

    #[test]
    fn a_headshot_keeps_the_walls_poster_ratio() {
        assert_eq!(HEADSHOT / headshot_width(), wall::POSTER);
        const { assert!(HEADSHOT < strip::POSTER) };
    }

    #[test]
    fn the_height_is_the_heading_the_headshot_and_two_caption_lines() {
        assert_eq!(
            HEIGHT,
            HEADING + HEADSHOT + FOOT + text::height(2, look::FACE)
        );
    }

    #[test]
    fn the_row_sits_under_the_heading() {
        let row = row(region());
        assert_eq!(row.y, region().y + HEADING);
        assert_eq!(row.height, HEADSHOT);
        assert_eq!(row.width, region().width);
    }

    #[test]
    fn slots_sit_beside_each_other_by_the_pitch() {
        let first = slot(region(), 4, None, 0);
        let second = slot(region(), 4, None, 1);
        assert_eq!(first.x, region().x);
        assert_eq!(second.x, first.x + pitch());
        assert_eq!(second.y, first.y);
        assert_eq!(first.width, headshot_width());
        assert_eq!(first.height, HEADSHOT);
    }

    #[test]
    fn a_stripe_that_fits_the_region_never_scrolls() {
        assert_eq!(slot(region(), 4, Some(3), 0).x, region().x);
    }

    #[test]
    fn a_stripe_longer_than_the_region_scrolls_its_focus_into_view() {
        let last = slot(region(), 60, Some(59), 59);
        assert!(last.x + last.width <= region().x + region().width);
        assert!(slot(region(), 60, Some(59), 0).x < region().x);
    }

    #[test]
    fn a_stripe_with_no_focus_starts_at_its_first_slot() {
        assert_eq!(slot(region(), 60, None, 0).x, region().x);
    }

    #[test]
    fn a_headshot_has_room_beside_it_for_the_mark() {
        const { assert!(GAP / 2.0 > REACH) };
    }

    #[test]
    fn the_two_caption_lines_sit_under_the_headshot_in_order() {
        let slot = slot(region(), 4, None, 0);
        let name = captioned(slot);
        let part = under(name);
        assert_eq!(name.width, pitch());
        assert!(name.y > slot.y + slot.height);
        assert_eq!(part.y, name.y + name.height);
        assert_eq!(part.y + part.height, region().y + HEIGHT);
    }
}
