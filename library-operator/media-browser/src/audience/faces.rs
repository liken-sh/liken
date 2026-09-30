// The pictures of the people, as every screen that draws a person in a
// circle draws them: the picker's tiles, the strip's circles, the
// continue-watching row's heading, and the circles of a franchise page.
// people-operator writes each thumbnail square and with no alpha, so the
// browser cuts it to the circle, with the edge antialiased, and decodes it
// at the panel's pixels for the size the circle draws at.
//
// The browser cuts the faces when it learns the `Person` list or the
// panel's scale, and never in a frame, because a frame draws every circle
// each time it is drawn. A face whose picture and side are unchanged is
// kept from the last cut, and its handle with it, so a new list decodes
// only the pictures that changed. The renderer keys its uploads by the
// handle, so a kept face is not uploaded again either.

use std::io::Cursor;

use image::imageops::FilterType;
use image::{ImageReader, Limits, RgbaImage};

use super::{Person, Thumbnail, Viewer};
use crate::art::Image;

// The largest side a thumbnail may declare. people-operator writes 256
// pixels, and the limit stops a small file that declares a huge picture
// before the decode allocates its pixels.
const LARGEST: u32 = 2048;

/// The panel pixels across a face that draws this many logical pixels
/// across at this scale.
pub fn side(size: f32, scale: f32) -> u32 {
    (size * scale) as u32
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

/// The faces of the known people, by `Person` name and by the logical
/// size each screen draws them at.
#[derive(Debug, Clone)]
pub struct Faces {
    faces: Vec<Cut>,
    // The scale of the last cut, which turns a logical size into the side
    // a face was cut at.
    scale: f32,
}

impl Default for Faces {
    fn default() -> Self {
        Self {
            faces: Vec::new(),
            scale: 1.0,
        }
    }
}

/// What one circle draws for one person.
#[derive(Debug, Clone, PartialEq)]
pub enum Face<'a> {
    /// The person's picture, cut to the circle.
    Picture(&'a Image),
    /// The first letter of the person's display name, for a person with no
    /// picture or one that did not decode.
    Letter(String),
}

// One cut face: the person it belongs to, and the picture and side it was
// cut from, which decide whether a later cut can keep it. The image is
// none where the picture did not decode, so a broken picture is tried once
// per side and not on every cut.
#[derive(Debug, Clone)]
struct Cut {
    name: String,
    thumbnail: Thumbnail,
    side: u32,
    image: Option<Image>,
}

impl Faces {
    /// Cut a face for each person of this list at each of these logical
    /// sizes, at this panel scale. A face whose picture and side are
    /// unchanged since the last cut is kept, and every other one is
    /// decoded. A person with no picture gets no face.
    pub fn refresh(&mut self, people: &[Person], sizes: &[f32], scale: f32) {
        let held = std::mem::take(&mut self.faces);
        self.scale = scale;
        for person in people {
            let Some(thumbnail) = &person.thumbnail else {
                continue;
            };
            for size in sizes {
                let side = side(*size, scale);
                // Two people may carry one picture, so the faces this cut
                // already made count as kept too.
                let kept = held
                    .iter()
                    .chain(&self.faces)
                    .find(|face| face.side == side && &face.thumbnail == thumbnail)
                    .map(|face| face.image.clone());
                let image = kept.unwrap_or_else(|| {
                    circle(thumbnail, side)
                        .map(|face| Image::at_scale(side, side, scale, face.into_raw().into()))
                });
                self.faces.push(Cut {
                    name: person.name.clone(),
                    thumbnail: thumbnail.clone(),
                    side,
                    image,
                });
            }
        }
    }

    /// The face of the person with this `Person` name at this logical
    /// size, or nothing where they have no picture, it did not decode, or
    /// no cut asked for this size.
    pub fn get(&self, name: &str, size: f32) -> Option<&Image> {
        let side = side(size, self.scale);
        self.faces
            .iter()
            .find(|face| face.name == name && face.side == side)?
            .image
            .as_ref()
    }

    /// What a circle of this logical size draws for this viewer: their
    /// face, or their letter where they have none.
    pub fn face(&self, viewer: &Viewer, size: f32) -> Face<'_> {
        match self.get(&viewer.name, size) {
            Some(image) => Face::Picture(image),
            None => Face::Letter(viewer.letter.clone()),
        }
    }
}

#[cfg(test)]
mod tests;
