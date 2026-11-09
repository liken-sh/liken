//! The control strip, the lowest region. It lays the tracks out in groups, a
//! heading over each track, moves the horizontal focus across the tracks
//! present for this file, and opens the focused track's chooser on select. A
//! chooser captures every press until it closes.
//!
//! The audio and subtitle choosers end on a row for the track's delay offset,
//! and a select there opens the offset's adjuster. The strip draws an offset
//! beside its track only while the offset is set, because a delay of nothing
//! is the usual case and tells the person who reads the strip nothing.

use iced::Point;

use crate::canvas::{Anchor, Brush, Canvas, Line, clip};
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
/// Within a group, a set offset sits this far right of the track value.
const MEMBER_W: f32 = 200.0;
/// The least space between the end of one value and the start of the next.
/// A value wider than its room ends on an ellipsis, so a long track title
/// cannot draw over the offset or the group beside it.
const STOP_GAP: f32 = 24.0;
/// The pitch between two groups, and the nominal ink width of one group. The
/// strip right-aligns the row on the nominal, so the row holds its place when a
/// value changes width.
const GROUP_PITCH: f32 = 430.0;
const GROUP_INK: f32 = 260.0;

/// One control of the strip: a track the focus stands on, or the offset that
/// its chooser's last row opens.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Control {
    Audio,
    AudioOffset,
    Subtitles,
    SubOffset,
    Video,
}

/// The flat focus order the left and right presses walk. An offset takes no
/// focus of its own, because its track's chooser opens it.
const ORDER: [Control; 3] = [Control::Audio, Control::Subtitles, Control::Video];

/// The layout groups, a heading over a track. The headings are in sentence
/// case, as the browser's are.
const GROUPS: [(&str, Control); 3] = [
    ("Audio", Control::Audio),
    ("Subtitles", Control::Subtitles),
    ("Video", Control::Video),
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

    /// The offset a track's chooser ends on. A video track has none.
    fn paired(self) -> Option<Control> {
        match self {
            Control::Audio => Some(Control::AudioOffset),
            Control::Subtitles => Some(Control::SubOffset),
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

    /// The chooser's rows: the tracks, then the offset row when the track
    /// has an offset.
    fn entries(self, film: &Film) -> Vec<String> {
        let Some(track) = self.track() else {
            return Vec::new();
        };
        let mut entries = track.entries(film);
        if let Some(offset) = self.paired().and_then(Control::offset) {
            entries.push(format!("Offset {}", offset.value(film)));
        }
        entries
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

    /// left and right walk the tracks present for this file, and select opens
    /// the focused track's chooser.
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
    /// select, and ignores left and right. A select on its offset row opens
    /// the delay adjuster in its place, which uses left and right to nudge the
    /// delay.
    pub fn handle(&mut self, action: Action, film: &Film) -> Vec<Command> {
        let Some(open) = self.open.as_mut() else {
            return Vec::new();
        };
        if let Some(track) = open.control.track() {
            let entries = open.control.entries(film).len();
            match action {
                Action::Up => open.selected = open.selected.saturating_sub(1),
                Action::Down => {
                    open.selected = (open.selected + 1).min(entries.saturating_sub(1));
                }
                Action::Select => {
                    if let Some(offset) = open.control.paired()
                        && open.selected + 1 == entries
                    {
                        self.open = Some(Open {
                            control: offset,
                            selected: 0,
                        });
                        return Vec::new();
                    }
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

    /// Every line of the strip: one heading and one value per present track,
    /// and a set offset beside its track. Right-align the row of groups, so
    /// the last group's nominal ink ends at the margin and the row holds its
    /// right edge as a value changes width.
    ///
    /// A track value takes the room up to the next group, less the gap. The
    /// last group's value takes the nominal ink, so it ends inside the margin,
    /// and a value with a set offset beside it takes one member.
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
        let groups: Vec<(&str, Control)> = GROUPS
            .into_iter()
            .filter(|(_, control)| present.contains(control))
            .collect();
        let start = canvas.right() - GROUP_INK - (groups.len() as f32 - 1.0) * GROUP_PITCH;

        let mut lines = Vec::new();
        for (at, (heading, control)) in groups.iter().copied().enumerate() {
            let x = start + at as f32 * GROUP_PITCH;
            lines.push(
                Line::new(
                    heading,
                    Point::new(x, Y_HEAD),
                    Anchor::Left,
                    theme::type_scale::TINY,
                    theme::color::muted(),
                )
                .alpha(theme::alpha::SUBDUED),
            );
            let offset = control
                .paired()
                .filter(|paired| paired.available(presentation, film))
                .and_then(Control::offset)
                .filter(|offset| offset.set(film));
            let room = match (offset, at + 1 == groups.len()) {
                (Some(_), _) => MEMBER_W - STOP_GAP,
                (None, true) => GROUP_INK,
                (None, false) => GROUP_PITCH - STOP_GAP,
            };
            let focused = Some(control) == here;
            lines.push(
                Line::new(
                    clip(&control.value(film), theme::type_scale::TINY, room),
                    Point::new(x, Y_VAL),
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
            if let Some(offset) = offset {
                lines.push(
                    Line::new(
                        clip(
                            &offset.value(film),
                            theme::type_scale::TINY,
                            GROUP_PITCH - MEMBER_W - STOP_GAP,
                        ),
                        Point::new(x + MEMBER_W, Y_VAL),
                        Anchor::Left,
                        theme::type_scale::TINY,
                        theme::color::text(),
                    )
                    .alpha(theme::alpha::SUBDUED),
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
        if open.control.track().is_some() {
            chooser::draw(brush, &open.control.entries(film), open.selected);
        } else if let Some(offset) = open.control.offset() {
            offset.draw(brush, film);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::canvas::measure;
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

    /// The film with a second video track, so all three groups draw.
    fn angles() -> Film {
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

    fn line(content: &str, x: f32, y: f32) -> (String, Point) {
        (content.to_string(), Point::new(x, y))
    }

    /// Three groups right-align on the margin: the audio group at 704, the
    /// subtitle group one pitch along, and the video group at the last pitch.
    /// With no delay set, no offset draws.
    #[test]
    fn the_groups_right_align_on_the_side_margin() {
        assert_eq!(
            shown(&Strip::default(), &angles(), false),
            vec![
                line("Audio", 704.0, 990.0),
                line("ENG", 704.0, 1016.0),
                line("Subtitles", 1134.0, 990.0),
                line("off", 1134.0, 1016.0),
                line("Video", 1564.0, 990.0),
                line("Main", 1564.0, 1016.0),
            ]
        );
    }

    /// A set delay draws one member to the right of its track, and a delay of
    /// nothing draws nothing.
    #[test]
    fn a_set_offset_draws_beside_its_track() {
        let mut film = angles();
        film.apply("sub-delay", &json!(-0.2));
        assert_eq!(
            shown(&Strip::default(), &film, false),
            vec![
                line("Audio", 704.0, 990.0),
                line("ENG", 704.0, 1016.0),
                line("Subtitles", 1134.0, 990.0),
                line("off", 1134.0, 1016.0),
                line("-200 ms", 1334.0, 1016.0),
                line("Video", 1564.0, 990.0),
                line("Main", 1564.0, 1016.0),
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
                line("Audio", 1134.0, 990.0),
                line("ENG", 1134.0, 1016.0),
                line("Subtitles", 1564.0, 990.0),
                line("off", 1564.0, 1016.0),
            ]
        );
    }

    /// A Blu-ray rip titles its audio track with the codec and the layout,
    /// and that title is wider than the room before a set offset. Every value
    /// of the row ends at least a gap short of the value to its right, and
    /// the last one ends inside the margin.
    #[test]
    fn a_long_value_stops_short_of_its_neighbour() {
        let mut film = Film::default();
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "video", "selected": true,
                  "title": "Main feature, theatrical cut" },
                { "id": 2, "type": "video", "title": "Angle" },
                { "id": 1, "type": "audio", "lang": "eng", "selected": true,
                  "title": "[English / DD+ 5.1 Atmos]" },
                { "id": 1, "type": "sub", "lang": "eng", "selected": true,
                  "title": "English (SDH) Full Subtitles Track" },
            ]),
        );
        film.apply("audio-delay", &json!(0.05));
        film.apply("sub-delay", &json!(-1.2));
        let values: Vec<Line> = Strip::default()
            .lines(&Canvas::default(), &block("{}"), &film, false)
            .into_iter()
            .filter(|line| line.at.y == Y_VAL)
            .collect();
        assert_eq!(values.len(), 5);
        for pair in values.windows(2) {
            let ends = pair[0].at.x + measure(&pair[0].content, pair[0].size);
            assert!(
                ends + STOP_GAP <= pair[1].at.x,
                "{:?} ends at {ends} and runs into {:?} at {}",
                pair[0].content,
                pair[1].content,
                pair[1].at.x
            );
        }
        let last = &values[values.len() - 1];
        assert!(last.at.x + measure(&last.content, last.size) <= Canvas::default().right());
    }

    /// With no offset beside it, a track value keeps the room up to the next
    /// group, so a title that fits there draws whole.
    #[test]
    fn a_value_alone_keeps_the_room_to_the_next_group() {
        let mut film = film();
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "audio", "lang": "eng", "selected": true,
                  "title": "[English / DD+ 5.1 Atmos]" },
                { "id": 1, "type": "sub", "lang": "spa" },
            ]),
        );
        assert_eq!(
            shown(&Strip::default(), &film, false)[1].0,
            "[English / DD+ 5.1 Atmos] (ENG)"
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
        assert!(Strip::present(&block("{}"), &Film::default()).is_empty());
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
            lines[3].color,
            theme::at(theme::color::text(), theme::alpha::SUBDUED)
        );
        assert_eq!(lines[0].color.r, theme::color::muted().r);

        let unfocused = strip.lines(&Canvas::default(), &block("{}"), &film(), false);
        assert_eq!(
            unfocused[1].color,
            theme::at(theme::color::text(), theme::alpha::SUBDUED)
        );
    }

    /// left and right walk the tracks and hold at the ends of the row. An
    /// offset takes no stop of its own.
    #[test]
    fn the_horizontal_focus_holds_at_the_ends_of_the_row() {
        let mut strip = Strip::default();
        let present = Strip::present(&block("{}"), &angles());
        assert_eq!(
            present,
            vec![Control::Audio, Control::Subtitles, Control::Video]
        );

        assert!(
            strip
                .press(Action::Left, &block("{}"), &angles())
                .is_empty()
        );
        assert_eq!(strip.index, 0);
        for _ in 0..2 {
            strip.press(Action::Right, &block("{}"), &angles());
        }
        assert_eq!(strip.index, 2);
        strip.press(Action::Right, &block("{}"), &angles());
        assert_eq!(strip.index, 2);
        strip.press(Action::Left, &block("{}"), &angles());
        assert_eq!(strip.index, 1);
    }

    /// A select opens the focused track's chooser, on the track that plays.
    #[test]
    fn a_select_opens_the_focused_controls_chooser() {
        let mut strip = Strip::default();
        strip.press(Action::Select, &block("{}"), &film());
        assert_eq!(strip.capturing(), Some(Control::Audio));

        strip.close();
        strip.press(Action::Right, &block("{}"), &film());
        strip.press(Action::Select, &block("{}"), &film());
        assert_eq!(strip.capturing(), Some(Control::Subtitles));
    }

    /// The audio and subtitle choosers end on the offset row, and the video
    /// chooser has none.
    #[test]
    fn a_track_chooser_ends_on_its_offset() {
        let mut film = angles();
        film.apply("audio-delay", &json!(0.05));
        assert_eq!(
            Control::Audio.entries(&film),
            vec!["ENG".to_string(), "Offset +50 ms".to_string()]
        );
        assert_eq!(
            Control::Subtitles.entries(&film),
            vec![
                "Off".to_string(),
                "SPA".to_string(),
                "Offset 0 ms".to_string()
            ]
        );
        assert_eq!(
            Control::Video.entries(&film),
            vec!["Main".to_string(), "Angle".to_string()]
        );
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
        assert_eq!(
            strip.handle(Action::Select, &film()),
            vec![vec![json!("set_property"), json!("aid"), json!("2")]]
        );
        assert_eq!(strip.capturing(), None);
    }

    /// A select on the offset row opens the adjuster, which nudges on left
    /// and right and closes on down and on select.
    #[test]
    fn the_offset_row_opens_the_adjuster() {
        let mut strip = Strip::default();
        strip.press(Action::Select, &block("{}"), &film());
        for _ in 0..3 {
            strip.handle(Action::Down, &film());
        }
        assert!(strip.handle(Action::Select, &film()).is_empty());
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
        for _ in 0..2 {
            strip.press(Action::Right, &block("{}"), &angles());
        }
        let mut alone = Film::default();
        alone.apply(
            "track-list",
            &json!([{ "id": 1, "type": "audio", "selected": true }]),
        );
        assert_eq!(
            shown(&strip, &alone, true),
            vec![
                line("Audio", 1564.0, 990.0),
                line("Track 1", 1564.0, 1016.0)
            ]
        );
    }
}
