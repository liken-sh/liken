// The two text networks. The detector marks the pixels of each frame that
// are inside a line of text. The reader writes the text of one line. Both
// are PP-OCR models, which OpenVINO reads as their authors publish them.

use std::collections::HashMap;
use std::path::{Path, PathBuf};

use openvino::{ElementType, InferRequest, Shape, Tensor};

use crate::Error;
use crate::ctc::{self, Characters};
use crate::engine::Engine;
use crate::picture::Picture;
use crate::textboxes::{self, Found, TextBox};

pub const DETECTOR: &str = "text_detection_en_ppocrv3_2023may";
pub const READER: &str = "text_recognition_ch_ppocrv4";

// The mean of each channel that OpenCV Zoo's sample subtracts before the
// detector, in BGR order. The sample then scales by 1/255.
const DETECTOR_MEAN: [f32; 3] = [122.678_91, 116.668_77, 104.006_99];

pub struct Detector {
    request: InferRequest,
    width: usize,
    height: usize,
}

impl Detector {
    pub fn new(
        engine: &mut Engine,
        model: &Path,
        width: usize,
        height: usize,
    ) -> Result<Self, Error> {
        let request = engine.compile(model, "x", &[1, 3, height as i64, width as i64])?;
        Ok(Detector {
            request,
            width,
            height,
        })
    }

    pub fn detect(&mut self, picture: &Picture) -> Result<Found, Error> {
        assert_eq!((picture.width, picture.height), (self.width, self.height));
        let plane = self.width * self.height;
        let shape = Shape::new(&[1, 3, self.height as i64, self.width as i64])?;
        let mut input = Tensor::new(ElementType::F32, &shape)?;
        let data = input.get_data_mut::<f32>()?;
        for (index, pixel) in picture.bgr.as_chunks::<3>().0.iter().enumerate() {
            for channel in 0..3 {
                data[channel * plane + index] =
                    (pixel[channel] as f32 - DETECTOR_MEAN[channel]) / 255.0;
            }
        }
        self.request.set_tensor("x", &input)?;
        self.request.infer()?;
        let output = self.request.get_output_tensor()?;
        Ok(textboxes::find(
            output.get_data::<f32>()?,
            self.width,
            self.height,
        ))
    }
}

// The height of a line the reader takes, in pixels.
const LINE_HEIGHT: usize = 48;
// The widths the reader is compiled for. A line is resized to the height
// above with its proportions kept, and padded on the right to the next of
// these widths. A GPU runs a fixed input size much faster than a dynamic
// one, and seven sizes cover a two-letter line and a full line of a credit
// roll with little padding. A line wider than the last is squeezed into it.
const LINE_WIDTHS: [usize; 7] = [96, 160, 240, 320, 480, 640, 960];
// A box under this many pixels high in the frame is too small to read.
const SMALLEST_LINE: f32 = 8.0;

pub struct Reader {
    model: PathBuf,
    characters: Characters,
    requests: HashMap<usize, InferRequest>,
}

impl Reader {
    pub fn new(model: &Path, characters: &Path) -> Result<Self, Error> {
        let list = std::fs::read_to_string(characters)
            .map_err(|e| format!("{}: {e}", characters.display()))?;
        Ok(Reader {
            model: model.to_path_buf(),
            characters: Characters::parse(&list),
            requests: HashMap::new(),
        })
    }

    // The text of one box of the frame and the reader's confidence in it,
    // or nothing for a box that is too small or that stands on its end.
    pub fn read(
        &mut self,
        engine: &mut Engine,
        frame: &Picture,
        text: &TextBox,
    ) -> Result<Option<(String, f32)>, Error> {
        let corners = text.corners(frame.width, frame.height);
        let (width, height) = (corners.2 - corners.0, corners.3 - corners.1);
        if height < SMALLEST_LINE || height > width * 1.5 {
            return Ok(None);
        }
        let wanted = (LINE_HEIGHT as f32 * width / height).ceil() as usize;
        let padded = line_width(wanted);
        let line = frame.crop(corners, wanted.clamp(8, padded), LINE_HEIGHT);
        if !self.requests.contains_key(&padded) {
            let shape = [1, 3, LINE_HEIGHT as i64, padded as i64];
            let request = engine.compile(&self.model, "x", &shape)?;
            self.requests.insert(padded, request);
        }
        let request = self.requests.get_mut(&padded).expect("compiled above");
        let plane = LINE_HEIGHT * padded;
        let shape = Shape::new(&[1, 3, LINE_HEIGHT as i64, padded as i64])?;
        let mut input = Tensor::new(ElementType::F32, &shape)?;
        let data = input.get_data_mut::<f32>()?;
        data.fill(0.0);
        for y in 0..LINE_HEIGHT {
            for x in 0..line.width {
                for channel in 0..3 {
                    let value = line.bgr[(y * line.width + x) * 3 + channel] as f32;
                    data[channel * plane + y * padded + x] = (value / 255.0 - 0.5) / 0.5;
                }
            }
        }
        request.set_tensor("x", &input)?;
        request.infer()?;
        let output = request.get_output_tensor()?;
        Ok(Some(ctc::decode(
            output.get_data::<f32>()?,
            &self.characters,
        )))
    }
}

// The compiled width that a line of `wanted` pixels is padded to.
fn line_width(wanted: usize) -> usize {
    LINE_WIDTHS
        .into_iter()
        .find(|width| *width >= wanted)
        .unwrap_or(LINE_WIDTHS[LINE_WIDTHS.len() - 1])
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn line_width_is_the_next_compiled_width() {
        assert_eq!(line_width(10), 96);
        assert_eq!(line_width(96), 96);
        assert_eq!(line_width(97), 160);
        assert_eq!(line_width(5000), 960);
    }
}
