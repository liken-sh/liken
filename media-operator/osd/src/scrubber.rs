//! The scrubber. It merges the fine seek and the chapter step into one
//! segmented bar. One segment per chapter marks the coarse axis, and the
//! playhead and its time mark the fine axis. It owns both axes and draws once
//! per frame, told which axis has focus.

use iced::{Point, Rectangle, Size};
use serde_json::json;

use crate::canvas::{Anchor, Brush, Canvas, Line};
use crate::film::Film;
use crate::ipc::Command;
use crate::theme;

// geometry
const LEFT: f32 = theme::MARGIN_X;
const BAR_Y: f32 = theme::BAR_Y;
const BAR_H: f32 = 14.0;
/// The gap between two segments, so the divisions read as separate chapters.
const SEG_GAP: f32 = 4.0;
const SEG_R: f32 = 3.0;
/// The time sits this far above the bar center, and the line below sits this
/// far under it. The gaps are named, so the vertical spacing is one place to
/// tune.
const TIME_ABOVE: f32 = 18.0;
const BELOW_GAP: f32 = 18.0;
/// The current time rides above the bar; the chapter title and the time left
/// sit below it.
const TOP_Y: f32 = BAR_Y - TIME_ABOVE;
const BELOW_Y: f32 = BAR_Y + BELOW_GAP;
/// The playhead stands proud of the bar, so its points read against the video
/// above and below the green fill it rides on.
const KNOB_R: f32 = 15.0;
/// The dark outline separates the green playhead from the green fill under it,
/// so the head reads against the fill.
const KNOB_BORDER: f32 = 2.0;

// scan and acceleration
// The scan is time based, not press based, so the speed does not depend on the
// keyboard or the controller repeat rate. The first press of a gesture moves
// the cursor TAP_STEP seconds. A hold ramps the speed from MIN_RATE to
// MAX_RATE seconds of film per second of hold, reaching MAX_RATE after RAMP
// seconds.
const TAP_STEP: f64 = 5.0;
const MIN_RATE: f64 = 30.0;
const MAX_RATE: f64 = 300.0;
const RAMP: f64 = 4.0;
/// A gap longer than this ends the gesture, so the next press is a tap and the
/// ramp starts again.
const GAP: f64 = 0.25;

/// Which of the bar's two axes has focus. The bar is one drawing, and the axis
/// brightens one group of its parts and subdues the other.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Axis {
    Fine,
    Chapter,
}

/// One chapter's box on the bar, and the whole bar when the film has no
/// chapters.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Segment {
    pub x0: f32,
    pub x1: f32,
    pub w: f32,
}

/// The scan in flight, and the gesture clock the ramp reads.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct Scrubber {
    cursor: Option<f64>,
    last_dir: f64,
    last_event: f64,
    hold_start: f64,
}

impl Scrubber {
    /// A fine scan previews a target without moving the video. left and right
    /// move the cursor, and the thumbnail shows the frame there, while the
    /// video plays on at its own position. So the preview is the only thing
    /// that moves, and the scan costs one crop per tile and no seek. A select
    /// commits the seek, and a cancel drops the cursor. Without this the video
    /// would chase the cursor, and the thumbnail would show the frame already
    /// on screen.
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

    /// Whether a scan is in flight, so the router sends select and back to the
    /// scan, and the display shows the thumbnail only then.
    pub fn scanning(&self) -> bool {
        self.cursor.is_some()
    }

    /// Land the seek on the previewed frame, so a select ends the scan at the
    /// target. The exact seek lands the frame the thumbnail showed.
    pub fn commit(&mut self) -> Vec<Command> {
        let Some(cursor) = self.cursor.take() else {
            return Vec::new();
        };
        self.last_dir = 0.0;
        vec![vec![json!("seek"), json!(cursor), json!("absolute+exact")]]
    }

    /// Drop the preview with no seek, so a back leaves the video where it
    /// plays. Leaving the fine stop cancels the scan the same way.
    pub fn cancel(&mut self) {
        self.cursor = None;
        self.last_dir = 0.0;
    }

    /// The displayed position in seconds, the in-flight cursor or the film's
    /// own position, and nothing with no duration. The thumbnail reads it to
    /// pick the frame to show.
    pub fn cursor_time(&self, film: &Film) -> Option<f64> {
        Some(self.displayed(film, length(film)?))
    }

    /// The playhead x in canvas coordinates, the same value the draw computes,
    /// and nothing with no duration. The thumbnail centers on it.
    pub fn cursor_x(&self, canvas: &Canvas, film: &Film) -> Option<f32> {
        let duration = length(film)?;
        Some(canvas.snap(position_x(canvas, duration, self.displayed(film, duration))))
    }

    /// Draw from the cursor while a seek is in flight, so the playhead leads
    /// the debounced seek. Fall back to the film's position after the reset.
    /// Without this the playhead stutters behind the seeks.
    fn displayed(&self, film: &Film, duration: f64) -> f64 {
        self.cursor
            .or(film.position)
            .unwrap_or(0.0)
            .clamp(0.0, duration)
    }

    /// Everything the bar draws at one position, in canvas units. A film with
    /// no duration draws no bar.
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
                Point::new(head.clamp(LEFT, right(canvas)), TOP_Y),
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
            remaining: Line::new(
                format!(
                    "{}{}{}",
                    remaining_phrase(duration - at),
                    crate::header::BETWEEN,
                    fmt(duration)
                ),
                Point::new(right(canvas), BELOW_Y),
                Anchor::TopRight,
                theme::type_scale::SMALL,
                theme::color::text(),
            )
            .alpha(position),
            position,
            chapter_alpha: chapter,
        })
    }

    pub fn draw(&self, brush: &mut Brush<'_>, film: &Film, axis: Option<Axis>) {
        let canvas = brush.canvas();
        let Some(bar) = self.bar(&canvas, film, axis) else {
            return;
        };
        let top = BAR_Y - BAR_H / 2.0;

        for (index, segment) in bar.segments.iter().enumerate() {
            // The deep green track box marks the segment. The bright green
            // fill covers it up to the playhead, so a segment before the head
            // fills whole, the segment under the head fills part way, and a
            // segment after stays the darker track green.
            brush.rounded(
                Rectangle::new(Point::new(segment.x0, top), Size::new(segment.w, BAR_H)),
                SEG_R,
                theme::color::track(),
                theme::alpha::TRACK,
            );
            // The fill ends at the playhead, so it moves on the same pixel grid
            // and its width holds still between two frames of the same pixel.
            let filled = bar.head.clamp(segment.x0, segment.x0 + segment.w) - segment.x0;
            if filled >= 1.0 {
                brush.rounded(
                    Rectangle::new(Point::new(segment.x0, top), Size::new(filled, BAR_H)),
                    SEG_R,
                    theme::color::fill(),
                    bar.position,
                );
            }
            // The chapter axis marks the current chapter by filling its whole
            // segment green, brighter than the progress fill under it.
            if bar.current == Some(index as i64) {
                brush.rounded(
                    Rectangle::new(Point::new(segment.x0, top), Size::new(segment.w, BAR_H)),
                    SEG_R,
                    theme::color::fill(),
                    bar.chapter_alpha,
                );
            }
        }

        // During a scan the video plays on while the cursor previews elsewhere,
        // so a thin tick marks where playback is. The playhead draws after it,
        // so the two read apart when they overlap.
        if let Some(live) = bar.live {
            brush.rect(
                Rectangle::new(
                    Point::new(live - 1.0, top - 6.0),
                    Size::new(2.0, BAR_H + 12.0),
                ),
                theme::color::text(),
                theme::alpha::SUBDUED,
            );
        }

        // The playhead reads as a regular hexagon at any window shape, because
        // the canvas holds the screen's own ratio and the two axes scale alike.
        brush.hexagon(
            Point::new(bar.head - KNOB_R, BAR_Y - KNOB_R),
            KNOB_R,
            theme::color::playhead(),
            bar.position,
            KNOB_BORDER,
            theme::color::SHADOW,
        );

        // The current time rides above the playhead and moves with it, so the
        // eye reads the position where it is already looking. The x is clamped
        // to the bar, so the label stays on screen at either end.
        brush.text(bar.time);
        // Below the bar on the left, the chapter title and position. Below on
        // the right, the plain-language time left and the exact total length.
        if let Some(chapter) = bar.chapter {
            brush.text(chapter);
        }
        brush.text(bar.remaining);
    }
}

/// What one draw of the bar puts on the screen.
#[derive(Debug, Clone, PartialEq)]
pub struct Bar {
    pub segments: Vec<Segment>,
    /// The playhead's x, snapped to the output pixel grid.
    pub head: f32,
    /// Where playback stands while a scan previews elsewhere.
    pub live: Option<f32>,
    /// The chapter the coarse axis marks, when that axis has focus.
    pub current: Option<i64>,
    pub time: Line,
    pub chapter: Option<Line>,
    pub remaining: Line,
    pub position: u8,
    pub chapter_alpha: u8,
}

/// The fine stop shows for a film with a length.
pub fn fine_available(film: &Film) -> bool {
    length(film).is_some()
}

/// The chapter stop shows for a film that carries chapters.
pub fn chapter_available(film: &Film) -> bool {
    !film.chapters.is_empty()
}

/// Stepping a chapter moves the playback position, so the fine playhead
/// follows. This is the coarse seek axis, one chapter a press, on the same bar
/// as the fine scrubber that moves in seconds.
/// The ends of the list hold. A step past the last chapter would end the run,
/// and ending the run is what back is for.
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

/// The output pixel column a position lands on, and nothing with no duration.
/// The frame loop reads it to tell whether a new position moves the bar at all.
pub fn position_pixel(canvas: &Canvas, film: &Film, at: Option<f64>) -> Option<i32> {
    let duration = length(film)?;
    let at = at?.clamp(0.0, duration);
    Some((position_x(canvas, duration, at) * canvas.scale + 0.5).floor() as i32)
}

fn length(film: &Film) -> Option<f64> {
    film.duration.filter(|duration| *duration > 0.0)
}

fn right(canvas: &Canvas) -> f32 {
    canvas.width - theme::MARGIN_X
}

fn bar_width(canvas: &Canvas) -> f32 {
    right(canvas) - LEFT
}

fn position_x(canvas: &Canvas, duration: f64, at: f64) -> f32 {
    LEFT + bar_width(canvas) * (at / duration) as f32
}

/// The segments for the bar. With chapters, one box per chapter with a gap on
/// its right. With no chapters, one continuous box across the full width.
pub fn segments(canvas: &Canvas, film: &Film, duration: f64) -> Vec<Segment> {
    if film.chapters.is_empty() {
        return vec![Segment {
            x0: LEFT,
            x1: right(canvas),
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
                x1,
                w: (x1 - x0 - SEG_GAP).max(2.0),
            }
        })
        .collect()
}

/// The two focus axes brighten different groups. The position group is the
/// playhead, its time, and the progress fill. The chapter group is the current
/// chapter segment and the title. The axis argument brightens one group and
/// subdues the other, and no axis subdues both.
fn brightness(axis: Option<Axis>) -> (u8, u8) {
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

/// The remaining time in whole minutes, phrased for a person. Under a minute
/// reads as such, and one minute drops the plural.
fn remaining_phrase(left: f64) -> String {
    let minutes = (left / 60.0 + 0.5).floor() as i64;
    if minutes <= 0 {
        return "less than a minute remaining".to_string();
    }
    if minutes == 1 {
        return "1 minute remaining".to_string();
    }
    format!("{minutes} minutes remaining")
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

    /// The first press of a gesture moves the cursor five seconds, whichever
    /// way it goes.
    #[test]
    fn the_first_press_of_a_gesture_taps_five_seconds() {
        let mut scrubber = Scrubber::default();
        scrubber.seek(1.0, 10.0, &film());
        assert_eq!(scrubber.cursor, Some(1205.0));

        let mut back = Scrubber::default();
        back.seek(-1.0, 10.0, &film());
        assert_eq!(back.cursor, Some(1195.0));
    }

    /// A press after a longer gap than the gesture allows starts a new
    /// gesture, and taps again from where the cursor stands.
    #[test]
    fn a_press_after_the_gap_taps_again() {
        let mut scrubber = Scrubber::default();
        scrubber.seek(1.0, 10.0, &film());
        scrubber.seek(1.0, 10.3, &film());
        assert_eq!(scrubber.cursor, Some(1210.0));
    }

    /// A turn moves one tap in the new direction, however fast the presses
    /// arrive.
    #[test]
    fn a_turn_taps_the_other_way() {
        let mut scrubber = Scrubber::default();
        scrubber.seek(1.0, 10.0, &film());
        scrubber.seek(-1.0, 10.1, &film());
        assert_eq!(scrubber.cursor, Some(1200.0));
    }

    /// A hold ramps from thirty seconds of film a second to three hundred over
    /// four seconds of hold, and holds there. The presses arrive a tenth of a
    /// second apart, the way a controller repeats.
    #[test]
    fn a_hold_ramps_from_thirty_to_three_hundred() {
        for (held, rate) in [(0.5, 63.75), (2.0, 165.0), (4.0, 300.0), (6.0, 300.0)] {
            let mut scrubber = Scrubber::default();
            scrubber.seek(1.0, 100.0, &film());
            let mut moved = 0.0;
            for step in 1..=(held * 10.0) as i64 {
                let before = scrubber.cursor.expect("the scan holds a cursor");
                scrubber.seek(1.0, 100.0 + step as f64 / 10.0, &film());
                moved = scrubber.cursor.expect("the hold moves the cursor") - before;
            }
            assert!(
                (moved - rate / 10.0).abs() < 0.05,
                "held {held} moved {moved}, not {}",
                rate / 10.0
            );
        }
    }

    #[test]
    fn a_scan_holds_inside_the_film() {
        let mut near_the_start = film();
        near_the_start.apply("time-pos", &json!(2.0));
        let mut scrubber = Scrubber::default();
        scrubber.seek(-1.0, 10.0, &near_the_start);
        assert_eq!(scrubber.cursor, Some(0.0));

        let mut near_the_end = film();
        near_the_end.apply("time-pos", &json!(5998.0));
        let mut forward = Scrubber::default();
        forward.seek(1.0, 10.0, &near_the_end);
        assert_eq!(forward.cursor, Some(6000.0));
    }

    #[test]
    fn a_film_with_no_length_scans_nowhere() {
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

    /// A commit seeks to the previewed frame and ends the scan. A cancel ends
    /// it with no seek at all.
    #[test]
    fn a_commit_seeks_where_the_preview_stands_and_a_cancel_seeks_nowhere() {
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

    /// A chapter step moves playback by one chapter, and the ends of the list
    /// hold.
    #[test]
    fn a_chapter_step_holds_at_the_ends_of_the_list() {
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

    /// A film with no chapters draws one box across the whole bar.
    #[test]
    fn a_film_with_no_chapters_draws_one_segment() {
        let segments = segments(&Canvas::default(), &film(), 6000.0);
        assert_eq!(
            segments,
            vec![Segment {
                x0: 96.0,
                x1: 1824.0,
                w: 1728.0
            }]
        );
    }

    /// One segment per chapter, each cut short by the gap that separates it
    /// from the next.
    #[test]
    fn one_segment_per_chapter_stops_short_of_the_next() {
        let segments = segments(&Canvas::default(), &chaptered(), 6000.0);
        assert_eq!(segments.len(), 3);
        assert_eq!(
            segments[0],
            Segment {
                x0: 96.0,
                x1: 528.0,
                w: 428.0
            }
        );
        assert_eq!(
            segments[1],
            Segment {
                x0: 528.0,
                x1: 1392.0,
                w: 860.0
            }
        );
        assert_eq!(
            segments[2],
            Segment {
                x0: 1392.0,
                x1: 1824.0,
                w: 428.0
            }
        );
    }

    /// A chapter shorter than the gap still draws two canvas pixels, so it
    /// reads as a division.
    #[test]
    fn a_chapter_too_short_to_draw_still_marks_its_division() {
        let mut film = film();
        film.apply(
            "chapter-list",
            &json!([{ "time": 0.0 }, { "time": 1.0 }, { "time": 2.0 }]),
        );
        let segments = segments(&Canvas::default(), &film, 6000.0);
        assert_eq!(segments[0].w, 2.0);
        assert_eq!(segments[1].w, 2.0);
    }

    /// The fine axis brightens the playhead and its two lines, the chapter axis
    /// brightens the chapter, and no axis subdues both.
    #[test]
    fn the_axis_brightens_its_own_group() {
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
    fn a_time_reads_in_hours_only_when_it_runs_to_one() {
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
    fn the_time_left_reads_in_whole_minutes() {
        assert_eq!(remaining_phrase(-30.0), "less than a minute remaining");
        assert_eq!(remaining_phrase(0.0), "less than a minute remaining");
        assert_eq!(remaining_phrase(29.0), "less than a minute remaining");
        assert_eq!(remaining_phrase(30.0), "1 minute remaining");
        assert_eq!(remaining_phrase(89.0), "1 minute remaining");
        assert_eq!(remaining_phrase(90.0), "2 minutes remaining");
        assert_eq!(remaining_phrase(3600.0), "60 minutes remaining");
    }

    /// The bar's own numbers at a film's middle: the playhead where the
    /// position falls, the time above it, and the two lines below.
    #[test]
    fn the_bar_puts_every_part_at_a_films_middle() {
        let bar = Scrubber::default()
            .bar(&Canvas::default(), &chaptered(), Some(Axis::Fine))
            .expect("a film with a length draws a bar");

        assert_eq!(bar.head, 442.0);
        assert_eq!(bar.live, None);
        assert_eq!(bar.current, None);
        assert_eq!(bar.time.content, "20:00");
        assert_eq!(bar.time.at, Point::new(442.0, 886.0));
        assert_eq!(bar.time.anchor, Anchor::BottomCentre);
        assert_eq!(bar.time.size, 40.0);
        assert_eq!(bar.time.alpha, theme::alpha::OPAQUE);

        let chapter = bar.chapter.expect("a chaptered film names its chapter");
        assert_eq!(chapter.content, "The road   \u{00B7}   2 of 3");
        assert_eq!(chapter.at, Point::new(96.0, 922.0));
        assert_eq!(chapter.anchor, Anchor::TopLeft);
        assert_eq!(chapter.size, 34.0);
        assert_eq!(chapter.alpha, theme::alpha::SUBDUED);

        assert_eq!(
            bar.remaining.content,
            "80 minutes remaining  \u{00B7}  1:40:00"
        );
        assert_eq!(bar.remaining.at, Point::new(1824.0, 922.0));
        assert_eq!(bar.remaining.anchor, Anchor::TopRight);
        assert_eq!(bar.remaining.alpha, theme::alpha::OPAQUE);
    }

    /// A frame of playback that moves the playhead less than one output pixel
    /// draws the same bar, and a few seconds of it draws another.
    #[test]
    fn a_frame_of_playback_holds_the_drawn_bar_still() {
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

    /// A chapter with no title of its own reads as its number alone, and a
    /// film with no chapters names none.
    #[test]
    fn a_chapter_with_no_title_reads_as_its_number() {
        let mut film = chaptered();
        film.apply("chapter", &json!(2));
        assert_eq!(chapter_line(&film).as_deref(), Some("3 of 3"));
        assert_eq!(chapter_line(&super::tests::film()), None);
    }

    /// While a scan is in flight the playhead stands at the cursor and the tick
    /// stands where playback is.
    #[test]
    fn a_scan_parts_the_playhead_from_the_live_tick() {
        let mut scrubber = Scrubber::default();
        scrubber.seek(1.0, 10.0, &film());
        let bar = scrubber
            .bar(&Canvas::default(), &film(), Some(Axis::Fine))
            .expect("a bar");
        assert_eq!(bar.head, 443.0);
        assert_eq!(bar.live, Some(442.0));
        assert_eq!(bar.time.content, "20:05");
    }

    /// The chapter axis marks the chapter mpv plays, and the fine axis marks
    /// none.
    #[test]
    fn the_chapter_axis_marks_the_chapter_mpv_plays() {
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

    /// The time label holds inside the bar at either end, so it stays on
    /// screen.
    #[test]
    fn the_time_label_holds_inside_the_bar() {
        let canvas = Canvas::default();
        let mut film = film();
        film.apply("time-pos", &json!(0.0));
        let bar = Scrubber::default().bar(&canvas, &film, None).unwrap();
        assert_eq!(bar.time.at.x, 96.0);

        film.apply("time-pos", &json!(6000.0));
        let bar = Scrubber::default().bar(&canvas, &film, None).unwrap();
        assert_eq!(bar.time.at.x, 1824.0);
        assert_eq!(
            bar.remaining.content,
            "less than a minute remaining  \u{00B7}  1:40:00"
        );
    }

    /// Every value that moves with the position lands on a whole output pixel,
    /// so the bar stands still between two frames that land on the same one.
    #[test]
    fn the_playhead_lands_on_a_whole_output_pixel() {
        let half = Canvas::for_output(Size::new(1280.0, 720.0));
        let mut film = film();
        film.apply("time-pos", &json!(1234.5));
        let bar = Scrubber::default().bar(&half, &film, None).unwrap();
        assert_eq!((bar.head * half.scale).fract(), 0.0);

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

    /// A position outside the film holds at its ends.
    #[test]
    fn a_position_outside_the_film_holds_at_its_ends() {
        let canvas = Canvas::default();
        assert_eq!(position_pixel(&canvas, &film(), Some(-10.0)), Some(96));
        assert_eq!(position_pixel(&canvas, &film(), Some(9000.0)), Some(1824));
    }

    /// The two stops show for the films that carry what they move.
    #[test]
    fn a_stop_shows_for_the_film_that_carries_it() {
        assert!(fine_available(&film()));
        assert!(!fine_available(&Film::default()));
        assert!(chapter_available(&chaptered()));
        assert!(!chapter_available(&film()));
    }

    /// The thumbnail reads the previewed second and the playhead's own x.
    #[test]
    fn the_thumbnail_reads_the_previewed_second_and_the_playhead() {
        let canvas = Canvas::default();
        let mut scrubber = Scrubber::default();
        assert_eq!(scrubber.cursor_time(&film()), Some(1200.0));
        assert_eq!(scrubber.cursor_x(&canvas, &film()), Some(442.0));

        scrubber.seek(1.0, 10.0, &film());
        assert_eq!(scrubber.cursor_time(&film()), Some(1205.0));
        assert_eq!(scrubber.cursor_x(&canvas, &film()), Some(443.0));
    }
}
