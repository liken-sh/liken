// The expensive pass: decode the end of one video, find the text in each
// frame, read some of it, and write the reading. The decode and the two
// networks cost seconds for each minute of video, so this runs once for
// each file, and everything that depends on a model of where the titles
// start works from the reading.

use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use crate::Error;
use crate::engine::Engine;
use crate::frames::{self, Frames};
use crate::networks::{self, Detector, Reader};
use crate::picture;
use crate::reading::{self, Header, Line, Range, Second, Writer};

pub struct Settings {
    pub models: PathBuf,
    pub device: String,
    pub threads: usize,
    pub cache: Option<PathBuf>,
    pub hwaccel: Option<String>,
    pub decode_threads: usize,
    // How many seconds of the end of the video to read.
    pub window: f64,
    pub records: Option<PathBuf>,
}

// The reader reads a frame that holds text when it read no frame in this
// many seconds before. A card of a title sequence stays up for 2 to 5
// seconds and a credit roll moves a line off the screen in about 8, so
// one read in 8 seconds samples a roll without a gap, and it reads about
// one card in two. The model needs to know that names and the words of a
// credit are on screen in a stretch, not every one of them.
const READ_EVERY: f64 = 8.0;
// The most lines the reader reads of one frame, the largest first. A full
// frame of a credit roll holds 30 to 60 lines, and the first 40 hold
// enough of the words that tell a roll from a sign in the story.
const MOST_LINES: usize = 40;
// The reader's lowest confidence for a line that the reading keeps.
const LINE_CONFIDENCE: f32 = 0.5;

#[derive(Default)]
struct Timing {
    frames: usize,
    detect: Duration,
    reads: usize,
    lines: usize,
    read: Duration,
}

// Reads one video and returns the path of its reading. A reading that is
// current for the video's size and the two models is left as it is.
pub fn run(video: &Path, settings: &Settings) -> Result<PathBuf, Error> {
    let started = Instant::now();
    let size = std::fs::metadata(video)
        .map_err(|e| format!("{}: {e}", video.display()))?
        .len();
    let (detector_path, detector_model) = reading::model(&settings.models, networks::DETECTOR)?;
    let (reader_path, reader_model) = reading::model(&settings.models, networks::READER)?;
    let path = reading::path(video, settings.records.as_deref());
    if reading::current(&path, size, &detector_model, &reader_model) {
        return Ok(path);
    }
    let probe = frames::probe(video)?;
    let (width, height) = frames::decoded_size(&probe);
    let start = frames::window_start(probe.duration, settings.window);

    let mut engine = Engine::new(
        &settings.device,
        settings.threads,
        settings.cache.as_deref(),
    )?;
    let mut detector = Detector::new(&mut engine, &detector_path, width / 2, height / 2)?;
    let characters = settings.models.join(format!("{}.txt", networks::READER));
    let mut reader = Reader::new(&reader_path, &characters)?;
    let header = Header {
        format: reading::FORMAT.into(),
        video: video
            .file_name()
            .unwrap_or_default()
            .to_string_lossy()
            .into(),
        size,
        duration: probe.duration,
        ranges: vec![Range(start, probe.duration)],
        detector: detector_model,
        reader: reader_model,
        device: engine.device.name().into(),
        width,
        height,
    };

    // A GPU that cannot decode or scale the video fails before its first
    // frame. The software decode of a window costs a minute or two of one
    // core, so the read falls back to it here.
    let mut hwaccel = settings.hwaccel.as_deref();
    let timing = loop {
        let mut writer = Writer::create(&path, &header)?;
        let frames = Frames::open(
            video,
            start,
            (width, height),
            hwaccel,
            settings.decode_threads,
        )?;
        match pass(
            frames,
            start,
            &mut engine,
            &mut detector,
            &mut reader,
            &mut writer,
        ) {
            Ok(timing) => {
                writer.land()?;
                break timing;
            }
            Err(error) if hwaccel.is_some() => {
                writer.discard();
                eprintln!("{}: {error}; decoding in software", video.display());
                hwaccel = None;
            }
            Err(error) => {
                writer.discard();
                return Err(error);
            }
        }
    };
    let per = |total: Duration, count: usize| total.as_secs_f64() * 1000.0 / count.max(1) as f64;
    eprintln!(
        "{}: {} seconds from {start} on {} at {width}x{height}, wall {:.1}s; \
         detect {:.1} ms/frame; {} frames read, {} lines, {:.1} ms/line",
        header.video,
        timing.frames,
        header.device,
        started.elapsed().as_secs_f64(),
        per(timing.detect, timing.frames),
        timing.reads,
        timing.lines,
        per(timing.read, timing.lines),
    );
    Ok(path)
}

fn pass(
    frames: Frames,
    start: f64,
    engine: &mut Engine,
    detector: &mut Detector,
    reader: &mut Reader,
    writer: &mut Writer,
) -> Result<Timing, Error> {
    let mut timing = Timing::default();
    let mut frames = frames;
    let mut last_read = f64::MIN;
    let mut before: Option<Vec<f32>> = None;
    for frame in frames.by_ref() {
        let frame = frame?;
        let t = start + timing.frames as f64;
        let half = frame.halved();
        let clock = Instant::now();
        let found = detector.detect(&half)?;
        timing.detect += clock.elapsed();
        let (bright, dark) = half.light();
        let grid = half.gray_grid();
        let change = before.as_ref().map_or(0.0, |b| picture::change(b, &grid));
        before = Some(grid);
        let mut lines = None;
        if !found.boxes.is_empty() && t - last_read >= READ_EVERY {
            let clock = Instant::now();
            let mut largest = found.boxes.clone();
            largest.sort_by(|a, b| (b.width() * b.height()).total_cmp(&(a.width() * a.height())));
            largest.truncate(MOST_LINES);
            largest.sort_by(|a, b| a.center_y().total_cmp(&b.center_y()));
            let mut read = Vec::new();
            for text in &largest {
                if let Some((words, confidence)) = reader.read(engine, &frame, text)?
                    && confidence >= LINE_CONFIDENCE
                    && !words.is_empty()
                {
                    let confidence = (confidence * 1000.0).round() / 1000.0;
                    read.push(Line(words, confidence, text.center_y(), text.height()));
                }
            }
            timing.reads += 1;
            timing.lines += largest.len();
            timing.read += clock.elapsed();
            last_read = t;
            lines = Some(read);
        }
        writer.second(&Second {
            t,
            coverage: (found.coverage * 100000.0).round() / 100000.0,
            boxes: found.boxes,
            bright: (bright * 10000.0).round() / 10000.0,
            dark: (dark * 10000.0).round() / 10000.0,
            change: (change * 100000.0).round() / 100000.0,
            lines,
        })?;
        timing.frames += 1;
    }
    frames.finish()?;
    if timing.frames == 0 {
        return Err("ffmpeg decoded no frame".into());
    }
    Ok(timing)
}
