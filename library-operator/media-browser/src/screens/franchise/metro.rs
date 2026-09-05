// The metro strip between the time labels and the cards: one vertical
// line per universe, in the order the file names them, at one pitch. A
// universe's line is one unbroken run from the middle of its first
// entry's row to the middle of its last, through the gaps between rows
// and through the thin rows; above and below it there is nothing. A row
// takes a dot on the line of every universe it names, a filled circle in
// the line's color, and a bar across the dots where it names several: a
// pill in the ink color behind the dots. The dot of a thin row is
// smaller. The line colors are evenly spaced hues at one lightness and
// one chroma, so eight universes stay distinct on black, and the first
// universe takes the first hue. With one universe the strip has no width
// and is not drawn, and the legend over it is not drawn either. The
// strip scrolls with the rows. The lines, the bars, and the dots are all
// fills, so they draw in that order inside one layer.

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Color, Point, Rectangle};

use super::wall::{GAP, Row};
use crate::look;
use crate::views::{area, rounded, text};

/// The space one universe's line takes across the strip.
pub const PITCH: f32 = 32.0;

/// The width of a line.
pub const LINE: f32 = 4.0;

/// The radius of a card's dot, and of a thin row's smaller one.
pub const DOT: f32 = 9.0;
pub const SMALL: f32 = 6.0;

// The opacity a line draws at, so a dot reads over it.
const RUN: f32 = 0.85;

// The lightness and the chroma every line shares, and the hue the first
// universe takes. The rest are spaced evenly around the wheel from it.
const LIGHTNESS: f32 = 0.78;
const CHROMA: f32 = 0.12;
const FIRST_HUE: f32 = 20.0;

// The space between the legend's items, and between a legend dot and its
// name.
const LEGEND_GAP: f32 = 24.0;
const NAME_GAP: f32 = 8.0;

/// The width the strip takes for this many universes, and none for one.
pub fn width(universes: usize) -> f32 {
    match universes > 1 {
        true => universes as f32 * PITCH,
        false => 0.0,
    }
}

/// Where one universe's line stands across the strip: the middle of its
/// pitch.
pub fn line_x(strip: Rectangle, index: usize) -> f32 {
    strip.x + index as f32 * PITCH + PITCH / 2.0
}

/// The color of one universe's line among this many: the hue spaced
/// evenly from the first, at the shared lightness and chroma.
pub fn color(index: usize, universes: usize) -> Color {
    let hue = FIRST_HUE + index as f32 * 360.0 / universes.max(1) as f32;
    oklch(LIGHTNESS, CHROMA, hue)
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

/// The first and the last row each universe takes a dot on, and nothing
/// for a universe no row names.
pub fn runs(rows: &[Row], universes: usize) -> Vec<Option<(usize, usize)>> {
    let mut runs = vec![None; universes];
    for (index, row) in rows.iter().enumerate() {
        for universe in &row.cell.universes {
            let Some(run) = runs.get_mut(*universe) else {
                continue;
            };
            *run = match *run {
                None => Some((index, index)),
                Some((first, _)) => Some((first, index)),
            };
        }
    }
    runs
}

/// The middle of one row, in frame space after the scroll.
pub fn middle(strip: Rectangle, row: usize, tops: &[f32], down: f32) -> f32 {
    let top = tops.get(row).copied().unwrap_or_default();
    let next = tops.get(row + 1).copied().unwrap_or(top + GAP);
    strip.y + (top + next - GAP) / 2.0 - down
}

/// The strip: every line first, then every bar, then every dot, so a dot
/// lies over a bar and a bar over the lines. Only the rows the strip
/// reaches build dots, because a wall of a hundred rows is drawn on every
/// frame.
pub fn draw(
    frame: &mut canvas::Frame<Renderer>,
    strip: Rectangle,
    rows: &[Row],
    tops: &[f32],
    down: f32,
) {
    let universes = (strip.width / PITCH).round() as usize;
    if universes < 2 {
        return;
    }
    for (index, run) in runs(rows, universes).iter().enumerate() {
        let Some((first, last)) = run else {
            continue;
        };
        let x = line_x(strip, index);
        let (top, bottom) = (
            middle(strip, *first, tops, down),
            middle(strip, *last, tops, down),
        );
        frame.fill_rectangle(
            Point::new(x - LINE / 2.0, top),
            iced_winit::core::Size::new(LINE, bottom - top),
            Color {
                a: RUN,
                ..color(index, universes)
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
        let named = &row.cell.universes;
        if let (Some(first), Some(last)) = (named.iter().min(), named.iter().max())
            && first != last
        {
            let bar = area(
                line_x(strip, *first) - radius,
                y - radius,
                line_x(strip, *last) - line_x(strip, *first) + 2.0 * radius,
                2.0 * radius,
            );
            frame.fill(&rounded(bar, radius), look::text());
        }
        for universe in named {
            frame.fill(
                &canvas::Path::circle(Point::new(line_x(strip, *universe), y), radius),
                color(*universe, universes),
            );
        }
    }
}

/// The legend: a dot and a name per universe in a row, from the left of
/// the strip, at the height of the band it stands in. A name too long for
/// the room left ends the row, so the legend never runs past the cards.
pub fn legend(frame: &mut canvas::Frame<Renderer>, band: Rectangle, names: &[String]) {
    if names.len() < 2 {
        return;
    }
    let mut x = band.x;
    let y = band.y + text::height(1, look::CAPTION) / 2.0;
    for (index, name) in names.iter().enumerate() {
        let room = band.x + band.width - x - SMALL * 2.0 - NAME_GAP;
        let shown = text::cut(name, look::CAPTION, room);
        if room <= 0.0 {
            break;
        }
        frame.fill(
            &canvas::Path::circle(Point::new(x + SMALL, y), SMALL),
            color(index, names.len()),
        );
        x += SMALL * 2.0 + NAME_GAP;
        text::line(
            frame,
            &shown,
            Point::new(x, band.y),
            look::CAPTION,
            look::muted(),
            room,
        );
        x += text::width(&shown, look::CAPTION) + LEGEND_GAP;
    }
}

#[cfg(test)]
mod tests;
