// YuNet's raw outputs, decoded into faces. OpenVINO returns the twelve
// tensors the network ends in, and OpenCV's FaceDetectorYN decodes them in
// C++ that no runtime here can call. This is that decoder: for each of the
// three strides, each grid cell carries a class score, an objectness score,
// a box as an offset and a log size, and five landmarks as offsets, all in
// units of the stride.

use serde::{Deserialize, Serialize};

pub const STRIDES: [usize; 3] = [8, 16, 32];

// The thresholds FaceDetectorYN takes by default, and the ones the
// experiments ran with. 0.9 keeps the detector's precision high, because
// the matcher cannot tell a false face from an unknown person.
pub const SCORE_THRESHOLD: f32 = 0.9;
pub const NMS_THRESHOLD: f32 = 0.3;

// One detected face, in the pixels of the picture it was found in. The
// landmarks are the right eye, the left eye, the nose tip, and the right
// and left corners of the mouth, from the viewer's left to right for the
// eyes and mouth.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Face {
    pub x: f32,
    pub y: f32,
    pub w: f32,
    pub h: f32,
    pub score: f32,
    pub landmarks: [(f32, f32); 5],
}

impl Face {
    pub fn scaled(&self, factor: f32) -> Face {
        Face {
            x: self.x * factor,
            y: self.y * factor,
            w: self.w * factor,
            h: self.h * factor,
            score: self.score,
            landmarks: self.landmarks.map(|(x, y)| (x * factor, y * factor)),
        }
    }

    fn area(&self) -> f32 {
        self.w * self.h
    }

    fn overlap(&self, other: &Face) -> f32 {
        let left = self.x.max(other.x);
        let top = self.y.max(other.y);
        let right = (self.x + self.w).min(other.x + other.w);
        let bottom = (self.y + self.h).min(other.y + other.h);
        let shared = (right - left).max(0.0) * (bottom - top).max(0.0);
        shared / (self.area() + other.area() - shared)
    }
}

// The outputs of one stride, each a flat slice in grid-cell order.
pub struct StrideOutputs<'a> {
    pub cls: &'a [f32],
    pub obj: &'a [f32],
    pub bbox: &'a [f32],
    pub kps: &'a [f32],
}

pub fn decode(stride: usize, input_width: usize, outputs: &StrideOutputs) -> Vec<Face> {
    let cols = input_width / stride;
    let s = stride as f32;
    let mut faces = Vec::new();
    for (index, (&cls, &obj)) in outputs.cls.iter().zip(outputs.obj).enumerate() {
        let score = (cls.clamp(0.0, 1.0) * obj.clamp(0.0, 1.0)).sqrt();
        if score < SCORE_THRESHOLD {
            continue;
        }
        let col = (index % cols) as f32;
        let row = (index / cols) as f32;
        let b = &outputs.bbox[index * 4..index * 4 + 4];
        let cx = (col + b[0]) * s;
        let cy = (row + b[1]) * s;
        let w = b[2].exp() * s;
        let h = b[3].exp() * s;
        let k = &outputs.kps[index * 10..index * 10 + 10];
        let landmarks = std::array::from_fn(|n| ((k[2 * n] + col) * s, (k[2 * n + 1] + row) * s));
        faces.push(Face {
            x: cx - w / 2.0,
            y: cy - h / 2.0,
            w,
            h,
            score,
            landmarks,
        });
    }
    faces
}

// Greedy non-maximum suppression: keep the best face, drop every face that
// overlaps it by more than the threshold, and repeat. The three strides
// find the same face at different scales, and this keeps one of them.
pub fn suppress(mut faces: Vec<Face>) -> Vec<Face> {
    faces.sort_by(|a, b| b.score.total_cmp(&a.score));
    let mut kept: Vec<Face> = Vec::new();
    for face in faces {
        if kept.iter().all(|k| k.overlap(&face) <= NMS_THRESHOLD) {
            kept.push(face);
        }
    }
    kept
}

// The network's input size for a picture: each side rounded up to a
// multiple of 32, the largest stride, so every grid divides evenly. The
// picture fills the top left of the input and black fills the rest.
pub fn padded(side: usize) -> usize {
    side.div_ceil(32) * 32
}

#[cfg(test)]
mod tests {
    use super::*;

    // One stride-8 grid of 4 by 2 cells, with a confident face in cell
    // (col 1, row 1) and nothing elsewhere.
    fn one_face() -> (Vec<f32>, Vec<f32>, Vec<f32>, Vec<f32>) {
        let cells = 8;
        let mut cls = vec![0.0; cells];
        let mut obj = vec![0.0; cells];
        let mut bbox = vec![0.0; cells * 4];
        let mut kps = vec![0.0; cells * 10];
        let index = 5;
        cls[index] = 0.99;
        obj[index] = 0.98;
        bbox[index * 4..index * 4 + 4].copy_from_slice(&[0.5, 0.5, 2f32.ln(), 3f32.ln()]);
        kps[index * 10..index * 10 + 2].copy_from_slice(&[0.25, 0.75]);
        (cls, obj, bbox, kps)
    }

    #[test]
    fn decode_places_the_box_from_its_cell() {
        let (cls, obj, bbox, kps) = one_face();
        let outputs = StrideOutputs {
            cls: &cls,
            obj: &obj,
            bbox: &bbox,
            kps: &kps,
        };
        let faces = decode(8, 32, &outputs);
        assert_eq!(faces.len(), 1);
        let face = &faces[0];
        // Center (1.5, 1.5) cells is (12, 12) px, the size 2x3 cells is
        // 16x24 px, so the corner is (4, 0).
        assert_eq!((face.x, face.y, face.w, face.h), (4.0, 0.0, 16.0, 24.0));
        assert_eq!(face.landmarks[0], (10.0, 14.0));
    }

    #[test]
    fn decode_drops_a_cell_under_the_score_threshold() {
        let (mut cls, obj, bbox, kps) = one_face();
        cls[5] = 0.5;
        let outputs = StrideOutputs {
            cls: &cls,
            obj: &obj,
            bbox: &bbox,
            kps: &kps,
        };
        assert!(decode(8, 32, &outputs).is_empty());
    }

    fn square(x: f32, score: f32) -> Face {
        Face {
            x,
            y: 0.0,
            w: 10.0,
            h: 10.0,
            score,
            landmarks: [(0.0, 0.0); 5],
        }
    }

    #[test]
    fn suppress_keeps_the_best_of_two_overlapping_faces() {
        let kept = suppress(vec![square(0.0, 0.92), square(1.0, 0.95)]);
        assert_eq!(kept, vec![square(1.0, 0.95)]);
    }

    #[test]
    fn suppress_keeps_two_faces_apart() {
        let kept = suppress(vec![square(0.0, 0.92), square(50.0, 0.95)]);
        assert_eq!(kept.len(), 2);
    }

    #[test]
    fn padded_rounds_up_to_the_largest_stride() {
        assert_eq!([padded(480), padded(800), padded(1080)], [480, 800, 1088]);
    }
}
