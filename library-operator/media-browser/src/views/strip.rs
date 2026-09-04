// The strip: one row of art under a heading, at one height, left to
// right. A poster draws at 2:3 and a still at 16:9 side by side, and
// nothing grows or crops to a ratio it does not have. A "see all" slot
// may end the row. A set strip on a movie page marks the film the page is
// about and dims its siblings. The row scrolls so the focused slot stays
// in view.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Point, Rectangle};

use super::{Card, Tone, area, artwork, label, mark, text, underline, wall};
use crate::look;
use crate::posters::Posters;

/// The height of a poster in the strip.
/// Every slot of a strip is this tall, whatever its ratio.
pub const POSTER: f32 = 270.0;

// The height the heading takes over the posters.
const HEADING: f32 = 46.0;

// The gap between two posters.
const GAP: f32 = 26.0;

// The space between a slot and the caption line under it, wider than
// the mark reaches.
const FOOT: f32 = 12.0;

/// The words on the last slot of a strip that ends in one.
pub const SEE_ALL: &str = "See all";

/// The height a strip takes with this many caption lines under each
/// slot: none on a set strip, one on a home strip of titles, two on the
/// libraries strip.
pub fn height(lines: usize) -> f32 {
    match lines {
        0 => HEADING + POSTER,
        lines => HEADING + POSTER + FOOT + text::height(lines, look::CAPTION),
    }
}

/// The width of one poster: the height at the wall's own ratio.
pub fn poster_width() -> f32 {
    width_at(wall::POSTER)
}

/// The width of a slot at this ratio, so a still is wider than a poster
/// at the same height.
pub fn width_at(ratio: f32) -> f32 {
    POSTER / ratio
}

/// One strip to draw: the members, the one the page is about, the focus,
/// and the region under the buttons.
/// One strip to draw. `current` is the member the page is about, and
/// nothing on a strip that is about no member. `see_all` ends the row
/// with a slot that opens the wall, and `lines` is the caption lines under
/// each slot.
pub struct Strip<'a, T> {
    /// The members in the order the catalog answered them.
    pub members: &'a [T],
    /// The index of the member the page is about. It draws at full
    /// brightness.
    pub current: Option<usize>,
    /// The member that holds focus, or nothing while another row of the
    /// page holds it.
    pub focus: Option<usize>,
    /// The set's own title, drawn over the posters.
    pub heading: &'a str,
    /// The library the art paths resolve against.
    pub library: &'a str,
    pub see_all: bool,
    pub lines: usize,
    /// The part of the frame the strip draws in.
    pub region: Rectangle,
}

/// Where every slot of the strip is before the scroll, as its left edge
/// and its width: each member at its own ratio, then the "see all" slot
/// at the poster's, each one gap after the last.
pub fn placed<T: Card>(members: &[T], see_all: bool) -> Vec<(f32, f32)> {
    let mut slots = Vec::with_capacity(members.len() + 1);
    let mut x = 0.0;
    let widths = members
        .iter()
        .map(|member| width_at(member.ratio()))
        .chain(see_all.then(poster_width));
    for width in widths {
        slots.push((x, width));
        x += width + GAP;
    }
    slots
}

/// How far the row has scrolled: the focused slot centered, clamped so
/// the row never leaves a gap at either end, and none while nothing holds
/// focus or the row fits.
pub fn offset(slots: &[(f32, f32)], focus: Option<usize>, viewport: f32) -> f32 {
    let Some((last, width)) = slots.last() else {
        return 0.0;
    };
    let content = last + width;
    if content <= viewport {
        return 0.0;
    }
    let Some((x, width)) = focus.and_then(|index| slots.get(index)) else {
        return 0.0;
    };
    (x + width / 2.0 - viewport / 2.0).clamp(0.0, content - viewport)
}

/// The slot of one index in frame space, after the scroll.
pub fn slot(strip_region: Rectangle, slots: &[(f32, f32)], offset: f32, index: usize) -> Rectangle {
    let (x, width) = slots[index];
    area(
        strip_region.x + x - offset,
        strip_region.y + HEADING,
        width,
        POSTER,
    )
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

    let slots = placed(strip.members, strip.see_all);
    let offset = offset(&slots, strip.focus.or(strip.current), strip.region.width);
    let right = strip.region.x + strip.region.width;

    for index in 0..slots.len() {
        let slot = slot(strip.region, &slots, offset, index);
        if slot.x + slot.width < strip.region.x || slot.x > right {
            continue;
        }
        let focused = strip.focus == Some(index);
        match strip.members.get(index) {
            Some(member) => {
                artwork(
                    frame,
                    posters,
                    library_of(member, strip.library),
                    member.art(),
                    slot,
                    member.name(),
                    tone(strip, index),
                );
                if strip.lines > 0 {
                    captioned(frame, member, slot, focused, strip.lines);
                }
            }
            None => artwork(frame, posters, "", "", slot, SEE_ALL, Tone::Full),
        }
        if strip.current == Some(index) {
            underline(frame, slot);
        }
        if focused {
            mark(frame, slot);
        }
    }
}

// The caption lines under one slot, in the wall's own words and colors:
// the slot's line muted, the focused slot's facts bright, and the second
// line faint where the strip draws two.
fn captioned<T: Card>(
    frame: &mut canvas::Frame<Renderer>,
    member: &T,
    slot: Rectangle,
    focused: bool,
    lines: usize,
) {
    let band = area(
        slot.center_x() - (slot.width + GAP) / 2.0,
        slot.y + slot.height + FOOT,
        slot.width + GAP,
        text::height(1, look::CAPTION),
    );
    let chars = text::fits(look::CAPTION, band.width);
    let (content, color) = wall::captioned(member, focused, chars);
    wall::written(frame, band, content, color);
    if lines > 1 {
        let under = Rectangle {
            y: band.y + band.height,
            ..band
        };
        wall::written(frame, under, member.under(), look::faint());
    }
}

// The library one slot's art resolves against: the slot's own where it
// names one, and the strip's otherwise, because a home strip spans
// libraries and a set strip does not.
fn library_of<'a, T: Card>(member: &'a T, strip: &'a str) -> &'a str {
    match member.library().is_empty() {
        true => strip,
        false => member.library(),
    }
}

/// How bright one member of the strip draws: the film the page is about
/// and the one that holds focus at full, and every sibling under it.
/// The tone one member draws at. Every member draws at full where the
/// strip is about no member.
pub fn tone<T>(strip: &Strip<'_, T>, index: usize) -> Tone {
    match strip.current {
        Some(current) if current != index && strip.focus != Some(index) => Tone::Dimmed,
        _ => Tone::Full,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    struct Member(f32);

    impl Card for Member {
        fn name(&self) -> &str {
            "Film one"
        }

        fn ratio(&self) -> f32 {
            self.0
        }
    }

    fn mixed() -> [Member; 3] {
        [
            Member(wall::POSTER),
            Member(wall::STILL),
            Member(wall::POSTER),
        ]
    }

    fn strip<'a>(
        members: &'a [Member],
        current: Option<usize>,
        focus: Option<usize>,
    ) -> Strip<'a, Member> {
        Strip {
            members,
            current,
            focus,
            heading: "The Set",
            library: "screening/films",
            see_all: false,
            lines: 1,
            region: area(0.0, 0.0, 1000.0, height(1)),
        }
    }

    #[test]
    fn a_poster_keeps_the_walls_ratio() {
        assert_eq!(POSTER / poster_width(), wall::POSTER);
        assert_eq!(width_at(wall::STILL), POSTER * 16.0 / 9.0);
    }

    #[test]
    fn a_strip_with_captions_is_taller_by_its_lines() {
        assert_eq!(height(0), HEADING + POSTER);
        assert_eq!(
            height(1),
            HEADING + POSTER + FOOT + text::height(1, look::CAPTION)
        );
        assert!(height(2) > height(1));
    }

    #[test]
    fn posters_and_stills_sit_side_by_side_at_one_height() {
        let members = mixed();
        let slots = placed(&members, false);
        assert_eq!(slots.len(), 3);
        assert_eq!(slots[0], (0.0, poster_width()));
        assert_eq!(slots[1].0, poster_width() + GAP);
        assert_eq!(slots[1].1, width_at(wall::STILL));
        assert_eq!(slots[2].0, slots[1].0 + slots[1].1 + GAP);
        let region = area(10.0, 20.0, 1000.0, height(1));
        let poster = slot(region, &slots, 0.0, 0);
        let still = slot(region, &slots, 0.0, 1);
        assert_eq!(poster.height, POSTER);
        assert_eq!(still.height, POSTER);
        assert_eq!(poster.y, 20.0 + HEADING);
        assert_eq!(still.y, poster.y);
        assert_eq!(poster.x, 10.0);
        assert!(still.width > poster.width);
    }

    #[test]
    fn see_all_is_a_last_slot_at_the_posters_ratio() {
        let members = mixed();
        let slots = placed(&members, true);
        assert_eq!(slots.len(), 4);
        assert_eq!(slots[3].1, poster_width());
        assert_eq!(slots[3].0, slots[2].0 + slots[2].1 + GAP);
        assert!(placed::<Member>(&[], true).len() == 1);
        assert!(placed::<Member>(&[], false).is_empty());
    }

    #[test]
    fn a_strip_that_fits_never_scrolls() {
        let members = mixed();
        assert_eq!(offset(&placed(&members, true), Some(3), 2000.0), 0.0);
        assert_eq!(offset(&[], Some(0), 2000.0), 0.0);
    }

    #[test]
    fn a_long_strip_keeps_the_focused_slot_in_view() {
        let members: Vec<Member> = (0..30).map(|_| Member(wall::POSTER)).collect();
        let slots = placed(&members, true);
        let viewport = 1000.0;
        assert_eq!(offset(&slots, None, viewport), 0.0);
        assert_eq!(offset(&slots, Some(0), viewport), 0.0);
        let middle = offset(&slots, Some(15), viewport);
        let (x, width) = slots[15];
        assert!(x - middle >= 0.0);
        assert!(x + width - middle <= viewport);
        let (last, width) = slots[30];
        let end = offset(&slots, Some(30), viewport);
        assert_eq!(end, last + width - viewport);
    }

    #[test]
    fn a_poster_has_room_beside_it_for_the_mark() {
        const { assert!(GAP / 2.0 > super::super::REACH) };
        const { assert!(FOOT > super::super::REACH) };
    }

    #[test]
    fn the_current_film_and_the_focused_one_draw_over_their_siblings() {
        let members = mixed();
        let strip = strip(&members, Some(1), Some(2));
        assert_eq!(tone(&strip, 0), Tone::Dimmed);
        assert_eq!(tone(&strip, 1), Tone::Full);
        assert_eq!(tone(&strip, 2), Tone::Full);
    }

    #[test]
    fn a_strip_about_no_member_draws_every_member_at_full() {
        let members = mixed();
        let strip = strip(&members, None, Some(2));
        assert_eq!(tone(&strip, 0), Tone::Full);
        assert_eq!(tone(&strip, 2), Tone::Full);
    }

    #[test]
    fn a_member_that_names_a_library_resolves_its_art_there() {
        struct Elsewhere;
        impl Card for Elsewhere {
            fn name(&self) -> &str {
                "A Title"
            }
            fn library(&self) -> &str {
                "screening/serials"
            }
        }
        assert_eq!(
            library_of(&Elsewhere, "screening/films"),
            "screening/serials"
        );
        assert_eq!(
            library_of(&Member(1.5), "screening/films"),
            "screening/films"
        );
    }
}
