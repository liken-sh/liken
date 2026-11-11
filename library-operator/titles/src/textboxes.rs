// The text detector's output, turned into boxes. The detector is a DBNet:
// it returns, for each pixel of its input, the probability that the pixel
// is inside a line of text, with each line shrunk toward its middle so
// that two lines never touch. OpenCV's TextDetectionModel_DB turns that
// map into boxes in C++ that no runtime here can call. This is the same
// procedure for lines that run left to right, which the lines of a title
// card and a credit roll do: threshold the map, take each connected region,
// keep the regions that score well, and grow each one back to the size of
// the line it came from.

use serde::{Deserialize, Serialize};

// A pixel is text when its probability is over this. OpenCV's sample and
// PP-OCR both use 0.3.
const PIXEL_THRESHOLD: f32 = 0.3;
// A region is a line when the mean probability of its pixels is at least
// this.
const REGION_THRESHOLD: f32 = 0.5;
// The detector shrinks each line by a distance of its area times a ratio
// over its perimeter. This grows the region by the same rule. 2.0 is the
// ratio of OpenCV's sample, and the experiments ran with it.
const UNCLIP_RATIO: f32 = 2.0;
// A region narrower or shorter than this many pixels is noise.
const SMALLEST_SIDE: usize = 3;

// One line of text, as shares of the frame's width and height: its center,
// its width, and its height. Shares let a reading of a 4K film and of a
// DVD compare.
#[derive(Clone, Copy, Debug, PartialEq, Serialize, Deserialize)]
pub struct TextBox(pub f32, pub f32, pub f32, pub f32);

impl TextBox {
    pub fn center_x(&self) -> f32 {
        self.0
    }

    pub fn center_y(&self) -> f32 {
        self.1
    }

    pub fn width(&self) -> f32 {
        self.2
    }

    pub fn height(&self) -> f32 {
        self.3
    }

    // The box's corners in the pixels of a picture: left, top, right,
    // bottom.
    pub fn corners(&self, width: usize, height: usize) -> (f32, f32, f32, f32) {
        let (w, h) = (width as f32, height as f32);
        (
            (self.0 - self.2 / 2.0) * w,
            (self.1 - self.3 / 2.0) * h,
            (self.0 + self.2 / 2.0) * w,
            (self.1 + self.3 / 2.0) * h,
        )
    }
}

pub struct Found {
    pub boxes: Vec<TextBox>,
    // The share of the frame inside a box.
    pub coverage: f32,
}

// The boxes of a probability map of width by height, in reading order from
// the top.
pub fn find(map: &[f32], width: usize, height: usize) -> Found {
    assert_eq!(map.len(), width * height);
    let mut seen = vec![false; map.len()];
    let mut covered = vec![false; map.len()];
    let mut boxes = Vec::new();
    let mut stack = Vec::new();
    for start in 0..map.len() {
        if seen[start] || map[start] <= PIXEL_THRESHOLD {
            continue;
        }
        // One connected region, by flood fill over the eight neighbors.
        let (mut left, mut right, mut top, mut bottom) = (width, 0, height, 0);
        let (mut sum, mut count) = (0.0f32, 0usize);
        seen[start] = true;
        stack.push(start);
        while let Some(index) = stack.pop() {
            let (x, y) = (index % width, index / width);
            (left, right) = (left.min(x), right.max(x));
            (top, bottom) = (top.min(y), bottom.max(y));
            sum += map[index];
            count += 1;
            for ny in y.saturating_sub(1)..=(y + 1).min(height - 1) {
                for nx in x.saturating_sub(1)..=(x + 1).min(width - 1) {
                    let neighbor = ny * width + nx;
                    if !seen[neighbor] && map[neighbor] > PIXEL_THRESHOLD {
                        seen[neighbor] = true;
                        stack.push(neighbor);
                    }
                }
            }
        }
        let (w, h) = (right - left + 1, bottom - top + 1);
        if w < SMALLEST_SIDE || h < SMALLEST_SIDE || sum / (count as f32) < REGION_THRESHOLD {
            continue;
        }
        let grow = (w * h) as f32 * UNCLIP_RATIO / (2 * (w + h)) as f32;
        let x0 = (left as f32 - grow).max(0.0);
        let y0 = (top as f32 - grow).max(0.0);
        let x1 = (right as f32 + 1.0 + grow).min(width as f32);
        let y1 = (bottom as f32 + 1.0 + grow).min(height as f32);
        for y in y0 as usize..(y1.ceil() as usize).min(height) {
            covered[y * width + x0 as usize..y * width + (x1.ceil() as usize).min(width)]
                .fill(true);
        }
        boxes.push(TextBox(
            round4((x0 + x1) / 2.0 / width as f32),
            round4((y0 + y1) / 2.0 / height as f32),
            round4((x1 - x0) / width as f32),
            round4((y1 - y0) / height as f32),
        ));
    }
    boxes.sort_by(|a, b| a.1.total_cmp(&b.1).then(a.0.total_cmp(&b.0)));
    let coverage = covered.iter().filter(|c| **c).count() as f32 / map.len() as f32;
    Found { boxes, coverage }
}

fn round4(value: f32) -> f32 {
    (value * 10000.0).round() / 10000.0
}

#[cfg(test)]
mod tests {
    use super::*;

    // A map of 100 by 50 with the named rectangles at the named
    // probability.
    fn map(regions: &[(usize, usize, usize, usize, f32)]) -> Vec<f32> {
        let mut map = vec![0.0; 100 * 50];
        for &(left, top, right, bottom, probability) in regions {
            for y in top..bottom {
                map[y * 100 + left..y * 100 + right].fill(probability);
            }
        }
        map
    }

    #[test]
    fn find_grows_a_region_back_to_its_line() {
        let found = find(&map(&[(20, 20, 60, 30, 0.9)]), 100, 50);
        // 40 by 10 grows by 400 * 2 / 100 = 8 pixels on each side.
        assert_eq!(found.boxes, vec![TextBox(0.4, 0.5, 0.56, 0.52)]);
        assert!((found.coverage - 0.56 * 0.52).abs() < 0.001);
    }

    #[test]
    fn find_orders_the_lines_from_the_top() {
        let found = find(
            &map(&[(10, 30, 50, 36, 0.9), (10, 5, 50, 11, 0.9)]),
            100,
            50,
        );
        assert_eq!(found.boxes.len(), 2);
        assert!(found.boxes[0].center_y() < found.boxes[1].center_y());
    }

    #[test]
    fn find_drops_a_faint_region_and_a_speck() {
        let found = find(
            &map(&[(10, 10, 50, 16, 0.4), (70, 10, 72, 12, 0.9)]),
            100,
            50,
        );
        assert!(found.boxes.is_empty());
        assert_eq!(found.coverage, 0.0);
    }

    #[test]
    fn find_stops_a_box_at_the_frame() {
        let found = find(&map(&[(0, 0, 40, 10, 0.9)]), 100, 50);
        let (left, top, _, _) = found.boxes[0].corners(100, 50);
        assert!(left.abs() < 0.01 && top.abs() < 0.01);
    }

    #[test]
    fn corners_gives_pixels_of_another_picture() {
        let corners = TextBox(0.5, 0.5, 0.5, 0.2).corners(1000, 500);
        assert_eq!(corners, (250.0, 200.0, 750.0, 300.0));
    }
}
