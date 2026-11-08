// The draw of the career axis: the line, the ticks and their years, the
// words at the left end and at a death, the bars of running series, the
// marks of the credits, the bright part of the line, and the label over
// the focused year. The draw makes no decision of its own: `Axis` decides
// what the axis holds, and `place` where it goes.

use std::f32::consts::PI;

use iced_wgpu::Renderer;
use iced_widget::canvas::{Frame, Path, Stroke, path::Arc};
use iced_winit::core::{Color, Point, Radians, Rectangle};

use super::place::DOT;
use super::{Axis, Mode, Start};
use crate::look;
use crate::screens::person::credit::{Credit, Seen};
use crate::views::{area, extent, text};

// The width of the ring of a credit nobody at the screen has seen. It is
// opaque and wide enough to read from a couch three meters away.
const RING: f32 = 2.5;

// The width of a bar's frame.
const FRAME: f32 = 2.0;

// How many steps the faded start of the line takes from clear to its
// full ink. The line is one pixel tall, so steps this short read as one
// fade.
const FADE_STEPS: usize = 24;

/// One frame's axis: the axis, the credits its years index into, where
/// the line is, the bright part of the line as this second draws it, and
/// the label over the focused year with the `x` of its center, while the
/// axis holds focus.
pub struct Drawn<'a> {
    pub axis: &'a Axis,
    pub credits: &'a [Credit],
    pub line: Rectangle,
    pub window: Option<(f32, f32)>,
    pub label: Option<(String, f32)>,
}

/// Draw the axis.
pub fn draw(frame: &mut Frame<Renderer>, drawn: &Drawn) {
    let (axis, line) = (drawn.axis, drawn.line);
    lined(frame, axis, line);
    ticked(frame, axis, line);
    for from in &axis.runs {
        let run = axis.run(line, *from);
        frame.fill_rectangle(run.position(), extent(run), look::axis_run());
    }
    if let Some((left, right)) = drawn.window {
        let window = area(left, line.y - 1.0, right - left, super::place::WINDOW);
        frame.fill_rectangle(window.position(), extent(window), look::axis_window());
    }
    match axis.mode {
        Mode::Dots => dotted(frame, axis, drawn.credits, line),
        Mode::Bars => barred(frame, axis, line),
    }
    if let Some((words, middle)) = &drawn.label {
        let width = text::measured(words, look::CAPTION);
        let band = axis.label_band(line, *middle, width);
        text::shown(frame, words, band, look::CAPTION, look::text());
    }
}

// The line: faded in from clear where the axis does not start at a birth,
// at full ink through the life, and dimmer after a death inside the span.
fn lined(frame: &mut Frame<Renderer>, axis: &Axis, line: Rectangle) {
    let right = line.x + line.width;
    let died = axis
        .death
        .as_ref()
        .map(|(year, _)| axis.middle(line, *year))
        .unwrap_or(right);
    let solid = match axis.left {
        Start::Born => line.x,
        Start::Faded { .. } => {
            let faded = axis.x(line, axis.first as f32).min(died);
            let step = (faded - line.x) / FADE_STEPS as f32;
            for index in 0..FADE_STEPS {
                let share = (index as f32 + 0.5) / FADE_STEPS as f32;
                let ink = Color {
                    a: look::axis_line().a * share,
                    ..look::axis_line()
                };
                rule(frame, line.x + index as f32 * step, step, line.y, ink);
            }
            faded
        }
    };
    rule(frame, solid, died - solid, line.y, look::axis_line());
    rule(frame, died, right - died, line.y, look::axis_after_death());
}

// One stretch of the line.
fn rule(frame: &mut Frame<Renderer>, x: f32, width: f32, y: f32, ink: Color) {
    if width > 0.0 {
        frame.fill_rectangle(Point::new(x, y), extent(area(x, y, width, 1.0)), ink);
    }
}

// The decade ticks and their years, the death's tick and its words, and
// the words at the left end. A decade's year whose words would run into
// the death's is left out, because the death is the rarer fact.
fn ticked(frame: &mut Frame<Renderer>, axis: &Axis, line: Rectangle) {
    let under = Axis::tick_labels(line);
    let death = axis.death.as_ref().map(|(year, words)| {
        let tick = axis.death_tick(line, *year);
        frame.fill_rectangle(tick.position(), extent(tick), look::death_tick());
        let width = text::measured(words, look::FACE);
        let band = Axis::centered(line, tick.x, under, width, look::FACE);
        italic(frame, words, band);
        band
    });
    for year in axis.ticks() {
        let tick = axis.tick(line, year);
        frame.fill_rectangle(tick.position(), extent(tick), look::axis_tick());
        let words = year.to_string();
        let width = text::measured(&words, look::FACE);
        let band = Axis::centered(line, tick.x, under, width, look::FACE);
        if death.is_some_and(|death| overlap(death, band)) {
            continue;
        }
        text::shown(frame, &words, band, look::FACE, look::faint());
    }
    let left = match &axis.left {
        Start::Born => Some("born"),
        Start::Faded { died } => died.as_deref(),
    };
    if let Some(words) = left {
        let width = text::measured(words, look::FACE);
        italic(
            frame,
            words,
            area(
                line.x,
                Axis::left_words(line),
                width,
                text::height(1, look::FACE),
            ),
        );
    }
}

// Whether two bands of words overlap along the line.
fn overlap(one: Rectangle, other: Rectangle) -> bool {
    one.x < other.x + other.width && other.x < one.x + one.width
}

// Words of the axis in the italic face: the left end's and a death's.
fn italic(frame: &mut Frame<Renderer>, words: &str, band: Rectangle) {
    text::faced(frame, words, band, look::FACE, look::faint(), look::ITALIC);
}

// One dot for each credit, stacked by year from the line up.
fn dotted(frame: &mut Frame<Renderer>, axis: &Axis, credits: &[Credit], line: Rectangle) {
    for year in &axis.years {
        for (stacked, index) in year.credits.iter().enumerate() {
            let center = axis.dot(line, year.year, stacked);
            dot(frame, center, credits[*index].seen);
        }
    }
}

/// One credit's dot: an open ring nobody has seen, the ring with its left
/// half filled where some of the people at the screen have seen it, and a
/// full disc where all of them have. The difference is a shape and not a
/// color, so it reads for a person who cannot tell the inks apart. The
/// ring fills with the ground, so a series' bar under it does not show
/// through.
fn dot(frame: &mut Frame<Renderer>, center: Point, seen: Seen) {
    let radius = DOT / 2.0;
    let disc = Path::circle(center, radius);
    if seen == Seen::Everyone {
        frame.fill(&disc, look::text());
        return;
    }
    frame.fill(&disc, look::BACKGROUND);
    if seen == Seen::Some {
        let half = Path::new(|path| {
            path.move_to(Point::new(center.x, center.y + radius));
            path.arc(Arc {
                center,
                radius,
                start_angle: Radians(PI / 2.0),
                end_angle: Radians(3.0 * PI / 2.0),
            });
            path.close();
        });
        frame.fill(&half, look::text());
    }
    frame.stroke(
        &Path::circle(center, radius - RING / 2.0),
        Stroke::default().with_color(look::muted()).with_width(RING),
    );
}

// One bar for each year on one scale: a frame for every credit of the
// year, filled at the bottom for what everyone has seen and lighter over
// that for what some have seen.
fn barred(frame: &mut Frame<Renderer>, axis: &Axis, line: Rectangle) {
    for index in 0..axis.years.len() {
        let [outline, everyone, some] = axis.bar(line, index);
        frame.fill_rectangle(everyone.position(), extent(everyone), look::text());
        frame.fill_rectangle(some.position(), extent(some), look::seen_by_some());
        frame.stroke_rectangle(
            Point::new(outline.x + FRAME / 2.0, outline.y + FRAME / 2.0),
            extent(area(
                0.0,
                0.0,
                outline.width - FRAME,
                outline.height - FRAME,
            )),
            Stroke::default()
                .with_color(look::bar_frame())
                .with_width(FRAME),
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn two_bands_of_words_overlap_only_where_they_share_some_of_the_line() {
        let band = |x: f32| area(x, 0.0, 50.0, 20.0);
        assert!(overlap(band(0.0), band(40.0)));
        assert!(!overlap(band(0.0), band(60.0)));
        assert!(!overlap(band(60.0), band(0.0)));
    }
}
