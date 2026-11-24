//! The scrubber bar at the bottom of the screen. It shows the playback
//! position and, for a film with chapters, one segment per chapter. The fine
//! axis moves the position in seconds, and the chapter axis steps one chapter
//! at a time. The caller says which axis has focus, and the bar draws the
//! parts of that axis brighter.

use iced::{Point, Rectangle, Size};
use serde_json::json;

use crate::canvas::{Anchor, Brush, Canvas, Line};
use crate::clock;
use crate::film::Film;
use crate::ipc::Command;
use crate::theme;

const LEFT: f32 = theme::MARGIN_X;
const BAR_Y: f32 = theme::BAR_Y;
const BAR_H: f32 = 14.0;
/// The gap between two segments, so each chapter reads as its own box.
const SEG_GAP: f32 = 4.0;
const SEG_R: f32 = 3.0;
/// The current time draws TIME_ABOVE over the bar's center line. The chapter
/// title and the time left draw BELOW_GAP under it.
const TIME_ABOVE: f32 = 18.0;
const BELOW_GAP: f32 = 18.0;
const TOP_Y: f32 = BAR_Y - TIME_ABOVE;
/// The top of the current time's text, the highest point the scrubber draws.
pub const TOP: f32 = TOP_Y - theme::type_scale::LABEL;
const BELOW_Y: f32 = BAR_Y + BELOW_GAP;
/// The playhead is taller than the bar, so it shows against the video above
/// and below the bar.
const KNOB_R: f32 = 15.0;
/// The dark outline keeps the green playhead visible against the green fill
/// under it.
const KNOB_BORDER: f32 = 2.0;

// The scan speed depends on elapsed time, not on the number of key events, so
// it does not change with the keyboard or controller repeat rate. The first
// press of a gesture moves the cursor TAP_STEP seconds. While the key stays
// down, the speed rises from MIN_RATE to MAX_RATE seconds of film per second
// of hold, and reaches MAX_RATE after RAMP seconds.
const TAP_STEP: f64 = 5.0;
const MIN_RATE: f64 = 30.0;
const MAX_RATE: f64 = 300.0;
const RAMP: f64 = 4.0;
/// Seconds between two presses after which the next press starts a new
/// gesture: a tap, with the speed ramp starting over.
const GAP: f64 = 0.25;

/// The axis of the bar that has focus. Its parts draw bright and the other
/// axis's parts draw dim.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Axis {
    Fine,
    Chapter,
}

/// The box of one chapter on the bar. A film with no chapters has one segment
/// across the whole bar.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Segment {
    pub x0: f32,
    pub w: f32,
}

/// A fine scan: the cursor, and the timing of the last presses, which sets
/// the scan speed.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Scrubber {
    cursor: Option<f64>,
    last_dir: f64,
    last_event: f64,
    hold_start: f64,
}

impl Scrubber {
    /// Moves the scan cursor in direction `dir` at time `now`, in seconds.
    /// Only `commit` seeks. While the scan runs the video keeps playing and
    /// the thumbnail shows the frame at the cursor. If each press seeked,
    /// the video would jump to the cursor and the thumbnail would show the
    /// same frame as the screen.
    pub fn seek(&mut self, dir: f64, now: f64, film: &Film) {
        let Some(duration) = length(film) else {
            return;
        };

        let gap = now - self.last_event;
        let mut cursor = self.cursor.unwrap_or_else(|| film.position.unwrap_or(0.0));

        if dir != self.last_dir || gap > GAP {
            self.hold_start = now;
            cursor += dir * TAP_STEP;
        } else {
            let held = now - self.hold_start;
            let rate = MIN_RATE + (MAX_RATE - MIN_RATE) * (held / RAMP).min(1.0);
            cursor += dir * rate * gap;
        }
        self.last_dir = dir;
        self.last_event = now;
        self.cursor = Some(cursor.clamp(0.0, duration));
    }

    /// Whether a scan is running. While it is, select and back go to the
    /// scan and the display shows the thumbnail.
    pub fn scanning(&self) -> bool {
        self.cursor.is_some()
    }

    /// Ends the scan and returns the mpv command that seeks to the cursor.
    /// The seek is exact, so playback resumes on the frame that the
    /// thumbnail showed.
    pub fn commit(&mut self) -> Vec<Command> {
        let Some(cursor) = self.cursor.take() else {
            return Vec::new();
        };
        self.last_dir = 0.0;
        vec![vec![json!("seek"), json!(cursor), json!("absolute+exact")]]
    }

    /// Ends the scan without a seek. Back and a focus move off the fine stop
    /// both call it.
    pub fn cancel(&mut self) {
        self.cursor = None;
        self.last_dir = 0.0;
    }

    /// The position to show, in seconds: the scan cursor, or the film's
    /// position when no scan is running. It is `None` when the film has no
    /// duration. The thumbnail uses it to pick its frame.
    pub fn cursor_time(&self, film: &Film) -> Option<f64> {
        Some(self.displayed(film, length(film)?))
    }

    /// The playhead's x in canvas coordinates, the same value `bar` returns
    /// as `head`, or `None` when the film has no duration. The thumbnail
    /// centers on it.
    pub fn cursor_x(&self, canvas: &Canvas, film: &Film) -> Option<f32> {
        let duration = length(film)?;
        Some(canvas.snap(position_x(canvas, duration, self.displayed(film, duration))))
    }

    /// The cursor while a scan runs, otherwise the film's position. The
    /// playhead follows the cursor, so it moves on each press.
    fn displayed(&self, film: &Film, duration: f64) -> f64 {
        self.cursor
            .or(film.position)
            .unwrap_or(0.0)
            .clamp(0.0, duration)
    }

    /// Builds everything the bar draws, in canvas units, or `None` when the
    /// film has no duration. `axis` is the axis that has focus, if any.
    pub fn bar(&self, canvas: &Canvas, film: &Film, axis: Option<Axis>) -> Option<Bar> {
        let duration = length(film)?;
        let (position, chapter) = brightness(axis);
        let at = self.displayed(film, duration);
        let head = canvas.snap(position_x(canvas, duration, at));

        Some(Bar {
            segments: segments(canvas, film, duration),
            head,
            live: self.cursor.map(|_| {
                canvas.snap(position_x(
                    canvas,
                    duration,
                    film.position.unwrap_or(0.0).clamp(0.0, duration),
                ))
            }),
            current: film
                .chapter
                .filter(|_| axis == Some(Axis::Chapter) && !film.chapters.is_empty()),
            time: Line::new(
                fmt(at),
                Point::new(head.clamp(LEFT, canvas.right()), TOP_Y),
                Anchor::BottomCentre,
                theme::type_scale::LABEL,
                theme::color::text(),
            )
            .alpha(position),
            chapter: chapter_line(film).map(|label| {
                Line::new(
                    label,
                    Point::new(LEFT, BELOW_Y),
                    Anchor::TopLeft,
                    theme::type_scale::SMALL,
                    theme::color::text(),
                )
                .alpha(chapter)
            }),
            times: Line::new(
                time_line(at, duration),
                Point::new(canvas.right(), BELOW_Y),
                Anchor::TopRight,
                theme::type_scale::SMALL,
                theme::color::text(),
            )
            .alpha(position),
            position,
            chapter_alpha: chapter,
        })
    }

    /// Draws a bar that `bar` built. The caller builds the bar once for each
    /// frame.
    pub fn draw(&self, brush: &mut Brush<'_>, bar: &Bar) {
        let canvas = brush.canvas();
        let top = BAR_Y - BAR_H / 2.0;

        for (index, segment) in bar.segments.iter().enumerate() {
            // The track covers the whole segment, and the fill covers it
            // up to the playhead.
            brush.rounded(
                Rectangle::new(Point::new(segment.x0, top), Size::new(segment.w, BAR_H)),
                SEG_R,
                theme::at(theme::color::track(), theme::alpha::TRACK),
            );
            // The fill ends at the playhead's snapped x, so it changes only
            // when the playhead moves to another output pixel.
            let filled = bar.head.clamp(segment.x0, segment.x0 + segment.w) - segment.x0;
            if filled >= canvas.to_canvas(1.0) {
                brush.rounded(
                    Rectangle::new(Point::new(segment.x0, top), Size::new(filled, BAR_H)),
                    SEG_R,
                    theme::at(theme::color::fill(), bar.position),
                );
            }
            // On the chapter axis, the current chapter's whole segment is
            // filled, brighter than the progress fill.
            if bar.current == Some(index as i64) {
                brush.rounded(
                    Rectangle::new(Point::new(segment.x0, top), Size::new(segment.w, BAR_H)),
                    SEG_R,
                    theme::at(theme::color::fill(), bar.chapter_alpha),
                );
            }
        }

        // During a scan the playhead shows the cursor, so a thin tick marks
        // where the video is playing. The playhead draws after the tick, on
        // top of it.
        if let Some(live) = bar.live {
            brush.rect(
                Rectangle::new(
                    Point::new(live - 1.0, top - 6.0),
                    Size::new(2.0, BAR_H + 12.0),
                ),
                theme::at(theme::color::text(), theme::alpha::SUBDUED),
            );
        }

        // The playhead is a regular hexagon at any screen shape, because the
        // canvas has the screen's aspect ratio and scales both axes equally.
        brush.hexagon(
            Point::new(bar.head - KNOB_R, BAR_Y - KNOB_R),
            KNOB_R,
            theme::at(theme::color::playhead(), bar.position),
            KNOB_BORDER,
            theme::at(theme::color::SHADOW, bar.position),
        );

        brush.text(bar.time.clone());
        if let Some(chapter) = bar.chapter.clone() {
            brush.text(chapter);
        }
        brush.text(bar.times.clone());
    }
}

/// The geometry and text of one frame of the bar.
#[derive(Debug, Clone, PartialEq)]
pub struct Bar {
    pub segments: Vec<Segment>,
    /// The playhead's x, snapped to the output pixel grid.
    pub head: f32,
    /// The x of the tick at the playback position, set only during a scan.
    pub live: Option<f32>,
    /// The index of the chapter to highlight, set only when the chapter axis
    /// has focus.
    pub current: Option<i64>,
    /// The current time over the playhead. Its x is clamped to the bar, so
    /// the text stays on screen at both ends.
    pub time: Line,
    pub chapter: Option<Line>,
    /// The time line under the bar's right end: position and time left.
    pub times: Line,
    pub position: f32,
    pub chapter_alpha: f32,
}

/// Whether the fine stop applies: the film has a duration.
pub fn fine_available(film: &Film) -> bool {
    length(film).is_some()
}

/// Whether the chapter stop applies: the film has chapters.
pub fn chapter_available(film: &Film) -> bool {
    !film.chapters.is_empty()
}

/// The mpv command that moves playback `dir` chapters from the current one.
/// It returns no command at either end of the chapter list. In mpv, a step
/// past the last chapter ends the file, and ending playback is the back
/// key's job.
pub fn chapter_step(dir: i64, film: &Film) -> Vec<Command> {
    let Some(at) = film.chapter else {
        return Vec::new();
    };
    if film.chapters.is_empty() {
        return Vec::new();
    }
    let next = at + dir;
    if next < 0 || next > film.chapters.len() as i64 - 1 {
        return Vec::new();
    }
    vec![vec![json!("add"), json!("chapter"), json!(dir)]]
}

/// The output pixel column of position `at`, or `None` when the film has no
/// duration or `at` is `None`. The display compares it with the last column
/// to skip a redraw when the playhead has not moved a pixel.
pub fn position_pixel(canvas: &Canvas, film: &Film, at: Option<f64>) -> Option<i32> {
    Some(pixel_at(canvas, length(film)?, at?))
}

/// The same column, for a caller that already has the duration.
pub fn pixel_at(canvas: &Canvas, duration: f64, at: f64) -> i32 {
    let at = at.clamp(0.0, duration);
    canvas.to_pixels(position_x(canvas, duration, at)) as i32
}

fn length(film: &Film) -> Option<f64> {
    film.duration.filter(|duration| *duration > 0.0)
}

fn bar_width(canvas: &Canvas) -> f32 {
    canvas.right() - LEFT
}

fn position_x(canvas: &Canvas, duration: f64, at: f64) -> f32 {
    LEFT + bar_width(canvas) * (at / duration) as f32
}

/// One segment per chapter, each ending SEG_GAP before the next, or one
/// segment across the bar when the film has no chapters.
pub fn segments(canvas: &Canvas, film: &Film, duration: f64) -> Vec<Segment> {
    if film.chapters.is_empty() {
        return vec![Segment {
            x0: LEFT,
            w: bar_width(canvas),
        }];
    }
    film.chapters
        .iter()
        .enumerate()
        .map(|(index, chapter)| {
            let finish = film
                .chapters
                .get(index + 1)
                .map_or(duration, |next| next.time);
            let x0 = position_x(canvas, duration, chapter.time);
            let x1 = position_x(canvas, duration, finish);
            Segment {
                x0,
                // The minimum width keeps a very short chapter visible as a
                // division.
                w: (x1 - x0 - SEG_GAP).max(canvas.to_canvas(2.0)),
            }
        })
        .collect()
}

/// The alpha of the position parts and of the chapter parts. The position
/// parts are the playhead, the times, and the fill. The chapter parts are the
/// current chapter's segment and its title. Only a focused axis is bright.
fn brightness(axis: Option<Axis>) -> (f32, f32) {
    match axis {
        Some(Axis::Fine) => (theme::alpha::OPAQUE, theme::alpha::SUBDUED),
        Some(Axis::Chapter) => (theme::alpha::SUBDUED, theme::alpha::OPAQUE),
        None => (theme::alpha::SUBDUED, theme::alpha::SUBDUED),
    }
}

fn chapter_line(film: &Film) -> Option<String> {
    if film.chapters.is_empty() {
        return None;
    }
    let at = film.chapter.unwrap_or(0);
    let label = format!("{} of {}", at + 1, film.chapters.len());
    Some(
        match film.current_chapter().and_then(|at| at.title.as_deref()) {
            Some(title) => format!("{title}   \u{00B7}   {label}"),
            None => label,
        },
    )
}

fn fmt(at: f64) -> String {
    let at = (at + 0.5).floor() as i64;
    let hours = at / 3600;
    let minutes = (at % 3600) / 60;
    let seconds = at % 60;
    if hours > 0 {
        return format!("{hours}:{minutes:02}:{seconds:02}");
    }
    format!("{minutes}:{seconds:02}")
}

/// The time line, such as "30:07  ·  1h 36m left". `clock::length` rounds
/// the time left to the nearest minute. The end time is not here, because
/// the clock at the top right draws it.
fn time_line(at: f64, duration: f64) -> String {
    let left = duration - at;
    let phrase = clock::length(left).map_or_else(
        || "less than a minute left".to_string(),
        |length| format!("{length} left"),
    );
    [fmt(at), phrase].join(crate::header::BETWEEN)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn film() -> Film {
        let mut film = Film::default();
        film.apply("duration", &json!(6000.0));
        film.apply("time-pos", &json!(1200.0));
        film
    }

    fn chaptered() -> Film {
        let mut film = film();
        film.apply("chapter", &json!(1));
        film.apply(
            "chapter-list",
            &json!([
                { "title": "Opening", "time": 0.0 },
                { "title": "The road", "time": 1500.0 },
                { "title": "", "time": 4500.0 },
            ]),
        );
        film
    }

    #[test]
    fn the_first_press_moves_the_cursor_five_seconds() {
        let mut scrubber = Scrubber::default();
        scrubber.seek(1.0, 10.0, &film());
        assert_eq!(scrubber.cursor_time(&film()), Some(1205.0));

        let mut back = Scrubber::default();
        back.seek(-1.0, 10.0, &film());
        assert_eq!(back.cursor_time(&film()), Some(1195.0));
    }

    /// A press more than GAP after the last one moves TAP_STEP again.
    #[test]
    fn a_press_after_the_gap_moves_five_seconds_again() {
        let mut scrubber = Scrubber::default();
        scrubber.seek(1.0, 10.0, &film());
        scrubber.seek(1.0, 10.3, &film());
        assert_eq!(scrubber.cursor_time(&film()), Some(1210.0));
    }

    #[test]
    fn a_press_in_the_other_direction_moves_five_seconds_back() {
        let mut scrubber = Scrubber::default();
        scrubber.seek(1.0, 10.0, &film());
        scrubber.seek(-1.0, 10.1, &film());
        assert_eq!(scrubber.cursor_time(&film()), Some(1200.0));
    }

    /// The presses arrive 0.1 seconds apart, like a controller's key repeat.
    #[test]
    fn a_hold_ramps_from_thirty_to_three_hundred_seconds_per_second() {
        for (held, rate) in [(0.5, 63.75), (2.0, 165.0), (4.0, 300.0), (6.0, 300.0)] {
            let mut scrubber = Scrubber::default();
            scrubber.seek(1.0, 100.0, &film());
            let mut moved = 0.0;
            for step in 1..=(held * 10.0) as i64 {
                let before = scrubber
                    .cursor_time(&film())
                    .expect("the scrubber has a cursor while scanning");
                scrubber.seek(1.0, 100.0 + step as f64 / 10.0, &film());
                moved = scrubber
                    .cursor_time(&film())
                    .expect("the cursor has a time after the scan step")
                    - before;
            }
            assert!(
                (moved - rate / 10.0).abs() < 0.05,
                "holding for {held} seconds moved the cursor {moved} seconds, want {}",
                rate / 10.0
            );
        }
    }

    #[test]
    fn a_scan_cursor_stays_inside_the_film() {
        let mut near_the_start = film();
        near_the_start.apply("time-pos", &json!(2.0));
        let mut scrubber = Scrubber::default();
        scrubber.seek(-1.0, 10.0, &near_the_start);
        assert_eq!(scrubber.cursor_time(&near_the_start), Some(0.0));

        let mut near_the_end = film();
        near_the_end.apply("time-pos", &json!(5998.0));
        let mut forward = Scrubber::default();
        forward.seek(1.0, 10.0, &near_the_end);
        assert_eq!(forward.cursor_time(&near_the_end), Some(6000.0));
    }

    #[test]
    fn a_film_with_no_duration_has_no_scan_and_no_bar() {
        let mut scrubber = Scrubber::default();
        scrubber.seek(1.0, 10.0, &Film::default());
        assert!(!scrubber.scanning());
        assert!(scrubber.commit().is_empty());
        assert_eq!(scrubber.cursor_time(&Film::default()), None);
        assert_eq!(
            scrubber.cursor_x(&Canvas::default(), &Film::default()),
            None
        );
        assert_eq!(
            scrubber.bar(&Canvas::default(), &Film::default(), None),
            None
        );
    }

    #[test]
    fn a_commit_seeks_to_the_cursor_and_a_cancel_does_not_seek() {
        let mut scrubber = Scrubber::default();
        scrubber.seek(1.0, 10.0, &film());
        assert!(scrubber.scanning());
        assert_eq!(
            scrubber.commit(),
            vec![vec![json!("seek"), json!(1205.0), json!("absolute+exact")]]
        );
        assert!(!scrubber.scanning());

        scrubber.seek(1.0, 20.0, &film());
        scrubber.cancel();
        assert!(!scrubber.scanning());
        assert!(scrubber.commit().is_empty());
    }

    #[test]
    fn a_chapter_step_does_nothing_at_the_ends_of_the_list() {
        let mut film = chaptered();
        assert_eq!(
            chapter_step(1, &film),
            vec![vec![json!("add"), json!("chapter"), json!(1)]]
        );
        assert_eq!(
            chapter_step(-1, &film),
            vec![vec![json!("add"), json!("chapter"), json!(-1)]]
        );

        film.apply("chapter", &json!(0));
        assert!(chapter_step(-1, &film).is_empty());
        film.apply("chapter", &json!(2));
        assert!(chapter_step(1, &film).is_empty());

        assert!(chapter_step(1, &super::tests::film()).is_empty());
    }

    #[test]
    fn a_film_with_no_chapters_draws_one_segment() {
        let segments = segments(&Canvas::default(), &film(), 6000.0);
        assert_eq!(
            segments,
            vec![Segment {
                x0: 96.0,
                w: 1728.0
            }]
        );
    }

    #[test]
    fn each_chapter_segment_ends_a_gap_before_the_next() {
        let segments = segments(&Canvas::default(), &chaptered(), 6000.0);
        assert_eq!(segments.len(), 3);
        assert_eq!(segments[0], Segment { x0: 96.0, w: 428.0 });
        assert_eq!(
            segments[1],
            Segment {
                x0: 528.0,
                w: 860.0
            }
        );
        assert_eq!(
            segments[2],
            Segment {
                x0: 1392.0,
                w: 428.0
            }
        );
    }

    #[test]
    fn a_chapter_shorter_than_the_gap_is_two_pixels_wide() {
        let mut film = film();
        film.apply(
            "chapter-list",
            &json!([{ "time": 0.0 }, { "time": 1.0 }, { "time": 2.0 }]),
        );
        let segments = segments(&Canvas::default(), &film, 6000.0);
        assert_eq!(segments[0].w, 2.0);
        assert_eq!(segments[1].w, 2.0);
    }

    #[test]
    fn only_the_focused_axis_is_bright() {
        assert_eq!(
            brightness(Some(Axis::Fine)),
            (theme::alpha::OPAQUE, theme::alpha::SUBDUED)
        );
        assert_eq!(
            brightness(Some(Axis::Chapter)),
            (theme::alpha::SUBDUED, theme::alpha::OPAQUE)
        );
        assert_eq!(
            brightness(None),
            (theme::alpha::SUBDUED, theme::alpha::SUBDUED)
        );
    }

    #[test]
    fn a_time_shows_hours_only_from_one_hour() {
        assert_eq!(fmt(0.0), "0:00");
        assert_eq!(fmt(0.4), "0:00");
        assert_eq!(fmt(0.5), "0:01");
        assert_eq!(fmt(59.6), "1:00");
        assert_eq!(fmt(1200.0), "20:00");
        assert_eq!(fmt(3599.0), "59:59");
        assert_eq!(fmt(3600.0), "1:00:00");
        assert_eq!(fmt(7265.0), "2:01:05");
    }

    #[test]
    fn the_time_line_shows_the_position_and_the_minutes_left() {
        assert_eq!(time_line(1807.0, 7559.0), "30:07  \u{00B7}  1h 36m left");
        assert_eq!(time_line(5970.0, 6000.0), "1:39:30  \u{00B7}  1m left");
        assert_eq!(
            time_line(6000.0, 6000.0),
            "1:40:00  \u{00B7}  less than a minute left"
        );
    }

    #[test]
    fn the_bar_places_each_part_for_a_film_partway_through() {
        let bar = Scrubber::default()
            .bar(&Canvas::default(), &chaptered(), Some(Axis::Fine))
            .expect("a film with a length has a bar");

        assert_eq!(bar.head, 442.0);
        assert_eq!(bar.live, None);
        assert_eq!(bar.current, None);
        assert_eq!(bar.time.content, "20:00");
        assert_eq!(bar.time.at, Point::new(442.0, 910.0));
        assert_eq!(bar.time.anchor, Anchor::BottomCentre);
        assert_eq!(bar.time.size, 40.0);
        assert_eq!(bar.time.color.a, theme::alpha::OPAQUE);

        let chapter = bar.chapter.expect("a chaptered film has a chapter line");
        assert_eq!(chapter.content, "The road   \u{00B7}   2 of 3");
        assert_eq!(chapter.at, Point::new(96.0, 946.0));
        assert_eq!(chapter.anchor, Anchor::TopLeft);
        assert_eq!(chapter.size, 34.0);
        assert_eq!(chapter.color.a, theme::alpha::SUBDUED);

        assert_eq!(bar.times.content, "20:00  \u{00B7}  1h 20m left");
        assert_eq!(bar.times.at, Point::new(1824.0, 946.0));
        assert_eq!(bar.times.anchor, Anchor::TopRight);
        assert_eq!(bar.times.color.a, theme::alpha::OPAQUE);
    }

    /// 0.0417 seconds is one frame at 24 frames per second.
    #[test]
    fn a_move_under_one_pixel_leaves_the_bar_unchanged() {
        let canvas = Canvas::default();
        let bar = |at: f64| {
            let mut film = film();
            film.apply("time-pos", &json!(at));
            Scrubber::default().bar(&canvas, &film, Some(Axis::Fine))
        };

        assert_eq!(bar(1200.0), bar(1200.0417));
        assert_ne!(bar(1200.0), bar(1204.0));
        assert_eq!(
            bar(1200.0).unwrap().time.content,
            bar(1200.0417).unwrap().time.content
        );
    }

    #[test]
    fn a_chapter_with_no_title_shows_only_its_number() {
        let mut film = chaptered();
        film.apply("chapter", &json!(2));
        assert_eq!(chapter_line(&film).as_deref(), Some("3 of 3"));
        assert_eq!(chapter_line(&super::tests::film()), None);
    }

    #[test]
    fn a_scan_draws_the_playhead_at_the_cursor_and_the_tick_at_playback() {
        let mut scrubber = Scrubber::default();
        scrubber.seek(1.0, 10.0, &film());
        let bar = scrubber
            .bar(&Canvas::default(), &film(), Some(Axis::Fine))
            .expect("a film with a length has a bar");
        assert_eq!(bar.head, 443.0);
        assert_eq!(bar.live, Some(442.0));
        assert_eq!(bar.time.content, "20:05");
    }

    #[test]
    fn only_the_chapter_axis_highlights_the_current_chapter() {
        let canvas = Canvas::default();
        let scrubber = Scrubber::default();
        assert_eq!(
            scrubber
                .bar(&canvas, &chaptered(), Some(Axis::Chapter))
                .unwrap()
                .current,
            Some(1)
        );
        assert_eq!(
            scrubber
                .bar(&canvas, &chaptered(), Some(Axis::Fine))
                .unwrap()
                .current,
            None
        );
        assert_eq!(
            scrubber
                .bar(&canvas, &film(), Some(Axis::Chapter))
                .unwrap()
                .current,
            None
        );
    }

    #[test]
    fn the_time_label_stays_inside_the_bar() {
        let canvas = Canvas::default();
        let mut film = film();
        film.apply("time-pos", &json!(0.0));
        let bar = Scrubber::default().bar(&canvas, &film, None).unwrap();
        assert_eq!(bar.time.at.x, 96.0);

        film.apply("time-pos", &json!(6000.0));
        let bar = Scrubber::default().bar(&canvas, &film, None).unwrap();
        assert_eq!(bar.time.at.x, 1824.0);
        assert_eq!(
            bar.times.content,
            "1:40:00  \u{00B7}  less than a minute left"
        );
    }

    #[test]
    fn the_playhead_is_on_a_whole_output_pixel() {
        let half = Canvas::for_output(Size::new(1280.0, 720.0));
        let mut film = film();
        film.apply("time-pos", &json!(1234.5));
        let bar = Scrubber::default().bar(&half, &film, None).unwrap();
        assert_eq!((bar.head * half.scale()).fract(), 0.0);

        assert_eq!(
            position_pixel(&Canvas::default(), &film, Some(1234.5)),
            Some(452)
        );
        assert_eq!(position_pixel(&half, &film, Some(1234.5)), Some(301));
        assert_eq!(position_pixel(&Canvas::default(), &film, None), None);
        assert_eq!(
            position_pixel(&Canvas::default(), &Film::default(), Some(1.0)),
            None
        );
    }

    #[test]
    fn a_position_outside_the_film_clamps_to_the_ends_of_the_bar() {
        let canvas = Canvas::default();
        assert_eq!(position_pixel(&canvas, &film(), Some(-10.0)), Some(96));
        assert_eq!(position_pixel(&canvas, &film(), Some(9000.0)), Some(1824));
    }

    /// The fine stop needs a duration and the chapter stop needs chapters.
    #[test]
    fn a_stop_is_available_only_when_the_film_has_what_it_moves() {
        assert!(fine_available(&film()));
        assert!(!fine_available(&Film::default()));
        assert!(chapter_available(&chaptered()));
        assert!(!chapter_available(&film()));
    }

    #[test]
    fn the_cursor_time_and_x_follow_the_scan() {
        let canvas = Canvas::default();
        let mut scrubber = Scrubber::default();
        assert_eq!(scrubber.cursor_time(&film()), Some(1200.0));
        assert_eq!(scrubber.cursor_x(&canvas, &film()), Some(442.0));

        scrubber.seek(1.0, 10.0, &film());
        assert_eq!(scrubber.cursor_time(&film()), Some(1205.0));
        assert_eq!(scrubber.cursor_x(&canvas, &film()), Some(443.0));
    }
}
