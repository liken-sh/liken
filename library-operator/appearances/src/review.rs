// The review command, a development tool. It reads a title's detections
// records and its gallery, names every face at a threshold and a margin the
// reviewer chooses, and writes three files outside the library: the spans as YAML,
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

use crate::film_gallery::{self, Naming};
use crate::gallery::Gallery;
use crate::matcher::{self, Best};
use crate::matches;
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
    // The person the headshots alone named, or null. With the film's own
    // gallery off, this is the person when `named` is true.
    headshots_named: Option<String>,
}

#[derive(Serialize)]
struct Keyframe {
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
    self_gallery: bool,
    keyframes: Vec<Keyframe>,
    spans: Vec<Span>,
}

fn round(value: f32) -> f32 {
    (value * 1000.0).round() / 1000.0
}

fn overlay(record: &Record, gallery: &Gallery, naming: Naming) -> Result<Overlay, Error> {
    let faces = matcher::faces(record)?;
    let passes = film_gallery::name(&faces, gallery, naming)?;
    let name_of = |best: &Option<Best>| {
        best.filter(|b| b.names(naming.rule))
            .map(|b| gallery.people[b.person].name.clone())
    };
    let mut keyframes = Vec::new();
    let mut shots = Vec::new();
    for ((line, last), headshots) in record
        .keyframes
        .iter()
        .zip(passes.last())
        .zip(&passes.headshots)
    {
        let mut boxes = Vec::new();
        let mut people = BTreeSet::new();
        for ((detection, best), before) in line.faces.iter().zip(last).zip(headshots) {
            let named = name_of(best);
            if let Some(person) = &named {
                people.insert(person.clone());
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
                headshots_named: name_of(before),
            });
        }
        keyframes.push(Keyframe {
            time: line.time,
            faces: boxes,
        });
        shots.push((line.time, people.into_iter().collect()));
    }
    Ok(Overlay {
        video: record.header.video.clone(),
        width: record.header.width,
        height: record.header.height,
        threshold: naming.rule.threshold,
        margin: naming.rule.margin,
        self_gallery: naming.self_gallery,
        keyframes,
        spans: spans::spans(&shots, record.header.duration),
    })
}

pub struct Settings {
    pub naming: Naming,
    pub out: Option<PathBuf>,
    pub play: bool,
    // How many faces each contact sheet shows, or None for no sheets.
    pub sheets: Option<usize>,
}

// The faces too small to name are not a loss worth a tile: in the
// experiments, 1% of faces under 40 pixels tall were named.
const UNNAMED_MINIMUM_HEIGHT: f32 = 80.0;

// The tiles for the contact sheets: each person's named faces with the
// weakest first, the tallest faces nobody was named for, and the named
// faces whose person leads the next closest person by the least, which is
// where the margin decides. With the
// film's own gallery on, each person also gets a sheet of the faces that
// only the second pass named, weakest first, because those are the faces
// the experiment adds and the ones to judge it by.
fn sheet_groups(overlay: &Overlay, per_sheet: usize) -> BTreeMap<String, Vec<sheets::Tile>> {
    let mut groups: BTreeMap<String, Vec<sheets::Tile>> = BTreeMap::new();
    for keyframe in &overlay.keyframes {
        for face in &keyframe.faces {
            let tile = sheets::Tile {
                time: keyframe.time,
                x: face.x,
                y: face.y,
                w: face.w,
                h: face.h,
                similarity: face.similarity,
                runner_up: face.runner_up,
                person: face.person.clone(),
            };
            if face.named && overlay.self_gallery && face.headshots_named.is_none() {
                groups
                    .entry(format!("{NEW}{}", face.person))
                    .or_default()
                    .push(tile.clone());
            }
            if face.named {
                groups.entry(CLOSE.into()).or_default().push(tile.clone());
                groups.entry(face.person.clone()).or_default().push(tile);
            } else if face.h >= UNNAMED_MINIMUM_HEIGHT {
                groups.entry(UNNAMED.into()).or_default().push(tile);
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
const NEW: &str = "_new ";
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
        let overlay = overlay(&record, &gallery, settings.naming)?;
        let stem = out.join(&overlay.video);
        let chapters = stem.with_extension("chapters.txt");
        let data = stem.with_extension("review.json");
        fs::write(&chapters, spans::chapters(&overlay.spans))?;
        fs::write(
            stem.with_extension("spans.yaml"),
            serde_yaml_ng::to_string(&overlay.spans)?,
        )?;
        fs::write(&data, serde_json::to_vec(&overlay)?)?;
        let named = overlay
            .keyframes
            .iter()
            .flat_map(|k| &k.faces)
            .filter(|f| f.named)
            .count();
        let faces: usize = overlay.keyframes.iter().map(|k| k.faces.len()).sum();
        let rule = settings.naming.rule;
        eprintln!(
            "{}: {} of {} faces named at threshold {} and margin {}, {} spans, in {}",
            overlay.video,
            named,
            faces,
            rule.threshold,
            rule.margin,
            overlay.spans.len(),
            out.display()
        );
        if overlay.self_gallery {
            let before = overlay
                .keyframes
                .iter()
                .flat_map(|k| &k.faces)
                .filter(|f| f.headshots_named.is_some())
                .count();
            eprintln!("  the headshots alone named {before}");
        }
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
