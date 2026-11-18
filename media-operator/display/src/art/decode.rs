//! Loads a logo, cover, or portrait for the display: reads the file or
//! fetches the URL, decodes it, and scales it to the box of the layout.

use std::io::{Cursor, Read};
use std::time::Duration;

use image::imageops::FilterType;
use image::{DynamicImage, ImageReader, RgbaImage};

use super::bitmap::Bitmap;

/// How long a fetch waits to connect, and then how long it waits for
/// each read.
const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);
const READ_TIMEOUT: Duration = Duration::from_secs(30);

const HTTPS_PREFIX: &str = "https://";

/// Read the bytes of one picture. An https reference is a URL to fetch,
/// and any other reference is a path under the media mount in the pod.
pub fn open(reference: &str) -> Option<Vec<u8>> {
    match reference.starts_with(HTTPS_PREFIX) {
        true => fetch(reference),
        false => std::fs::read(reference).ok(),
    }
}

/// `ureq` returns an error for a 4xx or 5xx status, so those responses
/// give `None`.
fn fetch(url: &str) -> Option<Vec<u8>> {
    let agent: ureq::Agent = ureq::Agent::config_builder()
        .timeout_connect(Some(CONNECT_TIMEOUT))
        .timeout_per_read(Some(READ_TIMEOUT))
        .build()
        .into();
    let mut bytes = Vec::new();
    agent
        .get(url)
        .call()
        .ok()?
        .into_body()
        .into_reader()
        .read_to_end(&mut bytes)
        .ok()?;
    Some(bytes)
}

/// Decode one picture. The format comes from the bytes, not from the
/// file name, so a `.jpg` file that holds a PNG still decodes.
pub fn read(bytes: &[u8]) -> Option<DynamicImage> {
    ImageReader::new(Cursor::new(bytes))
        .with_guessed_format()
        .ok()?
        .decode()
        .ok()
}

pub fn fit(bytes: &[u8], box_w: u32, box_h: u32) -> Option<Bitmap> {
    scale(&read(bytes)?, box_w, box_h)
}

/// Scale one decoded picture to fit inside `box_w` by `box_h` and keep
/// its aspect ratio, so the bitmap can be smaller than the box on one
/// axis.
///
/// The resize runs on premultiplied alpha. With straight alpha, the
/// color of a logo's transparent pixels would bleed into its edges. The
/// result is converted back to straight alpha, because iced's wgpu
/// renderer blends images with straight alpha.
///
/// The Triangle filter's kernel widens with the downscale ratio, so a
/// shrink averages the source pixels, at less cost than Lanczos3.
/// `media-browser` uses the same filter for its art.
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
            pixel[channel] = unpremultiply(pixel[channel], alpha);
        }
    }
    Bitmap::from_rgba(width, height, scaled.into_raw())
}

/// Decode one picture and scale it to cover the whole box, keeping its
/// aspect ratio. The overflow is cropped equally from the left and the
/// right, and one fifth from the top and four fifths from the bottom,
/// because the face is in the upper part of a portrait.
pub fn fill(bytes: &[u8], box_w: u32, box_h: u32) -> Option<Bitmap> {
    let source = read(bytes)?;
    let (source_w, source_h) = (source.width(), source.height());
    if source_w == 0 || source_h == 0 || box_w == 0 || box_h == 0 {
        return None;
    }
    let ratio =
        (f64::from(box_w) / f64::from(source_w)).max(f64::from(box_h) / f64::from(source_h));
    let crop_w = ((f64::from(box_w) / ratio).round() as u32).clamp(1, source_w);
    let crop_h = ((f64::from(box_h) / ratio).round() as u32).clamp(1, source_h);
    let x = (source_w - crop_w) / 2;
    let y = (source_h - crop_h) / 5;
    scale(&source.crop_imm(x, y, crop_w, crop_h), box_w, box_h)
}

/// Multiply each color channel of the picture by its alpha.
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

/// Divide the alpha back out of one premultiplied channel. A fully
/// transparent pixel has no color to recover, so it becomes 0.
fn unpremultiply(channel: u8, alpha: u8) -> u8 {
    match alpha {
        0 => 0,
        _ => ((u32::from(channel) * 255 + u32::from(alpha) / 2) / u32::from(alpha)).min(255) as u8,
    }
}

#[cfg(test)]
pub(super) mod tests {
    use super::*;
    use image::{ImageFormat, Rgba, RgbaImage};

    pub(in crate::art) fn encoded(
        width: u32,
        height: u32,
        format: ImageFormat,
        color: [u8; 4],
    ) -> Vec<u8> {
        encoded_from(&RgbaImage::from_pixel(width, height, Rgba(color)), format)
    }

    pub(in crate::art) fn encoded_from(picture: &RgbaImage, format: ImageFormat) -> Vec<u8> {
        let mut bytes = Vec::new();
        DynamicImage::ImageRgba8(picture.clone())
            .write_to(&mut Cursor::new(&mut bytes), format)
            .expect("decoding the test picture");
        bytes
    }

    pub(in crate::art) fn centre(bitmap: &Bitmap) -> [u8; 4] {
        let pixels = bitmap.pixels();
        let row = bitmap.width as usize * 4;
        let at = (bitmap.height / 2) as usize * row + (bitmap.width / 2) as usize * 4;
        [pixels[at], pixels[at + 1], pixels[at + 2], pixels[at + 3]]
    }

    #[test]
    fn a_picture_fits_the_box_and_keeps_its_colour() {
        for format in [ImageFormat::Png, ImageFormat::Jpeg] {
            let bytes = encoded(100, 40, format, [10, 200, 30, 255]);
            let bitmap = fit(&bytes, 50, 50).expect("fitting the test picture");
            assert_eq!((bitmap.width, bitmap.height), (50, 20), "{format:?}");
            assert_eq!(bitmap.pixels().len(), 50 * 20 * 4, "{format:?}");
            let [red, green, blue, alpha] = centre(&bitmap);
            assert!(
                red < 60 && green > 150 && blue < 60 && alpha == 255,
                "{format:?}"
            );
        }
    }

    #[test]
    fn a_fully_transparent_picture_decodes_to_nothing() {
        let bytes = encoded(8, 8, ImageFormat::Png, [255, 255, 255, 0]);
        let bitmap = fit(&bytes, 8, 8).expect("fitting the test picture");
        assert!(
            bitmap.pixels().iter().all(|byte| *byte == 0),
            "a pixel is not zero, want a fully transparent bitmap"
        );
    }

    #[test]
    fn what_is_not_a_picture_decodes_nothing() {
        assert!(
            read(&encoded(4, 4, ImageFormat::Png, [0, 0, 255, 255])).is_some(),
            "a valid PNG did not decode"
        );
        assert!(
            read(b"not an image").is_none(),
            "bytes that are not an image decoded"
        );
        assert!(
            fit(b"not an image", 10, 10).is_none(),
            "bytes that are not an image fit a box"
        );
    }

    /// The axis with the smaller ratio fills the box, and a picture
    /// smaller than the box grows to fit it.
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
            let bitmap = fit(&bytes, box_size.0, box_size.1).expect("fitting the test picture");
            assert_eq!((bitmap.width, bitmap.height), want, "{source:?}");
        }
    }

    /// The crop of a tall portrait keeps more of the top than of the
    /// bottom, so a picture that is red down to just above its middle and
    /// blue below comes back red at its center.
    #[test]
    fn a_portrait_fills_the_box_and_keeps_its_upper_part() {
        let mut picture = RgbaImage::from_pixel(60, 120, Rgba([0, 0, 255, 255]));
        for y in 0..52 {
            for x in 0..60 {
                picture.put_pixel(x, y, Rgba([255, 0, 0, 255]));
            }
        }
        let bytes = encoded_from(&picture, ImageFormat::Png);
        let bitmap = fill(&bytes, 60, 80).expect("filling the box with the portrait");
        assert_eq!((bitmap.width, bitmap.height), (60, 80));
        assert_eq!(centre(&bitmap), [255, 0, 0, 255]);
    }

    #[test]
    fn a_source_or_a_box_with_no_pixels_decodes_nothing() {
        let source = DynamicImage::ImageRgba8(RgbaImage::new(0, 0));
        assert!(
            scale(&source, 10, 10).is_none(),
            "scaling returned a bitmap, want none"
        );
        let source = DynamicImage::ImageRgba8(RgbaImage::new(10, 10));
        assert!(
            scale(&source, 0, 10).is_none(),
            "a box of width 0 returned a bitmap, want none"
        );
        assert!(
            scale(&source, 10, 0).is_none(),
            "a box of height 0 returned a bitmap, want none"
        );
    }

    #[test]
    fn a_reference_that_is_not_https_is_a_file_path() {
        let path = std::env::temp_dir().join("media-display-open.png");
        let bytes = encoded(4, 4, ImageFormat::Png, [0, 0, 0, 255]);
        std::fs::write(&path, &bytes).expect("writing the test file");
        assert_eq!(open(&path.to_string_lossy()), Some(bytes));
        assert_eq!(open("/art/nothing.png"), None);
    }

    #[test]
    fn an_https_reference_to_an_unreachable_host_reads_as_none() {
        assert_eq!(open("https://127.0.0.1:1/logo.png"), None);
    }
}
