// A frame as packed 8-bit BGR, the order ffmpeg writes with `-pix_fmt
// bgr24` and the order both text models were trained on.

#[derive(Clone, Debug, PartialEq)]
pub struct Picture {
    pub width: usize,
    pub height: usize,
    pub bgr: Vec<u8>,
}

impl Picture {
    pub fn new(width: usize, height: usize, bgr: Vec<u8>) -> Self {
        assert_eq!(
            bgr.len(),
            width * height * 3,
            "a BGR picture is 3 bytes a pixel"
        );
        Picture { width, height, bgr }
    }

    // The picture at half its width and height, each pixel the mean of
    // four. The text detector reads the half-size picture, and the reader
    // crops lines from the full one.
    pub fn halved(&self) -> Picture {
        let (width, height) = (self.width / 2, self.height / 2);
        let mut bgr = vec![0u8; width * height * 3];
        for y in 0..height {
            let top = y * 2 * self.width * 3;
            let bottom = top + self.width * 3;
            for x in 0..width {
                for channel in 0..3 {
                    let at = x * 6 + channel;
                    let sum = self.bgr[top + at] as u16
                        + self.bgr[top + at + 3] as u16
                        + self.bgr[bottom + at] as u16
                        + self.bgr[bottom + at + 3] as u16;
                    bgr[(y * width + x) * 3 + channel] = ((sum + 2) / 4) as u8;
                }
            }
        }
        Picture { width, height, bgr }
    }

    // The rectangle from (left, top) to (right, bottom), resized to width
    // by height with bilinear sampling. The corners are clamped to the
    // picture, so a text box that the detector widened past the frame's
    // edge still crops.
    pub fn crop(
        &self,
        (left, top, right, bottom): (f32, f32, f32, f32),
        width: usize,
        height: usize,
    ) -> Picture {
        let left = left.clamp(0.0, self.width as f32 - 1.0);
        let top = top.clamp(0.0, self.height as f32 - 1.0);
        let right = right.clamp(left + 1.0, self.width as f32);
        let bottom = bottom.clamp(top + 1.0, self.height as f32);
        let taps = |out: usize, from: f32, to: f32, limit: usize| -> Vec<(usize, usize, f32)> {
            let scale = (to - from) / out as f32;
            (0..out)
                .map(|i| {
                    let at = (from + (i as f32 + 0.5) * scale - 0.5).clamp(0.0, (limit - 1) as f32);
                    let low = at.floor() as usize;
                    (low, (low + 1).min(limit - 1), at - low as f32)
                })
                .collect()
        };
        let columns = taps(width, left, right, self.width);
        let rows = taps(height, top, bottom, self.height);
        let mut bgr = vec![0u8; width * height * 3];
        for (y, &(y0, y1, fy)) in rows.iter().enumerate() {
            for (x, &(x0, x1, fx)) in columns.iter().enumerate() {
                for channel in 0..3 {
                    let at =
                        |x: usize, y: usize| self.bgr[(y * self.width + x) * 3 + channel] as f32;
                    let above = at(x0, y0) * (1.0 - fx) + at(x1, y0) * fx;
                    let below = at(x0, y1) * (1.0 - fx) + at(x1, y1) * fx;
                    bgr[(y * width + x) * 3 + channel] =
                        (above * (1.0 - fy) + below * fy).round() as u8;
                }
            }
        }
        Picture { width, height, bgr }
    }

    // The mean brightness of the picture and the share of its pixels that
    // are nearly black, both from 0 to 1. End titles are mostly white text
    // on black, so a dark frame with text on it is evidence of them.
    pub fn light(&self) -> (f32, f32) {
        let mut sum = 0u64;
        let mut dark = 0usize;
        for pixel in self.bgr.as_chunks::<3>().0 {
            sum += pixel.iter().map(|&v| v as u64).sum::<u64>();
            dark += usize::from(pixel.iter().all(|&v| v < DARK));
        }
        let pixels = self.width * self.height;
        (
            sum as f32 / (pixels * 3) as f32 / 255.0,
            dark as f32 / pixels as f32,
        )
    }

    // The picture as a grid of gray cells, each the mean of a block of
    // 8 by 8 pixels. Two frames' grids give how much the picture changed
    // between them at little cost.
    pub fn gray_grid(&self) -> Vec<f32> {
        let (columns, rows) = (self.width / 8, self.height / 8);
        let mut grid = vec![0f32; columns * rows];
        for (index, cell) in grid.iter_mut().enumerate() {
            let (cx, cy) = (index % columns, index / columns);
            let mut sum = 0u32;
            for y in cy * 8..cy * 8 + 8 {
                let row = (y * self.width + cx * 8) * 3;
                sum += self.bgr[row..row + 24]
                    .iter()
                    .map(|&v| v as u32)
                    .sum::<u32>();
            }
            *cell = sum as f32 / 192.0;
        }
        grid
    }
}

// A pixel is nearly black when every channel is under this value of 255.
const DARK: u8 = 24;

// The mean difference of two gray grids, from 0 to 1.
pub fn change(before: &[f32], after: &[f32]) -> f32 {
    let total: f32 = before.iter().zip(after).map(|(a, b)| (a - b).abs()).sum();
    total / before.len().max(1) as f32 / 255.0
}

#[cfg(test)]
mod tests {
    use super::*;

    fn flat(width: usize, height: usize, value: u8) -> Picture {
        Picture::new(width, height, vec![value; width * height * 3])
    }

    #[test]
    fn halved_takes_the_mean_of_four_pixels() {
        let picture = Picture::new(
            2,
            2,
            vec![0, 0, 0, 100, 100, 100, 200, 200, 200, 100, 100, 100],
        );
        assert_eq!(picture.halved(), flat(1, 1, 100));
    }

    #[test]
    fn crop_keeps_a_flat_region_flat() {
        let cropped = flat(64, 32, 77).crop((-5.0, 4.0, 90.0, 20.0), 40, 48);
        assert_eq!(cropped, flat(40, 48, 77));
    }

    #[test]
    fn crop_takes_the_named_rectangle() {
        let mut picture = flat(8, 8, 0);
        for y in 2..6 {
            for x in 4..8 {
                picture.bgr[(y * 8 + x) * 3..(y * 8 + x) * 3 + 3].fill(255);
            }
        }
        assert_eq!(picture.crop((4.0, 2.0, 8.0, 6.0), 4, 4), flat(4, 4, 255));
    }

    #[test]
    fn light_counts_the_black_pixels() {
        let mut picture = flat(2, 1, 0);
        picture.bgr[3..6].fill(255);
        assert_eq!(picture.light(), (0.5, 0.5));
    }

    #[test]
    fn change_is_zero_for_the_same_frame_and_one_for_black_to_white() {
        let (black, white) = (flat(16, 16, 0).gray_grid(), flat(16, 16, 255).gray_grid());
        assert_eq!(change(&black, &black), 0.0);
        assert_eq!(change(&black, &white), 1.0);
    }
}
