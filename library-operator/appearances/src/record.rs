// The detections record: the output of the expensive pass, which opens the
// video. It is JSON Lines, so `head` shows the header, `jq` reads any
// sample, and a reader can stream it. The first line names the models and
// their hashes, the run, the video file's size in bytes, and the frame size
// and length of the video. Each line after it is one sample, with every face
// found in it, including a sample with no face, because a stretch with no
// face at all ends a span.
//
// The samples are every keyframe and a frame every second between them
// (frames.rs). A keyframe line carries `"keyframe": true`, and other lines
// leave the field out. Encoders place a keyframe at most cuts, so the spans
// stop each sample's stretch at the next keyframe (player.rs). The flag is
// on the line and not in a list in the header, because the decode learns
// each keyframe only when it reaches it: the header is written before the
// decode starts, and each line is written as its frame arrives. A list in
// the header would need the whole decode held in memory, or a second pass
// over the file.
//
// The format names what the record holds: v2 is the samples above, with
// every face that YuNet scored at 0.8 or more (yunet.rs). A reader takes
// v2 alone. The operator reads a record of any other format as stale and
// decodes the video again.
//
// Each embedding is SFace's unit vector as 16-bit floats in little-endian
// order, base64 encoded: 128 values in 256 bytes.

use std::fs;
use std::io::{self, BufRead, Write};
use std::os::unix::fs::{MetadataExt, lchown};
use std::path::{Path, PathBuf};

use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use half::f16;
use serde::{Deserialize, Serialize};

use crate::runtime::Error;
use crate::yunet::Face;

pub const FORMAT: &str = "liken.sh/appearances/detections/v2";

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Model {
    pub name: String,
    pub sha256: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Header {
    pub format: String,
    pub video: String,
    // The video file's size in bytes when the pass started. The library
    // treats a file whose size differs from its probe record as another
    // file, and this size tells a reader which file the faces came from.
    pub size: u64,
    pub duration: f64,
    // The size the faces' coordinates are in: the decoded frame, which is
    // narrower than the video for a source wider than the decode limit.
    pub width: usize,
    pub height: usize,
    pub detector: Model,
    pub detect_width: usize,
    pub embedder: Model,
    pub device: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Detection {
    #[serde(flatten)]
    pub face: Face,
    pub embedding: String,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct SampleLine {
    pub time: f64,
    // True when the frame is a keyframe.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub keyframe: bool,
    pub faces: Vec<Detection>,
}

pub fn encode(vector: &[f32]) -> String {
    let bytes: Vec<u8> = vector
        .iter()
        .flat_map(|v| f16::from_f32(*v).to_le_bytes())
        .collect();
    STANDARD.encode(bytes)
}

pub fn decode(text: &str) -> Result<Vec<f32>, Error> {
    let bytes = STANDARD.decode(text)?;
    Ok(bytes
        .as_chunks::<2>()
        .0
        .iter()
        .map(|pair| f16::from_le_bytes(*pair).to_f32())
        .collect())
}

pub fn write_line<T: Serialize>(out: &mut impl Write, value: &T) -> Result<(), Error> {
    serde_json::to_writer(&mut *out, value)?;
    out.write_all(b"\n")?;
    Ok(())
}

pub struct Record {
    pub header: Header,
    pub samples: Vec<SampleLine>,
}

pub fn read(input: impl BufRead) -> Result<Record, Error> {
    let mut lines = input.lines();
    let first = lines.next().ok_or("the detections record is empty")??;
    let header: Header = serde_json::from_str(&first)?;
    if header.format != FORMAT {
        return Err(format!("the record's format is {}, not {FORMAT}", header.format).into());
    }
    let mut samples: Vec<SampleLine> = Vec::new();
    for line in lines {
        samples.push(serde_json::from_str(&line?)?);
    }
    // The record keeps the order ffmpeg delivered the frames in. Every
    // reader wants time order, and the sort costs nothing when the two
    // orders agree.
    samples.sort_by(|a, b| a.time.total_cmp(&b.time));
    Ok(Record { header, samples })
}

// The name a file is written under before it is renamed into place. It
// carries the host and the process, so two writers of one file, such as two
// clusters that mount one library and decode one film at once, never write
// into each other's partial file, and each rename puts a whole file in place.
// The name ends in neither .jsonl nor .json, so no reader takes it for a
// record or a spans file.
pub fn partial(path: &Path) -> PathBuf {
    let host = std::env::var("HOSTNAME").unwrap_or_default();
    let mut name = path.file_name().unwrap_or_default().to_os_string();
    name.push(format!(".partial-{host}-{}", std::process::id()));
    path.with_file_name(name)
}

// Every directory and file the tool lands in a title's folder takes the
// owner and the group of the directory it lands in, the rule the operator's
// own writes follow (volumeowner.go). Radarr and Sonarr own the folders they
// make, and they rename and delete what is in them when they upgrade or
// remove a title, which fails on a directory that the pod's root user owns.
// The hand-over never fails the run: a file it cannot hand over keeps the
// owner it was written with. A run as a user other than root, and a share
// that maps root to another user, both refuse the change, and a directory
// that is gone fails the write's own rename after this.
pub fn adopt_owner(path: &Path, directory: &Path) {
    if let Ok(folder) = fs::metadata(directory) {
        let _ = lchown(path, Some(folder.uid()), Some(folder.gid()));
    }
}

// The directory a file lands in, made with every missing directory above it.
// Each directory this call made takes the owner of the nearest one that was
// already there.
pub fn make_dirs(directory: &Path) -> io::Result<()> {
    let mut made = Vec::new();
    let mut held = directory;
    loop {
        match fs::symlink_metadata(held) {
            Ok(_) => break,
            Err(e) if e.kind() == io::ErrorKind::NotFound => made.push(held),
            Err(e) => return Err(e),
        }
        match held.parent() {
            Some(parent) => held = parent,
            None => break,
        }
    }
    fs::create_dir_all(directory)?;
    for path in made {
        adopt_owner(path, held);
    }
    Ok(())
}

// The rename that puts a partial file under its name. The partial file takes
// its folder's owner first, so no reader sees the file under its name with
// another owner.
pub fn land(partial: &Path, path: &Path) -> io::Result<()> {
    if let Some(directory) = path.parent() {
        adopt_owner(partial, directory);
    }
    fs::rename(partial, path)
}

#[cfg(test)]
mod tests {
    use super::*;

    // A title's folder that another group owns, the way a folder Radarr or
    // Sonarr made is owned by the uid and gid they run as. A user who is not
    // root can give a directory to any group they belong to, so the tests
    // need a second group, and they pass without a check where the user
    // has one group alone.
    fn foreign_folder(name: &str) -> Option<(PathBuf, u32)> {
        let status = fs::read_to_string("/proc/self/status").unwrap();
        let field = |key: &str| -> Vec<u32> {
            status
                .lines()
                .find_map(|line| line.strip_prefix(key))
                .unwrap_or_default()
                .split_whitespace()
                .map(|id| id.parse().unwrap())
                .collect()
        };
        let own = field("Gid:")[1];
        let Some(other) = field("Groups:").into_iter().find(|g| *g != own) else {
            eprintln!("the user belongs to one group, so no folder can belong to another");
            return None;
        };
        let root = std::env::temp_dir().join(format!("appearances-{name}-{}", std::process::id()));
        let _ = fs::remove_dir_all(&root);
        let folder = root.join("Film [1999]");
        fs::create_dir_all(&folder).unwrap();
        std::os::unix::fs::chown(&folder, None, Some(other)).unwrap();
        Some((folder, other))
    }

    fn group_of(path: &Path) -> u32 {
        fs::symlink_metadata(path).unwrap().gid()
    }

    // Every directory and file the tool lands in a title's folder takes the
    // folder's owner, so the tool that owns the folder can rename and
    // delete it.
    #[test]
    fn a_landed_file_and_its_new_directories_take_the_owner_of_the_folder() {
        let Some((folder, group)) = foreign_folder("owner") else {
            return;
        };
        let path = folder.join(".liken/appearances/Film.mkv.jsonl");
        make_dirs(path.parent().unwrap()).unwrap();
        let staged = partial(&path);
        fs::write(&staged, b"{}").unwrap();

        land(&staged, &path).unwrap();

        for landed in [
            ".liken",
            ".liken/appearances",
            ".liken/appearances/Film.mkv.jsonl",
        ] {
            assert_eq!(group_of(&folder.join(landed)), group, "{landed}");
        }
    }

    // A run that may not change an owner, such as one as a user other than
    // root or on a share that maps root to another user, leaves each file
    // with the owner it was written with, and the run goes on.
    #[test]
    fn a_file_the_run_may_not_hand_over_keeps_its_owner() {
        let scratch = std::env::temp_dir().join(format!("appearances-keep-{}", std::process::id()));
        fs::create_dir_all(&scratch).unwrap();
        let path = scratch.join("Film.mkv.jsonl");
        fs::write(&path, b"{}").unwrap();
        let own = group_of(&path);

        for directory in [Path::new("/"), &scratch.join("gone")] {
            adopt_owner(&path, directory);
            assert_eq!(group_of(&path), own, "{}", directory.display());
        }
    }

    #[test]
    fn an_embedding_survives_the_round_trip_within_half_precision() {
        let vector = [0.125, -0.5, 0.0883883, 0.3];
        let back = decode(&encode(&vector)).unwrap();
        for (a, b) in vector.iter().zip(&back) {
            assert!((a - b).abs() < 1e-3, "{a} became {b}");
        }
    }

    #[test]
    fn a_partial_file_sits_beside_its_file_under_a_name_no_reader_takes() {
        let path = Path::new("/films/.liken/appearances/Film.mkv.jsonl");
        let partial = partial(path);
        assert_eq!(partial.parent(), path.parent());
        let name = partial.file_name().unwrap().to_string_lossy();
        assert!(name.starts_with("Film.mkv.jsonl.partial-"), "{name}");
        assert!(
            name.ends_with(&format!("-{}", std::process::id())),
            "{name}"
        );
        assert_ne!(partial.extension().unwrap(), "jsonl");
    }

    #[test]
    fn a_full_embedding_takes_256_bytes() {
        let text = encode(&[0.1; 128]);
        assert_eq!(STANDARD.decode(text).unwrap().len(), 256);
    }

    fn header() -> Header {
        let model = Model {
            name: "m".into(),
            sha256: "00".into(),
        };
        Header {
            format: FORMAT.into(),
            video: "film.mkv".into(),
            size: 4_000_000_000,
            duration: 100.0,
            width: 1920,
            height: 800,
            detector: model.clone(),
            detect_width: 1280,
            embedder: model,
            device: "CPU".into(),
        }
    }

    #[test]
    fn a_record_reads_back_what_was_written() {
        let face = Face {
            x: 1.0,
            y: 2.0,
            w: 3.0,
            h: 4.0,
            score: 0.95,
            landmarks: [(5.0, 6.0); 5],
        };
        let line = SampleLine {
            time: 4.5,
            keyframe: true,
            faces: vec![Detection {
                face,
                embedding: encode(&[0.5; 128]),
            }],
        };
        let mut out = Vec::new();
        write_line(&mut out, &header()).unwrap();
        write_line(&mut out, &line).unwrap();
        let record = read(out.as_slice()).unwrap();
        assert_eq!((record.header, record.samples), (header(), vec![line]));
    }

    #[test]
    fn a_record_reads_its_samples_in_time_order() {
        let mut out = Vec::new();
        write_line(&mut out, &header()).unwrap();
        for time in [9.0, 4.5, 12.0] {
            write_line(
                &mut out,
                &SampleLine {
                    time,
                    keyframe: false,
                    faces: vec![],
                },
            )
            .unwrap();
        }
        let times: Vec<f64> = read(out.as_slice())
            .unwrap()
            .samples
            .iter()
            .map(|k| k.time)
            .collect();
        assert_eq!(times, [4.5, 9.0, 12.0]);
    }

    #[test]
    fn only_a_keyframe_line_carries_the_flag() {
        let lines = [
            SampleLine {
                time: 1.0,
                keyframe: true,
                faces: vec![],
            },
            SampleLine {
                time: 2.0,
                keyframe: false,
                faces: vec![],
            },
        ];
        let text: Vec<String> = lines
            .iter()
            .map(|l| serde_json::to_string(l).unwrap())
            .collect();
        assert_eq!(
            text,
            [
                r#"{"time":1.0,"keyframe":true,"faces":[]}"#,
                r#"{"time":2.0,"faces":[]}"#
            ]
        );
    }

    #[test]
    fn a_record_of_the_keyframes_only_format_is_refused() {
        let mut first = header();
        first.format = "liken.sh/appearances/detections/v1".into();
        let mut out = Vec::new();
        write_line(&mut out, &first).unwrap();
        assert!(read(out.as_slice()).is_err());
    }

    #[test]
    fn a_record_of_another_format_is_refused() {
        let mut other = header();
        other.format = "something/else".into();
        let mut out = Vec::new();
        write_line(&mut out, &other).unwrap();
        assert!(read(out.as_slice()).is_err());
    }
}
