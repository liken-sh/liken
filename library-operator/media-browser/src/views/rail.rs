// The jump rail: one rotated bar per stretch of rows, at one edge of a
// long wall, so a person crosses a hundred rows in two presses. A bar names a
// stretch by its first and its last row, and the rail reads no further into
// what the stretch is: a franchise's eras, a series' seasons, and a wall's
// years are all bars here. Bars that overlap draw in lanes, the caller
// deciding which lane each one takes, and the rail draws at most LANES of
// them. The words read top to bottom, because a bar is tall and narrow.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Color, Rectangle};

use super::{area, mark, rounded, text};
use crate::look;

/// One stretch of rows: the words on it, the first and the last row it covers,
/// and the lane it draws in. `first` and `last` are row indices in the wall
/// the rail stands beside.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Bar {
    pub label: String,
    pub first: usize,
    pub last: usize,
    pub lane: usize,
}

/// The edge of the region the rail draws at. The franchise page draws
/// its eras at the left; a wall and a series page draw their jumps at
/// the right.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Side {
    Left,
    Right,
}

/// The two geometries a rail draws with. `Scrolled` is a map of the
/// whole wall: a bar covers the rows it names and moves with the scroll,
/// which is how the franchise page draws story time. `Fitted` holds
/// every bar on the screen at once, in equal shares of the region that
/// the scroll never moves, which is what a jump through a long list
/// needs.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Fit<'a> {
    Scrolled { tops: &'a [f32], offset: f32 },
    Fitted,
}

/// The width of one lane.
pub const LANE: f32 = 44.0;

/// The most lanes a rail draws. A third lane leaves the wall no room,
/// so a bar the caller puts deeper draws in the last one.
pub const LANES: usize = 2;

// The space under a bar and on the wall's side of its lane, so two bars
// in one lane read as two and the wall stands clear of the rail.
const GAP: f32 = 8.0;

/// How far a rail at the right stands in from the region's edge: the
/// focus stroke's room and a little more, so the mark on a bar draws
/// whole and no part of it falls off the frame.
pub const EDGE: f32 = look::MARK_GAP + look::MARK + 4.0;

// The radius the bars are drawn with.
const ROUND: f32 = 8.0;

// The room the label's box holds past the length the average advance
// measures, so the shaper's own line always fits inside it.
const SLACK: f32 = 24.0;

/// The width a rail of these bars takes, and none where it holds no
/// bars.
pub fn width(bars: &[Bar]) -> f32 {
    lanes(bars) as f32 * LANE
}

/// How many lanes these bars fill, at most [`LANES`].
pub fn lanes(bars: &[Bar]) -> usize {
    bars.iter()
        .map(|bar| bar.lane.min(LANES - 1) + 1)
        .max()
        .unwrap_or(0)
}

/// The box one bar draws in, in frame space after the scroll. `tops` is
/// where every row of the wall beside the rail starts, and where the last
/// one ends, from the region's top, because those rows are not one
/// height.
pub fn bar(region: Rectangle, held: &Bar, tops: &[f32], offset: f32) -> Rectangle {
    bar_at(region, held, tops, offset, Side::Left)
}

/// The same box, with the rail at the side the caller names.
pub fn bar_at(region: Rectangle, held: &Bar, tops: &[f32], offset: f32, side: Side) -> Rectangle {
    let lane = held.lane.min(LANES - 1) as f32;
    let top = tops.get(held.first).copied().unwrap_or_default();
    let end = tops
        .get(held.last + 1)
        .or(tops.last())
        .copied()
        .unwrap_or(top);
    area(
        lane_x(region, lane, side),
        region.y + top - offset,
        LANE - GAP,
        (end - top - GAP).max(0.0),
    )
}

// Where one lane starts, counted in from the rail's edge, so the gap
// falls on the wall's side of the lane at either edge.
fn lane_x(region: Rectangle, lane: f32, side: Side) -> f32 {
    match side {
        Side::Left => region.x + lane * LANE,
        Side::Right => region.x + region.width - EDGE - (lane + 1.0) * LANE + GAP,
    }
}

/// How many bars fit the region once the longest of their labels has
/// room to read whole. A caller whose labels grow as its bars merge
/// asks again with the labels of the count this answered.
pub fn fits(region: Rectangle, longest: &str) -> usize {
    holding(
        region.height * TOLERANCE,
        text::width(longest, look::HEADING) + SLACK + GAP,
    )
}

// The labels are fitted against nine tenths of the region. A caller
// counts its bars once, against the screen the browser is drawn for,
// and draws them in whatever window is open. The tenth held back keeps
// every label whole in a window up to a tenth shorter than that screen,
// and costs one bar in ten on the screen itself.
const TOLERANCE: f32 = 0.9;

// How many bars of this height the room holds, and one for a room
// shorter than one bar, because a rail of no bars leaves nothing to jump
// by.
fn holding(room: f32, height: f32) -> usize {
    ((room / height) as usize).max(1)
}

/// The box of every bar of a fitted rail: equal shares of the region
/// from its top, in list order, at the side the caller names.
pub fn fitted(region: Rectangle, bars: &[Bar], side: Side) -> Vec<Rectangle> {
    if bars.is_empty() {
        return Vec::new();
    }
    let height = region.height / bars.len() as f32;
    bars.iter()
        .enumerate()
        .map(|(index, held)| {
            let lane = held.lane.min(LANES - 1) as f32;
            area(
                lane_x(region, lane, side),
                region.y + index as f32 * height,
                LANE - GAP,
                (height - GAP).max(0.0),
            )
        })
        .collect()
}

/// Draw the rail. `focus` names the bar that holds focus, or nothing
/// while the wall beside it holds focus. Only the bars the region
/// reaches become geometry.
pub fn draw(
    frame: &mut canvas::Frame<Renderer>,
    region: Rectangle,
    bars: &[Bar],
    tops: &[f32],
    offset: f32,
    focus: Option<usize>,
) {
    draw_at(
        frame,
        region,
        bars,
        focus,
        Side::Left,
        Fit::Scrolled { tops, offset },
    );
}

/// The same draw, with the bars at the side and in the geometry the
/// caller names.
pub fn draw_at(
    frame: &mut canvas::Frame<Renderer>,
    region: Rectangle,
    bars: &[Bar],
    focus: Option<usize>,
    side: Side,
    fit: Fit,
) {
    for (index, (held, bounds)) in bars.iter().zip(boxes(region, bars, side, fit)).enumerate() {
        if bounds.y + bounds.height < region.y || bounds.y > region.y + region.height {
            continue;
        }
        frame.fill(&rounded(bounds, ROUND), ink(focus == Some(index)));
        written(frame, bounds, region, &held.label);
        if focus == Some(index) {
            mark(frame, bounds);
        }
    }
}

// The fill under a bar's words. A rail draws over a page's art, so the
// fill is the near-black ground the art reads through as a trace, and
// the focused bar takes the lighter ground a focused row takes under its
// mark.
fn ink(focused: bool) -> Color {
    match focused {
        true => look::slot(),
        false => look::ground(),
    }
}

/// The box of every bar in the geometry the caller names.
pub fn boxes(region: Rectangle, bars: &[Bar], side: Side, fit: Fit) -> Vec<Rectangle> {
    match fit {
        Fit::Scrolled { tops, offset } => bars
            .iter()
            .map(|held| bar_at(region, held, tops, offset, side))
            .collect(),
        Fit::Fitted => fitted(region, bars, side),
    }
}

/// Where one bar's label draws: the middle of the part of the bar the
/// region shows, not the middle of the whole bar. An era of a hundred
/// rows is taller than any screen, so a label at the whole bar's middle
/// is off screen for most of the scroll and the rail reads blank.
/// The label stays inside its own bar at either end, so a bar that has
/// scrolled almost away carries its label to the edge and no further.
/// `length` is the label's own length along the bar, and a bar shorter
/// than that carries as much of the label as it holds.
pub fn label_box(bounds: Rectangle, region: Rectangle, length: f32) -> Rectangle {
    let length = length.min(bounds.height);
    let top = bounds.y.max(region.y);
    let foot = (bounds.y + bounds.height).min(region.y + region.height);
    let middle = (top + foot - length) / 2.0;
    let top = middle.max(bounds.y).min(bounds.y + bounds.height - length);
    area(bounds.x, top, bounds.width, length)
}

// One bar's words, cut to the bar's own length and turned to read from
// its foot to its head.
fn written(
    frame: &mut canvas::Frame<Renderer>,
    bounds: Rectangle,
    region: Rectangle,
    content: &str,
) {
    let shown = text::cut(content, look::HEADING, bounds.height);
    let at = label_box(bounds, region, text::width(&shown, look::HEADING) + SLACK);
    text::downward(frame, &shown, at, look::HEADING, look::text());
}

/// The part of the region the wall beside the rail draws in: everything
/// to the right of the last lane.
pub fn beside(region: Rectangle, bars: &[Bar]) -> Rectangle {
    beside_at(region, bars, Side::Left)
}

/// The same part of the region, with the lanes taken off the edge the
/// caller names. A rail at the right also stands [`EDGE`] in from the
/// region, and the wall gives up that much along with the lanes.
pub fn beside_at(region: Rectangle, bars: &[Bar], side: Side) -> Rectangle {
    let taken = width(bars);
    match side {
        Side::Left => area(
            region.x + taken,
            region.y,
            region.width - taken,
            region.height,
        ),
        Side::Right => {
            let inset = match bars.is_empty() {
                true => 0.0,
                false => EDGE,
            };
            area(
                region.x,
                region.y,
                region.width - taken - inset,
                region.height,
            )
        }
    }
}

/// The bar that covers one row, and the first one where two lanes cover
/// it, so a move onto the rail lands on the widest stretch.
pub fn covering(bars: &[Bar], row: usize) -> Option<usize> {
    bars.iter()
        .position(|bar| bar.first <= row && row <= bar.last)
}

#[cfg(test)]
mod tests;
