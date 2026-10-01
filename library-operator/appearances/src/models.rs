// The two model files, found by name in one directory. The name of each file
// is the name OpenCV Zoo publishes it under, and it goes into every record
// with the file's hash, because vectors from two embedding models cannot be
// compared. A record whose embedder hash differs from the run's is stale.

use std::fs::File;
use std::io;
use std::path::{Path, PathBuf};

use sha2::{Digest, Sha256};

use crate::record::Model;
use crate::runtime::Error;

pub const DETECTOR: &str = "face_detection_yunet_2023mar";
pub const EMBEDDER: &str = "face_recognition_sface_2021dec";

pub struct ModelFile {
    pub path: PathBuf,
    pub model: Model,
}

// The SHA-256 of a file's bytes, in hex. A model's hash tells two models
// apart, and a headshot's hash tells a reader when the headshot changed.
pub fn sha256(path: &Path) -> Result<String, Error> {
    let mut hasher = Sha256::new();
    io::copy(
        &mut File::open(path).map_err(|e| format!("{}: {e}", path.display()))?,
        &mut hasher,
    )?;
    Ok(format!("{:x}", hasher.finalize()))
}

pub fn find(directory: &Path, name: &str) -> Result<ModelFile, Error> {
    let path = directory.join(format!("{name}.onnx"));
    let sha256 = sha256(&path)?;
    Ok(ModelFile {
        path,
        model: Model {
            name: name.into(),
            sha256,
        },
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn find_names_the_file_and_hashes_it() {
        let directory =
            std::env::temp_dir().join(format!("appearances-models-{}", std::process::id()));
        std::fs::create_dir_all(&directory).unwrap();
        std::fs::write(directory.join("tiny.onnx"), b"abc").unwrap();
        let found = find(&directory, "tiny").unwrap();
        assert_eq!(
            found.model.sha256,
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
        );
    }

    #[test]
    fn find_names_the_missing_path() {
        let error = find(Path::new("/nonexistent"), "tiny").err().unwrap();
        assert!(error.to_string().contains("/nonexistent/tiny.onnx"));
    }
}
