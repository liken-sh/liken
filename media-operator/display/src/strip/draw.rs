//! Turns the strip's views into lines and shapes. Every control for a
//! track is on the frame's vertical centre line, under the cue it changes.
//! The timing readout is centred on the line with its scale across under
//! it, and the scale's zero is on the line. The tracks run down the line:
//! each language is right-aligned to it, once for its group, and each
//! track's variant is left-aligned from it. The dot of the playing track
//! is on a faint hairline down the line. The strip's stops are either side
//! of a short hairline on the same line.
//!
//! The panel has no plate. A soft darkening with no edge sits behind it,
//! so its words read over a bright picture.

use iced::widget::canvas::Path;
use iced::{Color, Point, Rectangle, Size};

use super::{PanelFocus, PanelView, StopView, TimingView};
use crate::canvas::shade::Reach;
use crate::canvas::{Anchor, Brush, Line, clip, measure, measure_italic};
use crate::listing::{self, Row, Value};
use crate::motion;
use crate::theme;

/// The line box of the panel's and the strip's words, a font of 29
/// pixels, and of their smaller words, a font of 25.6 pixels.
const TYPE: f32 = 38.5;
const SMALL: f32 = theme::type_scale::SMALL;
/// The space between the centre line and the words either side of it.
const GUTTER: f32 = 24.0;
/// The space between two parts of a line: half the font's size, and the
/// whole size between a stop's heading and its value.
const PART_GAP: f32 = 14.5;
const HEADING_GAP: f32 = 29.0;

/// The y of the centre of the strip's line, 86 under the bar, which puts
/// its baseline 56 above the bottom edge, inside the area that a
/// television's overscan leaves visible. And the half height of its
/// hairline.
const STRIP_Y: f32 = theme::BAR_Y + 86.0;
const STRIP_HAIR: f32 = 13.0;
/// The space between the Subtitles stop and a Video stop after it.
const STOP_GAP: f32 = 48.0;

/// The bottom of the last row's line box, 20 above the time over the
/// scrubber.
const BOTTOM: f32 = crate::scrubber::TOP - 20.0;
/// The pitch of a track row.
const ROW: f32 = 46.0;
/// A list of up to this many rows shows whole. A longer list shows
/// `SCROLLED` rows and a line at each edge that names the languages above
/// and below, in the same height, so the cue over the panel stays in the
/// lower two thirds of the frame and does not move while the list scrolls.
const WHOLE: usize = 7;
const SCROLLED: usize = 5;

/// The timing block: the readout's row, the gap to the scale, and the
/// room under the scale for the compare's hint. The room is there with or
/// without the hint, so the rows and the cue do not move on a compare.
const SCALE_GAP: f32 = 14.0;
const HINT_ROOM: f32 = 56.0;
/// Half the scale's length, the length of its end caps, of its zero tick,
/// and of a graduation tick.
const HALF_SCALE: f32 = 340.0;
const END_CAP: f32 = 16.0;
const ZERO_TICK: f32 = 22.0;
const TICK: f32 = 8.0;
/// The space between an end of the scale and its word.
const END_GAP: f32 = 18.0;

/// The inks, from the brightest: the focused words, the words of the track
/// that plays or the readout of a delay, the resting words, and the
/// faint marks.
fn focus_ink() -> Color {
    Color::WHITE
}

fn ink() -> Color {
    theme::color::text()
}

fn resting() -> Color {
    theme::at(theme::color::text(), 0.72)
}

fn muted() -> Color {
    theme::color::muted()
}

fn faint(alpha: f32) -> Color {
    theme::at(theme::color::muted(), alpha)
}

fn accent() -> Color {
    theme::color::fill()
}

/// A line in `color`, at the color's own alpha. `Line::new` makes a line
/// opaque, and the panel's resting and faint words are translucent.
fn text(content: impl Into<String>, at: Point, anchor: Anchor, size: f32, color: Color) -> Line {
    Line::new(content, at, anchor, size, color).alpha(color.a)
}

/// One part of a line: its words, whether they are italic, their size,
/// and their ink.
#[derive(Debug, Clone)]
struct Part {
    text: String,
    italic: bool,
    size: f32,
    color: Color,
}

impl Part {
    fn new(text: &str, size: f32, color: Color) -> Self {
        Self {
            text: text.to_string(),
            italic: false,
            size,
            color,
        }
    }

    fn italic(self) -> Self {
        Self {
            italic: true,
            ..self
        }
    }

    fn width(&self) -> f32 {
        match self.italic {
            true => measure_italic(&self.text, self.size),
            false => measure(&self.text, self.size),
        }
    }
}

/// The width of parts set with `gap` between them.
fn width(parts: &[Part], gap: f32) -> f32 {
    let words: f32 = parts.iter().map(Part::width).sum();
    words + gap * parts.len().saturating_sub(1) as f32
}

/// The lines of parts set from `left`, each centred on `y`, so parts of
/// two sizes share the middle of the line.
fn set(parts: &[Part], left: f32, y: f32, gap: f32) -> Vec<Line> {
    let mut x = left;
    parts
        .iter()
        .map(|part| {
            let line = text(
                part.text.clone(),
                Point::new(x, y),
                Anchor::Left,
                part.size,
                part.color,
            );
            x += part.width() + gap;
            match part.italic {
                true => line.italic(),
                false => line,
            }
        })
        .collect()
}

/// One mark that is not text.
#[derive(Debug, Clone, PartialEq)]
pub enum Mark {
    Bar(Rectangle, Color),
    Dot(Point, f32, Color),
    /// A triangle that points up or down, before a line of hidden
    /// languages.
    Arrow(Point, bool, Color),
}

fn bar(x: f32, y: f32, width: f32, height: f32, color: Color) -> Mark {
    Mark::Bar(
        Rectangle::new(Point::new(x, y), Size::new(width, height)),
        color,
    )
}

/// A bar centred on `(x, y)`.
fn centred(x: f32, y: f32, width: f32, height: f32, color: Color) -> Mark {
    bar(x - width / 2.0, y - height / 2.0, width, height, color)
}

/// The focus underline under words that run from `left` for `width` on
/// a line centred on `y`.
fn bar_under(left: f32, y: f32, width: f32) -> Rectangle {
    Rectangle::new(
        Point::new(left, y + 0.62 * theme::type_size(TYPE)),
        Size::new(width, theme::underline::THICKNESS),
    )
}

/// The parts of a stop: its heading in italics, then the value, then the
/// delay. The heading reads in full ink while the stop's panel is open.
fn stop_parts(stop: &StopView, value: &Value) -> (Vec<Part>, usize) {
    let color = match (stop.focused, stop.open) {
        (true, false) => focus_ink(),
        (_, true) => ink(),
        _ => resting(),
    };
    let heading = if stop.open { ink() } else { muted() };
    let mut parts = vec![
        Part::new(stop.track.heading(), TYPE, heading).italic(),
        Part::new(&value.name, TYPE, color),
    ];
    if !value.role.is_empty() {
        parts.push(Part::new(value.role, TYPE, color).italic());
    }
    if !value.detail.is_empty() {
        parts.push(Part::new(&value.detail, TYPE, muted()));
    }
    let valued = parts.len();
    if let Some(delay) = &stop.delay {
        parts.push(Part::new("\u{b7}", TYPE, muted()));
        parts.push(Part::new(delay, TYPE, muted()));
    }
    (parts, valued)
}

/// The value a stop shows: the longest of its texts that fits in `room`
/// with the heading and the delay.
fn stop_fit(stop: &StopView, room: f32) -> (Vec<Part>, usize) {
    let fits = |value: &Value| {
        let (parts, _) = stop_parts(stop, value);
        line_width(&parts) <= room
    };
    let value = stop
        .values
        .iter()
        .find(|value| fits(value))
        .or(stop.values.last())
        .cloned()
        .unwrap_or_default();
    stop_parts(stop, &value)
}

/// The width of a stop's line: the heading, a wide gap, then the parts of
/// the value and the delay.
fn line_width(parts: &[Part]) -> f32 {
    match parts.split_first() {
        Some((heading, rest)) => heading.width() + HEADING_GAP + width(rest, PART_GAP),
        None => 0.0,
    }
}

fn set_stop(parts: &[Part], left: f32) -> Vec<Line> {
    let Some((heading, rest)) = parts.split_first() else {
        return Vec::new();
    };
    let mut lines = set(std::slice::from_ref(heading), left, STRIP_Y, 0.0);
    lines.extend(set(
        rest,
        left + heading.width() + HEADING_GAP,
        STRIP_Y,
        PART_GAP,
    ));
    lines
}

/// The lines and the marks of the strip. The Audio stop ends a gutter left
/// of the centre line and the Subtitles stop starts a gutter right of it,
/// with a short hairline between them. A file with no subtitles keeps the
/// hairline, with its audio on the left, because the panel opens on that
/// line. A Video stop follows the stop on the right.
pub fn strip_lines(centre: f32, right: f32, stops: &[StopView]) -> (Vec<Line>, Vec<Mark>) {
    let mut lines = Vec::new();
    let mut marks = vec![centred(centre, STRIP_Y, 2.0, 2.0 * STRIP_HAIR, faint(0.5))];
    let mut next = centre + GUTTER;
    for stop in stops {
        let left_side = stop.track == crate::track::Track::Audio;
        let room = match left_side {
            true => centre - GUTTER - theme::MARGIN_X,
            false => right - next,
        };
        let (parts, valued) = stop_fit(stop, room);
        let left = match left_side {
            true => centre - GUTTER - line_width(&parts),
            false => next,
        };
        if stop.focused && !stop.open {
            let value_left = left + parts[0].width() + HEADING_GAP;
            marks.push(Mark::Bar(
                bar_under(value_left, STRIP_Y, width(&parts[1..valued], PART_GAP)),
                theme::color::focus(),
            ));
        }
        if !left_side {
            next = left + line_width(&parts) + STOP_GAP;
        }
        lines.extend(set_stop(&parts, left));
    }
    (lines, marks)
}

pub fn strip(brush: &mut Brush<'_>, stops: &[StopView]) {
    let canvas = brush.canvas();
    let (lines, marks) = strip_lines(canvas.centre_x(), canvas.right(), stops);
    draw(brush, &marks, lines);
}

/// How many rows the panel shows, and whether it shows the edge lines.
fn shown(view: &PanelView) -> (usize, bool) {
    match view.rows.len() > WHOLE {
        true => (SCROLLED, true),
        false => (view.rows.len(), false),
    }
}

/// The top of the first row's slot.
fn rows_top(view: &PanelView) -> f32 {
    let (rows, edges) = shown(view);
    BOTTOM - (rows + if edges { 2 } else { 0 }) as f32 * ROW
}

/// The top of the panel: the top of the readout's row, or of the first
/// row when the panel has no timing. The cue rises above it.
pub fn panel_top(view: &PanelView) -> f32 {
    match view.timing {
        Some(_) => rows_top(view) - HINT_ROOM - SCALE_GAP - ROW,
        None => rows_top(view),
    }
}

/// The index of the first row that shows. It keeps the focused row in the
/// middle of the rows that show, and stops at the ends of the list. With
/// the focus on the timing, the list shows from its first row, the row a
/// down press reaches.
pub fn first_row(view: &PanelView) -> usize {
    let (rows, _) = shown(view);
    let focused = view.focused_row().unwrap_or(0);
    focused.saturating_sub(rows / 2).min(view.rows.len() - rows)
}

/// The panel: its marks, its lines, and the focus underline, which
/// glides.
#[derive(Debug, Clone, PartialEq)]
pub struct Picture {
    pub marks: Vec<Mark>,
    pub lines: Vec<Line>,
    pub underline: Option<Rectangle>,
}

/// Where the scale is on the y axis, and the x of a share of it.
fn scale_y(view: &PanelView) -> f32 {
    panel_top(view) + ROW + SCALE_GAP + 1.0
}

fn scale_x(centre: f32, share: f32) -> f32 {
    centre + share * HALF_SCALE
}

/// The underline as it is drawn on `tick` with the centre line at
/// `centre`, the place a new glide starts from. `None` when nothing in the
/// panel has the focus.
pub fn underline(view: &PanelView, centre: f32, tick: u64) -> Option<Rectangle> {
    picture(centre, view, tick).underline
}

pub fn picture(centre: f32, view: &PanelView, tick: u64) -> Picture {
    let mut picture = Picture {
        marks: Vec::new(),
        lines: Vec::new(),
        underline: None,
    };
    let mut target = None;
    if let Some(timing) = &view.timing {
        target = timing_block(&mut picture, view, timing, centre, tick).or(target);
    }
    target = rows(&mut picture, view, centre).or(target);
    // The underline glides from where it was drawn. The glide keeps its x
    // from the centre line, so the canvas's width does not move it.
    picture.underline = target.map(|to| {
        let relative = Rectangle {
            x: to.x - centre,
            ..to
        };
        let drawn = motion::drawn(view.underline.as_ref(), relative, tick);
        Rectangle {
            x: drawn.x + centre,
            ..drawn
        }
    });
    picture
}

/// The readout, the scale, and the compare's hint. Returns the underline
/// of the readout when it has the focus.
fn timing_block(
    picture: &mut Picture,
    view: &PanelView,
    timing: &TimingView,
    centre: f32,
    tick: u64,
) -> Option<Rectangle> {
    let top = panel_top(view);
    let focused = view.focus == PanelFocus::Timing;
    let color = match (timing.active, focused, timing.share != 0.0) {
        (false, _, _) => faint(0.6),
        (true, true, _) => focus_ink(),
        (true, false, true) => ink(),
        (true, false, false) => resting(),
    };
    let reading_y = top + ROW / 2.0;
    picture.lines.push(text(
        timing.reading.clone(),
        Point::new(centre, reading_y),
        Anchor::Centre,
        TYPE,
        color,
    ));
    let y = scale_y(view);
    let strength = if timing.active { 1.0 } else { 0.5 };
    let hair = faint(0.45 * strength);
    picture.marks.push(bar(
        centre - HALF_SCALE,
        y - 1.0,
        2.0 * HALF_SCALE,
        2.0,
        hair,
    ));
    for end in [-1.0, 1.0] {
        picture
            .marks
            .push(centred(scale_x(centre, end), y, 2.0, END_CAP, hair));
    }
    for share in &timing.ticks {
        picture.marks.push(centred(
            scale_x(centre, *share),
            y,
            1.5,
            TICK,
            faint(0.4 * strength),
        ));
    }
    picture
        .marks
        .push(centred(centre, y, 2.0, ZERO_TICK, faint(strength)));
    if let Some(back) = timing.returns_to {
        picture
            .marks
            .push(centred(scale_x(centre, back), y, 3.0, 26.0, muted()));
    }
    if timing.active {
        let mark = motion::drawn(view.mark.as_ref(), timing.share, tick);
        let x = scale_x(centre, mark);
        picture.marks.push(bar(
            centre.min(x),
            y - 2.0,
            (x - centre).abs(),
            4.0,
            accent(),
        ));
        picture.marks.push(centred(x, y, 4.0, 28.0, accent()));
    }
    let end_word = |at_end: bool| match (timing.active, at_end) {
        (true, false) => resting(),
        _ => faint(0.45),
    };
    picture.lines.push(
        text(
            "sooner",
            Point::new(centre - HALF_SCALE - END_GAP, y),
            Anchor::Right,
            SMALL,
            end_word(timing.at_sooner_end),
        )
        .italic(),
    );
    picture.lines.push(
        text(
            "later",
            Point::new(centre + HALF_SCALE + END_GAP, y),
            Anchor::Left,
            SMALL,
            end_word(timing.at_later_end),
        )
        .italic(),
    );
    if let Some(hint) = &timing.hint {
        picture.lines.push(text(
            hint.clone(),
            Point::new(centre, y + SCALE_GAP + SMALL / 2.0),
            Anchor::Centre,
            SMALL,
            muted(),
        ));
    }
    // The stem runs down the centre line from under the scale, or from
    // under the hint, so it strikes through no word.
    let from = match timing.hint {
        Some(_) => y + 50.0,
        None => y + 12.0,
    };
    picture.marks.push(bar(
        centre - 1.0,
        from,
        2.0,
        rows_top(view) - from,
        faint(0.28),
    ));
    focused.then(|| {
        bar_under(
            centre - measure(&timing.reading, TYPE) / 2.0,
            reading_y,
            measure(&timing.reading, TYPE),
        )
    })
}

/// The variant of a row: what differs from the other tracks of its
/// language. A row with no language shows its own name first.
///
/// The parts fit in `room`. A long title, such as a commentary credit that
/// names several people, gives way: it is cut with an ellipsis, so the
/// format after it, the short fact, stays whole.
fn variant(view: &PanelView, at: usize, color: Color, room: f32) -> Vec<Part> {
    let row = &view.rows[at];
    let mut parts = Vec::new();
    if !row.has_language {
        parts.push(Part::new(&row.language, TYPE, color));
    }
    if !row.role.is_empty() {
        parts.push(Part::new(row.role, TYPE, color).italic());
    } else if view.track == crate::track::Track::Subtitles && row.has_language {
        // Every subtitle row starts with what kind of track it is, so the
        // plain track has a word too. A plain audio track is named by its
        // format, so it has none.
        parts.push(Part::new(FULL, TYPE, color).italic());
    }
    if !row.title.is_empty() {
        parts.push(Part::new(&row.title, TYPE, color));
    }
    if !row.format.is_empty() && !format_with_language(view, at) {
        parts.push(Part::new(&row.format, TYPE, muted()));
    }
    if let Some(number) = row.number {
        parts.push(Part::new(&format!("Track {number}"), TYPE, muted()));
    }
    let over = width(&parts, PART_GAP) - room;
    if over > 0.0 && !parts.is_empty() {
        // The title gives way first, and a row with no title cuts its
        // first part, the name of a track with no language.
        let at = parts
            .iter()
            .position(|part| part.text == row.title && !row.title.is_empty())
            .unwrap_or(0);
        let keep = (parts[at].width() - over).max(0.0);
        parts[at].text = clip(&parts[at].text, TYPE, keep);
    }
    parts
}

/// The role word of a plain subtitle track in the panel. The media
/// browser and the strip write no word for it.
const FULL: &str = "full";

/// Whether the format of the row at `at` is written once, with its
/// language, and left off the row: when two or more subtitle tracks of the
/// language, all of the list's and not only those that show, have the
/// same format. It is a fact of the group, so a row reads the same
/// wherever the list is scrolled. A language with one track, or with
/// tracks of different formats, keeps the format on each row. Audio keeps
/// it on each row too, because the format names a plain audio track.
fn format_with_language(view: &PanelView, at: usize) -> bool {
    let row = &view.rows[at];
    view.track == crate::track::Track::Subtitles
        && listing::one_format(&view.rows, at)
        && view
            .rows
            .iter()
            .filter(|one| one.has_language && one.language == row.language)
            .count()
            > 1
}

/// The track rows and the edge lines. Returns the focused row's underline.
fn rows(picture: &mut Picture, view: &PanelView, centre: f32) -> Option<Rectangle> {
    let (count, edges) = shown(view);
    let first = first_row(view);
    let mut y = rows_top(view);
    if edges {
        edge(picture, &view.rows[..first], centre, y + ROW / 2.0, true);
        y += ROW;
    }
    let stem_top = y;
    let focused = view.focused_row();
    let mut target = None;
    for at in first..first + count {
        let row = &view.rows[at];
        let middle = y + ROW / 2.0;
        let lit = |one: &Row| one.playing || focused.is_some_and(|f| view.rows[f].id == one.id);
        if listing::names_its_language(&view.rows, at, first) {
            let group_lit = view
                .rows
                .iter()
                .filter(|one| one.has_language && one.language == row.language)
                .any(lit);
            let language = clip(&row.language, TYPE, centre - GUTTER - theme::MARGIN_X);
            let language_left = centre - GUTTER - measure(&language, TYPE);
            picture.lines.push(text(
                language,
                Point::new(centre - GUTTER, middle),
                Anchor::Right,
                TYPE,
                if group_lit { ink() } else { resting() },
            ));
            // A format that every track of the group shares is written once,
            // faint, before the language on its line, so the language stays
            // right-aligned to the centre line.
            if format_with_language(view, at) {
                let room = language_left - PART_GAP - theme::MARGIN_X;
                picture.lines.push(text(
                    clip(&row.format, SMALL, room),
                    Point::new(language_left - PART_GAP, middle),
                    Anchor::Right,
                    SMALL,
                    muted(),
                ));
            }
        }
        if row.playing {
            picture
                .marks
                .push(Mark::Dot(Point::new(centre, middle + 1.0), 5.0, accent()));
        }
        let color = match (focused == Some(at), row.playing) {
            (true, _) => focus_ink(),
            (false, true) => ink(),
            (false, false) => resting(),
        };
        let room = centre - GUTTER - theme::MARGIN_X;
        let parts = variant(view, at, color, room);
        if focused == Some(at) {
            target = Some(bar_under(centre + GUTTER, middle, width(&parts, PART_GAP)));
        }
        picture
            .lines
            .extend(set(&parts, centre + GUTTER, middle, PART_GAP));
        y += ROW;
    }
    // A list with no rows, for the moment before its panel closes, has no
    // stem to draw.
    if count > 0 {
        picture.marks.push(bar(
            centre - 1.0,
            stem_top,
            2.0,
            y - 8.0 - stem_top,
            faint(0.28),
        ));
    }
    if edges {
        edge(
            picture,
            &view.rows[first + count..],
            centre,
            y + ROW / 2.0,
            false,
        );
    }
    target
}

/// A line that names the languages of the rows above or below the rows
/// that show, centred on the centre line, after an arrowhead that points
/// to them. The playing language keeps its dot, so the answer to "what
/// plays" stays on the screen after the list moves.
fn edge(picture: &mut Picture, hidden: &[Row], centre: f32, y: f32, above: bool) {
    let languages = listing::languages(hidden);
    if languages.is_empty() {
        return;
    }
    let room = 2.0 * EDGE_HALF;
    let chosen = edge_names(&languages, above, |names| {
        edge_width(&edge_parts(&languages, names, above)) <= room
    });
    let parts = edge_parts(&languages, &chosen, above);
    let mut x = centre - edge_width(&parts) / 2.0;
    picture.marks.push(Mark::Arrow(
        Point::new(x + EDGE_ARROW / 2.0, y),
        above,
        muted(),
    ));
    x += EDGE_ARROW + PART_GAP;
    for (part, plays) in parts {
        if plays {
            picture
                .marks
                .push(Mark::Dot(Point::new(x + 4.0, y + 1.0), 4.0, accent()));
            x += EDGE_DOT;
        }
        picture
            .lines
            .extend(set(std::slice::from_ref(&part), x, y, 0.0));
        x += part.width();
    }
}

/// Half the widest a line of hidden languages draws, about the half width
/// of the scale with its end words, so the line reads as part of the panel.
/// Names that do not fit fold into the count.
const EDGE_HALF: f32 = HALF_SCALE + 90.0;

/// The arrowhead before a line of hidden languages, and the room of the dot
/// before the playing language.
const EDGE_ARROW: f32 = 14.0;
const EDGE_DOT: f32 = 16.0;

/// Which of the hidden languages a line names: `Off` and the playing
/// language, always, and then the languages nearest the rows that show,
/// the last ones above and the first ones below, for as long as `fits` says
/// the line still fits. The indexes are in list order.
fn edge_names(
    languages: &[(String, bool)],
    above: bool,
    fits: impl Fn(&[usize]) -> bool,
) -> Vec<usize> {
    let mut chosen: Vec<usize> = languages
        .iter()
        .enumerate()
        .filter(|(_, (language, plays))| *plays || language == listing::OFF)
        .map(|(at, _)| at)
        .collect();
    let nearest: Vec<usize> = match above {
        true => (0..languages.len()).rev().collect(),
        false => (0..languages.len()).collect(),
    };
    for at in nearest {
        if chosen.contains(&at) {
            continue;
        }
        let mut more = chosen.clone();
        more.push(at);
        more.sort_unstable();
        if !fits(&more) {
            break;
        }
        chosen = more;
    }
    chosen.sort_unstable();
    chosen
}

/// The parts of a line of hidden languages: the names at `chosen` in list
/// order, with dots between them, and a count of the others at the end of
/// the line far from the rows that show: "21 more" first on the line above,
/// so its last name is the nearest, and "and 21 more" last on the line
/// below, after its nearest names. Each part says whether it is the playing
/// language.
fn edge_parts(languages: &[(String, bool)], chosen: &[usize], above: bool) -> Vec<(Part, bool)> {
    let others = languages.len() - chosen.len();
    let mut words: Vec<(String, bool)> = chosen.iter().map(|at| languages[*at].clone()).collect();
    if others > 0 {
        match above {
            true => words.insert(0, (format!("{others} more"), false)),
            false => words.push((format!("and {others} more"), false)),
        }
    }
    let mut parts = Vec::new();
    for (at, (word, plays)) in words.iter().enumerate() {
        if at > 0 {
            parts.push((Part::new(" \u{b7} ", SMALL, muted()), false));
        }
        parts.push((Part::new(word, SMALL, muted()), *plays));
    }
    parts
}

/// The width of a line of hidden languages, with its arrowhead and dot.
fn edge_width(parts: &[(Part, bool)]) -> f32 {
    let words: f32 = parts
        .iter()
        .map(|(part, plays)| part.width() + if *plays { EDGE_DOT } else { 0.0 })
        .sum();
    EDGE_ARROW + PART_GAP + words
}

/// A line with its outline: eight copies in black around it, then the
/// line.
fn outlined(line: Line) -> Vec<Line> {
    let shadow = theme::at(theme::color::SHADOW, OUTLINE_ALPHA * line.color.a);
    let diagonal = OUTLINE * std::f32::consts::FRAC_1_SQRT_2;
    let mut lines: Vec<Line> = [
        (-OUTLINE, 0.0),
        (OUTLINE, 0.0),
        (0.0, -OUTLINE),
        (0.0, OUTLINE),
        (-diagonal, -diagonal),
        (diagonal, -diagonal),
        (-diagonal, diagonal),
        (diagonal, diagonal),
    ]
    .into_iter()
    .map(|(dx, dy)| Line {
        at: Point::new(line.at.x + dx, line.at.y + dy),
        color: shadow,
        ..line.clone()
    })
    .collect();
    lines.push(line);
    lines
}

fn draw(brush: &mut Brush<'_>, marks: &[Mark], lines: Vec<Line>) {
    for mark in marks {
        match mark {
            Mark::Bar(shape, color) => brush.rect(*shape, *color),
            Mark::Dot(centre, radius, color) => {
                brush.shape(&Path::circle(*centre, *radius), *color)
            }
            Mark::Arrow(at, up, color) => {
                let (tip, base) = match up {
                    true => (at.y - 4.5, at.y + 4.5),
                    false => (at.y + 4.5, at.y - 4.5),
                };
                let path = Path::new(|path| {
                    path.move_to(Point::new(at.x, tip));
                    path.line_to(Point::new(at.x + 7.0, base));
                    path.line_to(Point::new(at.x - 7.0, base));
                    path.close();
                });
                brush.shape(&path, *color);
            }
        }
    }
    for line in lines {
        brush.text(line);
    }
}

/// The darkening under the panel. It is centred on the centre line,
/// darkest across the middle of the words, and clear well before the
/// frame's sides. It starts at the top of the readout, under the cue, so it
/// does not dim the cue, and it darkens over a long ramp down from there.
/// It runs down to the bottom edge, where the bottom scrim is already
/// dark, so it has no bottom edge. The display draws it in the lower
/// layer, under the scrubber and the strip, so it dims no words.
const SHADE_HALF: f32 = 700.0;
const SHADE_SIDES: f32 = 420.0;
const SHADE_TOP: f32 = 180.0;
const SHADE_PEAK: f32 = 0.4;

/// The soft dark outline under each word of the panel: copies of the line
/// in black at eight places around it, this far away, at this alpha. Over
/// a bright picture it keeps each letter's edge, as mpv's outline keeps
/// the cue's, so the darkening can stay light.
const OUTLINE: f32 = 2.0;
const OUTLINE_ALPHA: f32 = 0.5;

/// Draws the darkening under the open panel.
pub fn shade(brush: &mut Brush<'_>, view: &PanelView) {
    let canvas = brush.canvas();
    let top = panel_top(view);
    let bounds = Rectangle::new(
        Point::new(canvas.centre_x() - SHADE_HALF, top),
        Size::new(2.0 * SHADE_HALF, canvas.height() - top),
    );
    let reach = Reach {
        sides: SHADE_SIDES,
        top: SHADE_TOP,
        bottom: 0.0,
    };
    brush.shade(bounds, reach, SHADE_PEAK);
}

pub fn panel(brush: &mut Brush<'_>, view: &PanelView, tick: u64) {
    let centre = brush.canvas().centre_x();
    let picture = picture(centre, view, tick);
    let mut marks = picture.marks;
    if let Some(underline) = picture.underline {
        marks.push(Mark::Bar(underline, theme::color::focus()));
    }
    let lines = picture.lines.into_iter().flat_map(outlined).collect();
    draw(brush, &marks, lines);
}

#[cfg(test)]
mod tests;
