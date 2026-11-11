// OpenVINO, on the device the run names: the CPU everywhere, or an Intel
// GPU where the node has one. OpenVINO reads the ONNX files that the
// models' authors publish, so no conversion step sits between the
// published weights and the run.

use std::path::Path;

use openvino::{Core, DeviceType, InferRequest, PartialShape, RwPropertyKey};

use crate::Error;

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
    // runtime, and the CPU otherwise.
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
        // The GPU compiles its kernels when a model compiles. The cache
        // keeps the compiled kernels, so the next run on the node skips
        // the compilation. The reader compiles once for each line width,
        // so the cache saves it the most.
        if let (Device::Gpu, Some(cache)) = (device, cache) {
            core.set_property(
                &DeviceType::GPU,
                &RwPropertyKey::CacheDir,
                &cache.to_string_lossy(),
            )?;
        }
        // The GPU computes in 16-bit floats unless it is told otherwise. In
        // 16 bits the text detector marks text in frames that hold none:
        // on one film it found text in every second, where the CPU found
        // it in 42% of them. 32 bits gives the CPU's boxes.
        if device == Device::Gpu {
            core.set_property(
                &DeviceType::GPU,
                &RwPropertyKey::HintInferencePrecision,
                "f32",
            )?;
        }
        Ok(Engine { core, device })
    }

    // One model compiled for one input size. Both text models publish a
    // dynamic input, and a GPU runs a fixed size much faster, so every
    // request here has a fixed size.
    pub fn compile(
        &mut self,
        model: &Path,
        input: &str,
        shape: &[i64; 4],
    ) -> Result<InferRequest, Error> {
        let mut network = self
            .core
            .read_model_from_file(&model.to_string_lossy(), "")?;
        network.reshape(&[(input, &PartialShape::new_static(4, shape)?)])?;
        let mut compiled = self.core.compile_model(&network, self.device.openvino())?;
        Ok(compiled.create_infer_request()?)
    }
}
