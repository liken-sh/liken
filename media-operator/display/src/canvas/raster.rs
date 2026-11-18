//! Rasterizes each shape on the CPU with antialiased edges, and the
//! frame draws the result as an image. The scrubber's segments, the
//! playhead, and the panels are drawn this way.
//!
//! iced draws a shape as a mesh. Without multisampling, the mesh has
//! jagged edges. With multisampling, every layer that has a mesh
//! renders into a multisampled target the size of the whole surface,
//! resolves it, and blends the result over the frame. The display's
//! window covers the whole screen, so that is three passes over every
//! pixel of a 3840 by 2160 surface to draw a bar and a hexagon. An
//! image costs one texture read for each pixel it covers, and the cache
//! below rasterizes a shape that does not change only once.

use std::cell::RefCell;
use std::collections::HashMap;
use std::hash::{DefaultHasher, Hash, Hasher};

use iced::advanced::graphics::geometry::path::lyon_path::Event;
use iced::advanced::image::Handle;
use iced::widget::canvas::Path;
use iced::{Color, Point, Rectangle, Size};
use tiny_skia::{FillRule, Mask, PathBuilder, Stroke, Transform};

/// How a shape covers its pixels.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Paint {
    /// Inside the path, by the nonzero rule, which is the toolkit's
    /// default.
    Fill,
    /// Along the path, with a width in logical pixels. iced applies the
    /// stroke width after the canvas scale, so the width scales with the
    /// window's scale factor and not with the canvas scale. The raster
    /// does the same, so a border is as wide as iced would draw it.
    Stroke(f32),
}

/// iced_wgpu uploads an image of this many bytes or more on a worker
/// thread and draws it no earlier than the next frame. A large shape
/// would then be missing from the first frame it appears in, so
/// `rasterize` cuts a raster into bands under this size. The value is
/// `MAX_SYNC_SIZE` in iced_wgpu's `src/image/cache.rs`.
const MAX_SYNC: usize = 2 * 1024 * 1024;

/// The number of shapes the cache keeps rasters for. A shape that
/// moves by whole pixels reuses its raster, but a shape that changes
/// size adds a new entry, so the cache grows while the playhead and the
/// fills change size. When the cache reaches this size it is cleared,
/// and each shape on screen is rasterized again.
const CACHE_LIMIT: usize = 256;

/// One horizontal slice of a rasterized shape: the canvas rectangle it
/// covers, and the toolkit image that holds its pixels.
#[derive(Debug, Clone)]
pub struct Band {
    pub bounds: Rectangle,
    pub handle: Handle,
}

thread_local! {
    // The frame loop draws on one thread, so the cache needs no lock.
    static CACHE: RefCell<HashMap<u64, Vec<Band>>> = RefCell::new(HashMap::new());
}

/// Rasterizes one shape in one colour and returns the bands that cover
/// it, in canvas units. The pixels do not include the colour's alpha.
/// The caller draws the bands at that alpha, so a shape that fades is
/// rasterized once.
///
/// `scale` is the logical pixels in one canvas unit, and `density` is
/// the window's scale factor, the output pixels in one logical pixel.
/// The shape is rasterized at output resolution, and its bounding box
/// is aligned to whole output pixels, so each image pixel draws on one
/// screen pixel.
///
/// The cache key is the outline relative to the shape's own box, so a
/// shape that moves by whole output pixels reuses its raster.
pub fn raster(path: &Path, paint: Paint, color: Color, scale: f32, density: f32) -> Vec<Band> {
    let pixels = scale * density;
    let Some(outline) = outline(path, pixels) else {
        return Vec::new();
    };
    let shape = match paint {
        Paint::Fill => Some(outline),
        Paint::Stroke(width) => outline.stroke(
            &Stroke {
                width: width * density,
                ..Stroke::default()
            },
            1.0,
        ),
    };
    let Some(shape) = shape else {
        return Vec::new();
    };

    // Round the box outward to whole output pixels, because a pixel
    // that an edge crosses holds partial coverage and must be in the
    // image.
    let bounds = shape.bounds();
    let (left, top) = (bounds.left().floor(), bounds.top().floor());
    let width = (bounds.right().ceil() - left) as u32;
    let height = (bounds.bottom().ceil() - top) as u32;
    if width == 0 || height == 0 {
        return Vec::new();
    }
    let rgb = [color.r, color.g, color.b].map(|channel| (channel * 255.0).round() as u8);
    let key = key(&shape, left, top, paint, rgb);

    let bands = CACHE.with(|cache| cache.borrow().get(&key).cloned());
    let bands = match bands {
        Some(bands) => bands,
        None => {
            let bands = rasterize(&shape, left, top, width, height, rgb, pixels);
            CACHE.with(|cache| {
                let mut cache = cache.borrow_mut();
                if cache.len() >= CACHE_LIMIT {
                    cache.clear();
                }
                cache.insert(key, bands.clone());
            });
            bands
        }
    };

    // The cached bands are relative to their own box, so move them to
    // this shape's position.
    let (x, y) = (left / pixels, top / pixels);
    bands
        .into_iter()
        .map(|band| Band {
            bounds: Rectangle::new(
                Point::new(band.bounds.x + x, band.bounds.y + y),
                band.bounds.size(),
            ),
            handle: band.handle,
        })
        .collect()
}

/// Converts an iced path to a tiny-skia path, scaled to output pixels.
fn outline(path: &Path, pixels: f32) -> Option<tiny_skia::Path> {
    let at = |point: Point| (point.x * pixels, point.y * pixels);
    let mut builder = PathBuilder::new();
    for event in path.raw().iter() {
        match event {
            Event::Begin { at: point } => {
                let (x, y) = at(Point::new(point.x, point.y));
                builder.move_to(x, y);
            }
            Event::Line { to, .. } => {
                let (x, y) = at(Point::new(to.x, to.y));
                builder.line_to(x, y);
            }
            Event::Quadratic { ctrl, to, .. } => {
                let (cx, cy) = at(Point::new(ctrl.x, ctrl.y));
                let (x, y) = at(Point::new(to.x, to.y));
                builder.quad_to(cx, cy, x, y);
            }
            Event::Cubic {
                ctrl1, ctrl2, to, ..
            } => {
                let (ax, ay) = at(Point::new(ctrl1.x, ctrl1.y));
                let (bx, by) = at(Point::new(ctrl2.x, ctrl2.y));
                let (x, y) = at(Point::new(to.x, to.y));
                builder.cubic_to(ax, ay, bx, by, x, y);
            }
            Event::End { close, .. } => {
                if close {
                    builder.close();
                }
            }
        }
    }
    builder.finish()
}

fn key(shape: &tiny_skia::Path, left: f32, top: f32, paint: Paint, rgb: [u8; 3]) -> u64 {
    let mut hasher = DefaultHasher::new();
    for point in shape.points() {
        (point.x - left).to_bits().hash(&mut hasher);
        (point.y - top).to_bits().hash(&mut hasher);
    }
    shape.verbs().len().hash(&mut hasher);
    match paint {
        Paint::Fill => 0u32.hash(&mut hasher),
        Paint::Stroke(width) => width.to_bits().hash(&mut hasher),
    }
    rgb.hash(&mut hasher);
    hasher.finish()
}

/// Rasterizes the shape into a coverage mask, then writes RGBA pixels
/// that all have the shape's exact colour, with the coverage as alpha.
/// iced blends images as RGBA that is not premultiplied, and a
/// premultiplied pixmap would round the colour of each pixel that the
/// shape covers only in part.
fn rasterize(
    shape: &tiny_skia::Path,
    left: f32,
    top: f32,
    width: u32,
    height: u32,
    rgb: [u8; 3],
    pixels: f32,
) -> Vec<Band> {
    let Some(mut mask) = Mask::new(width, height) else {
        return Vec::new();
    };
    mask.fill_path(
        shape,
        FillRule::Winding,
        true,
        Transform::from_translate(-left, -top),
    );
    let row = width as usize * 4;
    let per_band = ((MAX_SYNC - 1) / row).max(1);
    mask.data()
        .chunks(width as usize * per_band)
        .enumerate()
        .map(|(index, coverage)| {
            let rows = coverage.len() / width as usize;
            let rgba = coverage
                .iter()
                .flat_map(|alpha| [rgb[0], rgb[1], rgb[2], *alpha])
                .collect::<Vec<u8>>();
            Band {
                bounds: Rectangle::new(
                    Point::new(0.0, (index * per_band) as f32 / pixels),
                    Size::new(width as f32 / pixels, rows as f32 / pixels),
                ),
                handle: Handle::from_rgba(width, rows as u32, rgba),
            }
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The alpha bytes of every band, top to bottom, and the width of a
    /// row.
    fn coverage(bands: &[Band]) -> (u32, Vec<u8>) {
        let mut width = 0;
        let alphas = bands
            .iter()
            .flat_map(|band| match &band.handle {
                Handle::Rgba {
                    width: w, pixels, ..
                } => {
                    width = *w;
                    pixels.chunks(4).map(|pixel| pixel[3]).collect::<Vec<_>>()
                }
                _ => Vec::new(),
            })
            .collect();
        (width, alphas)
    }

    fn square(at: Point, side: f32) -> Path {
        Path::rectangle(at, Size::new(side, side))
    }

    #[test]
    fn a_square_on_whole_pixels_covers_its_box() {
        let bands = raster(
            &square(Point::new(10.0, 20.0), 4.0),
            Paint::Fill,
            Color::from_rgb8(0x12, 0x34, 0x56),
            1.0,
            1.0,
        );
        assert_eq!(bands.len(), 1);
        assert_eq!(
            bands[0].bounds,
            Rectangle::new(Point::new(10.0, 20.0), Size::new(4.0, 4.0))
        );
        let (width, alphas) = coverage(&bands);
        assert_eq!(width, 4);
        assert!(
            alphas.iter().all(|alpha| *alpha == 255),
            "the alphas are {alphas:?}, want all 255"
        );
        let Handle::Rgba { pixels, .. } = &bands[0].handle else {
            panic!("the handle is not Handle::Rgba");
        };
        assert_eq!(&pixels[..3], &[0x12, 0x34, 0x56]);
    }

    /// A square that starts half a pixel in covers half of each edge
    /// pixel.
    #[test]
    fn an_edge_between_pixels_covers_part_of_them() {
        let bands = raster(
            &square(Point::new(0.5, 0.0), 2.0),
            Paint::Fill,
            Color::WHITE,
            1.0,
            1.0,
        );
        let (width, alphas) = coverage(&bands);
        assert_eq!(width, 3);
        let first_row = &alphas[..3];
        assert!(
            (first_row[0] as i32 - 128).abs() <= 2,
            "the first edge pixel is {}, want 128 within 2",
            first_row[0]
        );
        assert_eq!(first_row[1], 255);
        assert!(
            (first_row[2] as i32 - 128).abs() <= 2,
            "the last edge pixel is {}, want 128 within 2",
            first_row[2]
        );
    }

    #[test]
    fn the_raster_has_output_pixels_and_returns_canvas_units() {
        let bands = raster(
            &square(Point::new(1.0, 1.0), 2.0),
            Paint::Fill,
            Color::WHITE,
            2.0,
            2.0,
        );
        let (width, _) = coverage(&bands);
        assert_eq!(width, 8);
        assert_eq!(
            bands[0].bounds,
            Rectangle::new(Point::new(1.0, 1.0), Size::new(2.0, 2.0))
        );
    }

    #[test]
    fn a_stroke_scales_with_the_window_and_not_the_canvas() {
        let line = Path::line(Point::new(0.0, 5.0), Point::new(10.0, 5.0));
        let stroke = |scale, density| {
            let bands = raster(&line, Paint::Stroke(2.0), Color::WHITE, scale, density);
            bands[0].bounds.height * scale * density
        };
        assert_eq!(stroke(1.0, 1.0), 2.0);
        assert_eq!(stroke(2.0, 1.0), 2.0);
        assert_eq!(stroke(1.0, 2.0), 4.0);
    }

    /// The bands of a large shape cover its whole box, with no gap or
    /// overlap.
    #[test]
    fn a_large_shape_is_cut_into_bands() {
        let bands = raster(
            &Path::rectangle(Point::ORIGIN, Size::new(1920.0, 1080.0)),
            Paint::Fill,
            Color::BLACK,
            1.0,
            1.0,
        );
        assert!(bands.len() > 1);
        let rows: f32 = bands.iter().map(|band| band.bounds.height).sum();
        assert_eq!(rows, 1080.0);
        assert!(
            bands
                .windows(2)
                .all(|pair| { pair[0].bounds.y + pair[0].bounds.height == pair[1].bounds.y })
        );
    }

    /// A shape that moves by part of a pixel is rasterized again.
    #[test]
    fn a_whole_pixel_move_reuses_the_raster() {
        let at = |x: f32| {
            raster(
                &square(Point::new(x, 3.0), 5.0),
                Paint::Fill,
                Color::WHITE,
                1.0,
                1.0,
            )
            .remove(0)
        };
        let (first, moved, between) = (at(40.0), at(47.0), at(47.5));
        assert_eq!(first.handle.id(), moved.handle.id());
        assert_eq!(moved.bounds.x, 47.0);
        assert_ne!(first.handle.id(), between.handle.id());
    }

    #[test]
    fn a_shape_with_no_area_draws_nothing() {
        let bands = raster(
            &square(Point::new(3.0, 3.0), 0.0),
            Paint::Fill,
            Color::WHITE,
            1.0,
            1.0,
        );
        assert!(bands.is_empty());
    }
}
