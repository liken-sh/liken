// A person's picture as the people file carries it: the `thumbnail` field
// that people-operator writes into the `Person`'s status, a `data:` URI
// of a square image. The browser opens the URI when it reads the file and
// keeps the encoded bytes. `audience::faces` decodes them at each size a
// screen draws, once per size, because the panel's scale decides that
// size and the file is read before the window opens.

use std::sync::Arc;

use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use image::ImageFormat;

/// The encoded bytes of one person's picture, and the format its media
/// type names.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Thumbnail {
    format: ImageFormat,
    // Shared, so a clone of the `Person` list copies no picture.
    bytes: Arc<[u8]>,
}

impl Thumbnail {
    /// The picture a `data:` URI holds, or nothing where the URI is not
    /// base64 or names a format the browser does not decode. The browser
    /// builds the JPEG and PNG decoders alone, so every other media type
    /// is refused here, and the person keeps their letter.
    pub fn from_uri(uri: &str) -> Option<Self> {
        let (meta, data) = uri.strip_prefix("data:")?.split_once(',')?;
        let media_type = meta.strip_suffix(";base64")?;
        let format = [ImageFormat::Jpeg, ImageFormat::Png]
            .into_iter()
            .find(|format| media_type.eq_ignore_ascii_case(format.to_mime_type()))?;
        let bytes = STANDARD.decode(data).ok()?;
        if bytes.is_empty() {
            return None;
        }
        Some(Self {
            format,
            bytes: bytes.into(),
        })
    }

    /// The format the media type named, which the decode holds the bytes
    /// to.
    pub fn format(&self) -> ImageFormat {
        self.format
    }

    /// The encoded picture.
    pub fn bytes(&self) -> &[u8] {
        &self.bytes
    }
}

/// A JPEG thumbnail of one colour, as people-operator would write it, for
/// the tests that draw a person's picture.
#[cfg(test)]
pub(crate) fn solid(side: u32, colour: [u8; 3]) -> Thumbnail {
    let picture = image::RgbImage::from_pixel(side, side, image::Rgb(colour));
    let mut bytes = Vec::new();
    image::codecs::jpeg::JpegEncoder::new_with_quality(&mut bytes, 85)
        .encode_image(&picture)
        .expect("a solid picture encodes");
    Thumbnail::from_uri(&format!(
        "data:image/jpeg;base64,{}",
        STANDARD.encode(bytes)
    ))
    .expect("an encoded picture opens")
}

#[cfg(test)]
mod tests;
