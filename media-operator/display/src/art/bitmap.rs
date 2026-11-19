//! One decoded picture, ready for the toolkit to draw.

use iced::advanced::image::Handle;
use iced::{Point, Rectangle, Size};

use crate::canvas::{Brush, Canvas};

/// iced_wgpu uploads an image of this many bytes or more on a worker
/// thread, and draws nothing for it until a later frame. A smaller image
/// uploads during the frame that draws it. So `from_rgba` splits a larger
/// picture into bands under this size, and the picture appears in the
/// first frame that draws it. The value is `MAX_SYNC_SIZE` in iced_wgpu's
/// `src/image/cache.rs`.
const MAX_SYNC: usize = 2 * 1024 * 1024;

/// A horizontal slice of a bitmap: its first row, its row count, and the
/// iced image that holds its pixels.
#[derive(Debug, Clone)]
struct Band {
    top: u32,
    height: u32,
    handle: Handle,
}

/// A decoded picture, with its size in output pixels, stored as one or more
/// bands. Cloning is cheap because an iced `Handle` shares its pixels.
#[derive(Debug, Clone)]
pub struct Bitmap {
    pub width: u32,
    pub height: u32,
    bands: Vec<Band>,
}

impl Bitmap {
    /// Builds a bitmap from straight-alpha RGBA pixels in row-major order.
    /// Returns `None` when a dimension is zero or `pixels` is shorter than
    /// `width * height * 4`, and drops the bytes past that length.
    pub fn from_rgba(width: u32, height: u32, mut pixels: Vec<u8>) -> Option<Self> {
        let (rows, row) = (height as usize, width as usize * 4);
        if width == 0 || height == 0 || pixels.len() < row * rows {
            return None;
        }
        // A one-band picture hands `pixels` to the toolkit as it is, so
        // without the cut its handle would hold the extra bytes of a
        // longer buffer. The bands of a larger picture take exact slices.
        pixels.truncate(row * rows);

        let per_band = ((MAX_SYNC - 1) / row).max(1);
        // A picture under the limit is one band, which takes `pixels`
        // without a copy.
        if per_band >= rows {
            return Some(Self {
                width,
                height,
                bands: vec![Band {
                    top: 0,
                    height,
                    handle: Handle::from_rgba(width, height, pixels),
                }],
            });
        }
        let bands = (0..rows)
            .step_by(per_band)
            .map(|top| {
                let height = per_band.min(rows - top);
                Band {
                    top: top as u32,
                    height: height as u32,
                    handle: Handle::from_rgba(
                        width,
                        height as u32,
                        pixels[top * row..(top + height) * row].to_vec(),
                    ),
                }
            })
            .collect();
        Some(Self {
            width,
            height,
            bands,
        })
    }

    /// Draws the picture with its top left corner at `at`, in canvas units.
    /// Every picture is decoded at the output's pixel size, so one picture
    /// pixel covers one output pixel.
    pub fn draw(&self, brush: &mut Brush<'_>, at: Point) {
        let canvas = brush.canvas();
        for band in &self.bands {
            brush.image(
                Rectangle::new(
                    Point::new(at.x, at.y + canvas.to_canvas(band.top as f32)),
                    Size::new(
                        canvas.to_canvas(self.width as f32),
                        canvas.to_canvas(band.height as f32),
                    ),
                ),
                &band.handle,
            );
        }
    }

    /// All the bands' pixels in row-major order. Tests compare pixels
    /// instead of handles, because every `Handle` has a unique id and two
    /// handles never compare equal.
    #[cfg(test)]
    pub fn pixels(&self) -> Vec<u8> {
        self.bands
            .iter()
            .flat_map(|band| match &band.handle {
                Handle::Rgba { pixels, .. } => pixels.to_vec(),
                _ => Vec::new(),
            })
            .collect()
    }

    /// The picture's height in canvas units, rounded to a whole unit. The
    /// header places its text below a logo of this height.
    pub fn canvas_height(&self, canvas: &Canvas) -> f32 {
        (canvas.to_canvas(self.height as f32) + 0.5).floor()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn canvas() -> Canvas {
        Canvas::for_output(Size::new(1920.0, 1080.0))
    }

    /// The pixels of one band's handle.
    fn read_back(handle: &Handle) -> Vec<u8> {
        match handle {
            Handle::Rgba { pixels, .. } => pixels.to_vec(),
            _ => Vec::new(),
        }
    }

    #[test]
    fn straight_rgba_reads_back_as_it_arrived() {
        let pixels = [255, 0, 0, 255, 255, 255, 255, 128, 0, 0, 0, 0];
        let bitmap = Bitmap::from_rgba(3, 1, pixels.to_vec())
            .expect("from_rgba accepts a 3x1 buffer of 12 bytes");
        assert_eq!(bitmap.width, 3);
        assert_eq!(bitmap.height, 1);
        assert_eq!(read_back(&bitmap.bands[0].handle), pixels.to_vec());
    }

    /// A buffer longer than its size gives the toolkit only the bytes of
    /// the picture, the same as a picture in several bands takes.
    #[test]
    fn a_buffer_longer_than_its_size_keeps_only_the_picture() {
        let pixels = [255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255, 9, 9, 9, 9];
        let bitmap = Bitmap::from_rgba(3, 1, pixels.to_vec())
            .expect("from_rgba accepts a 3x1 buffer of 16 bytes");
        assert_eq!(
            read_back(&bitmap.bands[0].handle),
            pixels[..12].to_vec(),
            "the band holds the 4 extra bytes, want only the 12 bytes of a 3x1 picture"
        );
    }

    #[test]
    fn a_buffer_too_short_for_its_size_is_no_picture() {
        assert!(Bitmap::from_rgba(2, 1, vec![0, 0, 0, 255]).is_none());
        assert!(Bitmap::from_rgba(0, 1, vec![0, 0, 0, 255]).is_none());
        assert!(Bitmap::from_rgba(1, 0, vec![0, 0, 0, 255]).is_none());
    }

    /// Every band is under the limit, and together the bands cover each
    /// row once.
    #[test]
    fn a_large_picture_reads_as_bands() {
        let (width, height) = (1024u32, 700u32);
        let bitmap = Bitmap::from_rgba(width, height, vec![255; (width * height * 4) as usize])
            .expect("from_rgba accepts a 1024x700 buffer of 4 bytes per pixel");
        assert!(bitmap.bands.len() > 1);
        assert_eq!(bitmap.bands[0].top, 0);
        assert_eq!(
            bitmap.bands.iter().map(|band| band.height).sum::<u32>(),
            height
        );
        for pair in bitmap.bands.windows(2) {
            assert_eq!(pair[0].top + pair[0].height, pair[1].top);
            assert!(read_back(&pair[0].handle).len() < MAX_SYNC);
        }
    }

    /// A small picture is one band, and its height scales with the canvas.
    #[test]
    fn a_small_picture_is_one_band() {
        let bitmap = Bitmap::from_rgba(4, 8, vec![0; 4 * 8 * 4])
            .expect("from_rgba accepts a 4x8 buffer of 4 bytes per pixel");
        assert_eq!(bitmap.bands.len(), 1);
        assert_eq!(bitmap.canvas_height(&canvas()), 8.0);
        assert_eq!(
            bitmap.canvas_height(&Canvas::for_output(Size::new(3840.0, 2160.0))),
            4.0
        );
    }
}
