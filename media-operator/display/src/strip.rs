//! The control strip, the lowest region. It lays the controls out in groups, a
//! heading over each track and its delay offset, moves the horizontal focus
//! across the controls present for this file, and asks the focused control to
//! act on select. A control that opens a chooser captures every press until it
//! closes.

use iced::Point;

use crate::canvas::{Anchor, Brush, Canvas, Line};
use crate::chooser;
use crate::film::Film;
use crate::focus::Action;
use crate::ipc::Command;
use crate::offset::Offset;
use crate::presentation::Presentation;
use crate::theme;
use crate::track::Track;

/// The heading row and the value row, in canvas pixels. The heading sits close
/// above its value, and the pair sits clear below the scrubber's time line.
const Y_HEAD: f32 = 990.0;
const Y_VAL: f32 = 1016.0;
/// Within a group, the offset value sits this far right of the track value.
const MEMBER_W: f32 = 200.0;
/// The pitch between two groups, and the nominal ink width of one group. The
/// strip right-aligns the row on the nominal, so the row holds its place when a
/// value changes width.
const GROUP_PITCH: f32 = 430.0;
const GROUP_INK: f32 = 260.0;

/// One control of the strip.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Control {
    Audio,
    AudioOffset,
    Subtitles,
    SubOffset,
    Video,
}

/// The flat focus order the left and right presses walk.
const ORDER: [Control; 5] = [
    Control::Audio,
    Control::AudioOffset,
    Control::Subtitles,
    Control::SubOffset,
    Control::Video,
];

/// The layout groups, a heading over a track and its offset. The offsets carry
/// no heading of their own, because they read under the group's heading.
const GROUPS: [(&str, &[Control]); 3] = [
    ("audio", &[Control::Audio, Control::AudioOffset]),
    ("subtitles", &[Control::Subtitles, Control::SubOffset]),
    ("video", &[Control::Video]),
];

impl Control {
    fn track(self) -> Option<Track> {
        match self {
            Control::Audio => Some(Track::Audio),
            Control::Subtitles => Some(Track::Subtitles),
            Control::Video => Some(Track::Video),
            _ => None,
        }
    }

    fn offset(self) -> Option<Offset> {
        match self {
            Control::AudioOffset => Some(Offset::Audio),
            Control::SubOffset => Some(Offset::Subtitle),
            _ => None,
        }
    }

    pub fn available(self, presentation: &Presentation, film: &Film) -> bool {
        match (self.track(), self.offset()) {
            (Some(track), _) => track.available(film),
            (_, Some(offset)) => offset.available(presentation, film),
            _ => false,
        }
    }

    pub fn value(self, film: &Film) -> String {
        match (self.track(), self.offset()) {
            (Some(track), _) => track.value(film),
            (_, Some(offset)) => offset.value(film),
            _ => String::new(),
        }
    }
}

/// The chooser one control opened, and the entry it stands on.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct Open {
    control: Control,
    selected: usize,
}

/// Where the horizontal focus stands, and the chooser that captures.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Strip {
    index: usize,
    open: Option<Open>,
}

impl Strip {
    /// A music item shows no strip. An album is one audio stream with nothing
    /// to choose, so the row would only repeat what the frame already says.
    pub fn present(presentation: &Presentation, film: &Film) -> Vec<Control> {
        if presentation.is_music() {
            return Vec::new();
        }
        ORDER
            .into_iter()
            .filter(|control| control.available(presentation, film))
            .collect()
    }

    pub fn available(presentation: &Presentation, film: &Film) -> bool {
        !Self::present(presentation, film).is_empty()
    }

    /// The control whose chooser captures every press, if one does.
    pub fn capturing(&self) -> Option<Control> {
        self.open.map(|open| open.control)
    }

    pub fn close(&mut self) {
        self.open = None;
    }

    /// left and right walk the controls present for this file, and select asks
    /// the focused one to act.
    pub fn press(
        &mut self,
        action: Action,
        presentation: &Presentation,
        film: &Film,
    ) -> Vec<Command> {
        let present = Self::present(presentation, film);
        if present.is_empty() {
            return Vec::new();
        }
        self.index = self.index.min(present.len() - 1);
        match action {
            Action::Left => self.index = self.index.saturating_sub(1),
            Action::Right => self.index = (self.index + 1).min(present.len() - 1),
            Action::Select => {
                let control = present[self.index];
                self.open = Some(Open {
                    control,
                    selected: control.track().map_or(0, |track| track.opens_on(film)),
                });
            }
            _ => {}
        }
        Vec::new()
    }

    /// A chooser is a vertical list, so it moves on up and down, applies on
    /// select, and ignores left and right. The delay adjuster uses left and
    /// right to nudge a delay.
    pub fn handle(&mut self, action: Action, film: &Film) -> Vec<Command> {
        let Some(open) = self.open.as_mut() else {
            return Vec::new();
        };
        if let Some(track) = open.control.track() {
            let entries = track.entries(film).len();
            match action {
                Action::Up => open.selected = open.selected.saturating_sub(1),
                Action::Down => {
                    open.selected = (open.selected + 1).min(entries.saturating_sub(1));
                }
                Action::Select => {
                    let commands = track.apply(film, open.selected);
                    self.close();
                    return commands;
                }
                _ => {}
            }
            return Vec::new();
        }
        let Some(offset) = open.control.offset() else {
            return Vec::new();
        };
        let commands = offset.handle(action, film);
        if !offset.holds(action) {
            self.close();
        }
        commands
    }

    /// The present groups, each with its available members. A group with no
    /// present member is left out, so a file with no subtitles draws no
    /// subtitle group. It reads the present controls the caller already
    /// resolved, so one rebuild asks each control once whether it is there.
    fn present_groups(present: &[Control]) -> Vec<(&'static str, Vec<Control>)> {
        GROUPS
            .into_iter()
            .filter_map(|(heading, members)| {
                let members: Vec<Control> = members
                    .iter()
                    .copied()
                    .filter(|control| present.contains(control))
                    .collect();
                (!members.is_empty()).then_some((heading, members))
            })
            .collect()
    }

    /// Every line of the strip: one heading per group, and one value per
    /// control. Right-align the row of groups, so the last group's nominal ink
    /// ends at the margin and the row holds its right edge as a value changes
    /// width.
    pub fn lines(
        &self,
        canvas: &Canvas,
        presentation: &Presentation,
        film: &Film,
        focused: bool,
    ) -> Vec<Line> {
        let present = Self::present(presentation, film);
        if present.is_empty() {
            return Vec::new();
        }
        let here = focused.then(|| present[self.index.min(present.len() - 1)]);
        let groups = Self::present_groups(&present);
        let start = canvas.right() - GROUP_INK - (groups.len() as f32 - 1.0) * GROUP_PITCH;

        let mut lines = Vec::new();
        for (at, (heading, members)) in groups.into_iter().enumerate() {
            let x = start + at as f32 * GROUP_PITCH;
            lines.push(
                Line::new(
                    heading.to_string(),
                    Point::new(x, Y_HEAD),
                    Anchor::Left,
                    theme::type_scale::TINY,
                    theme::color::muted(),
                )
                .alpha(theme::alpha::SUBDUED),
            );
            for (member, control) in members.into_iter().enumerate() {
                let focused = Some(control) == here;
                lines.push(
                    Line::new(
                        control.value(film),
                        Point::new(x + member as f32 * MEMBER_W, Y_VAL),
                        Anchor::Left,
                        theme::type_scale::TINY,
                        if focused {
                            theme::color::fill()
                        } else {
                            theme::color::text()
                        },
                    )
                    .alpha(if focused {
                        theme::alpha::OPAQUE
                    } else {
                        theme::alpha::SUBDUED
                    }),
                );
            }
        }
        lines
    }

    pub fn draw(
        &self,
        brush: &mut Brush<'_>,
        presentation: &Presentation,
        film: &Film,
        focused: bool,
    ) {
        let canvas = brush.canvas();
        for line in self.lines(&canvas, presentation, film, focused) {
            brush.text(line);
        }
    }

    /// The open chooser, which draws over every region and the dim under it.
    pub fn draw_chooser(&self, brush: &mut Brush<'_>, film: &Film) {
        let Some(open) = self.open else {
            return;
        };
        if let Some(track) = open.control.track() {
            chooser::draw(brush, &track.entries(film), open.selected);
        } else if let Some(offset) = open.control.offset() {
            offset.draw(brush, film);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn film() -> Film {
        let mut film = Film::default();
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "video", "title": "Main", "selected": true },
                { "id": 1, "type": "audio", "lang": "eng", "selected": true },
                { "id": 2, "type": "audio", "title": "Commentary", "lang": "eng" },
                { "id": 1, "type": "sub", "lang": "spa" },
            ]),
        );
        film
    }

    fn block(text: &str) -> Presentation {
        let mut presentation = Presentation::default();
        presentation.receive(text);
        presentation
    }

    fn shown(strip: &Strip, film: &Film, focused: bool) -> Vec<(String, Point)> {
        strip
            .lines(&Canvas::default(), &block("{}"), film, focused)
            .into_iter()
            .map(|line| (line.content, line.at))
            .collect()
    }

    /// Three groups right-align on the margin: the audio group at 704, the
    /// subtitle group one pitch along, and the video group at the last pitch.
    #[test]
    fn the_groups_right_align_on_the_side_margin() {
        let mut film = film();
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "video", "title": "Main", "selected": true },
                { "id": 2, "type": "video", "title": "Angle" },
                { "id": 1, "type": "audio", "lang": "eng", "selected": true },
                { "id": 1, "type": "sub", "lang": "spa" },
            ]),
        );
        assert_eq!(
            shown(&Strip::default(), &film, false),
            vec![
                ("audio".to_string(), Point::new(704.0, 990.0)),
                ("ENG".to_string(), Point::new(704.0, 1016.0)),
                ("0 ms".to_string(), Point::new(904.0, 1016.0)),
                ("subtitles".to_string(), Point::new(1134.0, 990.0)),
                ("off".to_string(), Point::new(1134.0, 1016.0)),
                ("0 ms".to_string(), Point::new(1334.0, 1016.0)),
                ("video".to_string(), Point::new(1564.0, 990.0)),
                ("Main".to_string(), Point::new(1564.0, 1016.0)),
            ]
        );
    }

    /// A file with no video group draws two groups, and the row still ends on
    /// the margin.
    #[test]
    fn a_group_with_no_member_is_left_out() {
        assert_eq!(
            shown(&Strip::default(), &film(), false),
            vec![
                ("audio".to_string(), Point::new(1134.0, 990.0)),
                ("ENG".to_string(), Point::new(1134.0, 1016.0)),
                ("0 ms".to_string(), Point::new(1334.0, 1016.0)),
                ("subtitles".to_string(), Point::new(1564.0, 990.0)),
                ("off".to_string(), Point::new(1564.0, 1016.0)),
                ("0 ms".to_string(), Point::new(1764.0, 1016.0)),
            ]
        );
    }

    /// A music item draws no strip at all, and a file with no tracks draws
    /// none either.
    #[test]
    fn a_music_item_draws_no_strip() {
        assert!(Strip::present(&block(r#"{"type":"music"}"#), &film()).is_empty());
        assert!(!Strip::available(&block(r#"{"type":"music"}"#), &film()));
        assert!(
            Strip::default()
                .lines(
                    &Canvas::default(),
                    &block(r#"{"type":"music"}"#),
                    &film(),
                    true
                )
                .is_empty()
        );
        // A film with no tracks at all still carries the audio offset.
        assert_eq!(
            Strip::present(&block("{}"), &Film::default()),
            vec![Control::AudioOffset]
        );
        assert!(Strip::available(&block("{}"), &film()));
    }

    /// The focused control reads bright, and every other one reads subdued.
    #[test]
    fn the_focused_control_reads_bright() {
        let strip = Strip::default();
        let lines = strip.lines(&Canvas::default(), &block("{}"), &film(), true);
        assert_eq!(
            lines[1].color,
            theme::at(theme::color::fill(), theme::alpha::OPAQUE)
        );
        assert_eq!(
            lines[2].color,
            theme::at(theme::color::text(), theme::alpha::SUBDUED)
        );
        assert_eq!(lines[0].color.r, theme::color::muted().r);

        let unfocused = strip.lines(&Canvas::default(), &block("{}"), &film(), false);
        assert_eq!(
            unfocused[1].color,
            theme::at(theme::color::text(), theme::alpha::SUBDUED)
        );
    }

    /// left and right walk the flat order and hold at its ends.
    #[test]
    fn the_horizontal_focus_holds_at_the_ends_of_the_row() {
        let mut strip = Strip::default();
        let present = Strip::present(&block("{}"), &film());
        assert_eq!(present.len(), 4);

        assert!(strip.press(Action::Left, &block("{}"), &film()).is_empty());
        assert_eq!(strip.index, 0);
        for _ in 0..3 {
            strip.press(Action::Right, &block("{}"), &film());
        }
        assert_eq!(strip.index, 3);
        strip.press(Action::Right, &block("{}"), &film());
        assert_eq!(strip.index, 3);
        strip.press(Action::Left, &block("{}"), &film());
        assert_eq!(strip.index, 2);
    }

    /// A select opens the focused control's chooser, on the track that plays.
    #[test]
    fn a_select_opens_the_focused_controls_chooser() {
        let mut strip = Strip::default();
        strip.press(Action::Select, &block("{}"), &film());
        assert_eq!(strip.capturing(), Some(Control::Audio));

        strip.close();
        strip.press(Action::Right, &block("{}"), &film());
        strip.press(Action::Select, &block("{}"), &film());
        assert_eq!(strip.capturing(), Some(Control::AudioOffset));
    }

    /// The chooser opens on the track that plays, moves on up and down, holds
    /// at the ends of the list, and applies on select.
    #[test]
    fn a_chooser_moves_on_up_and_down_and_applies_on_select() {
        let mut strip = Strip::default();
        strip.press(Action::Select, &block("{}"), &film());
        assert!(strip.handle(Action::Up, &film()).is_empty());
        assert!(strip.handle(Action::Left, &film()).is_empty());
        strip.handle(Action::Down, &film());
        strip.handle(Action::Down, &film());
        assert_eq!(
            strip.handle(Action::Select, &film()),
            vec![vec![json!("set_property"), json!("aid"), json!("2")]]
        );
        assert_eq!(strip.capturing(), None);
    }

    /// A delay adjuster nudges on left and right, resets on up, and closes on
    /// down and on select.
    #[test]
    fn an_adjuster_nudges_and_closes() {
        let mut strip = Strip::default();
        strip.press(Action::Right, &block("{}"), &film());
        strip.press(Action::Select, &block("{}"), &film());
        assert_eq!(strip.capturing(), Some(Control::AudioOffset));
        assert_eq!(
            strip.handle(Action::Right, &film()),
            vec![vec![
                json!("set_property"),
                json!("audio-delay"),
                json!(0.05)
            ]]
        );
        assert_eq!(strip.capturing(), Some(Control::AudioOffset));
        assert!(strip.handle(Action::Down, &film()).is_empty());
        assert_eq!(strip.capturing(), None);
        assert!(strip.handle(Action::Down, &film()).is_empty());
    }

    /// A control the file lost drops the focus back onto the row.
    #[test]
    fn a_focus_past_the_end_of_the_row_holds_on_its_last_control() {
        let mut strip = Strip::default();
        for _ in 0..3 {
            strip.press(Action::Right, &block("{}"), &film());
        }
        let mut alone = Film::default();
        alone.apply(
            "track-list",
            &json!([{ "id": 1, "type": "audio", "selected": true }]),
        );
        assert_eq!(
            shown(&strip, &alone, true),
            vec![
                ("audio".to_string(), Point::new(1564.0, 990.0)),
                ("Track 1".to_string(), Point::new(1564.0, 1016.0)),
                ("0 ms".to_string(), Point::new(1764.0, 1016.0)),
            ]
        );
    }
}
