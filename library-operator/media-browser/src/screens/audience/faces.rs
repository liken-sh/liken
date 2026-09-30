// The pictures the picker draws in its tiles. people-operator writes each
// thumbnail square and with no alpha, so every screen cuts it to its own
// shape. The picker cuts it to the circle a tile draws, with the edge
// antialiased, and decodes it at the panel's pixels for that circle.
//
// The decode and the cut run when the picker goes up, and when the
// `Person` list changes under a picker that stands, and never in a frame,
// because a frame draws every tile each time it is drawn. A face whose
// picture and size are unchanged is kept from the last look, and its
// handle with it, so a new list decodes only the pictures that changed.
// The renderer keys its uploads by the handle, so a kept face is not
// uploaded again either.

use std::io::Cursor;

use image::imageops::FilterType;
use image::{ImageReader, Limits, RgbaImage};

use super::CIRCLE;
use crate::art::Image;
use crate::audience::{Person, Thumbnail};
use crate::look;

// The largest side a thumbnail may declare. people-operator writes 256
// pixels, and the limit stops a small file that declares a huge picture
// before the decode allocates its pixels.
const LARGEST: u32 = 2048;

/// The panel pixels across one face at this scale: the circle inside the
/// chosen ring. The ring is a stroke, and the renderer draws every stroke
/// of a canvas under every image of it, so a face as wide as the tile
/// would cover the ring.
pub fn side(scale: f32) -> u32 {
    ((CIRCLE - 2.0 * look::MARK) * scale) as u32
}

/// The picture, decoded, filled to a square of this side, and cut to the
/// circle the square holds. A pixel outside the circle is clear, and a
/// pixel the edge crosses keeps the share of it inside. Nothing where the
/// bytes are not a picture of the format their media type named.
pub fn circle(thumbnail: &Thumbnail, side: u32) -> Option<RgbaImage> {
    let mut reader = ImageReader::with_format(Cursor::new(thumbnail.bytes()), thumbnail.format());
    let mut limits = Limits::default();
    limits.max_image_width = Some(LARGEST);
    limits.max_image_height = Some(LARGEST);
    reader.limits(limits);
    let mut face = reader
        .decode()
        .ok()?
        .resize_to_fill(side, side, FilterType::Triangle)
        .to_rgba8();

    let radius = side as f32 / 2.0;
    for (x, y, pixel) in face.enumerate_pixels_mut() {
        let across = x as f32 + 0.5 - radius;
        let down = y as f32 + 0.5 - radius;
        let inside = (radius - across.hypot(down) + 0.5).clamp(0.0, 1.0);
        pixel.0[3] = (f32::from(pixel.0[3]) * inside).round() as u8;
    }
    Some(face)
}

/// The faces of the known people, by their index in the list, as the
/// picker draws them.
#[derive(Debug, Clone, Default)]
pub struct Faces {
    faces: Vec<Option<Face>>,
}

// One cut face, and the picture and side it was cut from, which decide
// whether a later look can keep it.
#[derive(Debug, Clone)]
struct Face {
    thumbnail: Thumbnail,
    side: u32,
    image: Option<Image>,
}

impl Faces {
    /// Cut a face for each person of this list at this panel scale. A face
    /// whose picture and side are unchanged since the last look is kept,
    /// and every other one is decoded.
    pub fn refresh(&mut self, people: &[Person], scale: f32) {
        let side = side(scale);
        let held = std::mem::take(&mut self.faces);
        self.faces = people
            .iter()
            .map(|person| {
                let thumbnail = person.thumbnail.as_ref()?;
                let kept = held
                    .iter()
                    .flatten()
                    .find(|face| face.side == side && &face.thumbnail == thumbnail);
                Some(kept.cloned().unwrap_or_else(|| {
                    Face {
                        thumbnail: thumbnail.clone(),
                        side,
                        image: circle(thumbnail, side)
                            .map(|face| Image::at_scale(side, side, scale, face.into_raw().into())),
                    }
                }))
            })
            .collect();
    }

    /// The face of the person at this index in the list, or nothing where
    /// they have no picture or it did not decode.
    pub fn get(&self, index: usize) -> Option<&Image> {
        self.faces.get(index)?.as_ref()?.image.as_ref()
    }
}

#[cfg(test)]
mod tests;
