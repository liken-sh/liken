// The liken look in one place. Every view reads its colors and its measures
// from here, so a change to either lands in one file.
//
// The colors are the brand's, through the liken-iced crate, which parses them
// out of liken.css. The one measure below is this screen's own: a size for a
// person reading from a couch, which no stylesheet for a page states.

use iced_winit::core::Color;
use liken_iced::palette;

/// The ground the client fills. It is the clear color of every frame and every
/// capture.
///
/// The ground is black rather than the brand's `--page`, because the browser
/// shares one output with a film. A film's black and the browser's black have
/// to be the same black, or the panel shows a seam where one ends.
pub const BACKGROUND: Color = Color::BLACK;

/// The color of every line of text on the screen, the dark scheme's `--ink`.
pub fn text() -> Color {
    palette::dark().ink
}

/// The color of secondary text, the dark scheme's `--ink-muted`.
pub fn muted() -> Color {
    palette::dark().ink_muted
}

/// The color of the part under a name in a stripe: the muted ink at a
/// lower opacity, so a character's name reads as an aside to the
/// person's.
pub fn faint() -> Color {
    Color { a: 0.72, ..muted() }
}

/// The accent that marks focus, the dark scheme's `--link`.
pub fn accent() -> Color {
    palette::dark().link
}

/// The track the volume row's fill runs over: the light scheme's `--link`,
/// the darkest green of the family, so the fill over it reads at a glance.
pub fn track() -> Color {
    palette::light().link
}

/// The fill of a placeholder art slot and a focused row's ground: the
/// dark scheme's `--page`, slightly lighter than the black ground.
pub fn slot() -> Color {
    palette::dark().page
}

/// The darkest end of the scrim panel, at the left edge where the text
/// column starts.
pub fn shade() -> Color {
    Color::from_rgba(0.0, 0.0, 0.0, 0.94)
}

/// The fill the person picker draws over the whole screen. It is darker
/// than the text panel's shade, because the picker is a modal question
/// and the screen behind it must not compete with it.
pub fn picker_shade() -> Color {
    Color::from_rgba(0.0, 0.0, 0.0, 0.96)
}

/// The opacity art draws at where a screen drew it but the person did not
/// choose it, such as the siblings in a set strip. The dim is the image's
/// own opacity and not a veil over it, because a veil is a fill and a fill
/// draws under every image of its layer.
pub const DIM: f32 = 0.42;

/// The width of the stroke that marks focus, in logical pixels, thick
/// enough to read from a couch and thin enough that the art it frames
/// stays the larger thing.
pub const MARK: f32 = 4.0;

/// The space between the art and the inner edge of the focus stroke, so
/// the stroke frames the art and does not touch it.
pub const MARK_GAP: f32 = 4.0;

/// The color of the focus stroke and of the underline that marks the
/// current member of a strip: the accent, a little translucent, so the
/// art shows through it.
pub fn mark() -> Color {
    Color {
        a: 0.75,
        ..accent()
    }
}

/// The ground under a page's own art, such as the episode wall of a
/// series. It is nearly opaque, so the stills sit on near-black and the
/// backdrop shows through only as a trace.
pub fn ground() -> Color {
    Color::from_rgba(0.0, 0.0, 0.0, 0.9)
}

/// The far end of every gradient over art. It leaves the art as it is.
pub const CLEAR: Color = Color::from_rgba(0.0, 0.0, 0.0, 0.0);

/// The size of the focused title's name under its poster, in logical
/// pixels, large enough to read from a couch.
pub const NAME: f32 = 24.0;

/// The size of the one line under every slot of a wall.
pub const CAPTION: f32 = 19.0;

/// The size of the two lines under a headshot in a stripe. A headshot is
/// narrower than a poster, so its lines are smaller than a wall's caption
/// to fit a full name.
pub const FACE: f32 = 17.0;

/// The size of a page's title, where the item has no logo.
pub const TITLE: f32 = 54.0;

/// The size of a title inside a header of a fixed height, where the item
/// has no logo.
pub const HEAD_TITLE: f32 = 39.0;

/// The size of a page's facts line.
pub const FACTS: f32 = 22.0;

/// The size of a page's tagline.
pub const TAGLINE: f32 = 24.0;

/// The size of a page's plot.
pub const PLOT: f32 = 21.0;

/// The size of the word in a button.
pub const BUTTON: f32 = 22.0;

/// The size of the heading over a strip, over a stripe, and over a season's
/// divider.
pub const HEADING: f32 = 21.0;

/// The size of the word in a control of a wall's band.
pub const CONTROL: f32 = 19.0;

/// The size of a score on the ratings line, a step over the facts line
/// it sits under, so the number reads first and the site's scale after
/// it reads second.
pub const SCORE: f32 = 25.0;

/// The size of the credits and the cast on a page.
pub const CREDITS: f32 = 19.0;

/// The size of the number in the volume row.
pub const ROW_NAME: f32 = 29.0;

/// The size of secondary text: details and placeholder titles.
pub const DETAIL: f32 = 21.0;

/// How long the page takes to leave when a person chooses a title, in
/// seconds. The mark fades in over the same span.
pub const DEPARTURE: f64 = 0.4;

/// How long the page takes to return, in seconds, and the mark to fade
/// out: the same span as the way out, so the two read as one motion.
pub const RETURN: f64 = 0.4;

/// How bright the room is with the lights down, as a share of full. It
/// is dark and not black, so the page under a fading film reads as a
/// room with the lights down and not as a dead screen. Tuned by eye, to
/// an eighth of full. An eighth and its distance from full are both
/// exact in binary, so a level built from either lands on this floor
/// and on full to the last bit.
pub const LIGHTS_FLOOR: f32 = 0.125;

/// How long the room takes to reach the floor, in seconds, from the
/// second the `Player` moves off `Idle`. Tuned by eye against the 600 ms
/// the film's surface fades in over.
pub const LIGHTS_DOWN: f64 = 1.2;

/// How long the room takes to come back to full, in seconds. It is
/// shorter than the way down and shorter than `RETURN`, so the page is
/// most of the way up as the last of the film goes. Tuned by eye.
pub const LIGHTS_UP: f64 = 0.3;

/// How long the leading edge of the focus outline takes to reach the slot
/// focus moved to, in seconds. The leading edge is the one on the side the
/// press points to: the right edge on a right press, the bottom edge on a
/// down press. It is short, because from a couch the eye follows the press
/// to the next slot at once, and an outline that arrives late reads as a
/// slow remote.
pub const LEAD: f64 = 0.13;

/// How long the trailing edge of the focus outline takes to leave the slot
/// focus moved from, in seconds. It is longer than `LEAD`, so for a moment
/// the outline spans both slots and then closes behind the press, and the
/// eye reads which way focus went.
pub const FOLLOW: f64 = 0.24;

/// How long a row or a wall takes to scroll to the place a press moves it
/// to, in seconds. It sits between `LEAD` and `FOLLOW`, so the row lands
/// while the outline closes on the slot and the two read as one motion.
pub const SCROLL: f64 = 0.2;

/// The shortest share of its length a motion runs at while a key is held.
/// A remote repeats a held key faster than `FOLLOW`, so each length then
/// shrinks to the time between two presses and the outline never falls
/// behind the remote. It stops at a third, because a motion shorter than
/// that is a jump of a frame or two.
pub const HELD_FLOOR: f64 = 1.0 / 3.0;

/// How long focus rests after the last motion ends before the outline
/// starts to breathe, in seconds. A person who reads a card for this long
/// has stopped pressing, and the breath shows where focus is without a
/// press.
pub const BREATH_DELAY: f64 = 3.0;

/// How long one breath of the outline takes, in seconds: from the mark's
/// color up to the breath's peak and back. It is slow, so the change reads
/// as light and not as a blink.
pub const BREATH: f64 = 3.6;

/// How often a breath starts while focus rests, in seconds. The outline
/// stays still for the rest of each period, so a resting screen draws
/// frames for about a third of the time.
pub const BREATH_EVERY: f64 = 10.0;

/// How long the outline takes to return from a breath to the mark's color
/// when a press arrives, in seconds. It is short, so the press is what the
/// eye sees and not the end of the breath.
pub const BREATH_RELEASE: f64 = 0.15;

/// How far the breath's peak lifts the accent toward the text color. The
/// full accent is only a little brighter than the mark, which is the accent
/// at 0.75 opacity, so a breath that stopped at the accent would change the
/// stroke too little to see from a couch. Halfway to the text color keeps
/// the hue green and makes the change easy to see.
pub const BREATH_LIFT: f32 = 0.5;

/// The color of the focus outline at the top of a breath: the accent,
/// opaque, lifted toward the text color by `BREATH_LIFT`.
pub fn breath_peak() -> Color {
    mixed(accent(), text(), BREATH_LIFT)
}

/// The color of the focus outline at this point of a breath, from 0 at the
/// mark's color to 1 at the breath's peak. Only the color changes.
pub fn breathed(level: f32) -> Color {
    mixed(mark(), breath_peak(), level.clamp(0.0, 1.0))
}

// The color this share of the way from one color to another, opacity
// included.
fn mixed(from: Color, to: Color, share: f32) -> Color {
    let at = |a: f32, b: f32| a + (b - a) * share;
    Color {
        r: at(from.r, to.r),
        g: at(from.g, to.g),
        b: at(from.b, to.b),
        a: at(from.a, to.a),
    }
}

/// How far into a length of time this many seconds is, from 0 to 1.
/// Every motion of the look runs on a share of its own length, so the
/// frame it draws is a function of the clock and never of the frame
/// before it.
pub fn share(since: f64, length: f64) -> f32 {
    (since / length).clamp(0.0, 1.0) as f32
}

/// The smoothstep a motion of the look eases on, so the thing that moves
/// starts and settles rather than beginning and ending on a hard edge.
pub fn eased(share: f32) -> f32 {
    share * share * (3.0 - 2.0 * share)
}

/// The one family the whole display draws in, and the italic face of that
/// family, which the second caption line of a two-line card draws in. Both
/// come from the brand crate, which carries the files and loads them into
/// the toolkit at startup, so this screen and every other liken display
/// draw the same face.
pub use liken_iced::font::{FAMILY as FONT, ITALIC};

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_ground_is_black() {
        assert_eq!(BACKGROUND, Color::from_rgb(0.0, 0.0, 0.0));
    }

    #[test]
    fn the_text_is_the_brand_ink() {
        assert_eq!(text(), palette::dark().ink);
    }

    #[test]
    fn the_secondary_colors_come_from_the_dark_scheme() {
        assert_eq!(muted(), palette::dark().ink_muted);
        assert_eq!(accent(), palette::dark().link);
        assert_eq!(slot(), palette::dark().page);
    }

    #[test]
    fn the_veil_over_art_is_black_and_only_the_alpha_differs() {
        assert_eq!(shade().r, 0.0);
        assert_eq!(shade().g, 0.0);
        assert_eq!(shade().b, 0.0);
        assert!(shade().a > CLEAR.a);
        assert_eq!(CLEAR.a, 0.0);
    }

    #[test]
    fn the_picker_covers_the_screen_in_a_darker_black_than_the_text_panel() {
        assert_eq!(
            (picker_shade().r, picker_shade().g, picker_shade().b),
            (0.0, 0.0, 0.0)
        );
        assert!(picker_shade().a > shade().a);
    }

    #[test]
    fn the_way_back_takes_as_long_as_the_way_out() {
        const { assert!(RETURN > 0.0) };
        const { assert!(RETURN == DEPARTURE) };
    }

    #[test]
    fn the_lights_stop_short_of_black_and_come_up_faster_than_they_go_down() {
        const { assert!(LIGHTS_FLOOR > 0.0) };
        const { assert!(LIGHTS_FLOOR < 1.0) };
        const { assert!(LIGHTS_UP < LIGHTS_DOWN) };
        const { assert!(LIGHTS_UP < RETURN) };
    }

    #[test]
    fn the_outline_leads_with_one_edge_and_the_row_lands_between_the_two() {
        const { assert!(LEAD < SCROLL) };
        const { assert!(SCROLL < FOLLOW) };
        const { assert!(HELD_FLOOR > 0.0) };
        const { assert!(HELD_FLOOR < 1.0) };
    }

    #[test]
    fn a_breath_ends_before_the_next_one_starts() {
        const { assert!(BREATH < BREATH_EVERY) };
        const { assert!(BREATH_RELEASE < BREATH) };
    }

    #[test]
    fn a_breath_runs_from_the_marks_color_to_its_peak() {
        assert_eq!(breathed(0.0), mark());
        assert_eq!(breathed(1.0), breath_peak());
        assert_eq!(breathed(2.0), breath_peak());
    }

    #[test]
    fn the_peak_is_opaque_and_brighter_than_the_accent_in_every_channel() {
        let (peak, accent) = (breath_peak(), accent());
        assert_eq!(peak.a, 1.0);
        assert!(peak.r > accent.r && peak.g > accent.g && peak.b > accent.b);
    }

    #[test]
    fn a_motion_runs_from_none_of_its_length_to_all_of_it() {
        assert_eq!(share(-1.0, 2.0), 0.0);
        assert_eq!(share(0.5, 2.0), 0.25);
        assert_eq!(share(3.0, 2.0), 1.0);
    }

    #[test]
    fn the_smoothstep_holds_the_ends_and_the_middle_of_a_motion() {
        assert_eq!(eased(0.0), 0.0);
        assert_eq!(eased(0.5), 0.5);
        assert_eq!(eased(1.0), 1.0);
    }

    #[test]
    fn art_the_person_did_not_choose_draws_under_full_brightness() {
        const { assert!(DIM > 0.0) };
        const { assert!(DIM < 1.0) };
    }
}
