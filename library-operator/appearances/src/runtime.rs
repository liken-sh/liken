// The two networks on OpenVINO. One OpenVINO core runs both, on the device
// the run names: the CPU everywhere, or an Intel GPU where the node has one.
// The models are the ONNX files OpenCV Zoo publishes, which OpenVINO reads
// directly, so no conversion step and no second runtime sits between the
// published weights and the run.

use std::path::Path;

use openvino::{Core, DeviceType, ElementType, PartialShape, RwPropertyKey, Shape, Tensor};

use crate::picture::{Picture, Similarity};
use crate::yunet::{self, Face, StrideOutputs};

pub type Error = Box<dyn std::error::Error>;

#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Device {
    Cpu,
    Gpu,
}

impl Device {
    pub fn name(&self) -> &'static str {
        match self {
            Device::Cpu => "CPU",
            Device::Gpu => "GPU",
        }
    }

    fn openvino(&self) -> DeviceType<'static> {
        match self {
            Device::Cpu => DeviceType::CPU,
            Device::Gpu => DeviceType::GPU,
        }
    }
}

pub struct Engine {
    core: Core,
    pub device: Device,
}

impl Engine {
    // `wanted` is "cpu", "gpu", or "auto". Auto takes the GPU when OpenVINO
    // finds one, which needs both the GPU plugin and Intel's OpenCL
    // runtime, and the CPU otherwise. A node with no GPU claim has no
    // /dev/dri in its container, so auto falls back with no configuration.
    pub fn new(wanted: &str, threads: usize, cache: Option<&Path>) -> Result<Engine, Error> {
        let mut core = Core::new()?;
        let gpu = core
            .available_devices()?
            .iter()
            .any(|d| matches!(d, DeviceType::GPU));
        let device = match (wanted, gpu) {
            ("cpu", _) | ("auto", false) => Device::Cpu,
            ("gpu", true) | ("auto", true) => Device::Gpu,
            ("gpu", false) => return Err("no GPU is available to OpenVINO".into()),
            _ => return Err(format!("unknown device {wanted:?}").into()),
        };
        if device == Device::Cpu {
            core.set_property(
                &DeviceType::CPU,
                &RwPropertyKey::InferenceNumThreads,
                &threads.to_string(),
            )?;
        }
        // The GPU compiles its kernels when a model compiles, which took
        // most of the 17 seconds of startup in the experiments. The cache
        // keeps the compiled kernels, so the next run on the node skips it.
        if let (Device::Gpu, Some(cache)) = (device, cache) {
            core.set_property(
                &DeviceType::GPU,
                &RwPropertyKey::CacheDir,
                &cache.to_string_lossy(),
            )?;
        }
        Ok(Engine { core, device })
    }

    fn compile(
        &mut self,
        model: &Path,
        input: &str,
        shape: &[i64; 4],
    ) -> Result<openvino::InferRequest, Error> {
        let mut network = self
            .core
            .read_model_from_file(&model.to_string_lossy(), "")?;
        network.reshape(&[(input, &PartialShape::new_static(4, shape)?)])?;
        let mut compiled = self.core.compile_model(&network, self.device.openvino())?;
        Ok(compiled.create_infer_request()?)
    }
}

// Packs a BGR picture into a planar float tensor, channel by channel, with
// no scaling and no mean: the 0 to 255 range both networks take. `swap`
// writes the channels in RGB order, which SFace wants.
fn planar(picture: &Picture, width: usize, height: usize, swap: bool) -> Result<Tensor, Error> {
    let shape = Shape::new(&[1, 3, height as i64, width as i64])?;
    let mut tensor = Tensor::new(ElementType::F32, &shape)?;
    let data = tensor.get_data_mut::<f32>()?;
    data.fill(0.0);
    let plane = width * height;
    for y in 0..picture.height {
        for x in 0..picture.width {
            let source = (y * picture.width + x) * 3;
            for channel in 0..3 {
                let target = if swap { 2 - channel } else { channel };
                data[target * plane + y * width + x] = picture.bgr[source + channel] as f32;
            }
        }
    }
    Ok(tensor)
}

pub struct Detector {
    request: openvino::InferRequest,
    width: usize,
    height: usize,
}

impl Detector {
    // A detector for pictures up to width by height. The network is
    // reshaped to that size rounded up to a multiple of 32, as
    // FaceDetectorYN does, and compiled once for it.
    pub fn new(
        engine: &mut Engine,
        model: &Path,
        width: usize,
        height: usize,
    ) -> Result<Self, Error> {
        let (width, height) = (yunet::padded(width), yunet::padded(height));
        let request = engine.compile(model, "input", &[1, 3, height as i64, width as i64])?;
        Ok(Detector {
            request,
            width,
            height,
        })
    }

    pub fn detect(&mut self, picture: &Picture) -> Result<Vec<Face>, Error> {
        assert!(picture.width <= self.width && picture.height <= self.height);
        let input = planar(picture, self.width, self.height, false)?;
        self.request.set_tensor("input", &input)?;
        self.request.infer()?;
        let mut faces = Vec::new();
        for stride in yunet::STRIDES {
            let read = |name: &str| self.request.get_tensor(&format!("{name}_{stride}"));
            let (cls, obj, bbox, kps) = (read("cls")?, read("obj")?, read("bbox")?, read("kps")?);
            let outputs = StrideOutputs {
                cls: cls.get_data::<f32>()?,
                obj: obj.get_data::<f32>()?,
                bbox: bbox.get_data::<f32>()?,
                kps: kps.get_data::<f32>()?,
            };
            faces.extend(yunet::decode(stride, self.width, &outputs));
        }
        Ok(yunet::suppress(faces))
    }
}

// Where SFace expects the five landmarks in its 112 by 112 input. These are
// the values in OpenCV's FaceRecognizerSF, which the model was trained on.
const TEMPLATE: [(f32, f32); 5] = [
    (38.2946, 51.6963),
    (73.5318, 51.5014),
    (56.0252, 71.7366),
    (41.5493, 92.3655),
    (70.7299, 92.2041),
];

pub const EMBEDDING: usize = 128;

pub struct Embedder {
    request: openvino::InferRequest,
}

impl Embedder {
    pub fn new(engine: &mut Engine, model: &Path) -> Result<Self, Error> {
        let request = engine.compile(model, "data", &[1, 3, 112, 112])?;
        Ok(Embedder { request })
    }

    // The face, aligned to the template, as a unit vector. A unit vector
    // makes the cosine similarity a plain dot product, so the match never
    // needs the model.
    pub fn embed(&mut self, picture: &Picture, face: &Face) -> Result<Vec<f32>, Error> {
        let transform = Similarity::estimate(&face.landmarks, &TEMPLATE);
        let aligned = picture.warp(&transform, 112, 112);
        let input = planar(&aligned, 112, 112, true)?;
        self.request.set_tensor("data", &input)?;
        self.request.infer()?;
        let output = self.request.get_output_tensor()?;
        let vector = output.get_data::<f32>()?;
        let norm = vector.iter().map(|v| v * v).sum::<f32>().sqrt();
        Ok(vector.iter().map(|v| v / norm).collect())
    }
}
