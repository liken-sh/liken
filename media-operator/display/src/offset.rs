//! One delay offset control. The strip carries two, one for the audio delay
//! and one for the subtitle delay, so each offset reads beside the track it
//! shifts.

use iced::{Point, Rectangle, Size};
use serde_json::json;

use crate::canvas::{Anchor, Brush, Line};
use crate::film::Film;
use crate::focus::Action;
use crate::ipc::Command;
use crate::presentation::Presentation;
use crate::theme;

/// The nudge step and the clamp range for a delay, in seconds. One press moves
/// the delay by the step, and the clamp holds it within the range.
const STEP: f64 = 0.05;
const RANGE: f64 = 5.0;

/// The panel the adjuster draws: the label, the delay in large type, and a
/// nudge hint.
const X: f32 = theme::MARGIN_X;
const W: f32 = 720.0;
const PAD: f32 = 24.0;
const H: f32 = 200.0;
const HINT: &str = "left and right nudge, up resets, down done";

/// One offset bound to a property. `track` names the track type the offset
/// needs, when it needs one.
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

    /// The offset shows for a playing video. An offset bound to a track type
    /// also needs a track of that type, so the subtitle offset hides with no
    /// subtitles.
    pub fn available(self, presentation: &Presentation, film: &Film) -> bool {
        if presentation.is_image() {
            return false;
        }
        match self {
            Offset::Audio => true,
            Offset::Subtitle => film.tracks_of("sub").next().is_some(),
        }
    }

    /// The delay as mpv reports it.
    pub fn delay(self, film: &Film) -> f64 {
        match self {
            Offset::Audio => film.audio_delay,
            Offset::Subtitle => film.sub_delay,
        }
    }

    /// The cell's word.
    pub fn value(self, film: &Film) -> String {
        milliseconds(self.delay(film))
    }

    /// left and right nudge the delay by a step, and the nudge applies at
    /// once, so there is nothing to confirm. up resets the delay to zero. down
    /// and select close the adjuster, and back closes it through the router.
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
            Action::Left => write(clamp(at - STEP)),
            Action::Right => write(clamp(at + STEP)),
            Action::Up => write(0.0),
            _ => Vec::new(),
        }
    }

    /// Whether one press leaves the adjuster open.
    pub fn holds(self, action: Action) -> bool {
        !matches!(action, Action::Down | Action::Select)
    }

    /// The panel's own box, which the adjuster grows upward from the baseline
    /// every panel hangs on.
    pub fn shape(self) -> Rectangle {
        Rectangle::new(Point::new(X, theme::PANEL_BOTTOM - H), Size::new(W, H))
    }

    /// The three lines of the panel.
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
        brush.panel(self.shape());
        for line in self.lines(film) {
            brush.text(line);
        }
    }
}

fn clamp(at: f64) -> f64 {
    at.clamp(-RANGE, RANGE)
}

/// Format a delay in seconds with a sign, for the panel.
fn seconds(at: f64) -> String {
    if at.abs() < 0.001 {
        return "0.00 s".to_string();
    }
    let sign = if at > 0.0 { "+" } else { "-" };
    format!("{sign}{:.2} s", at.abs())
}

/// Format a delay in milliseconds, for the strip cell.
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

    #[test]
    fn a_nudge_moves_the_delay_by_one_step_and_holds_inside_the_range() {
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
            vec![vec![
                json!("set_property"),
                json!("sub-delay"),
                json!(-0.05)
            ]]
        );

        let far = film(5.0, -5.0);
        assert_eq!(
            Offset::Audio.handle(Action::Right, &far),
            vec![vec![
                json!("set_property"),
                json!("audio-delay"),
                json!(5.0)
            ]]
        );
        assert_eq!(
            Offset::Subtitle.handle(Action::Left, &far),
            vec![vec![json!("set_property"), json!("sub-delay"), json!(-5.0)]]
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

        assert!(Offset::Audio.holds(Action::Left));
        assert!(Offset::Audio.holds(Action::Up));
        assert!(!Offset::Audio.holds(Action::Down));
        assert!(!Offset::Audio.holds(Action::Select));
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

    /// The panel stands where every panel stands, and its three lines read
    /// down from its own top.
    #[test]
    fn the_panel_holds_the_label_the_delay_and_the_hint() {
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
        assert_eq!(lines[2].content, HINT);
        assert_eq!(lines[2].at, Point::new(120.0, 832.0));
        assert_eq!(lines[2].size, 34.0);
        assert_eq!(
            Offset::Subtitle.lines(&film(0.0, 0.0))[0].content,
            "Subtitle offset"
        );
    }
}
