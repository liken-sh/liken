// Decoded art as the handles the canvas draws, cut into horizontal bands
// so that every handle uploads on the frame that asks for it.

use std::ops::Range;

use iced_widget::core::{Bytes, Rectangle};
use iced_widget::image::Handle;

// iced_wgpu 0.14.0 uploads an image of fewer than this many bytes on the
// frame that asks for it. It hands a longer one to a worker thread and draws
// nothing until a later frame asks again. This client draws only on events,
// so that later frame never comes, and a full-frame backdrop stayed black.
// A band under this cap always takes the first path.
const SYNC_UPLOAD_BYTES: usize = 2 * 1024 * 1024;

/// One decoded image, ready to draw: its pixel size, and the bands the
/// canvas draws it as. A small image is one band.
#[derive(Clone, Debug, PartialEq)]
pub struct Art {
    width: u32,
    height: u32,
    bands: Vec<Band>,
}

// The rows this band holds, and the handle over those rows.
#[derive(Clone, Debug, PartialEq)]
struct Band {
    rows: Range<u32>,
    handle: Handle,
}

impl Art {
    /// Cut a row-major RGBA buffer into bands under the upload cap. A band
    /// is a contiguous run of rows, so each handle is a view of the one
    /// buffer and no pixel is copied.
    pub fn new(width: u32, height: u32, pixels: Bytes) -> Self {
        let row = width as usize * 4;
        let mut bands = Vec::new();
        let per_band = (SYNC_UPLOAD_BYTES - 1)
            .checked_div(row)
            .unwrap_or_default()
            .max(1) as u32;
        let mut top = 0;
        while row > 0 && top < height {
            let bottom = height.min(top + per_band);
            let rows = top..bottom;
            let bytes = top as usize * row..bottom as usize * row;
            bands.push(Band {
                handle: Handle::from_rgba(width, bottom - top, pixels.slice(bytes)),
                rows,
            });
            top = bottom;
        }
        Self {
            width,
            height,
            bands,
        }
    }

    /// The pixel size the decode landed at. A contain fit answers a size
    /// smaller than the box it was asked for.
    pub fn size(&self) -> (u32, u32) {
        (self.width, self.height)
    }

    /// Each band with the share of the target rectangle it draws in. A band
    /// ends where the next one starts, so the bands tile the target with no
    /// seam.
    pub fn bands(&self, into: Rectangle) -> impl Iterator<Item = (Rectangle, Handle)> + '_ {
        let height = self.height as f32;
        self.bands.iter().map(move |band| {
            let top = into.y + into.height * band.rows.start as f32 / height;
            let bottom = into.y + into.height * band.rows.end as f32 / height;
            (
                Rectangle {
                    x: into.x,
                    y: top,
                    width: into.width,
                    height: bottom - top,
                },
                band.handle.clone(),
            )
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // A buffer in which every pixel carries the number of its row, so a
    // test reads which rows a band holds.
    fn numbered(width: u32, height: u32) -> Bytes {
        let mut pixels = Vec::with_capacity((width * height * 4) as usize);
        for row in 0..height {
            pixels.extend(std::iter::repeat_n((row % 251) as u8, (width * 4) as usize));
        }
        Bytes::from_owner(pixels)
    }

    fn drawn(art: &Art, into: Rectangle) -> Vec<(Rectangle, Handle)> {
        art.bands(into).collect()
    }

    fn area(art: &Art) -> Rectangle {
        let (width, height) = art.size();
        Rectangle {
            x: 40.0,
            y: 10.0,
            width: width as f32,
            height: height as f32,
        }
    }

    #[test]
    fn a_small_image_draws_as_one_band_over_the_whole_rectangle() {
        let art = Art::new(300, 450, numbered(300, 450));
        let bands = drawn(&art, area(&art));
        assert_eq!(bands.len(), 1);
        assert_eq!(bands[0].0, area(&art));
    }

    #[test]
    fn a_frame_sized_image_draws_as_bands_under_the_upload_cap() {
        let art = Art::new(1920, 1080, numbered(1920, 1080));
        let bands = drawn(&art, area(&art));
        assert!(bands.len() > 1);
        for (_, handle) in &bands {
            let Handle::Rgba { pixels, .. } = handle else {
                panic!("a band is an Rgba handle");
            };
            assert!(pixels.len() < SYNC_UPLOAD_BYTES, "{} bytes", pixels.len());
        }
    }

    #[test]
    fn the_bands_tile_the_target_rectangle_with_no_seam() {
        let art = Art::new(1920, 1080, numbered(1920, 1080));
        let into = Rectangle {
            x: 0.0,
            y: 0.0,
            width: 1920.0,
            height: 1080.0,
        };
        let bands = drawn(&art, into);
        assert_eq!(bands[0].0.y, into.y);
        for pair in bands.windows(2) {
            assert_eq!(pair[0].0.y + pair[0].0.height, pair[1].0.y);
            assert_eq!(pair[0].0.x, into.x);
            assert_eq!(pair[0].0.width, into.width);
        }
        let last = bands.last().expect("a band").0;
        assert_eq!(last.y + last.height, into.y + into.height);
    }

    #[test]
    fn every_band_holds_the_rows_it_draws() {
        let art = Art::new(1920, 1080, numbered(1920, 1080));
        let mut top = 0u32;
        for (_, handle) in drawn(&art, area(&art)) {
            let Handle::Rgba {
                width,
                height,
                pixels,
                ..
            } = handle
            else {
                panic!("a band is an Rgba handle");
            };
            assert_eq!(width, 1920);
            assert_eq!(pixels.len(), (width * height * 4) as usize);
            assert_eq!(pixels[0], (top % 251) as u8);
            top += height;
        }
        assert_eq!(top, 1080);
    }

    #[test]
    fn an_empty_size_draws_nothing() {
        let art = Art::new(0, 0, Bytes::from_owner(Vec::new()));
        assert_eq!(art.size(), (0, 0));
        assert_eq!(drawn(&art, area(&art)).len(), 0);
    }
}
