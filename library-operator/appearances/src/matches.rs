// The match command. It embeds the cast's headshots into the gallery,
// writes the gallery to .liken/appearances/gallery.json, reads every
// detections record in the title's .liken/appearances/, and prints one JSON
// document for the title on standard output. The operator reads that
// document and owns the ledger, .liken/appearances.yaml, with its attempts,
// as it owns every other ledger in .liken/.
//
// The document names each face at its keyframe's time with the person, the
// similarity, and the next person's similarity, so a reader can raise the
// threshold or the margin without running the match again. It holds
// observations at keyframes, not spans. The match also writes each video's
// spans for the liken display (player.rs), which draws a span from one
// keyframe to the next.

use std::collections::{BTreeMap, HashMap};
use std::fs::{self, File};
use std::io::BufReader;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

use crate::film_gallery::{self, Naming};
use crate::gallery::{self, Gallery, HEADSHOT_SIDE, Headshot};
use crate::matcher;
use crate::models;
use crate::player;
use crate::record::{self, Model, Record};
use crate::runtime::{Detector, Embedder, Engine, Error};

pub const FORMAT: &str = "liken.sh/appearances/matches/v1";

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Matches {
    pub format: String,
    pub embedder: Model,
    pub threshold: f32,
    pub margin: f32,
    // True when the film's own faces joined the gallery for a second pass.
    pub self_gallery: bool,
    // Every credited actor the match compared faces with, in billing order.
    pub gallery: Vec<GalleryInput>,
    // The people who cannot match, so a gap in the answer is visible: an
    // entry with no headshot, or a headshot with no face in it.
    pub unmatched: Vec<Unmatched>,
    // Each video file's observations, by the file's name in the title's
    // folder.
    pub files: BTreeMap<String, FileMatches>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct GalleryInput {
    pub contributor: String,
    pub name: String,
    pub headshot: Headshot,
    // The SHA-256 of the headshot file, absent when the entry has none.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub sha256: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Unmatched {
    pub contributor: String,
    pub name: String,
    pub headshot: Headshot,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct FileMatches {
    // The size in bytes of the file the detections record came from.
    pub size: u64,
    pub observations: Vec<Observation>,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Observation {
    pub time: f64,
    // The face's place in its keyframe's line of the detections record.
    pub face: usize,
    pub contributor: String,
    pub similarity: f32,
    // The next closest person's similarity, absent in a gallery of one.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub runner_up: Option<f32>,
}

fn round(value: f32) -> f32 {
    (value * 1000.0).round() / 1000.0
}

pub fn observe(
    record: &Record,
    gallery: &Gallery,
    naming: Naming,
) -> Result<Vec<Observation>, Error> {
    let faces = matcher::faces(record)?;
    let passes = film_gallery::name(&faces, gallery, naming)?;
    let mut observations = Vec::new();
    for (keyframe, line) in record.keyframes.iter().zip(passes.last()) {
        for (index, best) in line.iter().enumerate() {
            let Some(best) = best.filter(|b| b.names(naming.rule)) else {
                continue;
            };
            observations.push(Observation {
                time: keyframe.time,
                face: index,
                contributor: gallery.people[best.person].contributor.clone(),
                similarity: round(best.similarity),
                runner_up: best.runner_up.map(round),
            });
        }
    }
    Ok(observations)
}

pub fn gallery_path(title: &Path) -> PathBuf {
    title
        .join(".liken")
        .join("appearances")
        .join("gallery.json")
}

pub fn records(title: &Path) -> Result<Vec<Record>, Error> {
    let directory = title.join(".liken").join("appearances");
    let mut paths: Vec<_> = fs::read_dir(&directory)
        .map_err(|e| format!("{}: {e}", directory.display()))?
        .filter_map(Result::ok)
        .map(|entry| entry.path())
        .filter(|path| path.extension().is_some_and(|e| e == "jsonl"))
        .collect();
    paths.sort();
    paths
        .iter()
        .map(|path| {
            record::read(BufReader::new(File::open(path)?))
                .map_err(|e| format!("{}: {e}", path.display()).into())
        })
        .collect()
}

// The document for a gallery and its title's records.
pub fn matches(gallery: &Gallery, records: &[Record], naming: Naming) -> Result<Matches, Error> {
    let mut files = BTreeMap::new();
    for record in records {
        // Vectors from two embedding models cannot be compared, so a record
        // from another embedder is stale and the document leaves its file
        // out. A season folder holds one record per episode, and one stale
        // record must not keep the others from a match.
        if record.header.embedder != gallery.embedder {
            eprintln!(
                "{}: left out, because it was embedded by {} {}, and the gallery by {} {}",
                record.header.video,
                record.header.embedder.name,
                record.header.embedder.sha256,
                gallery.embedder.name,
                gallery.embedder.sha256
            );
            continue;
        }
        let observations = observe(record, gallery, naming)?;
        eprintln!(
            "{}: {} faces named",
            record.header.video,
            observations.len()
        );
        files.insert(
            record.header.video.clone(),
            FileMatches {
                size: record.header.size,
                observations,
            },
        );
    }
    Ok(Matches {
        format: FORMAT.into(),
        embedder: gallery.embedder.clone(),
        threshold: naming.rule.threshold,
        margin: naming.rule.margin,
        self_gallery: naming.self_gallery,
        gallery: gallery
            .people
            .iter()
            .map(|p| GalleryInput {
                contributor: p.contributor.clone(),
                name: p.name.clone(),
                headshot: p.headshot,
                sha256: p.headshot_sha256.clone(),
            })
            .collect(),
        unmatched: gallery
            .people
            .iter()
            .filter(|p| p.headshot != Headshot::Found)
            .map(|p| Unmatched {
                contributor: p.contributor.clone(),
                name: p.name.clone(),
                headshot: p.headshot,
            })
            .collect(),
        files,
    })
}

// The match of one title folder. credits names the credits file to read the
// cast from, and None reads the folder's own.
pub fn run(
    title: &Path,
    credits: Option<&Path>,
    models_dir: &Path,
    device: &str,
    threads: usize,
    naming: Naming,
) -> Result<Matches, Error> {
    let root =
        gallery::library_root(title).ok_or("no .contributors/ folder at or above the title")?;
    let actors = match credits {
        Some(path) => gallery::read_credits(path)?,
        None => gallery::read_credits(&gallery::credits_path(title))?,
    };
    let detector_file = models::find(models_dir, models::DETECTOR)?;
    let embedder_file = models::find(models_dir, models::EMBEDDER)?;
    let mut engine = Engine::new(device, threads, None)?;
    let mut detector = Detector::new(
        &mut engine,
        &detector_file.path,
        HEADSHOT_SIDE,
        HEADSHOT_SIDE,
    )?;
    let mut embedder = Embedder::new(&mut engine, &embedder_file.path)?;
    let gallery = gallery::build(
        &root,
        &actors,
        &embedder_file.model,
        &mut detector,
        &mut embedder,
    )?;
    fs::create_dir_all(gallery_path(title).parent().unwrap())?;
    fs::write(gallery_path(title), serde_json::to_vec_pretty(&gallery)?)?;
    let records = records(title)?;
    let found = matches(&gallery, &records, naming)?;
    write_spans(title, &root, &gallery, &records, &found)?;
    Ok(found)
}

// The player's file of each video the document names. It is written whole
// on every match, so it always follows the latest gallery and the latest
// rule, and a file that names nobody still replaces an older one.
fn write_spans(
    title: &Path,
    root: &Path,
    gallery: &Gallery,
    records: &[Record],
    found: &Matches,
) -> Result<(), Error> {
    let entries: HashMap<String, player::Entry> = gallery
        .people
        .iter()
        .map(|p| (p.contributor.clone(), player::entry(root, &p.contributor)))
        .collect();
    for record in records {
        let Some(file) = found.files.get(&record.header.video) else {
            continue;
        };
        let spans = player::spans(
            record,
            &file.observations,
            &gallery.people,
            &entries,
            player::released(title, &record.header.video),
        );
        let path = player::path(title, &record.header.video);
        let partial = record::partial(&path);
        fs::write(&partial, serde_json::to_vec(&spans)?)?;
        fs::rename(&partial, &path)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::gallery::Person;
    use crate::matcher::Rule;
    use crate::record::{Detection, Header, KeyframeLine, encode};
    use crate::yunet::Face;

    fn model() -> Model {
        Model {
            name: "m".into(),
            sha256: "00".into(),
        }
    }

    fn detection(vector: &[f32]) -> Detection {
        Detection {
            face: Face {
                x: 0.0,
                y: 0.0,
                w: 10.0,
                h: 10.0,
                score: 0.95,
                landmarks: [(0.0, 0.0); 5],
            },
            embedding: encode(vector),
        }
    }

    // Three faces: one close to East alone, one as close to North as to
    // East, and one close to nobody.
    fn record() -> Record {
        Record {
            header: Header {
                format: record::FORMAT.into(),
                video: "film.mkv".into(),
                size: 1234,
                duration: 20.0,
                width: 100,
                height: 100,
                detector: model(),
                detect_width: 100,
                embedder: model(),
                device: "CPU".into(),
            },
            keyframes: vec![
                KeyframeLine {
                    time: 0.0,
                    faces: vec![],
                },
                KeyframeLine {
                    time: 4.5,
                    faces: vec![
                        detection(&[0.0, 0.0, 1.0]),
                        detection(&[1.0, 0.0, 0.0]),
                        detection(&[0.707, 0.707, 0.0]),
                    ],
                },
            ],
        }
    }

    fn person(name: &str, headshot: Headshot, embedding: Option<&[f32]>) -> Person {
        Person {
            contributor: format!(".contributors/{name}"),
            name: name.into(),
            role: String::new(),
            headshot,
            headshot_sha256: embedding.map(|_| "ab".into()),
            embedding: embedding.map(encode),
        }
    }

    fn gallery() -> Gallery {
        Gallery {
            embedder: model(),
            people: vec![
                person("east", Headshot::Found, Some(&[1.0, 0.0, 0.0])),
                person("north", Headshot::Found, Some(&[0.0, 1.0, 0.0])),
                person("missing", Headshot::Missing, None),
            ],
        }
    }

    const NAMING: Naming = Naming {
        rule: Rule {
            threshold: matcher::THRESHOLD,
            margin: 0.05,
        },
        self_gallery: false,
    };

    #[test]
    fn observe_names_the_faces_over_the_threshold_and_the_margin() {
        let observations = observe(&record(), &gallery(), NAMING).unwrap();
        assert_eq!(
            observations,
            vec![Observation {
                time: 4.5,
                face: 1,
                contributor: ".contributors/east".into(),
                similarity: 1.0,
                runner_up: Some(0.0),
            }]
        );
    }

    #[test]
    fn matches_carry_each_files_size_and_the_gallery_inputs() {
        let matches = matches(&gallery(), &[record()], NAMING).unwrap();
        assert_eq!(matches.files["film.mkv"].size, 1234);
        assert_eq!(matches.gallery[0].sha256.as_deref(), Some("ab"));
        assert_eq!(
            matches.unmatched,
            [Unmatched {
                contributor: ".contributors/missing".into(),
                name: "missing".into(),
                headshot: Headshot::Missing,
            }]
        );
    }

    #[test]
    fn matches_leave_out_a_record_from_another_embedder() {
        let mut other = record();
        other.header.video = "other.mkv".into();
        other.header.embedder.sha256 = "ff".into();
        let matches = matches(&gallery(), &[record(), other], NAMING).unwrap();
        assert_eq!(matches.files.keys().collect::<Vec<_>>(), ["film.mkv"]);
    }
}
