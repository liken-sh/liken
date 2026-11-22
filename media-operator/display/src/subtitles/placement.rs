//! The values of the mpv properties that put the last line of the
//! selected subtitle track on the house row at the house size, for each
//! kind of track. `media-operator` plan 46 derives the arithmetic.

use super::measurement::Measurement;
use super::script::Script;
use crate::film::{Screen, Track};

/// The values of `sub-ass-override`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Override {
    /// mpv's default. A text track that is not ASS takes the house style
    /// from mpv's options with it.
    Scale,
    /// mpv draws an ASS script's dialogue in the house style and keeps
    /// each event that the script places itself.
    Force,
}

impl Override {
    pub fn word(self) -> &'static str {
        match self {
            Self::Scale => "scale",
            Self::Force => "force",
        }
    }
}

/// The value of each property that places a track. `rest` is the
/// `sub-pos` while the display draws nothing at the foot of the frame.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Values {
    pub ass_override: Override,
    pub stretch: bool,
    pub scale: f64,
    pub rest: f64,
}

/// mpv's defaults, which are also the values for a plain text track.
pub const PLAIN: Values = Values {
    ass_override: Override::Scale,
    stretch: false,
    scale: 1.0,
    rest: 100.0,
};

/// How `sub-pos` moves a cue, which decides how the display lifts it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Lift {
    /// libass ends a text cue at `sub-pos` percent of the way down to the
    /// row where it ends at 100.
    Share,
    /// mpv moves each bitmap of an image track by `sub-pos` minus 100
    /// percent of the screen's height.
    Rows,
}

/// Why an image track is drawn as the disc placed it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Reason {
    /// The item's presentation has no `image` block for the track.
    Unmeasured,
    /// The screen is not 16:9.
    Screen { width: i64, height: i64 },
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Placement {
    pub values: Values,
    pub lift: Lift,
    /// `None` when the track ends on the house row.
    pub unplaced: Option<Reason>,
}

/// The kind of a track, from the `codec` that mpv reports for it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Format {
    Text,
    Ass,
    Pgs,
    VobSub,
}

impl Format {
    fn of(track: &Track) -> Self {
        match track.codec.as_deref() {
            Some("ass" | "ssa") => Self::Ass,
            Some("hdmv_pgs_subtitle") => Self::Pgs,
            Some("dvd_subtitle") => Self::VobSub,
            _ => Self::Text,
        }
    }
}

pub fn is_ass(track: &Track) -> bool {
    Format::of(track) == Format::Ass
}

/// The row of a 1080-row screen where mpv ends the line box of a plain
/// text cue in the house style. libass draws the box down to `PlayResY`
/// minus `MarginV`, and mpv stores `sub-margin-y=34` as the whole
/// `MarginV` 13 of a `PlayResY` of 288 before it scales it. So the box
/// ends at row 1031.25 on a full-height picture and at row 1032.5 on a
/// 1920x804 film, and the house row is between them.
const HOUSE_ROW: f64 = 1032.0;

/// The rows of the screen that the arithmetic is written for.
const SCREEN_ROWS: f64 = 1080.0;

/// The house line is 45 rows tall on a 1080-row screen, and mpv's integer
/// steps draw a shrunk line one row short, so the shrink aims one row
/// taller.
const HOUSE_LINE: f64 = 46.0;

/// The image arithmetic of plan 46 moves a cue by
/// `1030 × rows − inkBottom + (1 − scale) × lineHeight ÷ 2` canvas rows,
/// where 1030 is the row after the house line's last row, 1029, with the
/// one row that mpv's integer steps remove. `scale × lineHeight ÷ 2` is
/// `HOUSE_LINE ÷ 2 × rows`, so the move is
/// `(1030 − 23) × rows − inkBottom + lineHeight ÷ 2`. The display computes
/// that form, because on a 1080-row canvas it is exact in a float, and a
/// move of an exact half row then rounds the same way every time.
const MOVE_ROWS: f64 = 1030.0 - HOUSE_LINE / 2.0;

/// The shape of the screens that the image arithmetic holds on, and how
/// far from it a screen can be.
const SIXTEEN_BY_NINE: f64 = 16.0 / 9.0;
const SHAPE_TOLERANCE: f64 = 0.01;

/// mpv's range for `sub-pos`. It refuses a value outside it.
const POSITION_RANGE: (f64, f64) = (0.0, 150.0);

/// The placement of `track`, or `None` while a value that it needs has not
/// arrived from mpv.
pub fn place(
    track: &Track,
    script: Option<&Script>,
    screen: Option<&Screen>,
    measurement: Option<Measurement>,
) -> Option<Placement> {
    let plain = |unplaced| Placement {
        values: PLAIN,
        lift: Lift::Share,
        unplaced,
    };
    let format = Format::of(track);
    match format {
        Format::Text => Some(plain(None)),
        Format::Ass => Some(Placement {
            values: Values {
                ass_override: Override::Force,
                rest: ass_rest(script?, screen?),
                ..PLAIN
            },
            lift: Lift::Share,
            unplaced: None,
        }),
        Format::Pgs | Format::VobSub => {
            let Some(measurement) = measurement else {
                return Some(plain(Some(Reason::Unmeasured)));
            };
            let screen = screen?;
            if !sixteen_by_nine(screen) {
                return Some(plain(Some(Reason::Screen {
                    width: screen.width,
                    height: screen.height,
                })));
            }
            let (scale, rest) = image(measurement);
            Some(Placement {
                values: Values {
                    ass_override: Override::Scale,
                    stretch: format == Format::Pgs || fills_width(screen),
                    scale,
                    rest,
                },
                lift: Lift::Rows,
                unplaced: None,
            })
        }
    }
}

/// The resting `sub-pos` of an ASS track. Under `force`, libass measures
/// the style's `MarginV` up from the bottom of the screen, in rows of the
/// script's `PlayResY` scaled to the height of the picture. So the
/// dialogue ends at `bottom`, and `sub-pos` moves it by the share of
/// `bottom` that ends it on the house row. Both rows are in rows of a
/// 1080-row screen, because only their ratio matters.
fn ass_rest(script: &Script, screen: &Screen) -> f64 {
    let picture = (screen.height - screen.top - screen.bottom) as f64 / screen.height as f64;
    let bottom = SCREEN_ROWS
        - f64::from(script.margin_v) * picture * SCREEN_ROWS / f64::from(script.play_res_y);
    position(100.0 * HOUSE_ROW / bottom)
}

/// The `sub-scale` and the resting `sub-pos` of a measured image track.
/// `rows` is the number of canvas rows in one row of a 1080-row screen.
/// mpv moves each bitmap that starts in the lower half of the canvas by
/// `sub-pos` in whole canvas rows, and then shrinks it around its own
/// centre by `sub-scale`, so the move puts back the rows that the shrink
/// takes from the bottom of a one-line cue. The half row keeps a rounding
/// error in mpv's float from moving the cue one row up.
fn image(measurement: Measurement) -> (f64, f64) {
    let canvas = f64::from(measurement.canvas_height);
    let line = f64::from(measurement.line_height);
    let rows = canvas / SCREEN_ROWS;
    let scale = HOUSE_LINE * rows / line;
    let moved = MOVE_ROWS * rows - f64::from(measurement.ink_bottom) + line / 2.0;
    let rest = 100.0 + 100.0 * (moved.round() + 0.5) / canvas;
    ((scale * 10_000.0).round() / 10_000.0, position(rest))
}

/// Clamps a resting `sub-pos` to mpv's range and rounds it to a
/// thousandth, about the precision of the float that mpv stores it in.
fn position(rest: f64) -> f64 {
    (rest.clamp(POSITION_RANGE.0, POSITION_RANGE.1) * 1000.0).round() / 1000.0
}

fn sixteen_by_nine(screen: &Screen) -> bool {
    let shape = screen.width as f64 / screen.height as f64;
    (shape / SIXTEEN_BY_NINE - 1.0).abs() <= SHAPE_TOLERANCE
}

/// `stretch-image-subs-to-screen` maps a VobSub canvas onto the whole
/// screen. A 4:3 title fills the height of the screen and not its width,
/// and there the stretch draws the text 33% too wide, while mpv's own
/// mapping gives the same rows and the right width. An anamorphic DVD is
/// a pixel or two narrower than the screen, so the picture counts as
/// wide enough within the tolerance of the screen's shape.
fn fills_width(screen: &Screen) -> bool {
    let picture = (screen.width - screen.left - screen.right) as f64;
    picture >= screen.width as f64 * (1.0 - SHAPE_TOLERANCE)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn track(codec: &str) -> Track {
        Track {
            id: 3,
            kind: "sub".to_string(),
            selected: true,
            codec: Some(codec.to_string()),
            ..Track::default()
        }
    }

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

    const FULL: Screen = Screen {
        width: 1920,
        height: 1080,
        top: 0,
        bottom: 0,
        left: 0,
        right: 0,
    };

    fn plain(unplaced: Option<Reason>) -> Option<Placement> {
        Some(Placement {
            values: PLAIN,
            lift: Lift::Share,
            unplaced,
        })
    }

    /// A plain text track needs nothing from mpv or the presentation, so
    /// it is placed before anything else arrives.
    #[test]
    fn a_plain_text_track_takes_the_defaults_of_mpv() {
        for codec in ["subrip", "webvtt", "mov_text", "dvb_subtitle"] {
            assert_eq!(
                place(&track(codec), None, None, None),
                plain(None),
                "{codec}"
            );
        }
        assert_eq!(place(&Track::default(), None, None, None), plain(None));
    }

    fn script(play_res_y: u32, margin_v: i32) -> Script {
        Script {
            play_res_y,
            margin_v,
        }
    }

    /// The first two cases are the mock-up's 1920x804 film with a script
    /// at its own height and at twice its height, as `flags.py` computes
    /// them. libass scales `MarginV` by the picture's height, so a script
    /// on a full-height picture moves by the same share.
    #[test]
    fn an_ass_track_rests_where_its_dialogue_ends_on_the_house_row() {
        let cases = [
            (
                "PlayResY 804 on a 1920x804 film",
                script(804, 20),
                letterboxed(804),
                97.358,
            ),
            (
                "PlayResY 1608 on a 1920x804 film",
                script(1608, 20),
                letterboxed(804),
                96.449,
            ),
            (
                "PlayResY 1080 on a 16:9 title",
                script(1080, 20),
                FULL,
                97.358,
            ),
            ("libass's PlayResY of 288", script(288, 10), FULL, 98.993),
            (
                "a margin that would move the cue past 150",
                script(288, 200),
                FULL,
                150.0,
            ),
        ];
        for (name, script, screen, rest) in cases {
            for codec in ["ass", "ssa"] {
                assert_eq!(
                    place(&track(codec), Some(&script), Some(&screen), None),
                    Some(Placement {
                        values: Values {
                            ass_override: Override::Force,
                            stretch: false,
                            scale: 1.0,
                            rest,
                        },
                        lift: Lift::Share,
                        unplaced: None,
                    }),
                    "{name}, codec {codec}"
                );
            }
        }
    }

    /// Placing an ASS track before both values arrive would send a
    /// `sub-pos` for the wrong script or the wrong picture.
    #[test]
    fn an_ass_track_waits_for_its_header_and_the_screen() {
        let ass = track("ass");
        assert_eq!(place(&ass, None, Some(&FULL), None), None);
        assert_eq!(place(&ass, Some(&script(804, 20)), None, None), None);
    }

    fn measured(canvas_height: u32, line_height: u32, ink_bottom: u32) -> Measurement {
        Measurement {
            canvas_height,
            line_height,
            ink_bottom,
        }
    }

    /// The rows are tracks of `final-table.md`, with the `sub-pos` of the
    /// plan's arithmetic. On a 1080-row canvas with an odd line height the
    /// move is a whole row and a half, and the display rounds it up.
    #[test]
    fn a_measured_pgs_track_is_shrunk_and_moved_to_the_house_row() {
        let cases = [
            (measured(1080, 61, 918), 0.7541, 111.157),
            (measured(1080, 62, 928), 0.7419, 110.231),
            (measured(1080, 59, 975), 0.7797, 105.787),
            (measured(1080, 51, 965), 0.902, 106.343),
            (measured(1080, 73, 961), 0.6301, 107.731),
            (measured(1080, 61, 995), 0.7541, 104.028),
            (measured(480, 31, 434), 0.6595, 106.146),
            (measured(480, 35, 429), 0.5841, 107.604),
            (measured(480, 26, 413), 0.7863, 110.104),
        ];
        for (measurement, scale, rest) in cases {
            assert_eq!(
                place(
                    &track("hdmv_pgs_subtitle"),
                    None,
                    Some(&letterboxed(804)),
                    Some(measurement)
                ),
                Some(Placement {
                    values: Values {
                        ass_override: Override::Scale,
                        stretch: true,
                        scale,
                        rest,
                    },
                    lift: Lift::Rows,
                    unplaced: None,
                }),
                "{measurement:?}"
            );
        }
    }

    /// A 4:3 title fills the height of the screen and not its width, and
    /// the stretch would draw its text too wide. An anamorphic DVD is one
    /// pixel narrower than the screen.
    #[test]
    fn a_vobsub_track_is_stretched_only_on_a_picture_as_wide_as_the_screen() {
        let pillarboxed = Screen {
            left: 240,
            right: 240,
            ..FULL
        };
        let anamorphic = Screen { right: 1, ..FULL };
        let cases = [
            ("a 16:9 picture", FULL, true),
            ("a 1920x804 picture", letterboxed(804), true),
            ("an anamorphic DVD", anamorphic, true),
            ("a 4:3 picture", pillarboxed, false),
        ];
        for (name, screen, stretch) in cases {
            let placement = place(
                &track("dvd_subtitle"),
                None,
                Some(&screen),
                Some(measured(480, 31, 434)),
            );
            assert_eq!(
                placement.map(|placement| placement.values),
                Some(Values {
                    ass_override: Override::Scale,
                    stretch,
                    scale: 0.6595,
                    rest: 106.146,
                }),
                "{name}"
            );
        }
    }

    /// Without a measurement there is no row to move the track to, so mpv
    /// draws it as the disc placed it, before the screen is known too.
    #[test]
    fn an_unmeasured_image_track_takes_the_plain_values() {
        for codec in ["hdmv_pgs_subtitle", "dvd_subtitle"] {
            assert_eq!(
                place(&track(codec), None, None, None),
                plain(Some(Reason::Unmeasured)),
                "{codec}"
            );
        }
    }

    /// The arithmetic maps the canvas onto the screen's rows as a 1080-row
    /// screen does, so it holds only on a 16:9 screen.
    #[test]
    fn an_image_track_is_placed_only_on_a_screen_of_16_by_9() {
        let cases = [
            (1920, 1080, true),
            (3840, 2160, true),
            (1280, 720, true),
            (1366, 768, true),
            (1920, 1200, false),
            (1280, 1024, false),
            (2560, 1080, false),
        ];
        for (width, height, placed) in cases {
            let screen = Screen {
                width,
                height,
                ..Screen::default()
            };
            let placement = place(
                &track("hdmv_pgs_subtitle"),
                None,
                Some(&screen),
                Some(measured(1080, 61, 918)),
            );
            let want = match placed {
                true => None,
                false => Some(Reason::Screen { width, height }),
            };
            assert_eq!(
                placement.map(|placement| placement.unplaced),
                Some(want),
                "a {width}x{height} screen"
            );
        }
    }

    /// The screen's shape decides between the measured values and the
    /// plain ones, so a measured track waits for it.
    #[test]
    fn a_measured_image_track_waits_for_the_screen() {
        assert_eq!(
            place(
                &track("hdmv_pgs_subtitle"),
                None,
                None,
                Some(measured(1080, 61, 918))
            ),
            None
        );
    }
}
