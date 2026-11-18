//! The audio delay and subtitle delay controls of the strip. Selecting
//! one opens an adjuster panel. This module draws the panel and handles
//! the remote's keys while it is open.

use iced::{Point, Rectangle, Size};
use serde_json::json;

use crate::canvas::{Anchor, Brush, Line};
use crate::film::Film;
use crate::focus::Action;
use crate::ipc::Command;
use crate::presentation::Presentation;
use crate::theme;

// The adjuster panel: its position and size, and the hint line that
// lists the keys.
const X: f32 = theme::MARGIN_X;
const W: f32 = 720.0;
const PAD: f32 = 24.0;
const H: f32 = 200.0;
const HINT: &str = "Left and right nudge, up resets, down done";

/// One of the two mpv delay properties the adjuster changes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Offset {
    Audio,
    Subtitle,
}

impl Offset {
    pub fn property(self) -> &'static str {
        match self {
            Offset::Audio => "audio-delay",
            Offset::Subtitle => "sub-delay",
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Offset::Audio => "Audio offset",
            Offset::Subtitle => "Subtitle offset",
        }
    }

    /// Whether the strip shows this control. Neither shows for an image.
    /// The subtitle offset also needs at least one subtitle track.
    pub fn available(self, presentation: &Presentation, film: &Film) -> bool {
        if presentation.is_image() {
            return false;
        }
        match self {
            Offset::Audio => true,
            Offset::Subtitle => film.tracks_of("sub").next().is_some(),
        }
    }

    /// The size of one nudge, in seconds. The audio offset corrects the
    /// latency of a receiver or a television, and people hear audio that
    /// is a few tens of milliseconds off, so the audio step is small.
    /// People notice subtitle timing from about 100 ms off, so a smaller
    /// subtitle step would only add key presses.
    fn step(self) -> f64 {
        match self {
            Offset::Audio => 0.05,
            Offset::Subtitle => 0.1,
        }
    }

    /// The largest delay in either direction, in seconds. Receiver latency
    /// stays well under 5 s. A subtitle track made for a different cut of
    /// the video can be several seconds off, so its range is larger.
    fn range(self) -> f64 {
        match self {
            Offset::Audio => 5.0,
            Offset::Subtitle => 10.0,
        }
    }

    /// The delay one step from `at` in `direction` (1.0 or -1.0), clamped
    /// to the range. It rounds to a whole millisecond so that repeated
    /// steps do not accumulate floating-point error.
    fn nudge(self, at: f64, direction: f64) -> f64 {
        let next = ((at + direction * self.step()) * 1000.0).round() / 1000.0;
        next.clamp(-self.range(), self.range())
    }

    /// The current delay in seconds, as mpv last reported it.
    pub fn delay(self, film: &Film) -> f64 {
        match self {
            Offset::Audio => film.audio_delay,
            Offset::Subtitle => film.sub_delay,
        }
    }

    /// The delay in milliseconds, as the strip cell shows it.
    pub fn value(self, film: &Film) -> String {
        milliseconds(self.delay(film))
    }

    /// The mpv commands for one key press while the adjuster is open.
    /// Left and right move the delay one step, and up sets it to zero.
    /// mpv applies a new delay at once, so no key confirms it. Down and
    /// select return no commands, and the strip closes the adjuster
    /// because `holds` returns false for them. `Focus` in `focus.rs`
    /// handles the back key.
    pub fn handle(self, action: Action, film: &Film) -> Vec<Command> {
        let at = self.delay(film);
        let write = |value: f64| {
            vec![vec![
                json!("set_property"),
                json!(self.property()),
                json!(value),
            ]]
        };
        match action {
            Action::Left => write(self.nudge(at, -1.0)),
            Action::Right => write(self.nudge(at, 1.0)),
            Action::Up => write(0.0),
            _ => Vec::new(),
        }
    }

    /// Whether the adjuster stays open after `action`.
    pub fn stays_open(self, action: Action) -> bool {
        !matches!(action, Action::Down | Action::Select)
    }

    /// The panel's rectangle. Its bottom edge is `theme::PANEL_BOTTOM`,
    /// like every other panel of the strip.
    pub fn shape(self) -> Rectangle {
        Rectangle::new(Point::new(X, theme::PANEL_BOTTOM - H), Size::new(W, H))
    }

    /// The panel's text: the label, the delay in seconds, and the hint.
    pub fn lines(self, film: &Film) -> Vec<Line> {
        let top = self.shape().y;
        let muted = |content: String, y: f32| {
            Line::new(
                content,
                Point::new(X + PAD, y),
                Anchor::TopLeft,
                theme::type_scale::SMALL,
                theme::color::muted(),
            )
        };
        vec![
            muted(self.label().to_string(), top + PAD),
            Line::new(
                seconds(self.delay(film)),
                Point::new(X + PAD, top + PAD + 54.0),
                Anchor::TopLeft,
                theme::type_scale::TITLE,
                theme::color::text(),
            ),
            muted(HINT.to_string(), top + H - PAD - 20.0),
        ]
    }

    pub fn draw(self, brush: &mut Brush<'_>, film: &Film) {
        brush.plate(self.shape());
        for line in self.lines(film) {
            brush.text(line);
        }
    }
}

/// A signed delay in seconds, as the panel shows it.
fn seconds(at: f64) -> String {
    if at.abs() < 0.001 {
        return "0.00 s".to_string();
    }
    let sign = if at > 0.0 { "+" } else { "-" };
    format!("{sign}{:.2} s", at.abs())
}

/// A signed delay in milliseconds, as the strip cell shows it.
fn milliseconds(at: f64) -> String {
    let count = (at.abs() * 1000.0 + 0.5).floor() as i64;
    if count == 0 {
        return "0 ms".to_string();
    }
    let sign = if at > 0.0 { "+" } else { "-" };
    format!("{sign}{count} ms")
}

#[cfg(test)]
mod tests {
    use super::*;

    fn film(audio: f64, sub: f64) -> Film {
        let mut film = Film::default();
        film.apply("audio-delay", &json!(audio));
        film.apply("sub-delay", &json!(sub));
        film.apply(
            "track-list",
            &json!([{ "id": 1, "type": "sub", "lang": "eng" }]),
        );
        film
    }

    fn block(text: &str) -> Presentation {
        let mut presentation = Presentation::default();
        presentation.receive(text);
        presentation
    }

    #[test]
    fn each_offset_reads_its_own_delay() {
        let film = film(-0.05, 0.2);
        assert_eq!(Offset::Audio.value(&film), "-50 ms");
        assert_eq!(Offset::Subtitle.value(&film), "+200 ms");
        assert_eq!(Offset::Audio.value(&Film::default()), "0 ms");
    }

    #[test]
    fn a_delay_reads_in_milliseconds_on_the_strip_and_in_seconds_on_the_panel() {
        assert_eq!(milliseconds(0.0), "0 ms");
        assert_eq!(milliseconds(0.0004), "0 ms");
        assert_eq!(milliseconds(0.05), "+50 ms");
        assert_eq!(milliseconds(-0.05), "-50 ms");
        assert_eq!(milliseconds(1.2345), "+1235 ms");

        assert_eq!(seconds(0.0), "0.00 s");
        assert_eq!(seconds(0.0005), "0.00 s");
        assert_eq!(seconds(0.05), "+0.05 s");
        assert_eq!(seconds(-1.234), "-1.23 s");
    }

    fn written(offset: Offset, action: Action, film: &Film) -> f64 {
        offset.handle(action, film)[0][2].as_f64().unwrap()
    }

    #[test]
    fn each_offset_nudges_by_its_own_step_and_holds_inside_its_own_range() {
        let rest = film(0.0, 0.0);
        assert_eq!(
            Offset::Audio.handle(Action::Right, &rest),
            vec![vec![
                json!("set_property"),
                json!("audio-delay"),
                json!(0.05)
            ]]
        );
        assert_eq!(
            Offset::Subtitle.handle(Action::Left, &rest),
            vec![vec![json!("set_property"), json!("sub-delay"), json!(-0.1)]]
        );

        let subtitle_off = film(0.0, 0.2);
        assert_eq!(written(Offset::Subtitle, Action::Right, &subtitle_off), 0.3);

        let audio_far = film(5.0, 0.0);
        assert_eq!(written(Offset::Audio, Action::Right, &audio_far), 5.0);

        let subtitle_past_audio = film(0.0, -5.0);
        assert_eq!(
            written(Offset::Subtitle, Action::Left, &subtitle_past_audio),
            -5.1
        );

        let subtitle_far = film(0.0, -10.0);
        assert_eq!(
            written(Offset::Subtitle, Action::Left, &subtitle_far),
            -10.0
        );
    }

    #[test]
    fn up_resets_the_delay_and_down_and_select_close_the_adjuster() {
        let held = film(0.35, 0.0);
        assert_eq!(
            Offset::Audio.handle(Action::Up, &held),
            vec![vec![
                json!("set_property"),
                json!("audio-delay"),
                json!(0.0)
            ]]
        );
        assert!(Offset::Audio.handle(Action::Down, &held).is_empty());
        assert!(Offset::Audio.handle(Action::Select, &held).is_empty());

        assert!(Offset::Audio.stays_open(Action::Left));
        assert!(Offset::Audio.stays_open(Action::Up));
        assert!(!Offset::Audio.stays_open(Action::Down));
        assert!(!Offset::Audio.stays_open(Action::Select));
    }

    #[test]
    fn the_subtitle_offset_hides_with_no_subtitles() {
        let subtitled = film(0.0, 0.0);
        assert!(Offset::Subtitle.available(&block("{}"), &subtitled));
        assert!(!Offset::Subtitle.available(&block("{}"), &Film::default()));
        assert!(Offset::Audio.available(&block("{}"), &Film::default()));
        assert!(!Offset::Audio.available(&block(r#"{"type":"image"}"#), &subtitled));
        assert!(!Offset::Subtitle.available(&block(r#"{"type":"image"}"#), &subtitled));
    }

    #[test]
    fn the_panel_shows_the_label_the_delay_and_the_hint() {
        let lines = Offset::Audio.lines(&film(-0.05, 0.0));
        assert_eq!(
            Offset::Audio.shape(),
            Rectangle::new(Point::new(96.0, 676.0), Size::new(720.0, 200.0))
        );
        assert_eq!(lines[0].content, "Audio offset");
        assert_eq!(lines[0].at, Point::new(120.0, 700.0));
        assert_eq!(lines[0].size, 34.0);
        assert_eq!(lines[1].content, "-0.05 s");
        assert_eq!(lines[1].at, Point::new(120.0, 754.0));
        assert_eq!(lines[1].size, 64.0);
        assert_eq!(
            lines[2].content,
            "Left and right nudge, up resets, down done"
        );
        assert_eq!(lines[2].at, Point::new(120.0, 832.0));
        assert_eq!(lines[2].size, 34.0);
        assert_eq!(
            Offset::Subtitle.lines(&film(0.0, 0.0))[0].content,
            "Subtitle offset"
        );
    }
}
