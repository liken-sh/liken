//! The `MarginV` of one cue in the display's copy of a text track. A cue
//! of one line ends on the house row, in the lower bar of a film that has
//! one. A cue of two or more lines grows up from that row, and its top line
//! would cross the lower edge of the picture. So the copy gives such a cue
//! a margin of its own. libass reads the margin with the cue, so the cue is
//! in its place from the first frame that draws it. `media-operator` plan
//! 49 derives the rows.
//!
//! The lower bar is the bar that mpv draws below the frame and the black
//! rows inside the frame below its active area, which a film stored with
//! its letterbox bars in the frame has. `media-operator` plan 50 adds the
//! black rows inside the frame.

use super::placement::{SCREEN_ROWS, ass_rest};
use super::script::Script;
use crate::film::Screen;

/// The rows of a 1080-row screen that one line of house text inks, from
/// the top of a capital to the foot of a descender, and the rows from the
/// top of one line to the top of the next. mpv 0.41.0 drew them so in the
/// house style of `pod.go`.
const LINE_INK: f64 = 44.0;
const LINE_PITCH: f64 = 57.0;

/// The rows between the end of a cue's line box and the foot of a
/// descender in its last line.
const BOX_BELOW_INK: f64 = 4.0;

/// The rows of black that the bar must leave above and below the ink of
/// a cue to hold it, and the rows that a raised cue ends above the
/// picture. 12 rows keeps the outline of the top line off the picture,
/// and every film of about 2.31:1 or wider then has a bar that holds two
/// lines. A film of 2.20:1 has a bar of 104 rows, which is less than the
/// 101 rows of ink of two lines.
const SPACE: f64 = 12.0;

/// The number of lines of an event's text: one more than its hard line
/// breaks, `\N`. A soft break, `\n`, breaks a line only in a wrapping
/// style that subtitles do not use, and a line that libass wraps is not
/// counted.
pub fn lines(text: &str) -> usize {
    text.matches("\\N").count() + 1
}

/// How libass places the dialogue of a copy when it is at rest: the
/// script's `PlayResY`, the `MarginV` of its dialogue style, and the
/// `sub-pos` that the display sets for the copy.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Rest {
    pub play_res_y: u32,
    pub margin_v: i32,
    pub sub_pos: f64,
}

impl Rest {
    /// The rest of the copy of an ASS track: its own header, placed with
    /// the `sub-pos` of `ass_rest`, so its dialogue ends on the house row.
    pub fn ass(script: &Script, screen: &Screen) -> Self {
        Self {
            play_res_y: script.play_res_y,
            margin_v: script.margin_v,
            sub_pos: ass_rest(script, screen),
        }
    }

    /// The row where libass ends the line box of a cue with the margin
    /// `margin`. Under `force`, libass measures the margin up from the
    /// foot of the screen in rows of `PlayResY` scaled to the picture, and
    /// `sub-pos` then ends the cue at `sub-pos` percent of that row.
    fn row(&self, screen: &Screen, margin: f64) -> f64 {
        self.sub_pos / 100.0 * (SCREEN_ROWS - margin * picture(screen) / f64::from(self.play_res_y))
    }
}

/// The height of the picture that mpv draws, the whole frame, in rows of a
/// 1080-row screen. libass scales a margin by this height, so it includes
/// the black rows inside the frame.
fn picture(screen: &Screen) -> f64 {
    (screen.height - screen.top - screen.bottom) as f64 * SCREEN_ROWS / screen.height as f64
}

/// The lower bar in rows of a 1080-row screen: the bar that mpv draws below
/// the frame, and `encoded`, the share of the frame's height that is black
/// below its active area, in rows of the frame as mpv draws it.
fn bar(screen: &Screen, encoded: f64) -> f64 {
    screen.bottom as f64 * SCREEN_ROWS / screen.height as f64 + encoded * picture(screen)
}

/// The rows that a cue of `lines` lines moves down from its resting place
/// on `screen`, where the ink of its last line ends at `ink_end`. It is 0
/// for one line and on a picture with no lower bar. It centres the ink in
/// a bar that can hold it, which moves the cue down into the bar, and
/// otherwise ends the ink `SPACE` above the picture. A raise above the
/// picture is never positive, so a cue that already ends higher stays
/// where it is and never moves down onto the picture.
fn moved(screen: &Screen, encoded: f64, lines: usize, ink_end: f64) -> f64 {
    if lines < 2 || screen.height <= 0 {
        return 0.0;
    }
    let bar = bar(screen, encoded);
    if bar <= 0.0 {
        return 0.0;
    }
    let edge = SCREEN_ROWS - bar;
    let ink = LINE_INK + LINE_PITCH * (lines - 1) as f64;
    match bar >= ink + 2.0 * SPACE {
        true => (edge + SCREEN_ROWS) / 2.0 - (ink_end - ink / 2.0),
        false => (edge - SPACE - ink_end).min(0.0),
    }
}

/// The `MarginV` that places a cue of `lines` lines of a copy that rests
/// as `rest` on `screen`, or 0, the style's own margin, when the cue stays
/// where a cue of one line ends. `encoded` is the share of the frame's
/// height that is black below its active area, and 0 for a frame with no
/// measured area.
///
/// The margin is `Rest::row` solved for the row where the cue should end.
/// An event's margin of 0 means the style's margin, so a cue that rounds
/// to 0 takes 1 or −1. libass draws a negative margin below the foot of
/// the frame, which a cue in a deep bar needs when the script's margin is
/// large.
pub fn margin(rest: &Rest, screen: &Screen, encoded: f64, lines: usize) -> i32 {
    let resting = rest.row(screen, f64::from(rest.margin_v));
    let moved = moved(screen, encoded, lines, resting - BOX_BELOW_INK);
    if moved == 0.0 {
        return 0;
    }
    let bottom = (resting + moved) * 100.0 / rest.sub_pos;
    let exact = (SCREEN_ROWS - bottom) * f64::from(rest.play_res_y) / picture(screen);
    match exact.round() as i32 {
        0 if exact < 0.0 => -1,
        0 => 1,
        margin => margin,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A 1920x1080 screen with a picture `picture` rows tall between equal
    /// bars.
    fn letterboxed(picture: i64) -> Screen {
        let bar = (1080 - picture) / 2;
        Screen {
            width: 1920,
            height: 1080,
            top: bar,
            bottom: 1080 - picture - bar,
            left: 0,
            right: 0,
        }
    }

    const ASS: Script = Script {
        play_res_y: 804,
        margin_v: 20,
    };

    #[test]
    fn a_cue_has_one_line_more_than_its_hard_breaks() {
        let cases = [
            ("", 1),
            ("One line", 1),
            ("{\\i1}The first line\\Nand the second{\\i0}", 2),
            ("A soft\\nbreak", 1),
            ("One\\Ntwo\\Nthree", 3),
        ];
        for (text, want) in cases {
            assert_eq!(lines(text), want, "{text:?}");
        }
    }

    /// The rest of a converted copy on `screen`, as `converted` writes it.
    fn converted(screen: &Screen) -> Rest {
        super::super::copy::converted::rest(screen)
    }

    /// The rows are those of plan 49. A bar of 140 or 138 rows holds two
    /// lines, so the cue is centred in it. A bar of 104 rows does not, so
    /// the cue ends 12 rows above the picture. A bar of 21 rows is below a
    /// cue that already ends on the picture, so it keeps the style's
    /// margin.
    #[test]
    fn a_cue_of_several_lines_goes_in_the_bar_or_above_it() {
        let cases = [
            ("two lines on 1920x800", 800, 2, 6),
            ("two lines on 1920x804", 804, 2, 5),
            ("two lines on 1920x872", 872, 2, 37),
            ("two lines on 1920x1038", 1038, 2, 0),
            ("two lines at full height", 1080, 2, 0),
            ("three lines on 1920x800", 800, 3, 53),
            ("one line on 1920x800", 800, 1, 0),
        ];
        for (name, picture, lines, want) in cases {
            let screen = letterboxed(picture);
            assert_eq!(
                margin(&converted(&screen), &screen, 0.0, lines),
                want,
                "{name}"
            );
        }
    }

    #[test]
    fn an_ass_cue_takes_its_margin_from_its_own_header() {
        let cases = [
            ("two lines on 1920x800", 800, 2, -14),
            ("two lines on 1920x872", 872, 2, 81),
            ("one line on 1920x800", 800, 1, 0),
        ];
        for (name, picture, lines, want) in cases {
            let screen = letterboxed(picture);
            assert_eq!(
                margin(&Rest::ass(&ASS, &screen), &screen, 0.0, lines),
                want,
                "{name}"
            );
        }
    }

    /// A screen of `width` by `height` that draws a frame of `frame` rows
    /// between equal bars, at the screen's full width.
    fn drawing(width: i64, height: i64, frame_width: i64, frame: i64) -> Screen {
        let drawn = frame * width / frame_width;
        let bar = (height - drawn) / 2;
        Screen {
            width,
            height,
            top: bar,
            bottom: height - drawn - bar,
            left: 0,
            right: 0,
        }
    }

    /// The share of a frame of `frame` rows that is black below an active
    /// area of `height` rows from row `top`, as `streams` gives it.
    fn below(frame: i64, top: i64, height: i64) -> f64 {
        (frame - top - height) as f64 / frame as f64
    }

    /// The rows of plan 50. The black rows below the active area add to the
    /// bar that mpv draws, so a bar of 132 or more rows holds two lines and
    /// the cue is centred in it. A frame whose area is the whole frame, and
    /// a frame with no area, keep the margins of plan 49.
    #[test]
    fn a_cue_of_two_lines_goes_in_the_black_rows_of_the_frame() {
        let cases = [
            (
                "a 1920x804 area of a 1920x1080 frame on a 1920x1080 screen",
                drawing(1920, 1080, 1920, 1080),
                below(1080, 138, 804),
                2,
                4,
            ),
            (
                "a 1920x804 area of a 1920x1080 frame on a 3840x2160 screen",
                drawing(3840, 2160, 1920, 1080),
                below(1080, 138, 804),
                2,
                4,
            ),
            (
                "a 1920x817 area of a 1920x1080 frame on a 3840x2160 screen",
                drawing(3840, 2160, 1920, 1080),
                below(1080, 131, 817),
                2,
                3,
            ),
            (
                "a 1920x800 area of a 1920x872 frame between bars",
                drawing(1920, 1080, 1920, 872),
                below(872, 36, 800),
                2,
                5,
            ),
            (
                "an area that is the whole frame",
                drawing(1920, 1080, 1920, 1080),
                below(1080, 0, 1080),
                2,
                0,
            ),
            (
                "a frame with no area",
                drawing(1920, 1080, 1920, 1080),
                0.0,
                2,
                0,
            ),
            (
                "one line in the black rows of a frame",
                drawing(3840, 2160, 1920, 1080),
                below(1080, 131, 817),
                1,
                0,
            ),
        ];
        for (name, screen, encoded, lines, want) in cases {
            assert_eq!(
                margin(&converted(&screen), &screen, encoded, lines),
                want,
                "{name}"
            );
        }
    }

    /// mpv scales the text with the height of the screen, so the bar of a
    /// 2160-row screen gives the same margin as on a 1080-row one.
    #[test]
    fn the_rows_scale_with_the_height_of_the_screen() {
        let screen = Screen {
            width: 3840,
            height: 2160,
            top: 280,
            bottom: 280,
            left: 0,
            right: 0,
        };
        assert_eq!(margin(&converted(&screen), &screen, 0.0, 2), 6);
    }
}
