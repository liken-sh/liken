//! The volume indicator: a speaker glyph, a short bar, and the level as a
//! number.
//!
//! `media-operator` publishes the level that the unit's `Receiver` or `Sink`
//! reports on the `Player`'s volume topic, as a fraction from 0.0 to 1.0 of
//! the device's `spec.volume.max`. The command sidecar reads the topic and
//! sends each live change to the display as a `volume-changed` message. mpv
//! plays at its default volume of 100%, so its `volume` and `mute` properties
//! do not show the room's level, and the display does not read them.

use iced::widget::canvas::Path;
use iced::{Point, Rectangle, Size};

use crate::canvas::{Anchor, Brush, Canvas, Line};
use crate::fade::{Clock, Fade, Hide};
use crate::theme;

/// The row is at the right margin, two line pitches below the clock.
const ROW_Y: f32 = theme::MARGIN_Y + 2.0 * theme::LINE_PITCH;
/// The width reserved for the number at the right margin, so the bar and the
/// glyph do not move when the number has one, two, or three digits.
const NUM_W: f32 = 84.0;
/// The bar is short because the number beside it gives the exact level.
const BAR_W: f32 = 220.0;
const BAR_H: f32 = 12.0;
const BAR_R: f32 = 3.0;
/// The size of the glyph's box, and the gap between the glyph and the bar.
const GLYPH_W: f32 = 26.0;
const GLYPH_H: f32 = 30.0;
const GLYPH_GAP: f32 = 16.0;

/// The row can show with no scrim under it, and on a bright frame the glyph
/// and the number would be hard to read. So the row draws its own dark
/// rectangle, at the alpha of the scrim's edge, with this padding around the
/// glyph, the bar, and the number.
const PAD_X: f32 = 24.0;
const PAD_Y: f32 = 12.0;
const SURFACE_R: f32 = 14.0;

/// The bar and the glyph are centered vertically on the number's line.
const MID_Y: f32 = ROW_Y + theme::type_scale::SMALL / 2.0;
const BAR_TOP: f32 = MID_Y - BAR_H / 2.0;
const GLYPH_TOP: f32 = MID_Y - GLYPH_H / 2.0;
const SURFACE_Y: f32 = ROW_Y - PAD_Y;
/// The number is taller than the glyph and the bar, so its line sets the
/// height of the rectangle.
const SURFACE_H: f32 = theme::type_scale::SMALL + 2.0 * PAD_Y;

/// The speaker glyph, a closed polygon of the box and the cone. It is a path
/// because the player image has no icon font.
const SPEAKER: [(f32, f32); 6] = [
    (0.0, 10.0),
    (10.0, 10.0),
    (22.0, 0.0),
    (22.0, 30.0),
    (10.0, 20.0),
    (0.0, 20.0),
];
/// The slash across the speaker when muted. It is the same color as the
/// speaker, so a dark outline keeps the two from merging into one shape.
const SLASH: [(f32, f32); 4] = [(2.0, 24.0), (24.0, 2.0), (24.0, 8.0), (2.0, 30.0)];
const SLASH_BORDER: f32 = 2.0;

/// The x positions of the row's parts, measured from the right margin. They
/// are computed at each draw because the canvas width depends on the screen.
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

/// A closed path through `points`, which are relative to `at`.
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

/// Parses the words of a `volume-changed` message into the level, the muted
/// flag, and whether to draw the row. The words are the level as a decimal
/// from 0.0 to 1.0, `yes` or `no` for muted, and `yes` or `no` for draw. The
/// sidecar sends `no` for draw while a `Receiver` shows its own overlay on the
/// TV. A message with no draw word draws. Any other malformed message
/// returns `None`.
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

/// The last level the sidecar sent, and the indicator's fade. The OSD has a
/// separate fade at the same rates, so a level change shows only the
/// indicator, and showing the OSD does not show it.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Volume {
    level: f64,
    muted: bool,
    clock: Clock,
}

impl Volume {
    /// The indicator's fade, which the frame loop steps while it runs.
    pub fn fade(&self) -> &Fade {
        self.clock.fade()
    }

    pub fn fade_mut(&mut self) -> &mut Fade {
        self.clock.fade_mut()
    }

    /// Returns what the frame loop should do with the hide timer after the
    /// last change, and resets the request to `Hide::Keep`. The frame loop
    /// owns the timer.
    pub fn take_hide(&mut self) -> Hide {
        self.clock.take_hide()
    }

    /// Starts the fade out, after the hide timer ran out.
    pub fn hide(&mut self) {
        self.clock.hide();
    }

    /// Records the level, shows the row, and restarts the hide timer, so
    /// during a run of presses the row stays up and fades out after the last
    /// one. The command sidecar sends `volume-changed` for each live message
    /// on the volume topic, and not for the retained level that the broker
    /// sends when the sidecar connects. So a connect does not show the row.
    pub fn show(&mut self, level: f64, muted: bool) {
        self.level = level;
        self.muted = muted;
        self.clock.show(Hide::Arm);
    }

    /// Records the level without showing the row, for a level that the
    /// `Receiver` draws on the TV. A row that is already visible shows the
    /// new level.
    pub fn record(&mut self, level: f64, muted: bool) {
        self.level = level;
        self.muted = muted;
    }

    /// Whether the row is visible, including while it fades.
    pub fn showing(&self) -> bool {
        self.fade().value() > 0.0
    }

    fn surface(&self, canvas: &Canvas) -> Rectangle {
        let columns = columns(canvas);
        Rectangle::new(
            Point::new(columns.surface_x, SURFACE_Y),
            Size::new(columns.surface_w, SURFACE_H),
        )
    }

    /// The bar's track at full width, under the fill.
    fn track(&self, canvas: &Canvas) -> Rectangle {
        Rectangle::new(
            Point::new(columns(canvas).bar_x, BAR_TOP),
            Size::new(BAR_W, BAR_H),
        )
    }

    /// The fill of the bar, or `None` when the level is too low to fill one
    /// output pixel. A level above 1.0 fills the whole bar.
    fn fill(&self, canvas: &Canvas) -> Option<Rectangle> {
        let width = BAR_W * self.level.clamp(0.0, 1.0) as f32;
        (width >= canvas.to_canvas(1.0)).then(|| {
            Rectangle::new(
                Point::new(columns(canvas).bar_x, BAR_TOP),
                Size::new(width, BAR_H),
            )
        })
    }

    /// The top-left corner of the glyph's box, the origin of both the speaker
    /// and the slash.
    fn glyph_at(&self, canvas: &Canvas) -> Point {
        Point::new(columns(canvas).glyph_x, GLYPH_TOP)
    }

    /// The level as a whole percent, right-aligned at the margin.
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

    /// Draws the row at its own fade. Muting changes only the glyph, so the
    /// bar shows the level while muted.
    pub fn draw(&self, brush: &mut Brush<'_>) {
        if !self.showing() {
            return;
        }
        let canvas = brush.canvas();
        brush.at_fade(self.fade().value(), |brush| {
            brush.rounded(
                self.surface(&canvas),
                SURFACE_R,
                theme::at(theme::color::SHADOW, theme::alpha::VOLUME_ROW),
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

    /// An indicator that a level change showed, with its fade-in done.
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

    #[test]
    fn a_change_parses_the_level_and_both_flags() {
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

    #[test]
    fn a_malformed_change_returns_none() {
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

    #[test]
    fn a_change_records_the_level_and_shows_the_row() {
        let mut volume = Volume::default();
        assert!(!volume.showing());

        volume.show(0.4, true);
        assert!(volume.fade().running());
        assert!(volume.muted);
        assert_eq!(volume.number(&canvas()).content, "40");
    }

    #[test]
    fn a_recorded_level_shows_no_row() {
        let mut volume = Volume::default();

        volume.record(0.45, true);

        assert!(!volume.showing());
        assert_eq!(volume.take_hide(), Hide::Keep);
        assert!(volume.muted);
        assert_eq!(volume.number(&canvas()).content, "45");
    }

    #[test]
    fn the_row_fades_in_and_out_at_the_shared_rates() {
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

    #[test]
    fn a_change_during_a_fade_out_reverses_it() {
        let mut volume = shown(0.4);
        volume.hide();
        for _ in 0..18 {
            volume.fade_mut().step();
        }
        let partway = volume.fade().value();
        assert!(partway > 0.0 && partway < 1.0);

        volume.show(0.4, false);
        assert_eq!(volume.take_hide(), Hide::Arm);
        volume.fade_mut().step();
        assert!(volume.fade().value() > partway);
    }

    /// The x values that the design gives for a 1920 pixel canvas.
    #[test]
    fn the_row_is_positioned_from_the_right_margin() {
        let columns = columns(&canvas());
        assert_eq!(columns.bar_x, 1520.0);
        assert_eq!(columns.glyph_x, 1478.0);
        assert_eq!(columns.surface_x, 1454.0);
        assert_eq!(columns.surface_w, 394.0);
    }

    #[test]
    fn the_row_moves_to_a_wider_screens_right_margin() {
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

    #[test]
    fn the_fill_width_is_the_level_times_the_bar_width() {
        for (level, width) in [(0.5, 110.0), (1.0, 220.0), (1.5, 220.0)] {
            assert_eq!(shown(level).fill(&canvas()).unwrap().width, width);
        }
    }

    #[test]
    fn a_level_under_one_pixel_draws_no_fill() {
        assert_eq!(shown(0.0).fill(&canvas()), None);
        assert_eq!(shown(-0.1).fill(&canvas()), None);
        assert!(shown(0.01).fill(&canvas()).is_some());
    }

    #[test]
    fn the_number_is_the_level_rounded_to_a_whole_percent() {
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
