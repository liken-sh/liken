//! The control strip, the lowest region. It lays the controls out in groups, a
//! heading over each track and its delay offset, moves the horizontal focus
//! across the controls present for this file, and asks the focused control to
//! act on select: a track opens its chooser, and an offset opens its
//! adjuster. An open chooser or adjuster captures every press until it
//! closes.
//!
//! Each offset is a stop of its own beside its track, and it draws at every
//! delay, "0 ms" included. A person who hears the sound late then reads the
//! delay and reaches its adjuster from the strip, without opening a list of
//! tracks first.
//!
//! While the strip has the focus, the focused stop's value reads brighter
//! than the others and has a bar under it in the accent.

use iced::{Point, Rectangle, Size};

use crate::canvas::{Anchor, Brush, Canvas, Line, clip, measure};
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
/// The least space between the end of one value and the start of the next.
/// A value wider than its room ends on an ellipsis, so a long track title
/// cannot draw over the offset or the group beside it and hide the stop that
/// a press lands on.
const STOP_GAP: f32 = 24.0;
/// The pitch between two groups, and the nominal ink width of one group. The
/// strip right-aligns the row on the nominal, so the row holds its place when a
/// value changes width.
const GROUP_PITCH: f32 = 430.0;
const GROUP_INK: f32 = 260.0;

/// The bar under the focused stop's value: its thickness, and the space
/// between the value's line box and the bar. The bar is thick enough to read
/// from a couch at 1080 rows, and it is a bar and not a box so that the mark
/// stays as quiet as the row it marks.
const MARK_H: f32 = 3.0;
const MARK_GAP: f32 = 4.0;

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

/// The layout groups, a heading over a track and its offset. The offsets
/// have no heading of their own, because they read under the group's heading.
/// The headings are in sentence case, as the browser's are.
const GROUPS: [(&str, &[Control]); 3] = [
    ("Audio", &[Control::Audio, Control::AudioOffset]),
    ("Subtitles", &[Control::Subtitles, Control::SubOffset]),
    ("Video", &[Control::Video]),
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

/// One value of the row as it draws: its control, its text after the clip,
/// and its left edge.
struct Cell {
    control: Control,
    content: String,
    x: f32,
}

/// One group of the row as it draws: its heading, its left edge, and the
/// value of each member the file has.
struct Group {
    heading: &'static str,
    x: f32,
    cells: Vec<Cell>,
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

    /// Close the open chooser or adjuster. The focus stays on the control
    /// that opened it.
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

    /// The groups the file has, each with the members it has. A group with no
    /// present member is left out, so a file with no subtitles draws no
    /// subtitle group. Right-align the row of groups, so the last group's
    /// nominal ink ends at the margin and the row holds its right edge as a
    /// value changes width.
    ///
    /// A value with another member in its group takes one member, less the
    /// gap. That room holds the widest delay an offset reads, so only a track
    /// value is ever cut. A value that is the one member of its group takes
    /// the room up to the next group, less the gap, and in the last group it
    /// takes the nominal ink, so it ends inside the margin.
    fn groups(canvas: &Canvas, present: &[Control], film: &Film) -> Vec<Group> {
        let members: Vec<(&str, Vec<Control>)> = GROUPS
            .into_iter()
            .map(|(heading, members)| {
                let members = members
                    .iter()
                    .copied()
                    .filter(|control| present.contains(control))
                    .collect::<Vec<_>>();
                (heading, members)
            })
            .filter(|(_, members)| !members.is_empty())
            .collect();
        let last = members.len().saturating_sub(1);
        let start = canvas.right() - GROUP_INK - last as f32 * GROUP_PITCH;

        members
            .into_iter()
            .enumerate()
            .map(|(at, (heading, members))| {
                let x = start + at as f32 * GROUP_PITCH;
                let room = match (members.len(), at == last) {
                    (1, true) => GROUP_INK,
                    (1, false) => GROUP_PITCH - STOP_GAP,
                    _ => MEMBER_W - STOP_GAP,
                };
                let cells = members
                    .into_iter()
                    .enumerate()
                    .map(|(member, control)| Cell {
                        control,
                        content: clip(&control.value(film), theme::type_scale::TINY, room),
                        x: x + member as f32 * MEMBER_W,
                    })
                    .collect();
                Group { heading, x, cells }
            })
            .collect()
    }

    /// Every line of the strip: one heading per group, and one value per
    /// control.
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

        let mut lines = Vec::new();
        for group in Self::groups(canvas, &present, film) {
            lines.push(
                Line::new(
                    group.heading,
                    Point::new(group.x, Y_HEAD),
                    Anchor::Left,
                    theme::type_scale::TINY,
                    theme::color::muted(),
                )
                .alpha(theme::alpha::SUBDUED),
            );
            for cell in group.cells {
                let focused = Some(cell.control) == here;
                lines.push(
                    Line::new(
                        cell.content,
                        Point::new(cell.x, Y_VAL),
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

    /// The bar under the focused stop's value, and none while the strip does
    /// not have the focus. The focused value also reads brighter than the
    /// others, but from a couch that difference alone does not show which of
    /// five small values the focus is on, so the bar marks it in the accent.
    ///
    /// The bar is as wide as the value's text after the clip, so it ends
    /// inside the value's member as the text does.
    pub fn mark(
        &self,
        canvas: &Canvas,
        presentation: &Presentation,
        film: &Film,
        focused: bool,
    ) -> Option<Rectangle> {
        if !focused {
            return None;
        }
        let present = Self::present(presentation, film);
        let here = *present.get(self.index.min(present.len().checked_sub(1)?))?;
        Self::groups(canvas, &present, film)
            .into_iter()
            .flat_map(|group| group.cells)
            .find(|cell| cell.control == here)
            .map(|cell| {
                Rectangle::new(
                    Point::new(cell.x, Y_VAL + theme::type_scale::TINY / 2.0 + MARK_GAP),
                    Size::new(measure(&cell.content, theme::type_scale::TINY), MARK_H),
                )
            })
    }

    pub fn draw(
        &self,
        brush: &mut Brush<'_>,
        presentation: &Presentation,
        film: &Film,
        focused: bool,
    ) {
        let canvas = brush.canvas();
        if let Some(mark) = self.mark(&canvas, presentation, film, focused) {
            brush.rect(mark, theme::color::focus());
        }
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
    use crate::canvas::ELLIPSIS;
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

    /// A Blu-ray rip, which titles its audio track with the codec and the
    /// layout, and whose other titles are as long.
    fn rip() -> Film {
        let mut film = Film::default();
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "video", "selected": true,
                  "title": "Main feature, theatrical cut" },
                { "id": 2, "type": "video", "title": "Angle" },
                { "id": 1, "type": "audio", "lang": "en", "selected": true,
                  "title": "[English / DD+ 5.1 Atmos]" },
                { "id": 1, "type": "sub", "lang": "eng", "selected": true,
                  "title": "English (SDH) Full Subtitles Track" },
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

    /// The lines of one row of a film's strip, from left to right.
    fn row(presentation: &str, film: &Film, y: f32) -> Vec<Line> {
        Strip::default()
            .lines(&Canvas::default(), &block(presentation), film, false)
            .into_iter()
            .filter(|line| line.at.y == y)
            .collect()
    }

    /// Three groups right-align on the margin: the audio group at 704, the
    /// subtitle group one pitch along, and the video group at the last pitch.
    /// Each offset draws one member to the right of its track, and a delay of
    /// nothing draws as "0 ms".
    #[test]
    fn the_groups_right_align_on_the_side_margin() {
        assert_eq!(
            shown(&Strip::default(), &angles(), false),
            vec![
                line("Audio", 704.0, 990.0),
                line("ENG", 704.0, 1016.0),
                line("0 ms", 904.0, 1016.0),
                line("Subtitles", 1134.0, 990.0),
                line("off", 1134.0, 1016.0),
                line("0 ms", 1334.0, 1016.0),
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
                line("0 ms", 1334.0, 1016.0),
                line("Subtitles", 1564.0, 990.0),
                line("off", 1564.0, 1016.0),
                line("0 ms", 1764.0, 1016.0),
            ]
        );
    }

    /// The audio title of the rip is wider than the room before the audio
    /// offset. Every heading and every value of the row ends at least a gap
    /// short of the line to its right, at the widest delays the offsets read.
    #[test]
    fn a_long_value_stops_short_of_its_neighbour() {
        let mut film = rip();
        film.apply("audio-delay", &json!(-5.0));
        film.apply("sub-delay", &json!(-10.0));
        let headings = row("{}", &film, Y_HEAD);
        let values = row("{}", &film, Y_VAL);
        assert_eq!(headings.len(), 3);
        assert_eq!(values.len(), 5);
        assert!(values[0].content.ends_with(ELLIPSIS));
        for pair in headings.windows(2).chain(values.windows(2)) {
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

    /// The widest delay an offset reads draws whole, so a clip never hides
    /// the number a person sets, and the last group's offset ends on the
    /// screen.
    #[test]
    fn the_widest_offset_draws_whole() {
        let mut film = film();
        film.apply("sub-delay", &json!(-10.0));
        let values = row("{}", &film, Y_VAL);
        let last = &values[values.len() - 1];
        assert_eq!(last.content, "-10000 ms");
        assert!(last.at.x + measure(&last.content, last.size) <= Canvas::default().width());
    }

    /// A photo has no delay to set, so its audio track is the one member of
    /// its group and keeps the room up to the next group. A title that fits
    /// there draws whole.
    #[test]
    fn a_value_alone_keeps_the_room_to_the_next_group() {
        let values = row(r#"{"type":"image"}"#, &rip(), Y_VAL);
        assert_eq!(values.len(), 3);
        assert_eq!(values[0].content, "[English / DD+ 5.1 Atmos] (EN)");
    }

    /// A music item draws no strip at all.
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
        // A film with no tracks at all still has the audio offset.
        assert_eq!(
            Strip::present(&block("{}"), &Film::default()),
            vec![Control::AudioOffset]
        );
        assert!(Strip::available(&block("{}"), &film()));
    }

    /// The focused control reads bright, and every other one reads subdued.
    #[test]
    fn the_focused_control_reads_bright() {
        let bright = theme::at(theme::color::fill(), theme::alpha::OPAQUE);
        let subdued = theme::at(theme::color::text(), theme::alpha::SUBDUED);
        let mut strip = Strip::default();
        let lines = strip.lines(&Canvas::default(), &block("{}"), &film(), true);
        assert_eq!(lines[1].color, bright);
        assert_eq!(lines[2].color, subdued);
        assert_eq!(lines[0].color.r, theme::color::muted().r);

        strip.press(Action::Right, &block("{}"), &film());
        let lines = strip.lines(&Canvas::default(), &block("{}"), &film(), true);
        assert_eq!(lines[1].color, subdued);
        assert_eq!(lines[2].color, bright);

        let unfocused = strip.lines(&Canvas::default(), &block("{}"), &film(), false);
        assert_eq!(unfocused[2].color, subdued);
    }

    /// left and right walk the tracks and their offsets in one flat order,
    /// and hold at the ends of the row.
    #[test]
    fn the_horizontal_focus_holds_at_the_ends_of_the_row() {
        let mut strip = Strip::default();
        assert_eq!(
            Strip::present(&block("{}"), &angles()),
            vec![
                Control::Audio,
                Control::AudioOffset,
                Control::Subtitles,
                Control::SubOffset,
                Control::Video,
            ]
        );

        assert!(
            strip
                .press(Action::Left, &block("{}"), &angles())
                .is_empty()
        );
        assert_eq!(strip.index, 0);
        for _ in 0..4 {
            strip.press(Action::Right, &block("{}"), &angles());
        }
        assert_eq!(strip.index, 4);
        strip.press(Action::Right, &block("{}"), &angles());
        assert_eq!(strip.index, 4);
        strip.press(Action::Left, &block("{}"), &angles());
        assert_eq!(strip.index, 3);
    }

    /// A select on a track opens its chooser, and a select on an offset
    /// opens its adjuster.
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
    /// at the ends of the list, and applies on select. Its last row is a
    /// track, so a select there applies that track.
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

    /// A delay adjuster nudges on left and right, and closes on down and on
    /// select.
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

    /// An adjuster that closes leaves the focus on its offset, so the next
    /// select opens the same adjuster.
    #[test]
    fn a_closed_adjuster_leaves_the_focus_on_its_offset() {
        let mut strip = Strip::default();
        for _ in 0..3 {
            strip.press(Action::Right, &block("{}"), &film());
        }
        strip.press(Action::Select, &block("{}"), &film());
        strip.handle(Action::Select, &film());
        assert_eq!(strip.capturing(), None);

        strip.press(Action::Select, &block("{}"), &film());
        assert_eq!(strip.capturing(), Some(Control::SubOffset));
    }

    /// The mark under each stop of the three groups: the presses to the right
    /// that reach the stop, the left edge of its value, and the value.
    const MARKS: [(usize, f32, &str); 5] = [
        (0, 704.0, "ENG"),
        (1, 904.0, "0 ms"),
        (2, 1134.0, "off"),
        (3, 1334.0, "0 ms"),
        (4, 1564.0, "Main"),
    ];

    /// The focused stop has a bar under its value, as wide as the value's
    /// text, 3 canvas pixels thick, and 4 under the value's line box. The
    /// strip gives one mark at most, so no other stop has one.
    #[test]
    fn the_focused_stop_is_underlined() {
        for (presses, x, content) in MARKS {
            let mut strip = Strip::default();
            for _ in 0..presses {
                strip.press(Action::Right, &block("{}"), &angles());
            }
            assert_eq!(
                strip.mark(&Canvas::default(), &block("{}"), &angles(), true),
                Some(Rectangle::new(
                    Point::new(x, 1034.0),
                    Size::new(measure(content, theme::type_scale::TINY), 3.0),
                )),
                "the mark under {content:?} at {x}"
            );
        }
    }

    /// The mark under a clipped value is as wide as the text that draws, so
    /// it ends where the ellipsis ends and stays inside the value's member.
    #[test]
    fn the_mark_under_a_clipped_value_ends_with_its_text() {
        let strip = Strip::default();
        let value = &row("{}", &rip(), Y_VAL)[0];
        let mark = strip
            .mark(&Canvas::default(), &block("{}"), &rip(), true)
            .unwrap();
        assert!(value.content.ends_with(ELLIPSIS));
        assert_eq!(mark.width, measure(&value.content, value.size));
        assert!(mark.width <= MEMBER_W - STOP_GAP);
    }

    /// A strip without the focus draws no mark, and a file with no strip has
    /// none to draw.
    #[test]
    fn a_strip_without_the_focus_draws_no_mark() {
        let strip = Strip::default();
        assert_eq!(
            strip.mark(&Canvas::default(), &block("{}"), &angles(), false),
            None
        );
        assert_eq!(
            strip.mark(
                &Canvas::default(),
                &block(r#"{"type":"music"}"#),
                &angles(),
                true
            ),
            None
        );
    }

    /// A control the file lost drops the focus back onto the row.
    #[test]
    fn a_focus_past_the_end_of_the_row_holds_on_its_last_control() {
        let mut strip = Strip::default();
        for _ in 0..4 {
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
                line("Track 1", 1564.0, 1016.0),
                line("0 ms", 1764.0, 1016.0),
            ]
        );
    }
}
