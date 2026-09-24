// The identity block at the bottom left of the picker: the unit's name,
// then one line per part of the unit, with a charge bar beside a part that
// reports a battery level. A person who opens the picker reads which unit
// the room is on and which controllers it has, and how charged they are.
//
// The idle screen draws the same block between films, so the layout and
// the colours copy that screen's. The media operator states them in
// `idle/src/idle/identity.rs` and `idle/src/look.rs`, and the numbers here
// are those numbers. The idle screen's block eases a part between dim and
// full, and flashes a part that returns or takes focus. This block is
// static, because the picker is a short question and a frame here costs a
// redraw of the whole browser. A part draws at the brightness its last
// status settles on, and the block asks for no frame of its own.
//
// The idle screen measures in canvas pixels on a canvas 1080 rows tall.
// The browser measures in logical pixels, and the compositor's scale puts
// a 4K panel at 1080 logical rows too. So a canvas pixel of the idle
// screen is a logical pixel here, and every number below passes through
// unchanged. The block hangs from the frame's own bottom edge, so it keeps
// its margin on a frame of any height.
//
// The layout is one pure function of the unit and the frame's bounds, so
// the placement is tested with numbers and no window.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::alignment::Vertical;
use iced_winit::core::text::{Alignment, LineHeight, Shaping};
use iced_winit::core::{Color, Font, Pixels, Point, Rectangle};
use media_screen::status::{Component, Status};

use crate::look;
use crate::views::{clock, rounded, screen};

/// What the block names: the unit, and its parts in the order the status
/// lists them. The browser keeps this much of the last status, because the
/// picker reads it and nothing else in the browser does.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Unit {
    /// The unit's display name. An empty name draws no block.
    pub name: String,
    /// Every part that carries a name.
    pub parts: Vec<Component>,
}

impl Unit {
    /// The unit one status describes. A part with no name is dropped,
    /// because a line with no words next to a bar or a marker tells a
    /// person nothing about which part it is.
    pub fn of(status: &Status) -> Self {
        Self {
            name: status.display_name.clone(),
            parts: status
                .components
                .iter()
                .filter(|part| !part.name.is_empty())
                .cloned()
                .collect(),
        }
    }
}

// The face's metric: its bounding height over its em, 1326 units over
// 1000, from the `OS/2` and `head` tables of `SourceSans3-Regular.otf`.
// The idle screen draws through `libass`, which fills a line box of the
// size an ASS `\fs` states with the face's bounding height. One `\fs`
// number is therefore a line box in pixels and, through this metric, a
// type size. The block keeps the two measures apart the way the idle
// screen does.
const FACE_METRIC: f32 = 1326.0 / 1000.0;

// The line box one type size draws in.
fn line_box(size: f32) -> f32 {
    size * FACE_METRIC
}

// The header's type size: the idle screen's label size, `\fs40` through
// the metric. It is one step larger than the parts, so the unit's name
// reads as the title of the list.
const HEADER_SIZE: f32 = 30.0;

/// The line box a part's name draws in. It is four pixels under the
/// clock's box, which is the idle screen's small box, so the parts read a
/// little lighter than the clock.
pub const ITEM_BOX: f32 = clock::BOX - 4.0;

// The type size a part's name draws at, which is `ITEM_BOX` through the
// face metric.
const ITEM_SIZE: f32 = ITEM_BOX / FACE_METRIC;

/// The drop from one part's line to the next: 1.1 line boxes.
pub const ITEM_STEP: f32 = 1.1 * ITEM_BOX;

/// The drop from the header to the first part: 1.3 line boxes. The gap is
/// wider than `ITEM_STEP`, so the name reads as the title of the list
/// under it.
pub const HEADER_STEP: f32 = 1.3 * ITEM_BOX;

/// The focus marker's radius, from its center to a vertex: a fifth of a
/// line box, so the marker is as tall as the lowercase letters beside it.
pub const MARKER_R: f32 = ITEM_BOX / 5.0;

/// The distance from the marker's center to the screen margin. The marker
/// draws inside the margin, so the names keep one flush-left column with
/// or without a marker.
pub const MARKER_GAP: f32 = 18.0;

/// The lift from a line's anchor, the bottom of its line box, to the
/// middle of the lowercase letters: 0.42 of a line box. The marker and the
/// bar center on it.
pub const MARKER_RISE: f32 = 0.42 * ITEM_BOX;

// The gap between the bar's right end and the marker's place. The bar
// ends left of every marker, drawn or not, so the two never touch.
const BAR_GAP: f32 = 12.0;

/// The column every bar ends on: the marker's place less the gap. The bars
/// of two controllers end on one column whichever of them has the focus,
/// so a person compares their charges at a glance.
pub const BAR_RIGHT: f32 = screen::MARGIN_X - MARKER_GAP - MARKER_R - BAR_GAP;

/// The bar is two characters long and an eighth of a line box tall, so it
/// reads as a measure beside the name and not as a second line of the
/// list.
pub const BAR_LENGTH: f32 = 2.0 * ITEM_SIZE;
const BAR_HEIGHT: f32 = ITEM_BOX / 8.0;

// Half the height rounds the ends to semicircles.
const BAR_RADIUS: f32 = BAR_HEIGHT / 2.0;

// The charge the bar fills at, the top of the range a part reports.
const FULL_CHARGE: f32 = 100.0;

/// The opacity of a part that is away. It is the idle screen's dim, the
/// ASS alpha byte 0xA8, and not the browser's `look::DIM`, which is the
/// opacity of art a person did not choose.
pub const DIM: f32 = 87.0 / 255.0;

/// The opacity of a bar's track under its line's own colour, the ASS alpha
/// byte 0x50. The track draws in the colour of the line, so it reads as
/// the charge the part has used.
pub const TRACK_OPACITY: f32 = 175.0 / 255.0;

/// The marker's opacity on a line at full brightness, one step under the
/// name beside it.
pub const MARKER_REST: f32 = DIM;

/// The marker's opacity on a dim line. It keeps the same fraction of a dim
/// line that it keeps of a lit one, so a marker never draws brighter than
/// the part it marks.
pub const MARKER_DIM: f32 = DIM * DIM;

/// One run of text, by the point its bottom-left corner is at.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Run<'a> {
    pub words: &'a str,
    pub at: Point,
    pub size: f32,
    pub color: Color,
}

/// The charge bar of one line: the track at the whole length, the part of
/// it the charge fills, and the colour of the line.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Bar {
    pub track: Rectangle,
    pub fill: Rectangle,
    pub color: Color,
}

/// The focus marker of one line.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Marker {
    pub center: Point,
    pub color: Color,
}

/// One part's line: its name, and its bar and marker where it has them.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Line<'a> {
    pub run: Run<'a>,
    pub bar: Option<Bar>,
    pub marker: Option<Marker>,
}

/// The whole block: the unit's name, and one line per part from the top
/// of the list down.
#[derive(Debug, Clone, PartialEq)]
pub struct Block<'a> {
    pub header: Run<'a>,
    pub lines: Vec<Line<'a>>,
}

/// The block one unit draws in a frame, or nothing while no name is known
/// for the unit. A browser that has had no status draws no block, and the
/// picker is then the row alone.
///
/// The last part is on the bottom margin, and the list builds upward from
/// there. The bottom margin is the clock's top margin, so the block and
/// the clock keep one distance from the two edges of the panel.
pub fn block(unit: &Unit, bounds: Rectangle) -> Option<Block<'_>> {
    if unit.name.is_empty() {
        return None;
    }

    let bottom = bounds.y + bounds.height - clock::MARGIN_Y;
    let above = |index: usize| unit.parts.len() - 1 - index;
    let lines = unit
        .parts
        .iter()
        .enumerate()
        .map(|(index, part)| line(part, bounds.x, item_y(bottom, above(index))))
        .collect();

    Some(Block {
        header: Run {
            words: &unit.name,
            at: Point::new(
                bounds.x + screen::MARGIN_X,
                header_y(bottom, unit.parts.len()),
            ),
            size: HEADER_SIZE,
            color: look::text(),
        },
        lines,
    })
}

// One part's line, at the anchor `y` and in a frame whose left edge is at
// `left`. A part that reports it is away draws dim. A part that reports
// no presence, such as a wired screen, draws at full brightness, because a
// part that cannot be absent must not read as present for now.
fn line(part: &Component, left: f32, y: f32) -> Line<'_> {
    let away = part.connected == Some(false);
    let color = Color {
        a: if away { DIM } else { 1.0 },
        ..look::text()
    };
    let marker_opacity = if away { MARKER_DIM } else { MARKER_REST };
    let rise = y - MARKER_RISE;

    Line {
        run: Run {
            words: &part.name,
            at: Point::new(left + screen::MARGIN_X, y),
            size: ITEM_SIZE,
            color,
        },
        bar: part.battery.map(|level| {
            let track = Rectangle {
                x: left + BAR_RIGHT - BAR_LENGTH,
                y: rise - BAR_HEIGHT / 2.0,
                width: BAR_LENGTH,
                height: BAR_HEIGHT,
            };
            Bar {
                track,
                fill: Rectangle {
                    width: filled(level),
                    ..track
                },
                color,
            }
        }),
        marker: (part.focused == Some(true)).then(|| Marker {
            center: Point::new(left + screen::MARGIN_X - MARKER_GAP, rise),
            color: Color {
                a: marker_opacity,
                ..look::text()
            },
        }),
    }
}

/// How much of the bar one charge fills, in logical pixels. A reading
/// outside 0 to 100 fills to one end and no further.
pub fn filled(level: i64) -> f32 {
    BAR_LENGTH * (level as f32 / FULL_CHARGE).clamp(0.0, 1.0)
}

// The y one part's line hangs from. `above` counts up the list from the
// last part, which is the part on the bottom margin.
fn item_y(bottom: f32, above: usize) -> f32 {
    bottom - above as f32 * ITEM_STEP
}

// The y the unit's name hangs from, over `parts` lines of parts. A unit
// with no parts is a name on the bottom margin.
fn header_y(bottom: f32, parts: usize) -> f32 {
    match parts {
        0 => bottom,
        _ => item_y(bottom, parts - 1) - HEADER_STEP,
    }
}

/// The six vertices of a pointy-top regular hexagon about `center`. `r` is
/// the distance from the center to a vertex, and 0.8660254 is cos(30
/// degrees), the half-width of a pointy-top hexagon. The idle screen draws
/// its marker from the same formula, with sharp corners.
pub fn vertices(center: Point, r: f32) -> [Point; 6] {
    let half = 0.866_025_4 * r;
    [
        Point::new(center.x, center.y - r),
        Point::new(center.x + half, center.y - 0.5 * r),
        Point::new(center.x + half, center.y + 0.5 * r),
        Point::new(center.x, center.y + r),
        Point::new(center.x - half, center.y + 0.5 * r),
        Point::new(center.x - half, center.y - 0.5 * r),
    ]
}

/// Draw the block into the picker's frame. Every shape draws over the
/// picker's shade, and every run of text over every shape, because a
/// canvas frame is one layer and the renderer draws its text last.
pub fn draw(frame: &mut canvas::Frame<Renderer>, unit: &Unit, bounds: Rectangle) {
    let Some(block) = block(unit, bounds) else {
        return;
    };

    for line in &block.lines {
        if let Some(bar) = line.bar {
            let track = Color {
                a: bar.color.a * TRACK_OPACITY,
                ..bar.color
            };
            frame.fill(&rounded(bar.track, BAR_RADIUS), track);
            // A fill under a pixel wide draws as a speck, so an empty
            // battery shows the track alone.
            if bar.fill.width >= 1.0 {
                frame.fill(&rounded(bar.fill, BAR_RADIUS), bar.color);
            }
        }
        if let Some(marker) = line.marker {
            frame.fill(&hexagon(marker.center), marker.color);
        }
        write(frame, line.run);
    }
    write(frame, block.header);
}

// One filled hexagon about `center` at the marker's radius.
fn hexagon(center: Point) -> canvas::Path {
    let points = vertices(center, MARKER_R);
    canvas::Path::new(|builder| {
        builder.move_to(points[0]);
        for point in &points[1..] {
            builder.line_to(*point);
        }
        builder.close();
    })
}

// One run of text, anchored at its bottom left. The line box is the one
// the size states through the face metric, so the anchor is the bottom of
// the same box the idle screen places, and the steps above are the gaps
// between the anchors and nothing more.
fn write(frame: &mut canvas::Frame<Renderer>, run: Run<'_>) {
    frame.fill_text(canvas::Text {
        content: run.words.to_string(),
        position: run.at,
        color: run.color,
        size: Pixels(run.size),
        line_height: LineHeight::Absolute(Pixels(line_box(run.size))),
        font: Font::with_name(look::FONT),
        align_x: Alignment::Left,
        align_y: Vertical::Bottom,
        max_width: f32::INFINITY,
        shaping: Shaping::Advanced,
    });
}

#[cfg(test)]
mod tests;
