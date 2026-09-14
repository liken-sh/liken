//! The display reads its own art: it opens the file or fetches the URL,
//! decodes it, and scales it into the box the placement formulas give.

use std::io::{Cursor, Read};
use std::time::Duration;

use image::imageops::FilterType;
use image::{DynamicImage, ImageReader, RgbaImage};

use super::bitmap::Bitmap;

/// How long a fetch waits for the connection, and how long it waits on a read
/// that has begun.
const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);
const READ_TIMEOUT: Duration = Duration::from_secs(30);

/// The one reference form that is not a file the pod mounts.
const FETCHED: &str = "https://";

/// Read one picture's bytes. An https reference is a network fetch. Any other
/// value is a path the resolver rewrote under the media mount.
pub fn open(reference: &str) -> Option<Vec<u8>> {
    match reference.starts_with(FETCHED) {
        true => fetch(reference),
        false => std::fs::read(reference).ok(),
    }
}

/// Fetch one picture over https. A reply with any status but 200 carries no
/// picture, which `ureq` reports as an error of its own.
fn fetch(url: &str) -> Option<Vec<u8>> {
    let agent = ureq::AgentBuilder::new()
        .timeout_connect(CONNECT_TIMEOUT)
        .timeout_read(READ_TIMEOUT)
        .build();
    let mut bytes = Vec::new();
    agent
        .get(url)
        .call()
        .ok()?
        .into_reader()
        .read_to_end(&mut bytes)
        .ok()?;
    Some(bytes)
}

/// Decode one picture. The format comes from the bytes and not from the file
/// name, so a cover named `.jpg` that holds PNG still decodes.
pub fn read(bytes: &[u8]) -> Option<DynamicImage> {
    ImageReader::new(Cursor::new(bytes))
        .with_guessed_format()
        .ok()?
        .decode()
        .ok()
}

/// Decode one picture and scale it into the box.
pub fn fit(bytes: &[u8], box_w: u32, box_h: u32) -> Option<Bitmap> {
    scale(&read(bytes)?, box_w, box_h)
}

/// Scale one decoded picture to fit inside `box_w` by `box_h`, keeping the
/// aspect ratio, so the bitmap that comes back may be smaller than the box. A
/// logo passes the whole picture. A trickplay passes one cell of a sprite
/// sheet, so the crop and the scale are one step.
///
/// The blend runs on alpha-premultiplied colour, which keeps a transparent
/// edge clean: a logo with a clear surround would otherwise draw the surround's
/// own colour into every edge pixel. The pixels come back straight, because
/// that is what the toolkit blends on.
///
/// Triangle is the filter because its kernel widens with the downscale ratio,
/// so a shrink averages the source pixels like a box filter, at about a third
/// of Lanczos3's cost. The decode runs once per drawn size, so the cheaper
/// filter is enough. The library's browser scales its own art the same way.
pub fn scale(source: &DynamicImage, box_w: u32, box_h: u32) -> Option<Bitmap> {
    let (source_w, source_h) = (source.width(), source.height());
    if source_w == 0 || source_h == 0 || box_w == 0 || box_h == 0 {
        return None;
    }
    let ratio =
        (f64::from(box_w) / f64::from(source_w)).min(f64::from(box_h) / f64::from(source_h));
    let width = ((f64::from(source_w) * ratio).round() as u32).max(1);
    let height = ((f64::from(source_h) * ratio).round() as u32).max(1);

    let mut scaled =
        image::imageops::resize(&premultiplied(source), width, height, FilterType::Triangle);
    for pixel in scaled.pixels_mut() {
        let alpha = pixel[3];
        for channel in 0..3 {
            pixel[channel] = straight(pixel[channel], alpha);
        }
    }
    Bitmap::from_rgba(width, height, &scaled.into_raw())
}

/// One picture with every channel scaled by its own alpha.
fn premultiplied(source: &DynamicImage) -> RgbaImage {
    let mut pixels = source.to_rgba8();
    for pixel in pixels.pixels_mut() {
        let alpha = u32::from(pixel[3]);
        for channel in 0..3 {
            pixel[channel] = ((u32::from(pixel[channel]) * alpha + 127) / 255) as u8;
        }
    }
    pixels
}

/// One premultiplied channel with its alpha divided back out. A pixel that
/// covers nothing carries no colour.
fn straight(channel: u8, alpha: u8) -> u8 {
    match alpha {
        0 => 0,
        _ => ((u32::from(channel) * 255 + u32::from(alpha) / 2) / u32::from(alpha)).min(255) as u8,
    }
}

#[cfg(test)]
pub(super) mod tests {
    use super::*;
    use image::{ImageFormat, Rgba, RgbaImage};

    /// One picture of a single colour, encoded the way a logo or a cover
    /// arrives.
    pub(in crate::art) fn encoded(
        width: u32,
        height: u32,
        format: ImageFormat,
        color: [u8; 4],
    ) -> Vec<u8> {
        encoded_from(&RgbaImage::from_pixel(width, height, Rgba(color)), format)
    }

    /// One picture a test drew itself, encoded the same way.
    pub(in crate::art) fn encoded_from(picture: &RgbaImage, format: ImageFormat) -> Vec<u8> {
        let mut bytes = Vec::new();
        DynamicImage::ImageRgba8(picture.clone())
            .write_to(&mut Cursor::new(&mut bytes), format)
            .expect("a picture the tests can decode");
        bytes
    }

    /// The pixel at the middle of a decoded picture.
    pub(in crate::art) fn centre(bitmap: &Bitmap) -> [u8; 4] {
        let pixels = bitmap.pixels();
        let row = bitmap.width as usize * 4;
        let at = (bitmap.height / 2) as usize * row + (bitmap.width / 2) as usize * 4;
        [pixels[at], pixels[at + 1], pixels[at + 2], pixels[at + 3]]
    }

    /// A PNG and a JPEG both decode, the scale fits the box and keeps the
    /// ratio, and the colour survives.
    #[test]
    fn a_picture_fits_the_box_and_keeps_its_colour() {
        for format in [ImageFormat::Png, ImageFormat::Jpeg] {
            let bytes = encoded(100, 40, format, [10, 200, 30, 255]);
            let bitmap = fit(&bytes, 50, 50).expect("a picture the display can draw");
            assert_eq!((bitmap.width, bitmap.height), (50, 20), "{format:?}");
            assert_eq!(bitmap.pixels().len(), 50 * 20 * 4, "{format:?}");
            let [red, green, blue, alpha] = centre(&bitmap);
            assert!(
                red < 60 && green > 150 && blue < 60 && alpha == 255,
                "{format:?}"
            );
        }
    }

    /// A picture that covers nothing carries no colour, because the blend runs
    /// on premultiplied values.
    #[test]
    fn a_fully_transparent_picture_decodes_to_nothing() {
        let bytes = encoded(8, 8, ImageFormat::Png, [255, 255, 255, 0]);
        let bitmap = fit(&bytes, 8, 8).expect("a picture");
        assert!(bitmap.pixels().iter().all(|byte| *byte == 0));
    }

    /// The format comes from the bytes, so a reader that is handed something
    /// that is not a picture decodes nothing.
    #[test]
    fn what_is_not_a_picture_decodes_nothing() {
        assert!(read(&encoded(4, 4, ImageFormat::Png, [0, 0, 255, 255])).is_some());
        assert!(read(b"not an image").is_none());
        assert!(fit(b"not an image", 10, 10).is_none());
    }

    /// The fit is the whole rule: the tight axis fills the box, the other one
    /// falls short, and a picture smaller than the box grows into it.
    #[test]
    fn the_fit_keeps_the_ratio_in_both_directions() {
        for (source, box_size, want) in [
            ((400, 100), (760, 110), (440, 110)),
            ((100, 400), (760, 110), (28, 110)),
            ((760, 110), (760, 110), (760, 110)),
            ((1520, 220), (760, 110), (760, 110)),
            ((40, 20), (20, 20), (20, 10)),
            ((20, 20), (10, 10), (10, 10)),
            ((10, 10), (110, 220), (110, 110)),
        ] {
            let bytes = encoded(source.0, source.1, ImageFormat::Png, [0, 0, 0, 255]);
            let bitmap = fit(&bytes, box_size.0, box_size.1).expect("a picture");
            assert_eq!((bitmap.width, bitmap.height), want, "{source:?}");
        }
    }

    /// A picture with no pixels, and a box with no pixels, decode nothing.
    #[test]
    fn a_source_or_a_box_with_no_pixels_decodes_nothing() {
        let source = DynamicImage::ImageRgba8(RgbaImage::new(0, 0));
        assert!(scale(&source, 10, 10).is_none());
        let source = DynamicImage::ImageRgba8(RgbaImage::new(10, 10));
        assert!(scale(&source, 0, 10).is_none());
        assert!(scale(&source, 10, 0).is_none());
    }

    /// A reference that is not https names a file the pod mounts, and a file
    /// that is not there reads nothing.
    #[test]
    fn a_reference_that_is_not_https_names_a_file() {
        let path = std::env::temp_dir().join("media-display-open.png");
        let bytes = encoded(4, 4, ImageFormat::Png, [0, 0, 0, 255]);
        std::fs::write(&path, &bytes).expect("a file to read back");
        assert_eq!(open(&path.to_string_lossy()), Some(bytes));
        assert_eq!(open("/art/nothing.png"), None);
    }

    /// An https reference is a fetch, and a host that answers nothing carries
    /// no picture.
    #[test]
    fn an_https_reference_that_answers_nothing_carries_no_picture() {
        assert_eq!(open("https://127.0.0.1:1/logo.png"), None);
    }
}
