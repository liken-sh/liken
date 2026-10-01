// The expensive pass: decode the keyframes of one video, find the faces in
// each, embed each face, and write the detections record. Nothing here
// knows who anyone is. The match reads the record later, so a new headshot
// or a new threshold never opens the video again.

use std::fs::{self, File};
use std::io::{BufWriter, Write};
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use crate::frames::{self, Keyframes};
use crate::models;
use crate::record::{self, Detection, Header, KeyframeLine};
use crate::runtime::{Detector, Embedder, Engine, Error};

pub struct Settings {
    pub models: PathBuf,
    pub device: String,
    pub threads: usize,
    pub cache: Option<PathBuf>,
    // The widest frame ffmpeg decodes to. Faces are cropped for the
    // embedder from frames this size. At 1280 wide the matches named as
    // many faces as crops from 1920-wide frames, within 1% on a 1080p and
    // a 4K film, and ffmpeg does the scaling instead of this program.
    pub decode_width: usize,
    // The widest picture the detector sees. YuNet's cost grows with the
    // pixels it reads, and a face too small to find at this width is too
    // small for SFace to name: in the experiments, 1% of faces under 40
    // pixels tall were named.
    pub detect_width: usize,
    pub hwaccel: Option<String>,
    pub decode_threads: usize,
}

// Where the record of a video goes: in .liken/appearances/ beside it, one
// record per video file, named for the file.
pub fn record_path(video: &Path) -> PathBuf {
    let folder = video.parent().unwrap_or(Path::new("."));
    let name = video.file_name().unwrap_or_default().to_string_lossy();
    folder
        .join(".liken")
        .join("appearances")
        .join(format!("{name}.jsonl"))
}

#[derive(Default)]
struct Timing {
    keyframes: usize,
    faces: usize,
    resize: Duration,
    detect: Duration,
    embed: Duration,
}

pub fn run(video: &Path, settings: &Settings) -> Result<PathBuf, Error> {
    let started = Instant::now();
    // The size is read before the decode starts. If another file replaces
    // the video during the pass, the record holds the earlier file's size,
    // and the library reads the record as one for a different file.
    let size = fs::metadata(video)?.len();
    let detector_file = models::find(&settings.models, models::DETECTOR)?;
    let embedder_file = models::find(&settings.models, models::EMBEDDER)?;
    let probe = frames::probe(video)?;
    let (width, height) = frames::decoded_size(&probe, settings.decode_width);
    let detect_width = settings.detect_width.min(width);
    let detect_height = (height * detect_width / width).max(1);
    let scale = width as f32 / detect_width as f32;

    let mut engine = Engine::new(
        &settings.device,
        settings.threads,
        settings.cache.as_deref(),
    )?;
    let mut detector = Detector::new(
        &mut engine,
        &detector_file.path,
        detect_width,
        detect_height,
    )?;
    let mut embedder = Embedder::new(&mut engine, &embedder_file.path)?;
    let ready = started.elapsed();

    let path = record_path(video);
    fs::create_dir_all(path.parent().unwrap())?;
    // The record is written beside its final name and renamed into place,
    // so a run that stops partway leaves no record that looks complete.
    let partial = path.with_extension("jsonl.partial");
    let mut out = BufWriter::new(File::create(&partial)?);
    let header = Header {
        format: record::FORMAT.into(),
        video: video
            .file_name()
            .unwrap_or_default()
            .to_string_lossy()
            .into(),
        size,
        duration: probe.duration,
        width,
        height,
        detector: detector_file.model,
        detect_width,
        embedder: embedder_file.model,
        device: engine.device.name().into(),
    };
    record::write_line(&mut out, &header)?;

    let mut timing = Timing::default();
    let mut keyframes = Keyframes::open(
        video,
        width,
        height,
        settings.hwaccel.as_deref(),
        settings.decode_threads,
    )?;
    for keyframe in keyframes.by_ref() {
        let keyframe = keyframe?;
        let clock = Instant::now();
        let small = if detect_width == width {
            keyframe.picture.clone()
        } else {
            keyframe.picture.resize(detect_width, detect_height)
        };
        timing.resize += clock.elapsed();
        let clock = Instant::now();
        let found = detector.detect(&small)?;
        timing.detect += clock.elapsed();
        let clock = Instant::now();
        let mut faces = Vec::with_capacity(found.len());
        for face in found {
            let face = face.scaled(scale);
            let vector = embedder.embed(&keyframe.picture, &face)?;
            faces.push(Detection {
                face,
                embedding: record::encode(&vector),
            });
        }
        timing.embed += clock.elapsed();
        timing.keyframes += 1;
        timing.faces += faces.len();
        let line = KeyframeLine {
            time: keyframe.time,
            faces,
        };
        record::write_line(&mut out, &line)?;
    }
    keyframes.finish()?;
    out.flush()?;
    drop(out);
    fs::rename(&partial, &path)?;

    let per = |total: Duration, count: usize| total.as_secs_f64() * 1000.0 / count.max(1) as f64;
    eprintln!(
        "{}: {} keyframes, {} faces on {} at {}x{}, detect at {}x{}",
        header.video,
        timing.keyframes,
        timing.faces,
        header.device,
        width,
        height,
        detect_width,
        detect_height
    );
    eprintln!(
        "  startup {:.1}s, wall {:.1}s; resize {:.1} ms/frame, detect {:.1} ms/frame, embed {:.1} ms/face",
        ready.as_secs_f64(),
        started.elapsed().as_secs_f64(),
        per(timing.resize, timing.keyframes),
        per(timing.detect, timing.keyframes),
        per(timing.embed, timing.faces),
    );
    Ok(path)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_record_goes_into_liken_beside_the_video() {
        let path = record_path(Path::new("/m/Film [2015]/Film [2015, Bluray-1080p].mkv"));
        assert_eq!(
            path,
            Path::new("/m/Film [2015]/.liken/appearances/Film [2015, Bluray-1080p].mkv.jsonl")
        );
    }
}
