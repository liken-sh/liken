// A shade that changes along one axis, drawn as a picture. The toolkit's
// gradient shader searches its stops at every pixel it covers, and a
// scrim covers most of the frame, so on a small GPU the scrim cost more
// than all the art under it. A picture of the same shade costs one
// texture read a pixel.
//
// The picture is a tile at the panel's own pixels: as long as the run
// and at most `TILE` pixels across it, repeated across the bounds on
// whole pixels. A solid is one texel stretched over its bounds, because
// the toolkit fills a solid with no noise. Each pixel of a tile
// takes the gradient shader's own noise before its alpha rounds to a
// byte, so a row of one value rounds up on some pixels and down on
// others. Without the noise, every pixel of a row rounds the same way,
// and an eight-bit panel shows the steps of the falloff as bands. Each
// tile is built once and kept, so the renderer uploads it once and every
// later frame draws the same handle.
//
// The picture is an image, and inside one layer the renderer draws
// every image after every mesh. A ramp therefore draws over the meshes
// of its layer. A layer that puts a mesh over a ramp puts the ramp on a
// layer under it.

use std::cell::{Cell, RefCell};
use std::collections::HashMap;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_widget::core::Bytes;
use iced_widget::image::Handle;
use iced_winit::core::{Color, Point, Rectangle, Size};

/// The axis a ramp changes along.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Axis {
    /// From the left edge of the bounds to the right edge.
    Across,
    /// From the top edge of the bounds to the foot.
    Down,
}

/// One stop of a ramp: the share of the run it stands at, and the color
/// there.
pub type Stop = (f32, Color);

/// The most panel pixels a tile spans across its run. The renderer draws
/// every repeat of a tile from one upload, so a narrow tile costs no more
/// to draw than one picture the size of the bounds, and it holds a small
/// share of the memory. A full-frame scrim tile at 1080p holds 0.6 MB
/// where a picture of the whole scrim would hold 5.6 MB.
pub const TILE: u32 = 128;

// The fewest pixels a tile spans across its run where the span divides
// into whole tiles. A span no tile between this and `TILE` divides takes
// even tiles and one shorter tile at its end.
const FEWEST: u32 = 64;

// The toolkit uploads a picture of fewer bytes than this within the
// frame that asks for it. A longer one draws on a later frame, and this
// client draws no later frame until an event, so a tile stays under it.
const SYNC_UPLOAD_BYTES: u32 = 2 * 1024 * 1024;

// How far the noise moves a pixel's alpha, in steps of one byte: up to
// this much above or below. It is the gradient shader's amplitude, 0.3
// of a step.
const DITHER: f32 = 0.3;

// The most tiles the cache keeps. A frame draws a few ramps at a few
// sizes, and a resize makes new ones, so a full cache starts again
// rather than grow with every size the window ever had.
const KEPT: usize = 32;

thread_local! {
    // The tiles built so far. The frame loop draws on one thread, so
    // the cache belongs to that thread and takes no lock.
    static PICTURES: RefCell<HashMap<Key, Handle>> = RefCell::new(HashMap::new());
    // The panel pixels one logical pixel spans. The canvas lays out in
    // logical pixels and does not say this, so the browser states it on
    // every rescale.
    static DENSITY: Cell<f32> = const { Cell::new(1.0) };
}

/// State the panel pixels one logical pixel spans. The tiles are built at
/// the panel's own pixels, so each texel covers one pixel and keeps its
/// own noise.
pub fn density(scale: f32) {
    if scale.is_finite() && scale > 0.0 {
        DENSITY.with(|density| density.set(scale));
    }
}

// What makes two tiles the same picture: the axis, the run in panel
// pixels, the tile's size in panel pixels along and across the run, the
// density the noise is placed at, and the stops, bit for bit.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
struct Key {
    axis: Axis,
    run: u32,
    along: u32,
    across: u32,
    density: u32,
    stops: Vec<[u32; 5]>,
}

/// Fill these bounds with a ramp along this axis. The run is the width
/// for [`Axis::Across`] and the height for [`Axis::Down`], and the stops
/// are shares of that run, in order.
pub fn fill(frame: &mut canvas::Frame<Renderer>, bounds: Rectangle, axis: Axis, stops: &[Stop]) {
    let density = DENSITY.with(Cell::get);
    let (run, across) = match axis {
        Axis::Across => (bounds.width, bounds.height),
        Axis::Down => (bounds.height, bounds.width),
    };
    let (run, across) = (
        (run * density).round() as u32,
        (across * density).round() as u32,
    );
    // The tiles start on a whole panel pixel, so each texel covers one
    // pixel and the filter never mixes the noise of two.
    let snap = |at: f32| (at * density).round() / density;
    let (x, y) = (snap(bounds.x), snap(bounds.y));
    if stops.is_empty() || run == 0 || across == 0 {
        return;
    }
    // A solid is one texel over the whole bounds, with no noise, the way
    // the toolkit fills a solid.
    if stops.windows(2).all(|pair| pair[0].1 == pair[1].1) {
        let key = Key {
            axis,
            run: 1,
            along: 1,
            across: 1,
            density: 0,
            stops: bits(stops),
        };
        frame.draw_image(bounds, canvas::Image::new(picture(key, stops)));
        return;
    }
    // A run too long for a tile of `TILE` rows under the upload cap takes
    // tiles of fewer rows.
    let most = TILE.min((SYNC_UPLOAD_BYTES - 1) / (4 * run)).max(1);
    for (offset, across) in spans(across, most) {
        let key = Key {
            axis,
            run,
            along: run,
            across,
            density: density.to_bits(),
            stops: bits(stops),
        };
        let (offset, across) = (offset as f32 / density, across as f32 / density);
        let place = match axis {
            Axis::Across => Rectangle::new(
                Point::new(x, y + offset),
                Size::new(run as f32 / density, across),
            ),
            Axis::Down => Rectangle::new(
                Point::new(x + offset, y),
                Size::new(across, run as f32 / density),
            ),
        };
        // The tile keeps the default filter. The renderer draws every
        // image of one filter in a batch of its own, so a tile on nearest
        // filtering would draw under the art its layer drew before it. A
        // texel lands on the center of its own pixel, so the filter reads
        // that texel alone.
        frame.draw_image(place, canvas::Image::new(picture(key, stops)));
    }
}

// The spans a length of panel pixels divides into: whole tiles of one
// size where a size between `FEWEST` and the most divides the length,
// and otherwise even tiles with one shorter tile at the end.
fn spans(length: u32, most: u32) -> Vec<(u32, u32)> {
    let size = (FEWEST.min(most)..=most)
        .rev()
        .find(|size| length.is_multiple_of(*size))
        .unwrap_or_else(|| length.div_ceil(length.div_ceil(most)));
    (0..length.div_ceil(size))
        .map(|index| (index * size, size.min(length - index * size)))
        .collect()
}

fn bits(stops: &[Stop]) -> Vec<[u32; 5]> {
    stops
        .iter()
        .map(|(at, color)| {
            [
                at.to_bits(),
                color.r.to_bits(),
                color.g.to_bits(),
                color.b.to_bits(),
                color.a.to_bits(),
            ]
        })
        .collect()
}

// The tile for a key, from the cache or built now.
fn picture(key: Key, stops: &[Stop]) -> Handle {
    PICTURES.with(|pictures| {
        let mut pictures = pictures.borrow_mut();
        if let Some(handle) = pictures.get(&key) {
            return handle.clone();
        }
        if pictures.len() >= KEPT {
            pictures.clear();
        }
        let (width, height) = match key.axis {
            Axis::Across => (key.along, key.across),
            Axis::Down => (key.across, key.along),
        };
        let handle = Handle::from_rgba(width, height, Bytes::from_owner(rgba(&key, stops)));
        pictures.insert(key, handle.clone());
        handle
    })
}

// The texels of a tile, row by row. Each one is the ramp's color at the
// center of its pixel along the run, with the noise at its place in
// logical pixels added to the alpha before it rounds.
fn rgba(key: &Key, stops: &[Stop]) -> Vec<u8> {
    let density = f32::from_bits(key.density);
    let (width, height) = match key.axis {
        Axis::Across => (key.along, key.across),
        Axis::Down => (key.across, key.along),
    };
    let run = key.run as f32;
    (0..height)
        .flat_map(|row| (0..width).map(move |column| (column, row)))
        .flat_map(|(column, row)| {
            let along = match key.axis {
                Axis::Across => column,
                Axis::Down => row,
            };
            let color = at((along as f32 + 0.5) / run, stops);
            // A noise of one half moves nothing, which is what a solid
            // takes.
            let noise = match key.density {
                0 => 0.5,
                _ => noise(
                    (column as f32 + 0.5) / density,
                    (row as f32 + 0.5) / density,
                ),
            };
            encode(color, noise)
        })
        .collect()
}

// The color of a ramp at this share of its run, as linear light with
// the alpha multiplied in. It is the toolkit's own rule for a gradient:
// the first stop's color before it, the last stop's color after it, and
// between two stops a smoothstep from one color to the next.
fn at(share: f32, stops: &[Stop]) -> [f32; 4] {
    let Some(((first, before), (last, after))) = stops.first().zip(stops.last()) else {
        return [0.0; 4];
    };
    if share <= *first {
        return premultiplied(*before);
    }
    if share >= *last {
        return premultiplied(*after);
    }
    let pair = stops
        .windows(2)
        .find(|pair| pair[0].0 <= share && share <= pair[1].0)
        .unwrap_or(&stops[..2]);
    let ((from_at, from), (to_at, to)) = (pair[0], pair[1]);
    let factor = smoothstep(from_at, to_at, share);
    let (from, to) = (premultiplied(from), premultiplied(to));
    std::array::from_fn(|channel| from[channel] + (to[channel] - from[channel]) * factor)
}

fn smoothstep(from: f32, to: f32, share: f32) -> f32 {
    let t = ((share - from) / (to - from)).clamp(0.0, 1.0);
    t * t * (3.0 - 2.0 * t)
}

fn premultiplied(color: Color) -> [f32; 4] {
    let [r, g, b, a] = color.into_linear();
    [r * a, g * a, b * a, a]
}

// The noise at one place in logical pixels, from 0 to 1. It is the hash
// the gradient shader dithers with, so the grain matches that shader's.
fn noise(x: f32, y: f32) -> f32 {
    ((x * 12.9898 + y * 78.233).sin() * 43758.547).fract().abs()
}

// One texel as the renderer's image atlas holds it: the color in sRGB
// with the alpha apart, because the atlas decodes sRGB and the image
// shader multiplies the alpha in again when it blends.
//
// The noise moves all four channels of the premultiplied color by up to
// `DITHER` of a step, the way the gradient shader moves them. The alpha
// takes it before it rounds. The color takes it too, so on a dark ground
// the shade lifts the pixel by the noise the way the shader's did, and a
// color that the noise would take below zero stays at zero.
fn encode([r, g, b, a]: [f32; 4], noise: f32) -> [u8; 4] {
    let moved = DITHER / 255.0 * (2.0 * noise - 1.0);
    let alpha = ((a + moved) * 255.0).round().clamp(0.0, 255.0);
    let straight = |channel: f32| match alpha > 0.0 {
        true => ((channel + moved).max(0.0) * 255.0 / alpha).min(1.0),
        false => 0.0,
    };
    let srgb = |channel: f32| (to_srgb(straight(channel)) * 255.0).round() as u8;
    [srgb(r), srgb(g), srgb(b), alpha as u8]
}

fn to_srgb(linear: f32) -> f32 {
    match linear <= 0.003_130_8 {
        true => linear * 12.92,
        false => 1.055 * linear.powf(1.0 / 2.4) - 0.055,
    }
}

#[cfg(test)]
mod tests;
