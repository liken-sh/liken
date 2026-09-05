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
use iced_winit::core::{Color, Point, Rectangle};

use super::{Card, Tone, area, artwork, card, clock, label, mark, mosaic, text, underline, wall};
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

/// The words on the last slot of a strip whose read answered more than
/// the strip shows.
pub const SEE_ALL: &str = "See all";

/// The height a strip takes with this many lines under each slot: none on
/// a set strip, and the card's two everywhere else.
pub fn height(lines: usize) -> f32 {
    match lines {
        0 => HEADING + POSTER,
        lines => HEADING + POSTER + FOOT + card::height(lines),
    }
}

// The space the heading's box holds past the width the average advance
// measures, so the mark never cuts the last letter of a name the
// shaper set wider than the estimate.
const SLACK: f32 = 24.0;

/// A heading in two runs: the name, and the dot and the words after it. A
/// heading with no dot is the name alone, and the second run is empty. The two
/// runs draw in two colors, so a person reads the name first and the scope
/// second.
pub fn split(heading: &str) -> (&str, &str) {
    match heading.find(DOT) {
        Some(at) => heading.split_at(at),
        None => (heading, ""),
    }
}

// The words that stand between the name of a strip and the scope after
// it. The library bands join their facts with the same three
// characters.
const DOT: &str = " · ";

// The heading over a strip, in two colors: the name bright, and the dot and
// the words after it muted. The whole heading draws muted, and the name draws
// over it in the bright ink, so the shaper places both runs and no estimate of
// the name's width stands between them. An estimate is what the page has, and
// it is short by a few pixels on a long name, which would close the space
// before the dot.
fn headed(frame: &mut canvas::Frame<Renderer>, region: Rectangle, heading: &str) {
    let (name, _) = split(heading);
    for (content, color) in [(heading, look::muted()), (name, look::text())] {
        if content.is_empty() {
            continue;
        }
        frame.fill_text(label(
            content,
            Point::new(region.x, region.y),
            look::HEADING,
            color,
            Alignment::Left,
            Vertical::Top,
            region.width,
        ));
    }
}

/// The box the heading over a strip draws in, which the mark follows
/// where the heading holds focus. It is as wide as the words, so the
/// mark frames the name and not the row.
pub fn heading_box(region: Rectangle, heading: &str) -> Rectangle {
    area(
        region.x,
        region.y,
        (text::width(heading, look::HEADING) + SLACK).min(region.width),
        text::height(1, look::HEADING),
    )
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

/// The width of the caption band under a slot at this ratio. It is a
/// constant of the strip and not of the frame, so a caption is cut to it
/// once, at the read.
pub fn caption_width(ratio: f32) -> f32 {
    width_at(ratio) + GAP
}

/// The slot that ends a strip and opens what the strip is about: its
/// words, and the art it draws as with the library that art resolves
/// against, both empty where it draws its words alone.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Last<'a> {
    pub words: &'a str,
    pub library: &'a str,
    pub art: &'a str,
}

/// One strip to draw. `current` is the member the page is about, and
/// nothing on a strip that is about no member. `last` is the slot that
/// ends the row and opens what the strip is about, or nothing where the
/// row ends with its members, and `lines` is the caption lines under
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
    pub last: Option<Last<'a>>,
    pub lines: usize,
    /// Whether the heading over the strip holds focus. A strip whose
    /// heading opens a page of its own takes focus there as well as on
    /// its members, and the mark says which.
    pub headed: bool,
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
    headed(frame, strip.region, strip.heading);
    if strip.headed {
        mark(frame, heading_box(strip.region, strip.heading));
    }

    let slots = placed(strip.members, strip.last.is_some());
    let offset = offset(&slots, strip.focus.or(strip.current), strip.region.width);
    let right = strip.region.x + strip.region.width;

    for index in 0..slots.len() {
        let slot = slot(strip.region, &slots, offset, index);
        if slot.x + slot.width < strip.region.x || slot.x > right {
            continue;
        }
        let focused = strip.focus == Some(index);
        match strip.members.get(index) {
            // A shelf draws the mosaic of its own posters, and every other
            // slot draws the one art the member names.
            Some(member) => {
                match member.tiles().is_empty() {
                    true => artwork(
                        frame,
                        posters,
                        library_of(member, strip.library),
                        member.art(),
                        slot,
                        member.name(),
                        tone(strip, index),
                    ),
                    false => mosaic(frame, posters, member.tiles(), slot, tone(strip, index)),
                }
                pilled(frame, member, slot);
                if strip.lines > 0 {
                    card::draw(frame, member, caption_band(slot));
                }
            }
            // The last slot draws its art with its words as the caption,
            // or its words alone in the slot where it has no art.
            None => {
                let Some(last) = strip.last else {
                    continue;
                };
                artwork(
                    frame,
                    posters,
                    last.library,
                    last.art,
                    slot,
                    last.words,
                    Tone::Full,
                );
                if !last.art.is_empty() && strip.lines > 0 {
                    worded(frame, caption_band(slot), last.words);
                }
            }
        }
        if strip.current == Some(index) {
            underline(frame, slot);
        }
        if focused {
            mark(frame, slot);
        }
    }
}

// The pill's inset from the top and left edges of the still.
const PILL_INSET: f32 = 10.0;

// The words on the pill over a show's still, and nothing where the show
// holds one new episode or none, because one new thing is what every
// slot of the strip is.
fn pill_words(new: usize) -> Option<String> {
    (new > 1).then(|| format!("{new} new"))
}

// The band the pill's words draw in: as wide as the shaper sets them, in
// the top-left corner of the art.
fn pill(slot: Rectangle, words: &str) -> Rectangle {
    area(
        slot.x + PILL_INSET,
        slot.y + PILL_INSET,
        text::measured(words, look::CAPTION),
        text::height(1, look::CAPTION),
    )
}

// The pill over the still of a show that holds more than one new
// episode, and nothing over any other slot. The words draw over a halo
// of dark copies, as the clock does, because a layer draws every fill
// under every image, so a plate under the words would never show over
// the still.
fn pilled<T: Card>(frame: &mut canvas::Frame<Renderer>, member: &T, slot: Rectangle) {
    let Some(words) = pill_words(member.new_episodes()) else {
        return;
    };
    let band = pill(slot, &words);
    let at = Point::new(band.x, band.center_y());
    let ink = |point: Point, color: Color| {
        label(
            &words,
            point,
            look::CAPTION,
            color,
            Alignment::Left,
            Vertical::Center,
            band.width + SLACK,
        )
    };
    for point in clock::halo(at) {
        frame.fill_text(ink(point, look::BACKGROUND));
    }
    frame.fill_text(ink(at, look::text()));
}

// The band the first caption line of a slot draws in, a gap wider than
// the slot so a caption may run a little past its edges.
fn caption_band(slot: Rectangle) -> Rectangle {
    area(
        slot.center_x() - (slot.width + GAP) / 2.0,
        slot.y + slot.height + FOOT,
        slot.width + GAP,
        text::height(1, look::CAPTION),
    )
}

// The one muted line under the slot that ends a strip. The shaper cuts it
// to the band, as it cuts the cards beside it, because a word the
// estimate calls short enough can set wider than the band and clip in
// the middle of a letter.
fn worded(frame: &mut canvas::Frame<Renderer>, band: Rectangle, words: &str) {
    let cut = card::cut(words, band.width);
    text::shown(frame, &cut, band, look::CAPTION, look::muted());
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
            last: None,
            lines: 1,
            headed: false,
            region: area(0.0, 0.0, 1000.0, height(1)),
        }
    }

    #[test]
    fn a_heading_splits_into_its_name_and_the_scope_after_the_dot() {
        assert_eq!(
            split("Wizarding World · a 4-film set"),
            ("Wizarding World", " · a 4-film set")
        );
        assert_eq!(
            split("Marvel Cinematic Universe · a franchise of 124 films and series"),
            (
                "Marvel Cinematic Universe",
                " · a franchise of 124 films and series"
            )
        );
        assert_eq!(split("Franchises · 32"), ("Franchises", " · 32"));
        assert_eq!(split("Genres"), ("Genres", ""));
        assert_eq!(split(""), ("", ""));
    }

    #[test]
    fn a_heading_splits_on_its_first_dot_alone() {
        assert_eq!(split("One · Two · Three"), ("One", " · Two · Three"));
    }

    #[test]
    fn the_heading_takes_a_box_as_wide_as_its_words() {
        let region = area(10.0, 20.0, 1000.0, height(1));
        let box_of = heading_box(region, "The Order");
        assert_eq!(box_of.x, 10.0);
        assert_eq!(box_of.y, 20.0);
        assert!(box_of.width < region.width);
        assert!(box_of.width > text::width("The Order", look::HEADING));
        assert_eq!(
            heading_box(area(0.0, 0.0, 40.0, 10.0), "A Long Name").width,
            40.0
        );
        assert_eq!(box_of.height, text::height(1, look::HEADING));
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
    fn a_strips_two_lines_are_the_cards_own() {
        assert_eq!(height(2), HEADING + POSTER + FOOT + card::height(2));
        assert!(height(2) - height(1) < text::height(1, look::CAPTION));
        let members = mixed();
        let slots = placed(&members, false);
        let region = area(0.0, 0.0, 1000.0, height(2));
        let band = caption_band(slot(region, &slots, 0.0, 0));
        assert_eq!(
            band.y + band.height + card::under(band).height,
            region.y + HEADING + POSTER + FOOT + card::height(2)
        );
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
    fn the_caption_band_is_one_slot_and_one_gap_wide() {
        assert_eq!(caption_width(wall::POSTER), poster_width() + GAP);
        assert_eq!(caption_width(wall::STILL), width_at(wall::STILL) + GAP);
        let members = mixed();
        let slots = placed(&members, false);
        let region = area(0.0, 0.0, 1000.0, height(2));
        assert_eq!(
            caption_band(slot(region, &slots, 0.0, 0)).width,
            caption_width(wall::POSTER)
        );
        assert_eq!(
            caption_band(slot(region, &slots, 0.0, 1)).width,
            caption_width(wall::STILL)
        );
    }

    #[test]
    fn a_show_of_more_than_one_new_episode_carries_a_pill_and_no_other_slot_does() {
        assert_eq!(pill_words(2).as_deref(), Some("2 new"));
        assert_eq!(pill_words(12).as_deref(), Some("12 new"));
        assert_eq!(pill_words(1), None);
        assert_eq!(pill_words(0), None);
    }

    #[test]
    fn the_pill_sits_in_the_top_left_corner_of_the_still_it_marks() {
        let slot = area(100.0, 200.0, width_at(wall::STILL), POSTER);
        let pill = pill(slot, "2 new");
        assert_eq!(pill.x, slot.x + PILL_INSET);
        assert_eq!(pill.y, slot.y + PILL_INSET);
        assert_eq!(pill.width, text::measured("2 new", look::CAPTION));
        assert_eq!(pill.height, text::height(1, look::CAPTION));
        assert!(pill.x + pill.width < slot.x + slot.width);
        assert!(pill.y + pill.height < slot.y + slot.height);
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
