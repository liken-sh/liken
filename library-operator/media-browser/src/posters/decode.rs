// One decode: guess the format from the file's bytes, scale to cover
// the drawn box, center-crop to exactly that box, and return
// straight-alpha RGBA.
//
// Triangle is the filter because its kernel widens with the downscale
// ratio, so a poster shrink averages the source pixels like a box
// filter, at about a third of Lanczos3's cost. The decode runs once
// per drawn size, so the cheaper filter is enough.

use std::path::Path;

use image::ImageReader;
use image::imageops::FilterType;

use super::store::Poster;

pub(crate) fn decode_cover(path: &Path, width: u32, height: u32) -> Option<Poster> {
    let reader = ImageReader::open(path).ok()?.with_guessed_format().ok()?;
    let decoded = reader.decode().ok()?;
    let filled = decoded.resize_to_fill(width, height, FilterType::Triangle);
    Some(Poster {
        width,
        height,
        rgba: filled.to_rgba8().into_raw().into(),
    })
}
