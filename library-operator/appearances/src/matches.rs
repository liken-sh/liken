// The match command. It embeds the cast's headshots into the gallery,
// writes the gallery to .liken/appearances/gallery.json, reads every
// detections record in the title's .liken/appearances/, and names the
// faces in two passes: from the headshots (matcher.rs), then from the faces
// the headshots named (knn_guard.rs).
//
// For each video it writes two files beside the detections record. The
// matches file, <video>.matches.json, names each face at its sample's time
// with the person, the pass that named it, the similarity to the person's
// headshot, and the next person's similarity, so a reader can raise the
// threshold or the margin for the headshot names without running the match
// again. It holds observations at samples, not spans. The spans file is the
// liken display's (player.rs). Then the match prints a summary for the
// title on standard output (summary.rs), which the operator writes into the
// ledger, .liken/appearances.yaml, with its attempts, as it owns every other
// ledger in .liken/.
//
// The matches file is one JSON document, as the spans file is, and not JSON
// Lines: the title's .liken/appearances/ holds the detections records as
// .jsonl files, and `records` reads every .jsonl file there as one. A film's
// matches file is about 100 bytes per face named, under a megabyte.

use std::collections::HashMap;
use std::fs::{self, File};
use std::io::BufReader;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

use crate::gallery::{self, Gallery, HEADSHOT_SIDE, Headshot};
use crate::knn_guard;
use crate::matcher::{self, Best, Candidates, Faces, Rule};
use crate::models;
use crate::player;
use crate::record::{self, Model, Record};
use crate::runtime::{Detector, Embedder, Engine, Error};
use crate::summary::{self, Summary};

pub const FORMAT: &str = "liken.sh/appearances/matches/v2";

// What every answer of a title depends on, besides the video: the
// embedder, the rule, and the gallery. The matches file and the summary
// both carry it.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Inputs {
    pub embedder: Model,
    pub threshold: f32,
    pub margin: f32,
    // Every credited actor the match compared faces with, in billing order.
    pub gallery: Vec<GalleryInput>,
    // The people who cannot match, so a gap in the answer is visible: an
    // entry with no headshot, or a headshot with no face in it.
    pub unmatched: Vec<Unmatched>,
}

// One video's matches file.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Matches {
    pub format: String,
    // The video file's name in the title's folder.
    pub video: String,
    // The size in bytes of the file the detections record came from.
    pub size: u64,
    #[serde(flatten)]
    pub inputs: Inputs,
    pub observations: Vec<Observation>,
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
pub struct Observation {
    pub time: f64,
    // The face's place in its sample's line of the detections record.
    pub face: usize,
    pub contributor: String,
    pub by: By,
    // The similarity to this person's headshot. A face named by the film
    // pass can be under the threshold, and its runner-up can be higher.
    pub similarity: f32,
    // The next closest person's similarity, absent in a gallery of one.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub runner_up: Option<f32>,
}

// The pass that named a face.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum By {
    Headshot,
    Film,
}

// A face's name: the person, the pass, and the similarities.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Named {
    pub best: Best,
    pub by: By,
}

// Every face's name from both passes, in the shape of `Faces`.
pub fn name(
    faces: &Faces,
    gallery: &Gallery,
    rule: Rule,
) -> Result<Vec<Vec<Option<Named>>>, Error> {
    let candidates = Candidates::new(gallery)?;
    let closest = matcher::closest(faces, &candidates);
    let headshots: knn_guard::Persons = closest
        .iter()
        .map(|line| {
            line.iter()
                .map(|best| best.filter(|b| b.names(rule)).map(|b| b.person))
                .collect()
        })
        .collect();
    let film = knn_guard::names(faces, &headshots)?;
    let mut names = Vec::with_capacity(faces.len());
    for ((vectors, closest), (headshot, film)) in
        faces.iter().zip(&closest).zip(headshots.iter().zip(&film))
    {
        let mut line = Vec::with_capacity(vectors.len());
        for (index, vector) in vectors.iter().enumerate() {
            line.push(match (headshot[index], film[index]) {
                (Some(_), _) => closest[index].map(|best| Named {
                    best,
                    by: By::Headshot,
                }),
                (None, Some(person)) => candidates
                    .against(vector, person)
                    .map(|best| Named { best, by: By::Film }),
                (None, None) => None,
            });
        }
        names.push(line);
    }
    Ok(names)
}

fn round(value: f32) -> f32 {
    (value * 1000.0).round() / 1000.0
}

pub fn observe(record: &Record, gallery: &Gallery, rule: Rule) -> Result<Vec<Observation>, Error> {
    let names = name(&matcher::faces(record)?, gallery, rule)?;
    let mut observations = Vec::new();
    for (sample, line) in record.samples.iter().zip(names) {
        for (index, named) in line.into_iter().enumerate() {
            let Some(Named { best, by }) = named else {
                continue;
            };
            observations.push(Observation {
                time: sample.time,
                face: index,
                contributor: gallery.people[best.person].contributor.clone(),
                by,
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

// The inputs of a match.
pub fn inputs(gallery: &Gallery, rule: Rule) -> Inputs {
    Inputs {
        embedder: gallery.embedder.clone(),
        threshold: rule.threshold,
        margin: rule.margin,
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
    }
}

// The matches file of each record the gallery can be compared with.
pub fn matches(gallery: &Gallery, records: &[Record], rule: Rule) -> Result<Vec<Matches>, Error> {
    let inputs = inputs(gallery, rule);
    let mut files = Vec::new();
    for record in records {
        // Vectors from two embedding models cannot be compared, so a record
        // from another embedder is stale and the match leaves its file out.
        // A season folder holds one record per episode, and one stale
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
        let observations = observe(record, gallery, rule)?;
        let film = observations.iter().filter(|o| o.by == By::Film).count();
        eprintln!(
            "{}: {} faces named, {} of them by the film pass",
            record.header.video,
            observations.len(),
            film
        );
        files.push(Matches {
            format: FORMAT.into(),
            video: record.header.video.clone(),
            size: record.header.size,
            inputs: inputs.clone(),
            observations,
        });
    }
    Ok(files)
}

pub fn path(title: &Path, video: &str) -> PathBuf {
    title
        .join(".liken")
        .join("appearances")
        .join(format!("{video}.matches.json"))
}

// The match of one title folder. credits names the credits file to read the
// cast from, and None reads the folder's own.
pub fn run(
    title: &Path,
    credits: Option<&Path>,
    models_dir: &Path,
    device: &str,
    threads: usize,
    rule: Rule,
) -> Result<Summary, Error> {
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
    let found = matches(&gallery, &records, rule)?;
    write_files(title, &root, &gallery, &records, &found)?;
    Ok(summary::summary(&inputs(&gallery, rule), &found))
}

// The matches file and the player's file of each video. Each is written
// whole on every match, so it always follows the latest gallery and the
// latest rule, and a file that names nobody still replaces an older one.
// Each is written beside its final name and renamed into place, so a
// reader never sees half a file.
fn write_files(
    title: &Path,
    root: &Path,
    gallery: &Gallery,
    records: &[Record],
    found: &[Matches],
) -> Result<(), Error> {
    let entries: HashMap<String, player::Entry> = gallery
        .people
        .iter()
        .map(|p| (p.contributor.clone(), player::entry(root, &p.contributor)))
        .collect();
    for record in records {
        let Some(file) = found.iter().find(|f| f.video == record.header.video) else {
            continue;
        };
        write_whole(&path(title, &file.video), &serde_json::to_vec(file)?)?;
        let spans = player::spans(
            record,
            &file.observations,
            &gallery.people,
            &entries,
            player::released(title, &record.header.video),
        );
        write_whole(
            &player::path(title, &record.header.video),
            &serde_json::to_vec(&spans)?,
        )?;
    }
    Ok(())
}

fn write_whole(path: &Path, bytes: &[u8]) -> Result<(), Error> {
    let partial = record::partial(path);
    fs::write(&partial, bytes)?;
    fs::rename(&partial, path)?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::gallery::Person;
    use crate::record::{Detection, Header, SampleLine, encode};
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

    fn sample(time: f64, vectors: &[&[f32]]) -> SampleLine {
        SampleLine {
            time,
            keyframe: false,
            faces: vectors.iter().map(|v| detection(v)).collect(),
        }
    }

    fn record_of(samples: Vec<SampleLine>) -> Record {
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
            samples,
        }
    }

    // Three faces: one close to nobody, one close to East alone, and one as
    // close to North as to East.
    fn record() -> Record {
        record_of(vec![
            sample(0.0, &[]),
            sample(
                4.5,
                &[&[0.0, 0.0, 1.0], &[1.0, 0.0, 0.0], &[0.707, 0.707, 0.0]],
            ),
        ])
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

    const RULE: Rule = Rule {
        threshold: matcher::THRESHOLD,
        margin: 0.05,
    };

    #[test]
    fn observe_names_the_faces_over_the_threshold_and_the_margin() {
        let observations = observe(&record(), &gallery(), RULE).unwrap();
        assert_eq!(
            observations,
            vec![Observation {
                time: 4.5,
                face: 1,
                contributor: ".contributors/east".into(),
                by: By::Headshot,
                similarity: 1.0,
                runner_up: Some(0.0),
            }]
        );
    }

    // East's headshot names two faces at 0.6. A third face is at 0.0 to
    // the headshot and at 0.8 to both of them, so the film pass names it.
    #[test]
    fn observe_names_a_face_by_the_film_pass_with_its_headshot_similarity() {
        let record = record_of(vec![
            sample(1.0, &[&[0.6, 0.0, 0.8]]),
            sample(2.0, &[&[0.6, 0.0, 0.8]]),
            sample(3.0, &[&[0.0, 0.0, 1.0]]),
        ]);
        let observations = observe(&record, &gallery(), RULE).unwrap();
        assert_eq!(
            observations[2],
            Observation {
                time: 3.0,
                face: 0,
                contributor: ".contributors/east".into(),
                by: By::Film,
                similarity: 0.0,
                runner_up: Some(0.0),
            }
        );
    }

    #[test]
    fn an_observation_names_its_pass_in_lowercase() {
        let observation = Observation {
            time: 3.0,
            face: 0,
            contributor: ".contributors/east".into(),
            by: By::Film,
            similarity: 0.25,
            runner_up: None,
        };
        assert_eq!(
            serde_json::to_string(&observation).unwrap(),
            r#"{"time":3.0,"face":0,"contributor":".contributors/east","by":"film","similarity":0.25}"#
        );
    }

    #[test]
    fn matches_carry_each_files_size_and_the_gallery_inputs() {
        let matches = matches(&gallery(), &[record()], RULE).unwrap();
        assert_eq!(
            (matches[0].format.as_str(), matches[0].size),
            (FORMAT, 1234)
        );
        assert_eq!(matches[0].inputs.gallery[0].sha256.as_deref(), Some("ab"));
        assert_eq!(
            matches[0].inputs.unmatched,
            [Unmatched {
                contributor: ".contributors/missing".into(),
                name: "missing".into(),
                headshot: Headshot::Missing,
            }]
        );
    }

    #[test]
    fn the_matches_file_goes_beside_the_spans_file() {
        assert_eq!(
            path(Path::new("/m/Film"), "Film.mkv"),
            Path::new("/m/Film/.liken/appearances/Film.mkv.matches.json")
        );
    }

    #[test]
    fn matches_leave_out_a_record_from_another_embedder() {
        let mut other = record();
        other.header.video = "other.mkv".into();
        other.header.embedder.sha256 = "ff".into();
        let matches = matches(&gallery(), &[record(), other], RULE).unwrap();
        let videos: Vec<&str> = matches.iter().map(|m| m.video.as_str()).collect();
        assert_eq!(videos, ["film.mkv"]);
    }
}
