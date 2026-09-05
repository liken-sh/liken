// The metro strip between the time labels and the cards: one vertical
// line per universe, drawn the way a git graph draws branches. A
// universe's line is one unbroken run from the middle of its first
// entry's row to the middle of its last, through the gaps between rows
// and through the thin rows; above and below it there is nothing. Runs
// that never overlap share a lane, so the strip is as wide as the story
// is at its widest and not as wide as the count of universes. A row
// takes a dot on the line of every universe it names, a filled circle in
// the line's color, and a bar across the dots where it names several: a
// pill in the ink color behind the dots. The dot of a thin row is
// smaller. Each run's name reads upward beside its own line, in the
// line's color, so a person reads the name at the point where the line
// starts and no legend has to be matched to a lane. With one run the
// strip has no width and is not drawn, because a line every row stands
// on says nothing. The strip scrolls with the rows. The lines, the bars,
// and the dots are all fills, so they draw in that order inside one
// layer, and the names draw last, because rotated words are meshes too.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Color, Point, Rectangle};

use super::wall::{GAP, Row};
use crate::look;
use crate::views::{area, rounded, stack, text};

/// The space one lane takes across the strip. It holds a dot, and the
/// gutter beside the dot holds a name, so a name never covers the next
/// lane's line.
pub const PITCH: f32 = 40.0;

/// The width of a line.
pub const LINE: f32 = 4.0;

/// The radius of a card's dot, and of a thin row's smaller one.
pub const DOT: f32 = 9.0;
pub const SMALL: f32 = 6.0;

// The opacity a line draws at, so a dot reads over it.
const RUN: f32 = 0.85;

// The lightness and the chroma every line shares, and the hues the runs
// take in turn. The palette is fixed and the runs cycle through it, so a
// franchise of twenty universes draws in colors a person tells apart
// while a franchise of three keeps the same three it had before the
// twentieth arrived. Two runs of a long story share a hue, and the name
// beside each line tells them apart.
const LIGHTNESS: f32 = 0.78;
const CHROMA: f32 = 0.12;
const HUES: [f32; 8] = [20.0, 65.0, 110.0, 155.0, 200.0, 245.0, 290.0, 335.0];

// The size a run's name draws at, the space between a run's first dot
// and the foot of its name, and the room a name has across the strip:
// the gutter between the dots of two lanes.
const NAME: f32 = 17.0;
const NAME_GAP: f32 = 8.0;
const NAME_ROOM: f32 = PITCH - 2.0 * DOT;

/// One universe's line on the strip: the rows it reaches, the lane it
/// draws in, the hue it takes, and the name it carries. `universe` is
/// the universe's place in the page's own list, which is what a row's
/// cell names.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Run {
    pub name: String,
    pub universe: usize,
    pub first: usize,
    pub last: usize,
    pub lane: usize,
    pub hue: usize,
}

/// The runs of these rows, in the order they start. A universe no row
/// names has no run. A run takes the lowest lane that is free where it
/// starts, and its lane is free again for a run that starts below its
/// last row, so a franchise of twenty universes told one after another
/// draws in one lane. Each run takes the next hue of the palette in that
/// same order.
pub fn runs(rows: &[Row], universes: &[String]) -> Vec<Run> {
    let mut spans: Vec<Option<(usize, usize)>> = vec![None; universes.len()];
    for (index, row) in rows.iter().enumerate() {
        for universe in &row.cell.universes {
            let Some(span) = spans.get_mut(*universe) else {
                continue;
            };
            *span = match *span {
                None => Some((index, index)),
                Some((first, _)) => Some((first, index)),
            };
        }
    }

    let mut runs: Vec<Run> = spans
        .iter()
        .enumerate()
        .filter_map(|(universe, span)| {
            span.map(|(first, last)| Run {
                name: universes[universe].clone(),
                universe,
                first,
                last,
                lane: 0,
                hue: 0,
            })
        })
        .collect();
    runs.sort_by_key(|run| (run.first, run.universe));

    // The last row of the run each lane holds. A lane is free where its
    // run ended above the row the next one starts on, because two runs
    // alive on one row would draw as one line.
    let mut ends: Vec<usize> = Vec::new();
    for (order, run) in runs.iter_mut().enumerate() {
        run.hue = order % HUES.len();
        run.lane = match ends.iter().position(|end| *end < run.first) {
            Some(free) => free,
            None => {
                ends.push(run.last);
                ends.len() - 1
            }
        };
        ends[run.lane] = run.last;
    }
    runs
}

/// How many lanes these runs fill: the most runs the story has alive at
/// one time.
pub fn lanes(runs: &[Run]) -> usize {
    runs.iter().map(|run| run.lane + 1).max().unwrap_or(0)
}

/// The width the strip takes for these runs, and none for one run. Every
/// row of a franchise of one universe stands on that one line, and a
/// line every row stands on tells a person nothing.
pub fn width(runs: &[Run]) -> f32 {
    match runs.len() > 1 {
        true => lanes(runs) as f32 * PITCH,
        false => 0.0,
    }
}

/// Where one lane's line stands across the strip: the middle of its
/// pitch.
pub fn line_x(strip: Rectangle, lane: usize) -> f32 {
    strip.x + lane as f32 * PITCH + PITCH / 2.0
}

/// Where one lane's name stands across the strip: the middle of the
/// gutter right of its line. The name covers no dot of its own lane and
/// none of the next, and it draws in its line's own color, which is what
/// ties the two together.
pub fn name_x(strip: Rectangle, lane: usize) -> f32 {
    line_x(strip, lane) + PITCH / 2.0
}

/// The color of a line: the palette's hue at the shared lightness and
/// chroma. The palette cycles, so a run past the eighth takes the first
/// hue again.
pub fn color(hue: usize) -> Color {
    oklch(LIGHTNESS, CHROMA, HUES[hue % HUES.len()])
}

// One OKLCH color as the sRGB the canvas draws, clamped to the gamut:
// OKLab to linear sRGB by the matrices of the OKLab definition, then the
// sRGB transfer curve. No crate, because this is the only color the
// browser computes.
fn oklch(lightness: f32, chroma: f32, hue: f32) -> Color {
    let (a, b) = (
        chroma * hue.to_radians().cos(),
        chroma * hue.to_radians().sin(),
    );
    let l = (lightness + 0.396_337_8 * a + 0.215_803_8 * b).powi(3);
    let m = (lightness - 0.105_561_3 * a - 0.063_854_2 * b).powi(3);
    let s = (lightness - 0.089_484_2 * a - 1.291_485_5 * b).powi(3);
    let red = 4.076_741_7 * l - 3.307_711_6 * m + 0.230_97 * s;
    let green = -1.268_438 * l + 2.609_757_4 * m - 0.341_319_4 * s;
    let blue = -0.004_196_1 * l - 0.703_418_6 * m + 1.707_614_7 * s;
    Color::from_rgb(gamma(red), gamma(green), gamma(blue))
}

// One linear channel as the sRGB curve encodes it, clamped to the unit
// range.
fn gamma(linear: f32) -> f32 {
    let linear = linear.clamp(0.0, 1.0);
    match linear <= 0.003_130_8 {
        true => 12.92 * linear,
        false => 1.055 * linear.powf(1.0 / 2.4) - 0.055,
    }
}

/// The middle of one row, in frame space after the scroll.
pub fn middle(strip: Rectangle, row: usize, tops: &[f32], down: f32) -> f32 {
    let top = tops.get(row).copied().unwrap_or_default();
    let next = tops.get(row + 1).copied().unwrap_or(top + GAP);
    strip.y + (top + next - GAP) / 2.0 - down
}

/// The runs one row takes a dot on, in the order the row names their
/// universes. A row names no run where the page's list holds no such
/// universe.
pub fn dotted<'a>(runs: &'a [Run], row: &Row) -> Vec<&'a Run> {
    row.cell
        .universes
        .iter()
        .filter_map(|universe| runs.iter().find(|run| run.universe == *universe))
        .collect()
}

/// The bar across the dots of one row: a pill from the leftmost lane the
/// row's runs occupy to the rightmost. A row on one lane draws none,
/// because its own dot says as much.
pub fn bar(strip: Rectangle, lanes: &[usize], y: f32, radius: f32) -> Option<Rectangle> {
    let (low, high) = (lanes.iter().min()?, lanes.iter().max()?);
    if low == high {
        return None;
    }
    Some(area(
        line_x(strip, *low) - radius,
        y - radius,
        line_x(strip, *high) - line_x(strip, *low) + 2.0 * radius,
        2.0 * radius,
    ))
}

/// The box one run's name draws in: the gutter beside its line, over the
/// run's first dot, so the name's foot touches the space above the dot
/// and the words read up from it. While the first dot is above the strip
/// the name holds the top of the strip, the way a jump rail's label
/// holds the top of its bar, and the run's last dot pushes the name off
/// with it, so a name never leaves its own run. `length` is the name's
/// own length along the line.
///
/// A name draws only where the strip reaches it. A run whose first dot
/// is still under the foot of the strip draws none, because a name grows
/// up out of that dot and would otherwise show as its last few letters
/// along the bottom edge. A run whose own last dot has passed over the
/// head of the strip draws none either.
pub fn name_box(
    strip: Rectangle,
    run: &Run,
    tops: &[f32],
    down: f32,
    length: f32,
) -> Option<Rectangle> {
    let first = middle(strip, run.first, tops, down);
    if first - DOT > strip.y + strip.height {
        return None;
    }
    let top = first - DOT - NAME_GAP - length;
    let section = area(
        name_x(strip, run.lane) - NAME_ROOM / 2.0,
        top,
        NAME_ROOM,
        middle(strip, run.last, tops, down) - top,
    );
    let at = stack::held(section, strip, length);
    match at.y + at.height < strip.y {
        true => None,
        false => Some(at),
    }
}

/// The strip: every line first, then every bar, then every dot, then
/// every name. A dot lies over a bar, a bar over the lines, and a name
/// over all of them. Only the rows and the names the strip reaches build
/// geometry, because a wall of a hundred rows is drawn on every frame.
pub fn draw(
    frame: &mut canvas::Frame<Renderer>,
    strip: Rectangle,
    runs: &[Run],
    rows: &[Row],
    tops: &[f32],
    down: f32,
) {
    if width(runs) <= 0.0 {
        return;
    }
    for run in runs {
        let x = line_x(strip, run.lane);
        let (top, bottom) = (
            middle(strip, run.first, tops, down),
            middle(strip, run.last, tops, down),
        );
        frame.fill_rectangle(
            Point::new(x - LINE / 2.0, top),
            iced_winit::core::Size::new(LINE, bottom - top),
            Color {
                a: RUN,
                ..color(run.hue)
            },
        );
    }
    for (index, row) in rows.iter().enumerate() {
        let y = middle(strip, index, tops, down);
        if y < strip.y - DOT || y > strip.y + strip.height + DOT {
            continue;
        }
        let radius = match row.cell.held() {
            true => DOT,
            false => SMALL,
        };
        let named = dotted(runs, row);
        let lanes: Vec<usize> = named.iter().map(|run| run.lane).collect();
        if let Some(bar) = bar(strip, &lanes, y, radius) {
            frame.fill(&rounded(bar, radius), look::text());
        }
        for run in named {
            frame.fill(
                &canvas::Path::circle(Point::new(line_x(strip, run.lane), y), radius),
                color(run.hue),
            );
        }
    }
    for run in runs {
        let shown = text::cut(&run.name, NAME, strip.height);
        let Some(at) = name_box(strip, run, tops, down, text::measured(&shown, NAME)) else {
            continue;
        };
        text::upward(frame, &shown, at, NAME, color(run.hue));
    }
}

#[cfg(test)]
mod tests;
