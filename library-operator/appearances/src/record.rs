// The detections record: the output of the expensive pass, which opens the
// video. It is JSON Lines, so `head` shows the header, `jq` reads any
// keyframe, and a reader can stream it. The first line names the models and
// their hashes, the run, the video file's size in bytes, and the frame size
// and length of the video. Each line after it
// is one keyframe, with every face found in it, including a keyframe with no
// face, because the gaps between keyframes are what bound a span.
//
// Each embedding is SFace's unit vector as 16-bit floats in little-endian
// order, base64 encoded: 128 values in 256 bytes, which keeps a two-hour
// film's record near half a megabyte.

use std::io::{BufRead, Write};
use std::path::{Path, PathBuf};

use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use half::f16;
use serde::{Deserialize, Serialize};

use crate::runtime::Error;
use crate::yunet::Face;

pub const FORMAT: &str = "liken.sh/appearances/detections/v1";

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
pub struct KeyframeLine {
    pub time: f64,
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
    pub keyframes: Vec<KeyframeLine>,
}

pub fn read(input: impl BufRead) -> Result<Record, Error> {
    let mut lines = input.lines();
    let first = lines.next().ok_or("the detections record is empty")??;
    let header: Header = serde_json::from_str(&first)?;
    if header.format != FORMAT {
        return Err(format!("the record's format is {}, not {FORMAT}", header.format).into());
    }
    let mut keyframes: Vec<KeyframeLine> = Vec::new();
    for line in lines {
        keyframes.push(serde_json::from_str(&line?)?);
    }
    // The record keeps the order ffmpeg delivered the keyframes in, which
    // is not always the order of their times: with only keyframes decoded,
    // a keyframe that opens a group of pictures can arrive before the one
    // shown just ahead of it. Every reader wants time order.
    keyframes.sort_by(|a, b| a.time.total_cmp(&b.time));
    Ok(Record { header, keyframes })
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

#[cfg(test)]
mod tests {
    use super::*;

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
        let line = KeyframeLine {
            time: 4.5,
            faces: vec![Detection {
                face,
                embedding: encode(&[0.5; 128]),
            }],
        };
        let mut out = Vec::new();
        write_line(&mut out, &header()).unwrap();
        write_line(&mut out, &line).unwrap();
        let record = read(out.as_slice()).unwrap();
        assert_eq!((record.header, record.keyframes), (header(), vec![line]));
    }

    #[test]
    fn a_record_reads_its_keyframes_in_time_order() {
        let mut out = Vec::new();
        write_line(&mut out, &header()).unwrap();
        for time in [9.0, 4.5, 12.0] {
            write_line(
                &mut out,
                &KeyframeLine {
                    time,
                    faces: vec![],
                },
            )
            .unwrap();
        }
        let times: Vec<f64> = read(out.as_slice())
            .unwrap()
            .keyframes
            .iter()
            .map(|k| k.time)
            .collect();
        assert_eq!(times, [4.5, 9.0, 12.0]);
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
