//! The `MarginV` of one cue in the display's copy of a text track. A cue
//! of one line ends on the house row, in the lower bar of a film that has
//! one. A cue of two or more lines grows up from that row, and its top line
//! would cross the lower edge of the picture. So the copy gives such a cue
//! a margin of its own. libass reads the margin with the cue, so the cue is
//! in its place from the first frame that draws it. `media-operator` plan
//! 49 derives the rows.

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

/// The height of the picture in rows of a 1080-row screen.
fn picture(screen: &Screen) -> f64 {
    (screen.height - screen.top - screen.bottom) as f64 * SCREEN_ROWS / screen.height as f64
}

/// The rows that a cue of `lines` lines moves down from its resting place
/// on `screen`, where the ink of its last line ends at `ink_end`. It is 0
/// for one line and on a picture with no lower bar. It centres the ink in
/// a bar that can hold it, and otherwise ends the ink `SPACE` above the
/// picture. A cue never moves down onto the picture, so a raise is never
/// positive.
fn moved(screen: &Screen, lines: usize, ink_end: f64) -> f64 {
    if lines < 2 || screen.bottom <= 0 || screen.height <= 0 {
        return 0.0;
    }
    let bar = screen.bottom as f64 * SCREEN_ROWS / screen.height as f64;
    let edge = SCREEN_ROWS - bar;
    let ink = LINE_INK + LINE_PITCH * (lines - 1) as f64;
    match bar >= ink + 2.0 * SPACE {
        true => (edge + SCREEN_ROWS) / 2.0 - (ink_end - ink / 2.0),
        false => (edge - SPACE - ink_end).min(0.0),
    }
}

/// The `MarginV` that places a cue of `lines` lines of a copy that rests
/// as `rest`, or 0, the style's own margin, when the cue stays where a cue
/// of one line ends.
///
/// The margin is `Rest::row` solved for the row where the cue should end.
/// An event's margin of 0 means the style's margin, so a cue that rounds
/// to 0 takes 1 or −1. libass draws a negative margin below the foot of
/// the frame, which a cue in a deep bar needs when the script's margin is
/// large.
pub fn margin(rest: &Rest, screen: &Screen, lines: usize) -> i32 {
    let resting = rest.row(screen, f64::from(rest.margin_v));
    let moved = moved(screen, lines, resting - BOX_BELOW_INK);
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
            assert_eq!(margin(&converted(&screen), &screen, lines), want, "{name}");
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
                margin(&Rest::ass(&ASS, &screen), &screen, lines),
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
        assert_eq!(margin(&converted(&screen), &screen, 2), 6);
    }
}
