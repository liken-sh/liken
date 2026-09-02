// One decode: guess the format from the file's bytes, scale the image
// into the drawn box the way the fit asks for, and return straight-alpha
// RGBA at the size the scale landed at.
//
// Triangle is the filter because its kernel widens with the downscale
// ratio, so a poster shrink averages the source pixels like a box
// filter, at about a third of Lanczos3's cost. The decode runs once
// per drawn size, so the cheaper filter is enough.

use std::path::Path;

use image::ImageReader;
use image::imageops::FilterType;

use super::Fit;
use super::store::Poster;

pub(crate) fn decode_art(path: &Path, width: u32, height: u32, fit: Fit) -> Option<Poster> {
    let reader = ImageReader::open(path).ok()?.with_guessed_format().ok()?;
    let decoded = reader.decode().ok()?;
    let scaled = match fit {
        Fit::Cover => decoded.resize_to_fill(width, height, FilterType::Triangle),
        Fit::Contain => decoded.resize(width, height, FilterType::Triangle),
    };
    Some(Poster::new(
        scaled.width(),
        scaled.height(),
        scaled.to_rgba8().into_raw().into(),
    ))
}
