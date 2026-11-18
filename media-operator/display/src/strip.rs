//! The control strip along the bottom of the screen. It shows the file's
//! audio, subtitle, and video tracks, each under a heading, and beside the
//! audio and subtitle tracks an offset that sets their delay. Left and
//! right move the focus across the controls, and select opens the focused
//! one: a track opens its chooser and an offset opens its adjuster. While
//! one is open, `Focus` sends it every press through `Strip::handle`
//! until it closes.
//!
//! Each offset is a focus stop of its own and shows even at "0 ms", so a
//! person who hears the sound late can read the delay and open its
//! adjuster without opening the track list first.
//!
//! While the strip has the focus, the focused value is brighter than the
//! others and underlined in the accent color, like a chooser's focused row.

use iced::{Point, Rectangle};

use crate::canvas::{Anchor, Brush, Canvas, Line, clip, measure};
use crate::chooser;
use crate::film::Film;
use crate::focus::Action;
use crate::ipc::Command;
use crate::offset::Offset;
use crate::presentation::Presentation;
use crate::theme;
use crate::track::Track;

/// The y positions of the heading row and the value row, in canvas units,
/// below the scrubber.
const Y_HEAD: f32 = 990.0;
const Y_VAL: f32 = 1016.0;
/// The x distance from a track value to its offset value in the same
/// group.
const MEMBER_W: f32 = 200.0;
/// The minimum space between the end of one value and the start of the
/// next. A value wider than its room is clipped with an ellipsis, so a long
/// track title cannot cover the offset or the group beside it.
const STOP_GAP: f32 = 24.0;
/// The x distance between the left edges of two groups, and the width the
/// right alignment assumes for one group, so the row does not move when a
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

/// The focus order that left and right walk.
const ORDER: [Control; 5] = [
    Control::Audio,
    Control::AudioOffset,
    Control::Subtitles,
    Control::SubOffset,
    Control::Video,
];

/// The groups of the row, each a heading over a track and its offset, which
/// shares the heading. The headings use sentence case, like the library
/// browser's.
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

/// The chooser or adjuster that is open, and the entry selected in it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct Open {
    control: Control,
    selected: usize,
}

/// One value of the row as drawn: its control, its text after clipping,
/// and its left edge.
struct Cell {
    control: Control,
    content: String,
    x: f32,
}

/// One group of the row as drawn: its heading, its left edge, and a value
/// for each control the file has.
struct Group {
    heading: &'static str,
    x: f32,
    cells: Vec<Cell>,
}

/// The focused control's position in the row, and the open chooser or
/// adjuster, if any.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Strip {
    index: usize,
    open: Option<Open>,
}

impl Strip {
    /// The controls this file has, in focus order. A music item has none,
    /// because an album is one audio stream with nothing to choose and the
    /// row would repeat what the frame already shows.
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

    /// The control whose chooser or adjuster is open and receives every
    /// press, if any.
    pub fn capturing(&self) -> Option<Control> {
        self.open.map(|open| open.control)
    }

    /// Closes the open chooser or adjuster. The focus stays on the control
    /// that opened it.
    pub fn close(&mut self) {
        self.open = None;
    }

    /// Handles a press while no chooser or adjuster is open.
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

    /// Handles a press while a chooser or an adjuster is open. In a chooser,
    /// up and down move the selection, and select applies it and closes the
    /// chooser. An adjuster changes the delay on left and right, and closes
    /// on down and select.
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
        if !offset.stays_open(action) {
            self.close();
        }
        commands
    }

    /// Lays out the groups that have at least one control in this file, so
    /// a file with no subtitles has no subtitle group. The row is
    /// right-aligned so that the last group's `GROUP_INK` ends at the
    /// margin.
    ///
    /// The room for a value depends on its group. When the group has two
    /// controls, a value gets `MEMBER_W - STOP_GAP`, which fits the widest
    /// delay an offset shows, so only track values are clipped. A lone
    /// value gets the distance to the next group less `STOP_GAP`. In the
    /// last group it gets `GROUP_INK`, so it ends inside the margin.
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

    /// The text of the strip: one heading per group and one value per
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

    /// The underline bar for the focused value, or `None` when the strip
    /// does not have the focus. The focused value is also brighter, but at
    /// viewing distance brightness alone does not show which small value
    /// has the focus.
    ///
    /// The bar is as wide as the clipped text, so it stays inside the
    /// value's room.
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
                theme::underline(
                    cell.x,
                    Y_VAL + theme::type_scale::TINY / 2.0,
                    measure(&cell.content, theme::type_scale::TINY),
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

    /// The top edge of the open chooser's or adjuster's panel, or `None`
    /// when none is open.
    pub fn panel_top(&self, film: &Film) -> Option<f32> {
        let open = self.open?;
        match (open.control.track(), open.control.offset()) {
            (Some(track), _) => Some(chooser::top(track.entries(film).len())),
            (_, Some(offset)) => Some(offset.shape().y),
            _ => None,
        }
    }

    /// Draws the open chooser or adjuster, if any.
    pub fn draw_chooser(&self, brush: &mut Brush<'_>, film: &Film) {
        let Some(open) = self.open else {
            return;
        };
        if let Some(track) = open.control.track() {
            chooser::draw(
                brush,
                &track.entries(film),
                open.selected,
                track.playing(film),
            );
        } else if let Some(offset) = open.control.offset() {
            offset.draw(brush, film);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::canvas::ELLIPSIS;
    use iced::Size;
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

    /// A Blu-ray rip. Its audio track title has the codec and the channel
    /// layout, and its other titles are long too.
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

    /// Three groups are right-aligned on the margin: audio at x 704,
    /// subtitles one `GROUP_PITCH` after it, and video one more after that.
    /// Each offset draws `MEMBER_W` to the right of its track, and a zero
    /// delay draws as "0 ms".
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

    /// A file with no video track draws two groups, and the row still ends
    /// on the margin.
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
    /// short of the line to its right, at the widest delays the offsets show.
    #[test]
    fn a_long_value_stops_short_of_its_neighbour() {
        let mut film = rip();
        film.apply("audio-delay", &json!(-5.0));
        film.apply("sub-delay", &json!(-10.0));
        let headings = row("{}", &film, Y_HEAD);
        let values = row("{}", &film, Y_VAL);
        assert_eq!(headings.len(), 3);
        assert_eq!(values.len(), 5);
        assert!(
            values[0].content.ends_with(ELLIPSIS),
            "the first value is clipped and ends with the ellipsis"
        );
        for pair in headings.windows(2).chain(values.windows(2)) {
            let ends = pair[0].at.x + measure(&pair[0].content, pair[0].size);
            assert!(
                ends + STOP_GAP <= pair[1].at.x,
                "{:?} ends at {ends} and overlaps {:?} at {}",
                pair[0].content,
                pair[1].content,
                pair[1].at.x
            );
        }
        let last = &values[values.len() - 1];
        assert!(
            last.at.x + measure(&last.content, last.size) <= Canvas::default().right(),
            "the last value ends at or before the right margin"
        );
    }

    /// The widest delay an offset shows draws whole, so a clip never hides
    /// the number a person sets, and the last group's offset ends on the
    /// screen.
    #[test]
    fn the_widest_offset_draws_whole() {
        let mut film = film();
        film.apply("sub-delay", &json!(-10.0));
        let values = row("{}", &film, Y_VAL);
        let last = &values[values.len() - 1];
        assert_eq!(last.content, "-10000 ms");
        assert!(
            last.at.x + measure(&last.content, last.size) <= Canvas::default().width(),
            "the last value ends inside the canvas"
        );
    }

    /// A photo has no delay to set, so its audio track is the only control
    /// in its group and gets the room up to the next group. A title that
    /// fits there is not clipped.
    #[test]
    fn a_value_alone_keeps_the_room_to_the_next_group() {
        let values = row(r#"{"type":"image"}"#, &rip(), Y_VAL);
        assert_eq!(values.len(), 3);
        assert_eq!(values[0].content, "[English / DD+ 5.1 Atmos] (EN)");
    }

    #[test]
    fn a_music_item_draws_no_strip() {
        assert!(
            Strip::present(&block(r#"{"type":"music"}"#), &film()).is_empty(),
            "a music block has no controls"
        );
        assert!(
            !Strip::available(&block(r#"{"type":"music"}"#), &film()),
            "the strip is unavailable for a music block"
        );
        assert!(
            Strip::default()
                .lines(
                    &Canvas::default(),
                    &block(r#"{"type":"music"}"#),
                    &film(),
                    true
                )
                .is_empty(),
            "a music block draws no lines"
        );
        // A film with no tracks at all still has the audio offset.
        assert_eq!(
            Strip::present(&block("{}"), &Film::default()),
            vec![Control::AudioOffset]
        );
        assert!(
            Strip::available(&block("{}"), &film()),
            "the strip is available for a film"
        );
    }

    /// The focused control is bright and every other control is subdued.
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

    /// Left and right move through the tracks and their offsets in one flat
    /// order and stop at the ends of the row.
    #[test]
    fn the_horizontal_focus_stops_at_the_ends_of_the_row() {
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
                .is_empty(),
            "Left at the first control changes nothing"
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

    /// The chooser opens on the playing track, moves on up and down, stops
    /// at the ends of the list, and applies the selected entry on select.
    #[test]
    fn a_chooser_moves_on_up_and_down_and_applies_on_select() {
        let mut strip = Strip::default();
        strip.press(Action::Select, &block("{}"), &film());
        assert!(
            strip.handle(Action::Up, &film()).is_empty(),
            "Up changes nothing"
        );
        assert!(
            strip.handle(Action::Left, &film()).is_empty(),
            "Left changes nothing"
        );
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
        assert!(
            strip.handle(Action::Down, &film()).is_empty(),
            "Down changes nothing"
        );
        assert_eq!(strip.capturing(), None);
        assert!(
            strip.handle(Action::Down, &film()).is_empty(),
            "Down changes nothing"
        );
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

    /// The focused control has a bar under its value, as wide as the
    /// value's text. No other control has one.
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
                    Size::new(measure(content, theme::type_scale::TINY), 4.0),
                )),
                "the mark under {content:?} at {x}"
            );
        }
    }

    /// The mark under a clipped value is as wide as the text that draws, so
    /// it ends where the ellipsis ends and stays inside the value's room.
    #[test]
    fn the_mark_under_a_clipped_value_ends_with_its_text() {
        let strip = Strip::default();
        let value = &row("{}", &rip(), Y_VAL)[0];
        let mark = strip
            .mark(&Canvas::default(), &block("{}"), &rip(), true)
            .unwrap();
        assert!(
            value.content.ends_with(ELLIPSIS),
            "the value is clipped and ends with the ellipsis"
        );
        assert_eq!(mark.width, measure(&value.content, value.size));
        assert!(
            mark.width <= MEMBER_W - STOP_GAP,
            "the mark fits the member width less the stop gap"
        );
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

    /// When the file loses controls, a focus index past the end of the row
    /// moves to the last control.
    #[test]
    fn a_focus_past_the_end_of_the_row_moves_to_its_last_control() {
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
