// A frame or a headshot as packed 8-bit BGR, the order ffmpeg writes with
// `-pix_fmt bgr24` and the order YuNet was trained on. The two samplers here
// are the only image operations the pipeline needs: a resize to the
// detector's input, and an affine warp that aligns a face for the embedder.
// Both match OpenCV's INTER_LINEAR, with pixel centers at half-pixel
// offsets, because the models were evaluated on OpenCV's preprocessing and a
// shifted sample moves every landmark.

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

    // One channel of one pixel, with zero outside the picture, which is
    // OpenCV's BORDER_CONSTANT. A face at the frame's edge warps in black
    // where the frame ends, the same as it did in the experiments.
    fn at(&self, x: isize, y: isize, channel: usize) -> f32 {
        if x < 0 || y < 0 || x as usize >= self.width || y as usize >= self.height {
            return 0.0;
        }
        self.bgr[(y as usize * self.width + x as usize) * 3 + channel] as f32
    }

    fn bilinear(&self, x: f32, y: f32, channel: usize) -> f32 {
        let x0 = x.floor();
        let y0 = y.floor();
        let fx = x - x0;
        let fy = y - y0;
        let (x0, y0) = (x0 as isize, y0 as isize);
        let top = self.at(x0, y0, channel) * (1.0 - fx) + self.at(x0 + 1, y0, channel) * fx;
        let bottom =
            self.at(x0, y0 + 1, channel) * (1.0 - fx) + self.at(x0 + 1, y0 + 1, channel) * fx;
        top * (1.0 - fy) + bottom * fy
    }

    // The resize runs on every sample, so it computes each column's two
    // source pixels and weight once and reuses them on every row. It
    // clamps at the edges instead of reading black, as OpenCV's resize
    // does, so a scaled frame keeps its border pixels.
    pub fn resize(&self, width: usize, height: usize) -> Picture {
        let taps = |out: usize, source: usize| -> Vec<(usize, usize, f32)> {
            let scale = source as f32 / out as f32;
            (0..out)
                .map(|i| {
                    let x = ((i as f32 + 0.5) * scale - 0.5).clamp(0.0, (source - 1) as f32);
                    let x0 = x.floor() as usize;
                    (x0, (x0 + 1).min(source - 1), x - x0 as f32)
                })
                .collect()
        };
        let columns = taps(width, self.width);
        let rows = taps(height, self.height);
        let stride = self.width * 3;
        let mut bgr = Vec::with_capacity(width * height * 3);
        for &(y0, y1, fy) in &rows {
            let top = &self.bgr[y0 * stride..(y0 + 1) * stride];
            let bottom = &self.bgr[y1 * stride..(y1 + 1) * stride];
            for &(x0, x1, fx) in &columns {
                for channel in 0..3 {
                    let sample = |row: &[u8]| {
                        row[x0 * 3 + channel] as f32 * (1.0 - fx)
                            + row[x1 * 3 + channel] as f32 * fx
                    };
                    let value = sample(top) * (1.0 - fy) + sample(bottom) * fy;
                    bgr.push(value.round() as u8);
                }
            }
        }
        Picture::new(width, height, bgr)
    }

    // Each output pixel (u, v) reads the source at the inverse of the
    // transform, which is how warpAffine fills its output.
    pub fn warp(&self, transform: &Similarity, width: usize, height: usize) -> Picture {
        let inverse = transform.inverse();
        let mut bgr = Vec::with_capacity(width * height * 3);
        for v in 0..height {
            for u in 0..width {
                let (x, y) = inverse.apply(u as f32, v as f32);
                for channel in 0..3 {
                    bgr.push(self.bilinear(x, y, channel).round() as u8);
                }
            }
        }
        Picture::new(width, height, bgr)
    }
}

// A rotation, a uniform scale, and a translation: x' = a*x - b*y + tx,
// y' = b*x + a*y + ty. Alignment needs no shear, because a face turned
// toward the camera differs from the template by these four values alone.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Similarity {
    pub a: f32,
    pub b: f32,
    pub tx: f32,
    pub ty: f32,
}

impl Similarity {
    pub fn apply(&self, x: f32, y: f32) -> (f32, f32) {
        (
            self.a * x - self.b * y + self.tx,
            self.b * x + self.a * y + self.ty,
        )
    }

    pub fn inverse(&self) -> Similarity {
        let det = self.a * self.a + self.b * self.b;
        let a = self.a / det;
        let b = -self.b / det;
        Similarity {
            a,
            b,
            tx: -(a * self.tx - b * self.ty),
            ty: -(b * self.tx + a * self.ty),
        }
    }

    // The least-squares similarity that carries `from` onto `to`. This is
    // Umeyama's estimate restricted to two dimensions, where it has a
    // closed form, and it is what OpenCV's FaceRecognizerSF computes from
    // the five landmarks.
    pub fn estimate(from: &[(f32, f32)], to: &[(f32, f32)]) -> Similarity {
        assert_eq!(from.len(), to.len());
        let n = from.len() as f32;
        let mean = |points: &[(f32, f32)]| {
            let (sx, sy) = points
                .iter()
                .fold((0.0, 0.0), |(sx, sy), (x, y)| (sx + x, sy + y));
            (sx / n, sy / n)
        };
        let (fx, fy) = mean(from);
        let (tx, ty) = mean(to);
        let (mut dot, mut cross, mut norm) = (0.0, 0.0, 0.0);
        for ((x, y), (u, v)) in from.iter().zip(to) {
            let (x, y, u, v) = (x - fx, y - fy, u - tx, v - ty);
            dot += x * u + y * v;
            cross += x * v - y * u;
            norm += x * x + y * y;
        }
        let a = dot / norm;
        let b = cross / norm;
        Similarity {
            a,
            b,
            tx: tx - (a * fx - b * fy),
            ty: ty - (b * fx + a * fy),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn gradient(width: usize, height: usize) -> Picture {
        let mut bgr = Vec::new();
        for y in 0..height {
            for x in 0..width {
                bgr.extend([(x * 10) as u8, (y * 10) as u8, 200]);
            }
        }
        Picture::new(width, height, bgr)
    }

    #[test]
    fn a_resize_to_the_same_size_changes_nothing() {
        let picture = gradient(8, 6);
        assert_eq!(picture.resize(8, 6), picture);
    }

    #[test]
    fn a_half_size_resize_averages_each_square_of_four() {
        let picture = gradient(4, 4);
        let half = picture.resize(2, 2);
        // The first output pixel reads source (0.5, 0.5): the mean of x 0
        // and 1 is 5, and the mean of y 0 and 1 is 5.
        assert_eq!(&half.bgr[0..3], &[5, 5, 200]);
    }

    #[test]
    fn the_identity_warp_changes_nothing() {
        let picture = gradient(8, 6);
        let identity = Similarity {
            a: 1.0,
            b: 0.0,
            tx: 0.0,
            ty: 0.0,
        };
        assert_eq!(picture.warp(&identity, 8, 6), picture);
    }

    #[test]
    fn a_warp_reads_black_outside_the_source() {
        let picture = gradient(4, 4);
        let shift = Similarity {
            a: 1.0,
            b: 0.0,
            tx: 10.0,
            ty: 0.0,
        };
        assert_eq!(&picture.warp(&shift, 2, 2).bgr[0..3], &[0, 0, 0]);
    }

    #[test]
    fn the_estimate_recovers_a_known_similarity() {
        let known = Similarity {
            a: 0.8,
            b: 0.3,
            tx: 12.0,
            ty: -4.0,
        };
        let from = [
            (10.0, 20.0),
            (40.0, 22.0),
            (25.0, 35.0),
            (14.0, 50.0),
            (38.0, 51.0),
        ];
        let to: Vec<_> = from.iter().map(|&(x, y)| known.apply(x, y)).collect();
        let found = Similarity::estimate(&from, &to);
        for (got, want) in [
            (found.a, known.a),
            (found.b, known.b),
            (found.tx, known.tx),
            (found.ty, known.ty),
        ] {
            assert!((got - want).abs() < 1e-3, "{found:?} is not {known:?}");
        }
    }

    #[test]
    fn the_inverse_undoes_the_transform() {
        let t = Similarity {
            a: 0.8,
            b: 0.3,
            tx: 12.0,
            ty: -4.0,
        };
        let (x, y) = t.inverse().apply(t.apply(7.0, 9.0).0, t.apply(7.0, 9.0).1);
        assert!((x - 7.0).abs() < 1e-4 && (y - 9.0).abs() < 1e-4);
    }
}
