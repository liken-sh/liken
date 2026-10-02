// The review command, a development tool. It reads a title's detections
// records and its gallery, names every face as match does, at a threshold
// and a margin the reviewer chooses, and writes three files outside the library: the spans as YAML,
// the spans as an mpv chapters file, and every face with its label for the
// overlay script. With --play it opens mpv on the video with the chapters
// and the overlay loaded.
//
// It runs no model, so a new threshold or margin takes a second, and nothing it
// writes touches the title's folder.

use std::collections::{BTreeMap, BTreeSet};
use std::fs::{self, File};
use std::io::BufReader;
use std::path::{Path, PathBuf};
use std::process::Command;

use serde::Serialize;

use crate::gallery::Gallery;
use crate::matcher::{self, Candidates, Rule};
use crate::matches::{self, By};
use crate::record::Record;
use crate::runtime::Error;
use crate::sheets;
use crate::spans::{self, Span};

pub const SCRIPT: &str = include_str!("../dev/appearances.lua");

#[derive(Serialize)]
struct FaceBox {
    x: f32,
    y: f32,
    w: f32,
    h: f32,
    // The closest person, or empty in a gallery with no headshots.
    person: String,
    similarity: f32,
    // The next closest person's similarity, or null in a gallery of one.
    runner_up: Option<f32>,
    named: bool,
    // The pass that named the face, or null.
    by: Option<By>,
}

#[derive(Serialize)]
struct Sample {
    time: f64,
    faces: Vec<FaceBox>,
}

#[derive(Serialize)]
struct Overlay {
    video: String,
    width: usize,
    height: usize,
    threshold: f32,
    margin: f32,
    samples: Vec<Sample>,
    spans: Vec<Span>,
}

fn round(value: f32) -> f32 {
    (value * 1000.0).round() / 1000.0
}

fn overlay(record: &Record, gallery: &Gallery, rule: Rule) -> Result<Overlay, Error> {
    let faces = matcher::faces(record)?;
    let closest = matcher::closest(&faces, &Candidates::new(gallery)?);
    let names = matches::name(&faces, gallery, rule)?;
    let mut samples = Vec::new();
    let mut shots = Vec::new();
    for ((line, closest), names) in record.samples.iter().zip(&closest).zip(&names) {
        let mut boxes = Vec::new();
        let mut people = BTreeSet::new();
        for ((detection, closest), named) in line.faces.iter().zip(closest).zip(names) {
            // A named face shows its person and the similarity to that
            // person's headshot. An unnamed face shows the closest person,
            // so a near miss is visible.
            let best = named.map(|n| n.best).or(*closest);
            if let Some(named) = named {
                people.insert(gallery.people[named.best.person].name.clone());
            }
            let f = &detection.face;
            boxes.push(FaceBox {
                x: f.x,
                y: f.y,
                w: f.w,
                h: f.h,
                person: best.map_or(String::new(), |b| gallery.people[b.person].name.clone()),
                similarity: round(best.map_or(0.0, |b| b.similarity)),
                runner_up: best.and_then(|b| b.runner_up).map(round),
                named: named.is_some(),
                by: named.map(|n| n.by),
            });
        }
        samples.push(Sample {
            time: line.time,
            faces: boxes,
        });
        shots.push((line.time, people.into_iter().collect()));
    }
    Ok(Overlay {
        video: record.header.video.clone(),
        width: record.header.width,
        height: record.header.height,
        threshold: rule.threshold,
        margin: rule.margin,
        samples,
        spans: spans::spans(&shots, record.header.duration),
    })
}

pub struct Settings {
    pub rule: Rule,
    pub out: Option<PathBuf>,
    pub play: bool,
    // How many faces each contact sheet shows, or None for no sheets.
    pub sheets: Option<usize>,
}

// The faces too small to name are not a loss worth a tile: in the
// experiments, 1% of faces under 40 pixels tall were named.
const UNNAMED_MINIMUM_HEIGHT: f32 = 80.0;

// The tiles for the contact sheets: each person's named faces with the
// weakest first, the tallest faces nobody was named for, and the faces the
// headshots named whose person leads the next closest person by the least,
// which is where the margin decides. Each person also gets a sheet of the
// faces that the film pass named, weakest first, because a wrong name of
// that pass shows there.
fn sheet_groups(overlay: &Overlay, per_sheet: usize) -> BTreeMap<String, Vec<sheets::Tile>> {
    let mut groups: BTreeMap<String, Vec<sheets::Tile>> = BTreeMap::new();
    for sample in &overlay.samples {
        for face in &sample.faces {
            let tile = sheets::Tile {
                time: sample.time,
                x: face.x,
                y: face.y,
                w: face.w,
                h: face.h,
                similarity: face.similarity,
                runner_up: face.runner_up,
                person: face.person.clone(),
            };
            match face.by {
                Some(By::Headshot) => {
                    groups.entry(CLOSE.into()).or_default().push(tile.clone());
                    groups.entry(face.person.clone()).or_default().push(tile);
                }
                Some(By::Film) => {
                    groups
                        .entry(format!("{FILM}{}", face.person))
                        .or_default()
                        .push(tile.clone());
                    groups.entry(face.person.clone()).or_default().push(tile);
                }
                None if face.h >= UNNAMED_MINIMUM_HEIGHT => {
                    groups.entry(UNNAMED.into()).or_default().push(tile);
                }
                None => {}
            }
        }
    }
    for (name, tiles) in groups.iter_mut() {
        if name == UNNAMED {
            tiles.sort_by(|a, b| b.h.total_cmp(&a.h));
        } else if name == CLOSE {
            let lead = |t: &sheets::Tile| t.runner_up.map_or(f32::MAX, |r| t.similarity - r);
            tiles.sort_by(|a, b| lead(a).total_cmp(&lead(b)));
        } else {
            tiles.sort_by(|a, b| a.similarity.total_cmp(&b.similarity));
        }
        tiles.truncate(per_sheet);
    }
    groups
}

const UNNAMED: &str = "_unnamed";
const FILM: &str = "_film ";
const CLOSE: &str = "_closest leads";

pub fn run(title: &Path, settings: &Settings) -> Result<(), Error> {
    let gallery_path = matches::gallery_path(title);
    let gallery: Gallery = serde_json::from_reader(BufReader::new(
        File::open(&gallery_path)
            .map_err(|e| format!("{}: {e} (run match first)", gallery_path.display()))?,
    ))?;
    let name = title
        .file_name()
        .unwrap_or_default()
        .to_string_lossy()
        .to_string();
    let out = settings
        .out
        .clone()
        .unwrap_or_else(|| std::env::temp_dir().join("appearances-review").join(&name));
    fs::create_dir_all(&out)?;
    let script = out.join("appearances.lua");
    fs::write(&script, SCRIPT)?;
    for record in matches::records(title)? {
        let overlay = overlay(&record, &gallery, settings.rule)?;
        let stem = out.join(&overlay.video);
        let chapters = stem.with_extension("chapters.txt");
        let data = stem.with_extension("review.json");
        fs::write(&chapters, spans::chapters(&overlay.spans))?;
        fs::write(
            stem.with_extension("spans.yaml"),
            serde_yaml_ng::to_string(&overlay.spans)?,
        )?;
        fs::write(&data, serde_json::to_vec(&overlay)?)?;
        let faces = || overlay.samples.iter().flat_map(|k| &k.faces);
        let named = faces().filter(|f| f.named).count();
        let film = faces().filter(|f| f.by == Some(By::Film)).count();
        let rule = settings.rule;
        eprintln!(
            "{}: {} of {} faces named at threshold {} and margin {}, {} of them by the film pass, {} spans, in {}",
            overlay.video,
            named,
            faces().count(),
            rule.threshold,
            rule.margin,
            film,
            overlay.spans.len(),
            out.display()
        );
        if let Some(per_sheet) = settings.sheets {
            let directory = stem.with_extension("sheets");
            // The sheets of an earlier review name people at another rule,
            // and a sheet left from it would read as part of this one.
            if directory.exists() {
                fs::remove_dir_all(&directory)?;
            }
            let groups = sheet_groups(&overlay, per_sheet);
            sheets::write(
                &title.join(&overlay.video),
                overlay.width,
                overlay.height,
                &groups,
                &directory,
            )?;
            eprintln!("  contact sheets in {}", directory.display());
        }
        if settings.play {
            // The data path goes through the environment, because mpv's
            // --script-opts splits its value on commas, and the names of
            // library files carry them.
            let status = Command::new("mpv")
                .arg(format!("--script={}", script.display()))
                .arg(format!("--chapters-file={}", chapters.display()))
                .env("APPEARANCES_REVIEW", &data)
                .arg(title.join(&overlay.video))
                .status()?;
            if !status.success() {
                return Err(format!("mpv exited with {status}").into());
            }
        }
    }
    Ok(())
}
