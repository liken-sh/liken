// The block under a playback row: a bar the width of the text column, and
// under it the status line, which holds the status of the work and the
// marks that change it. At rest the marks are quiet, a thin outline and
// muted words, so they read as bookkeeping beside the status and not as
// more ways to play. A focused mark takes a playback button's fill, full
// ink, and the focus stroke. While a mark holds focus, the bar is twice as
// tall and the status takes full ink, so the person sees the progress the
// mark will change.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::Alignment;
use iced_winit::core::{Color, Point, Radians, Rectangle};

use super::{area, clock, extent, label, mark, progress, text};
use crate::look;

/// The space between the foot of the playback row and the top of the bar,
/// so the bar reads as a mark under the row and not as its edge.
pub const BAR_LEAD: f32 = 18.0;

// The height of the bar while a mark holds focus. It grows about the
// resting bar's middle, so nothing under it moves.
const BAR_HELD: f32 = 8.0;

// The space between the foot of the resting bar and the top of the line.
const LINE_LEAD: f32 = 14.0;

/// The height of a mark, which is the height of the line.
pub const MARK_HEIGHT: f32 = 48.0;

// The size of the status and of a mark's words: a step under a playback
// button's word, so the line reads after the row.
const SIZE: f32 = 19.0;

// The space between the status and the first mark, and between two marks.
const STATUS_GAP: f32 = 28.0;
const MARK_GAP: f32 = 16.0;

// The padding at both ends of a mark, the side of its icon, and the space
// between the icon and the words. The check before a finished status
// stands a little closer to its word.
const PAD: f32 = 20.0;
const ICON: f32 = 20.0;
const ICON_GAP: f32 = 10.0;
const CHECK_GAP: f32 = 8.0;

// The width of a resting mark's outline and of an icon's stroke.
const EDGE: f32 = 2.0;

/// The glyph a mark draws before its words.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Icon {
    /// A check: the work watched.
    Check,
    /// An arrow that turns back on itself: the work returned to its start.
    Undo,
}

/// Everything the line draws.
pub struct Line<'a> {
    /// The share of the work the bar fills, from 0 to 1.
    pub share: f32,
    /// The status words.
    pub status: &'a str,
    /// Whether the work is finished, which draws a check before the
    /// status.
    pub finished: bool,
    /// The marks, in order, each with its glyph and its words.
    pub marks: &'a [(Icon, &'static str)],
    /// The mark that holds focus, or nothing while focus is elsewhere.
    pub focus: Option<usize>,
    /// Whether the status stands over art, which draws it over a halo of
    /// dark copies. A line inside a scrim's full shade needs none.
    pub halo: bool,
}

/// The height the line adds under a playback row: the lead, the bar, the
/// lead under the bar, and the marks.
pub fn height() -> f32 {
    BAR_LEAD + progress::HEIGHT + LINE_LEAD + MARK_HEIGHT
}

/// The bar's box for a line whose band starts at `at`, the foot of the
/// playback row, this wide. A held bar is taller about the same middle.
pub fn bar(at: Point, width: f32, held: bool) -> Rectangle {
    let rest = area(at.x, at.y + BAR_LEAD, width, progress::HEIGHT);
    match held {
        false => rest,
        true => area(rest.x, rest.center_y() - BAR_HELD / 2.0, width, BAR_HELD),
    }
}

/// The band the status and the marks draw in, under the resting bar.
pub fn band(at: Point) -> Rectangle {
    area(
        at.x,
        at.y + BAR_LEAD + progress::HEIGHT + LINE_LEAD,
        0.0,
        MARK_HEIGHT,
    )
}

/// The width a mark takes around these words.
pub fn mark_width(words: &str) -> f32 {
    2.0 * PAD + ICON + ICON_GAP + text::measured(words, SIZE)
}

/// The width the status takes, its check included.
pub fn status_width(status: &str, finished: bool) -> f32 {
    let check = match finished {
        true => ICON + CHECK_GAP,
        false => 0.0,
    };
    check + text::measured(status, SIZE)
}

/// The boxes of the marks, after a status this wide, in a band that
/// starts at `band`. Each mark is as wide as its words.
pub fn marks(band: Rectangle, status: f32, widths: &[f32]) -> Vec<Rectangle> {
    let mut left = band.x + status + STATUS_GAP;
    widths
        .iter()
        .map(|width| {
            let shape = area(left, band.y, *width, MARK_HEIGHT);
            left += width + MARK_GAP;
            shape
        })
        .collect()
}

/// Draw the line under a playback row whose foot is at `at`, with a bar
/// this wide.
pub fn draw(frame: &mut canvas::Frame<Renderer>, line: &Line, at: Point, width: f32) {
    let held = line.focus.is_some();
    let track = bar(at, width, held);
    frame.fill_rectangle(track.position(), extent(track), track_color(held));
    let watched = area(
        track.x,
        track.y,
        track.width * line.share.clamp(0.0, 1.0),
        track.height,
    );
    if watched.width > 0.0 {
        frame.fill_rectangle(watched.position(), extent(watched), look::text());
    }

    let band = band(at);
    let ink = match held {
        true => look::text(),
        false => look::muted(),
    };
    let mut words_at = Point::new(band.x, band.center_y());
    if line.finished {
        icon(
            frame,
            Icon::Check,
            area(words_at.x, band.center_y() - ICON / 2.0, ICON, ICON),
            ink,
        );
        words_at.x += ICON + CHECK_GAP;
    }
    let status = |point: Point, color: Color| {
        label(
            line.status,
            point,
            SIZE,
            color,
            Alignment::Left,
            Vertical::Center,
            f32::INFINITY,
        )
    };
    if line.halo {
        for point in clock::halo(words_at) {
            frame.fill_text(status(point, look::BACKGROUND));
        }
    }
    frame.fill_text(status(words_at, ink));

    let widths: Vec<f32> = line
        .marks
        .iter()
        .map(|(_, words)| mark_width(words))
        .collect();
    let status = status_width(line.status, line.finished);
    for (index, ((glyph, words), shape)) in line
        .marks
        .iter()
        .zip(marks(band, status, &widths))
        .enumerate()
    {
        let focused = line.focus == Some(index);
        one_mark(frame, *glyph, words, shape, focused);
    }
}

// The track of the bar: faint at rest, brighter while a mark holds focus.
fn track_color(held: bool) -> Color {
    let muted = look::muted();
    match held {
        true => Color { a: 0.9, ..muted },
        false => look::faint(),
    }
}

// One mark in its box. At rest it is a translucent plate with a thin
// outline and muted words. Focused, it is a playback button: the button's
// fill, full ink, and the focus stroke outside it.
fn one_mark(
    frame: &mut canvas::Frame<Renderer>,
    glyph: Icon,
    words: &str,
    shape: Rectangle,
    focused: bool,
) {
    let slot = look::slot();
    let ink = match focused {
        true => {
            frame.fill_rectangle(shape.position(), extent(shape), slot);
            look::text()
        }
        false => {
            frame.fill_rectangle(shape.position(), extent(shape), Color { a: 0.8, ..slot });
            // The toolkit centres a stroke on its path, so the outline
            // follows a box inset by half its width to stay inside the
            // mark.
            let inset = area(
                shape.x + EDGE / 2.0,
                shape.y + EDGE / 2.0,
                shape.width - EDGE,
                shape.height - EDGE,
            );
            frame.stroke_rectangle(
                inset.position(),
                extent(inset),
                canvas::Stroke::default()
                    .with_color(Color {
                        a: 0.55,
                        ..look::muted()
                    })
                    .with_width(EDGE),
            );
            look::muted()
        }
    };
    let left = shape.x + PAD;
    icon(
        frame,
        glyph,
        area(left, shape.center_y() - ICON / 2.0, ICON, ICON),
        ink,
    );
    frame.fill_text(label(
        words,
        Point::new(left + ICON + ICON_GAP, shape.center_y()),
        SIZE,
        ink,
        Alignment::Left,
        Vertical::Center,
        f32::INFINITY,
    ));
    if focused {
        mark(frame, shape);
    }
}

// One glyph in its box, drawn on a grid of 24 units a side: the grid the
// brand's icons are drawn on, so the strokes match theirs.
fn icon(frame: &mut canvas::Frame<Renderer>, glyph: Icon, at: Rectangle, ink: Color) {
    let unit = at.width / 24.0;
    let point = |x: f32, y: f32| Point::new(at.x + x * unit, at.y + y * unit);
    let path = canvas::Path::new(|path| match glyph {
        Icon::Check => {
            path.move_to(point(5.0, 12.5));
            path.line_to(point(9.5, 17.0));
            path.line_to(point(19.0, 7.5));
        }
        // Three quarters and more of a circle, from its left edge round
        // under and up to the upper left, and the arrowhead at its start.
        Icon::Undo => {
            path.arc(canvas::path::Arc {
                center: point(12.0, 12.0),
                radius: 8.0 * unit,
                start_angle: Radians(std::f32::consts::PI),
                end_angle: Radians(-2.348),
            });
            path.move_to(point(4.0, 4.0));
            path.line_to(point(4.0, 8.5));
            path.line_to(point(8.5, 8.5));
        }
    });
    frame.stroke(
        &path,
        canvas::Stroke::default()
            .with_color(ink)
            .with_width(EDGE)
            .with_line_cap(canvas::LineCap::Round)
            .with_line_join(canvas::LineJoin::Round),
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    const AT: Point = Point::new(96.0, 555.0);

    #[test]
    fn the_resting_bar_stands_a_lead_under_the_row_at_the_width_it_is_given() {
        assert_eq!(bar(AT, 806.0, false), area(96.0, 573.0, 806.0, 4.0));
    }

    #[test]
    fn a_held_bar_is_twice_as_tall_about_the_same_middle() {
        let rest = bar(AT, 806.0, false);
        let held = bar(AT, 806.0, true);
        assert_eq!(held.height, 8.0);
        assert_eq!(held.center_y(), rest.center_y());
        assert_eq!((held.x, held.width), (rest.x, rest.width));
    }

    #[test]
    fn the_line_stands_under_the_bar_and_the_block_ends_at_its_foot() {
        let band = band(AT);
        assert_eq!(band.y, 591.0);
        assert_eq!(band.y + band.height, AT.y + height());
        assert_eq!(height(), 84.0);
    }

    #[test]
    fn the_marks_follow_the_status_a_gap_apart() {
        let shapes = marks(band(AT), 280.0, &[190.0, 200.0]);
        assert_eq!(shapes[0].x, 96.0 + 280.0 + STATUS_GAP);
        assert_eq!(shapes[1].x, shapes[0].x + 190.0 + MARK_GAP);
        assert_eq!(shapes[1].width, 200.0);
        assert_eq!(shapes[0].y, band(AT).y);
        assert_eq!(shapes[0].height, MARK_HEIGHT);
    }

    #[test]
    fn a_longer_word_takes_a_wider_mark_and_a_check_widens_the_status() {
        assert!(mark_width("Clear progress") > mark_width("Mark watched"));
        assert_eq!(
            status_width("Watched", true),
            status_width("Watched", false) + ICON + CHECK_GAP
        );
    }
}
