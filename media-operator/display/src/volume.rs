//! The volume indicator: a speaker glyph, a short bar, and the number. It
//! comes and goes on a clock of its own, and it draws alone: a level change
//! brings up the indicator and nothing else on the display.
//!
//! The level is the one `media-operator` relays on the `Player`'s volume
//! topic: the level the unit's `Receiver` or `Sink` reports, from 0.0 to 1.0
//! of the device's `spec.volume.max`. The command sidecar reads the topic and
//! sends each live change to the display as a `volume-changed` message, with
//! a word that says whether to draw the row. A `Receiver` that shows its own
//! overlay on the TV gets no row, so the TV shows one indicator. mpv
//! plays at unity, so its own `volume` and `mute` properties say nothing about
//! the room's level, and the display does not read them.

use iced::widget::canvas::Path;
use iced::{Point, Rectangle, Size};

use crate::canvas::{Anchor, Brush, Canvas, Line};
use crate::fade::{Clock, Fade, Hide};
use crate::theme;

/// The row draws in the top-right column, two line pitches under the clock,
/// because the scrubber draws across the low center of the screen.
const ROW_Y: f32 = theme::MARGIN_Y + 2.0 * theme::LINE_PITCH;
/// The number reserves this much width at the right margin, so the bar and the
/// glyph hold their place as the number moves between one and three digits.
const NUM_W: f32 = 84.0;
/// The bar is short because the number beside it carries the reading, and the
/// bar shows the level at a glance.
const BAR_W: f32 = 220.0;
const BAR_H: f32 = 12.0;
const BAR_R: f32 = 3.0;
/// The glyph's box, and the gap between the glyph and the bar.
const GLYPH_W: f32 = 26.0;
const GLYPH_H: f32 = 30.0;
const GLYPH_GAP: f32 = 16.0;

/// The row appears alone over whatever frame is on screen, with no scrim under
/// it, and on a bright frame the glyph and the number would vanish. So the row
/// carries a dark surface of its own, at the scrim's edge alpha, and this is
/// the padding around the three parts.
const PAD_X: f32 = 24.0;
const PAD_Y: f32 = 12.0;
const SURFACE_R: f32 = 14.0;

/// The bar and the glyph center on the middle of the number's line, so the
/// three parts read as one row.
const MID_Y: f32 = ROW_Y + theme::type_scale::SMALL / 2.0;
const BAR_TOP: f32 = MID_Y - BAR_H / 2.0;
const GLYPH_TOP: f32 = MID_Y - GLYPH_H / 2.0;
const SURFACE_Y: f32 = ROW_Y - PAD_Y;
/// The surface covers the three parts and the padding, and the number's line
/// is the tallest of the three.
const SURFACE_H: f32 = theme::type_scale::SMALL + 2.0 * PAD_Y;

/// The speaker is one closed polygon, the driver box and the cone, drawn as a
/// path because the player image carries no icon font.
const SPEAKER: [(f32, f32); 6] = [
    (0.0, 10.0),
    (10.0, 10.0),
    (22.0, 0.0),
    (22.0, 30.0),
    (10.0, 20.0),
    (0.0, 20.0),
];
/// The muted state draws this slash across the speaker, so one element carries
/// both the level and the mute. The slash takes a dark outline because it
/// draws in the speaker's own color, and the two would read as one shape
/// without it.
const SLASH: [(f32, f32); 4] = [(2.0, 24.0), (24.0, 2.0), (24.0, 8.0), (2.0, 30.0)];
const SLASH_BORDER: f32 = 2.0;

/// The three parts hang off the right margin, so the row measures itself when
/// it draws. The canvas width follows the screen, and a row measured at load
/// would hold the margin of another screen.
#[derive(Debug, Clone, Copy, PartialEq)]
struct Columns {
    bar_x: f32,
    glyph_x: f32,
    surface_x: f32,
    surface_w: f32,
}

fn columns(canvas: &Canvas) -> Columns {
    let right = canvas.right();
    let bar_x = right - NUM_W - BAR_W;
    let glyph_x = bar_x - GLYPH_GAP - GLYPH_W;
    let surface_x = glyph_x - PAD_X;
    Columns {
        bar_x,
        glyph_x,
        surface_x,
        surface_w: right + PAD_X - surface_x,
    }
}

/// One polygon in the glyph's own local box, offset to where it stands.
fn polygon(at: Point, points: &[(f32, f32)]) -> Path {
    Path::new(|path| {
        for (index, (x, y)) in points.iter().enumerate() {
            let point = Point::new(at.x + x, at.y + y);
            if index == 0 {
                path.move_to(point);
            } else {
                path.line_to(point);
            }
        }
        path.close();
    })
}

/// Read the level, the muted flag, and whether to draw the row off the words
/// of one `volume-changed` message: the level as a decimal from 0.0 to 1.0,
/// then `yes` or `no` for the mute, then `yes` or `no` for the draw. The
/// sidecar sends `no` for the draw while a `Receiver` shows its own overlay on
/// the TV. A message with no third word draws, the way a sidecar that sends two
/// words asks. A message that carries anything else is none of the sidecar's,
/// and it changes nothing.
pub fn changed(words: &[String]) -> Option<(f64, bool, bool)> {
    let level = words
        .get(1)?
        .parse::<f64>()
        .ok()
        .filter(|level| level.is_finite())?;
    let muted = yes_or_no(words.get(2)?)?;
    let draw = words.get(3).map_or(Some(true), |word| yes_or_no(word))?;
    Some((level, muted, draw))
}

fn yes_or_no(word: &str) -> Option<bool> {
    match word {
        "yes" => Some(true),
        "no" => Some(false),
        _ => None,
    }
}

/// The last level the sidecar sent, and the indicator's own fade. The OSD
/// runs the same clock at the same rates, and neither one reads the other, so
/// a level change shows the level alone and a summoned OSD shows no level.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Volume {
    level: f64,
    muted: bool,
    clock: Clock,
}

impl Volume {
    /// The indicator's own fade, which the frame loop steps while it is
    /// moving.
    pub fn fade(&self) -> &Fade {
        self.clock.fade()
    }

    pub fn fade_mut(&mut self) -> &mut Fade {
        self.clock.fade_mut()
    }

    /// The hide window this state asks for, which the frame loop arms and
    /// cancels because it owns the timer.
    pub fn take_hide(&mut self) -> Hide {
        self.clock.take_hide()
    }

    /// The hide window ran out, so the row leaves on its own fade.
    pub fn hide(&mut self) {
        self.clock.hide();
    }

    /// Record one level and show the row. The command sidecar sends
    /// `volume-changed` for each live message on the volume topic, and not for
    /// the retained value it reads when it first connects. So the indicator
    /// answers a change, and it stays off screen while a pod restores the
    /// level it starts with.
    ///
    /// Each change restarts the wait, so a run of presses holds the indicator
    /// on screen and the fade out starts from the last one.
    pub fn show(&mut self, level: f64, muted: bool) {
        self.level = level;
        self.muted = muted;
        self.clock.show(Hide::Arm);
    }

    /// Record one level and leave the row where it is. The receiver draws
    /// this level on the TV, so the row does not rise for it, and a row that
    /// is already up reads the new level.
    pub fn record(&mut self, level: f64, muted: bool) {
        self.level = level;
        self.muted = muted;
    }

    /// Whether the row is on screen.
    pub fn showing(&self) -> bool {
        self.fade().value() > 0.0
    }

    /// The dark surface the three parts read against.
    fn surface(&self, canvas: &Canvas) -> Rectangle {
        let columns = columns(canvas);
        Rectangle::new(
            Point::new(columns.surface_x, SURFACE_Y),
            Size::new(columns.surface_w, SURFACE_H),
        )
    }

    /// The bar's unplayed track, which the fill draws over.
    fn track(&self, canvas: &Canvas) -> Rectangle {
        Rectangle::new(
            Point::new(columns(canvas).bar_x, BAR_TOP),
            Size::new(BAR_W, BAR_H),
        )
    }

    /// The bar's fill, which is nothing at a level too low to draw a pixel of.
    /// A level above 1.0 fills no further.
    fn fill(&self, canvas: &Canvas) -> Option<Rectangle> {
        let width = BAR_W * self.level.clamp(0.0, 1.0) as f32;
        (width >= canvas.to_canvas(1.0)).then(|| {
            Rectangle::new(
                Point::new(columns(canvas).bar_x, BAR_TOP),
                Size::new(width, BAR_H),
            )
        })
    }

    /// The glyph's own top-left, which both the speaker and the slash draw
    /// from.
    fn glyph_at(&self, canvas: &Canvas) -> Point {
        Point::new(columns(canvas).glyph_x, GLYPH_TOP)
    }

    /// The number, which reads the level as a whole percent.
    fn number(&self, canvas: &Canvas) -> Line {
        let percent = (self.level.clamp(0.0, 1.0) * 100.0).round() as i64;
        Line::new(
            format!("{percent}"),
            Point::new(canvas.right(), ROW_Y),
            Anchor::TopRight,
            theme::type_scale::SMALL,
            theme::color::text(),
        )
    }

    /// Draw the row, or nothing while the indicator is off screen. The glyph
    /// alone carries the muted state, so the bar reads the level in both
    /// states. The row draws at its own fade, and puts back the factor the
    /// caller drew the rest of the frame at.
    pub fn draw(&self, brush: &mut Brush<'_>) {
        if !self.showing() {
            return;
        }
        let canvas = brush.canvas();
        brush.at_fade(self.fade().value(), |brush| {
            brush.rounded(
                self.surface(&canvas),
                SURFACE_R,
                theme::at(theme::color::SHADOW, theme::alpha::SCRIM_EDGE),
            );
            brush.rounded(
                self.track(&canvas),
                BAR_R,
                theme::at(theme::color::track(), theme::alpha::TRACK),
            );
            if let Some(fill) = self.fill(&canvas) {
                brush.rounded(
                    fill,
                    BAR_R,
                    theme::at(theme::color::fill(), theme::alpha::OPAQUE),
                );
            }
            let color = if self.muted {
                theme::color::muted()
            } else {
                theme::color::text()
            };
            let at = self.glyph_at(&canvas);
            brush.shape(
                &polygon(at, &SPEAKER),
                theme::at(color, theme::alpha::OPAQUE),
            );
            if self.muted {
                brush.bordered(
                    &polygon(at, &SLASH),
                    theme::at(color, theme::alpha::OPAQUE),
                    SLASH_BORDER,
                    theme::at(theme::color::SHADOW, theme::alpha::OPAQUE),
                );
            }
            brush.text(self.number(&canvas));
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// One indicator at a level, on screen the way a change puts it there.
    fn shown(level: f64) -> Volume {
        let mut volume = Volume::default();
        volume.show(level, false);
        while volume.fade().running() {
            volume.fade_mut().step();
        }
        volume
    }

    fn words(line: &str) -> Vec<String> {
        line.split(' ').map(String::from).collect()
    }

    fn canvas() -> Canvas {
        Canvas::default()
    }

    /// The message carries the level as a decimal, the flag as `yes` or
    /// `no`, and whether to draw the row as `yes` or `no`. A message with no
    /// third word draws.
    #[test]
    fn a_change_reads_the_level_and_the_flag() {
        for (line, read) in [
            ("volume-changed 0.63 no", Some((0.63, false, true))),
            ("volume-changed 1 yes", Some((1.0, true, true))),
            ("volume-changed 0.000000 no", Some((0.0, false, true))),
            ("volume-changed 0.63 no yes", Some((0.63, false, true))),
            ("volume-changed 0.63 yes no", Some((0.63, true, false))),
        ] {
            assert_eq!(changed(&words(line)), read, "{line}");
        }
    }

    /// A message the sidecar did not write changes nothing.
    #[test]
    fn a_change_that_does_not_parse_is_nothing() {
        for line in [
            "volume-changed",
            "volume-changed 0.5",
            "volume-changed loud no",
            "volume-changed NaN no",
            "volume-changed 0.5 maybe",
            "volume-changed 0.5 no maybe",
        ] {
            assert_eq!(changed(&words(line)), None, "{line}");
        }
    }

    /// A change records the level and the flag and shows the row.
    #[test]
    fn a_change_shows_the_level_it_carries() {
        let mut volume = Volume::default();
        assert!(!volume.showing());

        volume.show(0.4, true);
        assert!(volume.fade().running());
        assert!(volume.muted);
        assert_eq!(volume.number(&canvas()).content, "40");
    }

    /// A level the row does not draw is recorded, and the row stays down.
    #[test]
    fn a_recorded_level_shows_no_row() {
        let mut volume = Volume::default();

        volume.record(0.45, true);

        assert!(!volume.showing());
        assert_eq!(volume.take_hide(), Hide::Keep);
        assert!(volume.muted);
        assert_eq!(volume.number(&canvas()).content, "45");
    }

    /// The row rises on the in rate and leaves on the out rate, the way the
    /// OSD does, and each change restarts the wait.
    #[test]
    fn the_row_rises_and_leaves_on_the_shared_rates() {
        let mut volume = Volume::default();

        volume.show(0.4, false);
        assert_eq!(volume.take_hide(), Hide::Arm);
        let mut ticks = 0;
        while volume.fade().running() {
            volume.fade_mut().step();
            ticks += 1;
        }
        assert_eq!(ticks, 21);
        assert!(volume.showing());

        volume.hide();
        let mut ticks = 0;
        while volume.fade().running() {
            volume.fade_mut().step();
            ticks += 1;
        }
        assert_eq!(ticks, 36);
        assert!(!volume.showing());
    }

    /// A change while the row is leaving reverses the same fade in place.
    #[test]
    fn a_change_during_a_fade_out_reverses_it() {
        let mut volume = shown(0.4);
        volume.hide();
        for _ in 0..18 {
            volume.fade_mut().step();
        }
        let standing = volume.fade().value();
        assert!(standing > 0.0 && standing < 1.0);

        volume.show(0.4, false);
        assert_eq!(volume.take_hide(), Hide::Arm);
        volume.fade_mut().step();
        assert!(volume.fade().value() > standing);
    }

    /// The three parts hang off the right margin, at the columns the design
    /// gives at 1920.
    #[test]
    fn the_row_measures_itself_off_the_right_margin() {
        let columns = columns(&canvas());
        assert_eq!(columns.bar_x, 1520.0);
        assert_eq!(columns.glyph_x, 1478.0);
        assert_eq!(columns.surface_x, 1454.0);
        assert_eq!(columns.surface_w, 394.0);
    }

    /// A wider screen moves the whole row with its own margin.
    #[test]
    fn the_row_follows_the_screens_own_margin() {
        let wide = Canvas::for_output(Size::new(2560.0, 1080.0));
        let volume = shown(0.4);
        assert_eq!(
            volume.surface(&wide).x + volume.surface(&wide).width,
            wide.right() + PAD_X
        );
        assert_eq!(volume.number(&wide).at.x, wide.width() - 96.0);
    }

    #[test]
    fn the_surface_covers_the_three_parts_and_the_padding() {
        let surface = shown(0.4).surface(&canvas());
        assert_eq!(surface.x, 1454.0);
        assert_eq!(surface.y, 170.0);
        assert_eq!(surface.width, 394.0);
        assert_eq!(surface.height, 58.0);
    }

    #[test]
    fn the_bar_centres_on_the_numbers_line() {
        let track = shown(0.4).track(&canvas());
        assert_eq!(track.x, 1520.0);
        assert_eq!(track.y, 193.0);
        assert_eq!(track.width, 220.0);
        assert_eq!(track.height, 12.0);
        assert_eq!(shown(0.4).glyph_at(&canvas()), Point::new(1478.0, 184.0));
    }

    /// The fill runs the share of the bar the level names, and a level above
    /// 1.0 fills no further.
    #[test]
    fn the_fill_runs_the_share_the_level_names() {
        for (level, width) in [(0.5, 110.0), (1.0, 220.0), (1.5, 220.0)] {
            assert_eq!(shown(level).fill(&canvas()).unwrap().width, width);
        }
    }

    /// A level too low to draw a pixel of draws no fill at all.
    #[test]
    fn a_level_with_no_pixel_to_draw_draws_no_fill() {
        assert_eq!(shown(0.0).fill(&canvas()), None);
        assert_eq!(shown(-0.1).fill(&canvas()), None);
        assert!(shown(0.01).fill(&canvas()).is_some());
    }

    /// The number reads the level rounded to a whole percent, at the margin.
    #[test]
    fn the_number_reads_the_level_rounded() {
        for (level, reading) in [
            (0.4, "40"),
            (0.404, "40"),
            (0.406, "41"),
            (1.0, "100"),
            (1.5, "100"),
        ] {
            let number = shown(level).number(&canvas());
            assert_eq!(number.content, reading);
            assert_eq!(number.at, Point::new(1824.0, 182.0));
            assert_eq!(number.anchor, Anchor::TopRight);
            assert_eq!(number.size, theme::type_scale::SMALL);
        }
    }

    /// The bar reads the level in both states, so a mute changes the glyph
    /// alone.
    #[test]
    fn a_mute_changes_the_glyph_and_leaves_the_bar() {
        let mut volume = shown(0.4);
        let fill = volume.fill(&canvas());
        volume.show(0.4, true);
        assert!(volume.muted);
        assert_eq!(volume.fill(&canvas()), fill);
        assert_eq!(volume.number(&canvas()).content, "40");
    }
}
