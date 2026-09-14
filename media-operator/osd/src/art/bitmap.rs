//! One decoded picture, ready for the toolkit to draw.

use iced::advanced::image::Handle;
use iced::{Point, Rectangle, Size};

use crate::canvas::{Brush, Canvas};

/// What the toolkit may hand a bitmap in one go. A toolkit image at or over
/// this size uploads on a thread of its own and draws no earlier than the next
/// frame, so a picture that large is read as bands under the limit.
// The toolkit uploads a picture synchronously up to a size limit, so a
// larger one goes up in bands.
const MAX_SYNC: usize = 2 * 1024 * 1024;

/// One band of a bitmap: the row it starts at, the rows it covers, and the
/// toolkit image it was read into.
#[derive(Debug, Clone)]
struct Band {
    top: u32,
    height: u32,
    handle: Handle,
}

/// One decoded picture: its size in real pixels, and the toolkit images the
/// display read it into. A clone shares the pixels, because a toolkit image
/// holds its own.
#[derive(Debug, Clone)]
pub struct Bitmap {
    pub width: u32,
    pub height: u32,
    bands: Vec<Band>,
}

impl Bitmap {
    /// Read straight-alpha RGBA, row major, as the decoder hands it back. A
    /// buffer shorter than the size it states is no picture at all.
    pub fn from_rgba(width: u32, height: u32, pixels: &[u8]) -> Option<Self> {
        let (rows, row) = (height as usize, width as usize * 4);
        if width == 0 || height == 0 || pixels.len() < row * rows {
            return None;
        }

        let per_band = ((MAX_SYNC - 1) / row).max(1);
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

    /// Draw the picture at the place the formulas give it, in real pixels. The
    /// display decodes every picture to the pixel size the screen takes, so one
    /// pixel of it covers one output pixel.
    pub fn draw(&self, brush: &mut Brush<'_>, at: Point) {
        let scale = brush.canvas().scale;
        for band in &self.bands {
            brush.image(
                Rectangle::new(
                    Point::new(at.x / scale, (at.y + band.top as f32) / scale),
                    Size::new(self.width as f32 / scale, band.height as f32 / scale),
                ),
                &band.handle,
            );
        }
    }

    /// The pixels the bands carry, row major, as the decoder handed them
    /// over. A test reads a drawn picture through this and not through the
    /// handles, because a handle takes an id of its own on every call.
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

    /// The rows the picture covers in canvas units, which the header measures
    /// its second line down from.
    pub fn canvas_height(&self, canvas: &Canvas) -> f32 {
        (self.height as f32 / canvas.scale + 0.5).floor()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn canvas() -> Canvas {
        Canvas::for_output(Size::new(1920.0, 1080.0))
    }

    /// The pixels one toolkit image carries. A handle takes an id of its own
    /// on every call, so two handles never compare equal and the pixels are
    /// what a test reads.
    fn read_back(handle: &Handle) -> Vec<u8> {
        match handle {
            Handle::Rgba { pixels, .. } => pixels.to_vec(),
            _ => Vec::new(),
        }
    }

    /// The decoder hands back straight alpha, which is what the toolkit
    /// blends on, so the pixels arrive as they are.
    #[test]
    fn straight_rgba_reads_back_as_it_arrived() {
        let pixels = [255, 0, 0, 255, 255, 255, 255, 128, 0, 0, 0, 0];
        let bitmap = Bitmap::from_rgba(3, 1, &pixels).expect("a picture the display can draw");
        assert_eq!(bitmap.width, 3);
        assert_eq!(bitmap.height, 1);
        assert_eq!(read_back(&bitmap.bands[0].handle), pixels.to_vec());
    }

    /// A buffer shorter than the size it states is no picture at all, and
    /// neither is a size of nothing.
    #[test]
    fn a_buffer_that_does_not_match_its_size_is_no_picture() {
        assert!(Bitmap::from_rgba(2, 1, &[0, 0, 0, 255]).is_none());
        assert!(Bitmap::from_rgba(0, 1, &[0, 0, 0, 255]).is_none());
        assert!(Bitmap::from_rgba(1, 0, &[0, 0, 0, 255]).is_none());
    }

    /// A picture too large to upload inside one frame reads as bands under the
    /// limit, and the bands cover every row once.
    #[test]
    fn a_large_picture_reads_as_bands() {
        let (width, height) = (1024u32, 700u32);
        let bitmap = Bitmap::from_rgba(width, height, &vec![255; (width * height * 4) as usize])
            .expect("a picture in bands");
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

    /// A small picture is one band, and its rows measure in canvas units at the
    /// surface's own scale.
    #[test]
    fn a_small_picture_is_one_band() {
        let bitmap = Bitmap::from_rgba(4, 8, &[0; 4 * 8 * 4]).expect("one band");
        assert_eq!(bitmap.bands.len(), 1);
        assert_eq!(bitmap.canvas_height(&canvas()), 8.0);
        assert_eq!(
            bitmap.canvas_height(&Canvas::for_output(Size::new(3840.0, 2160.0))),
            4.0
        );
    }
}
