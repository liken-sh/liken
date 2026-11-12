// The reading of one video: for each second of a part of the video, where
// text is on screen, how dark the frame is, how much it changed, and, for
// some seconds, what the text says. `read` writes it once, because it
// costs a decode. `train` and `place` read it as often as the model
// changes.
//
// The reading is a file of JSON lines, the header first and then one line
// for each second, at `.liken/titles/<video>.jsonl` beside the video, or
// under another directory for a library that only reads.

use std::fs::{self, File};
use std::io::{BufRead, BufReader, BufWriter, Read, Write};
use std::os::unix::fs::MetadataExt;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::Error;
use crate::textboxes::TextBox;

// The format names what a reader of the file can rely on. A change to the
// sampling, to the box rule, or to the fields makes a new format, and a
// reading of another format is read again.
pub const FORMAT: &str = "liken.sh/titles/reading/v1";

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Model {
    pub name: String,
    pub sha256: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Header {
    pub format: String,
    // The video file's name and its size in bytes when the read started.
    // A file of another size under the same name is another video.
    pub video: String,
    pub size: u64,
    // The video's length, and the parts of it that the reading holds a
    // line for each second of, in the order of their seconds. `read`
    // reads one part, the end of the video, which is where the end titles
    // are. The list leaves room for the start of the video, where the
    // main titles are, in the same file.
    pub duration: f64,
    pub ranges: Vec<Range>,
    pub detector: Model,
    pub reader: Model,
    pub device: String,
    // The size of the decoded frames. The detector read each at half this.
    pub width: usize,
    pub height: usize,
}

// One part of a video, from its first second to its last.
#[derive(Clone, Copy, Debug, PartialEq, Serialize, Deserialize)]
pub struct Range(pub f64, pub f64);

impl Header {
    // The second that the reading of the end of the video starts at: the
    // start of the last part. A reading with no part starts at the end.
    pub fn end_start(&self) -> f64 {
        self.ranges.last().map_or(self.duration, |range| range.0)
    }
}

// One line of text that the reader read: the text, the reader's
// confidence, and the line's center and height as shares of the frame's
// height.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Line(pub String, pub f32, pub f32, pub f32);

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Second {
    pub t: f64,
    // The share of the frame inside a text box, and the boxes.
    pub coverage: f32,
    pub boxes: Vec<TextBox>,
    // The frame's mean brightness, the share of it that is nearly black,
    // and how much it differs from the frame a second before, each from 0
    // to 1.
    pub bright: f32,
    pub dark: f32,
    pub change: f32,
    // The text of the frame's lines, on the seconds the reader read. A
    // second it did not read leaves the field out, which differs from a
    // read that found no text.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub lines: Option<Vec<Line>>,
}

pub struct Reading {
    pub header: Header,
    pub seconds: Vec<Second>,
}

// Where a video's reading is. `records` names a directory that holds the
// readings in place of the library, for a library that only reads: the
// video's own directory is repeated under it.
pub fn path(video: &Path, records: Option<&Path>) -> PathBuf {
    let folder = video.parent().unwrap_or(Path::new("."));
    let folder = match records {
        Some(records) => records.join(folder.strip_prefix("/").unwrap_or(folder)),
        None => folder.to_path_buf(),
    };
    let name = video.file_name().unwrap_or_default().to_string_lossy();
    folder
        .join(".liken")
        .join("titles")
        .join(format!("{name}.jsonl"))
}

pub fn load(path: &Path) -> Result<Reading, Error> {
    let file = File::open(path).map_err(|e| format!("{}: {e}", path.display()))?;
    let mut lines = BufReader::new(file).lines();
    let first = lines.next().ok_or("the reading is empty")??;
    let header: Header = serde_json::from_str(&first)?;
    if header.format != FORMAT {
        return Err(format!("{}: the format is {}", path.display(), header.format).into());
    }
    let seconds = lines
        .map(|line| Ok(serde_json::from_str(&line?)?))
        .collect::<Result<Vec<Second>, Error>>()?;
    Ok(Reading { header, seconds })
}

// True when the file at `path` is a reading of this format of a video of
// this size, by these two models.
pub fn current(path: &Path, size: u64, detector: &Model, reader: &Model) -> bool {
    let Ok(file) = File::open(path) else {
        return false;
    };
    let mut first = String::new();
    if BufReader::new(file).read_line(&mut first).is_err() {
        return false;
    }
    serde_json::from_str::<Header>(&first).is_ok_and(|header| {
        header.format == FORMAT
            && header.size == size
            && header.detector == *detector
            && header.reader == *reader
    })
}

// A reading as it is written. The lines go to a partial file, which takes
// the reading's name only when `land` is called, so no reader finds half a
// reading under the name.
pub struct Writer {
    out: BufWriter<File>,
    partial: PathBuf,
    path: PathBuf,
}

impl Writer {
    pub fn create(path: &Path, header: &Header) -> Result<Writer, Error> {
        let folder = path.parent().ok_or("the reading has no folder")?;
        fs::create_dir_all(folder)?;
        let name = path.file_name().unwrap_or_default().to_string_lossy();
        let partial = folder.join(format!(".partial-{}-{name}", std::process::id()));
        let mut out = BufWriter::new(File::create(&partial)?);
        writeln!(out, "{}", serde_json::to_string(header)?)?;
        Ok(Writer {
            out,
            partial,
            path: path.to_path_buf(),
        })
    }

    pub fn second(&mut self, second: &Second) -> Result<(), Error> {
        writeln!(self.out, "{}", serde_json::to_string(second)?)?;
        Ok(())
    }

    pub fn land(mut self) -> Result<PathBuf, Error> {
        self.out.flush()?;
        drop(self.out);
        adopt_owner(&self.partial);
        fs::rename(&self.partial, &self.path)?;
        Ok(self.path)
    }

    // Removes the partial file of a read that failed.
    pub fn discard(self) {
        drop(self.out);
        let _ = fs::remove_file(&self.partial);
    }
}

// Gives a file the owner of the library folder that holds its `.liken`
// directory. The worker runs as root, and the tools that manage a library
// run as the user who owns its folders, so a file that root owns would be
// one they cannot replace. The change fails for a user who is not root,
// and the file then keeps that user, who made it.
fn adopt_owner(file: &Path) {
    let folder = file
        .ancestors()
        .find(|a| a.file_name().is_some_and(|n| n == ".liken"));
    if let Some(Ok(owner)) = folder.and_then(Path::parent).map(fs::metadata) {
        for path in file.ancestors().take(3) {
            let _ = std::os::unix::fs::chown(path, Some(owner.uid()), Some(owner.gid()));
        }
    }
}

// The SHA-256 of a file's bytes, in hex. A model's hash tells two models
// apart: boxes from two detectors do not compare, so a reading names the
// models that made it.
pub fn sha256(path: &Path) -> Result<String, Error> {
    let mut file = File::open(path).map_err(|e| format!("{}: {e}", path.display()))?;
    let mut hasher = Sha256::new();
    let mut buffer = [0u8; 64 * 1024];
    loop {
        let read = file.read(&mut buffer)?;
        if read == 0 {
            break;
        }
        hasher.update(&buffer[..read]);
    }
    Ok(hasher
        .finalize()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect())
}

pub fn model(directory: &Path, name: &str) -> Result<(PathBuf, Model), Error> {
    let path = directory.join(format!("{name}.onnx"));
    let sha256 = sha256(&path)?;
    Ok((
        path,
        Model {
            name: name.into(),
            sha256,
        },
    ))
}

#[cfg(test)]
pub mod tests {
    use super::*;

    pub fn scratch(name: &str) -> PathBuf {
        let folder = std::env::temp_dir().join(format!("titles-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&folder);
        fs::create_dir_all(&folder).unwrap();
        folder
    }

    pub fn header() -> Header {
        Header {
            format: FORMAT.into(),
            video: "film.mkv".into(),
            size: 10,
            duration: 100.0,
            ranges: vec![Range(40.0, 100.0)],
            detector: Model {
                name: "detector".into(),
                sha256: "aa".into(),
            },
            reader: Model {
                name: "reader".into(),
                sha256: "bb".into(),
            },
            device: "CPU".into(),
            width: 1472,
            height: 832,
        }
    }

    pub fn second(t: f64) -> Second {
        Second {
            t,
            coverage: 0.0,
            boxes: vec![],
            bright: 0.5,
            dark: 0.0,
            change: 0.0,
            lines: None,
        }
    }

    #[test]
    fn path_is_beside_the_video_or_under_the_records_directory() {
        let video = Path::new("/library/Film [2000]/film.mkv");
        assert_eq!(
            path(video, None),
            Path::new("/library/Film [2000]/.liken/titles/film.mkv.jsonl")
        );
        assert_eq!(
            path(video, Some(Path::new("/records"))),
            Path::new("/records/library/Film [2000]/.liken/titles/film.mkv.jsonl")
        );
    }

    #[test]
    fn a_landed_reading_loads_as_it_was_written() {
        let folder = scratch("round-trip");
        let target = path(&folder.join("film.mkv"), None);
        let mut read = second(41.0);
        read.boxes = vec![TextBox(0.5, 0.5, 0.25, 0.05)];
        read.lines = Some(vec![Line("Directed by".into(), 0.97, 0.5, 0.05)]);
        let mut writer = Writer::create(&target, &header()).unwrap();
        writer.second(&second(40.0)).unwrap();
        writer.second(&read).unwrap();
        writer.land().unwrap();
        let loaded = load(&target).unwrap();
        assert_eq!(loaded.header, header());
        assert_eq!(loaded.seconds, vec![second(40.0), read]);
    }

    #[test]
    fn the_end_starts_at_the_last_part() {
        let mut both = header();
        both.ranges = vec![Range(0.0, 20.0), Range(40.0, 100.0)];
        assert_eq!(both.end_start(), 40.0);
        both.ranges.clear();
        assert_eq!(both.end_start(), 100.0);
    }

    #[test]
    fn a_discarded_reading_leaves_no_file() {
        let folder = scratch("discard");
        let target = path(&folder.join("film.mkv"), None);
        Writer::create(&target, &header()).unwrap().discard();
        assert_eq!(fs::read_dir(target.parent().unwrap()).unwrap().count(), 0);
    }

    #[test]
    fn current_is_true_only_for_the_same_size_and_models() {
        let folder = scratch("current");
        let target = path(&folder.join("film.mkv"), None);
        Writer::create(&target, &header()).unwrap().land().unwrap();
        let (detector, reader) = (header().detector, header().reader);
        assert!(current(&target, 10, &detector, &reader));
        assert!(!current(&target, 11, &detector, &reader));
        assert!(!current(&target, 10, &reader, &reader));
        assert!(!current(&folder.join("absent"), 10, &detector, &reader));
    }
}
