// The cheap pass: the second the end titles of a video start, from its
// reading and the model. It decodes nothing, so it runs again over every
// reading whenever the model changes.
//
// The videos of one folder are placed together, because the episodes of a
// season mostly have end titles of one length. Each video is placed alone
// first. Then each is placed again with the season's usual length as a
// prior, taken from the other videos that were placed with confidence.

use std::collections::HashMap;
use std::path::{Path, PathBuf};

use serde::Serialize;

use crate::Error;
use crate::changepoint::{self, Placement, Prior};
use crate::model::Model;
use crate::reading;
use crate::signals;

// One video ready to place: the model's answer for each second of its
// reading.
pub struct Item {
    pub video: PathBuf,
    pub duration: f64,
    pub times: Vec<f64>,
    pub probabilities: Vec<f32>,
    // The second a database's mark gives for the start, when the video
    // has one.
    pub mark: Option<f64>,
}

impl Item {
    pub fn new(
        video: &Path,
        records: Option<&Path>,
        credits: Option<&Path>,
        model: &Model,
    ) -> Result<Item, Error> {
        let reading = reading::load(&reading::path(video, records))?;
        let names = signals::people(video, credits)?;
        let matrix = signals::matrix(&reading, &names);
        let probabilities: Vec<f32> = (0..matrix.times.len())
            .map(|index| model.forest.probability(matrix.row(index)))
            .collect();
        Ok(Item {
            video: video.to_path_buf(),
            duration: reading.header.duration,
            times: matrix.times,
            probabilities,
            mark: None,
        })
    }
}

// A season's usual length counts when at least this many other videos of
// the folder were placed with at least this confidence.
const NEIGHBORS: usize = 3;
const SURE: f64 = 0.8;

// The placement of each item, in the items' order. An item's mark is a
// prior on its start: the marks are within 3 seconds of the start for
// most videos and wrong by minutes for some, which is the shape of
// `Prior`, so a mark that the picture agrees with sets the second, and a
// mark that the picture contradicts costs the placement a fixed amount.
pub fn place(items: &[Item], temperature: f64, lag: f64) -> Vec<Placement> {
    let marked = |item: &Item| item.mark.map(|center| Prior { center });
    let alone: Vec<Placement> = items
        .iter()
        .map(|item| {
            let priors: Vec<Prior> = marked(item).into_iter().collect();
            changepoint::place(&item.times, &item.probabilities, temperature, lag, &priors)
        })
        .collect();
    let mut folders: HashMap<&Path, Vec<usize>> = HashMap::new();
    for (index, item) in items.iter().enumerate() {
        folders
            .entry(item.video.parent().unwrap_or(Path::new("")))
            .or_default()
            .push(index);
    }
    let mut placed = alone.clone();
    for members in folders.values() {
        for &index in members {
            let mut lengths: Vec<f64> = members
                .iter()
                .filter(|other| **other != index && alone[**other].confidence >= SURE)
                .filter_map(|other| {
                    alone[*other]
                        .start
                        .map(|start| items[*other].duration - start)
                })
                .collect();
            if lengths.len() < NEIGHBORS {
                continue;
            }
            lengths.sort_by(f64::total_cmp);
            let item = &items[index];
            let mut priors = vec![Prior {
                center: item.duration - lengths[lengths.len() / 2],
            }];
            priors.extend(marked(item));
            placed[index] =
                changepoint::place(&item.times, &item.probabilities, temperature, lag, &priors);
        }
    }
    placed
}

// What `place` prints for one video.
#[derive(Debug, PartialEq, Serialize)]
pub struct Answer {
    pub video: String,
    // The second the end titles start, or null when the reading holds
    // none.
    pub start: Option<f64>,
    pub confidence: f64,
    // The second the reading starts at. A start at this second means the
    // end titles began before the reading did.
    pub from: f64,
    pub duration: f64,
}

// `marks` holds the videos that a database has a mark for, with the
// second each mark gives.
pub fn run(
    videos: &[PathBuf],
    model: &Path,
    records: Option<&Path>,
    credits: Option<&Path>,
    marks: &HashMap<PathBuf, f64>,
) -> Result<Vec<Answer>, Error> {
    let model = Model::load(model)?;
    let mut items = videos
        .iter()
        .map(|video| Item::new(video, records, credits, &model))
        .collect::<Result<Vec<Item>, Error>>()?;
    for item in &mut items {
        item.mark = marks.get(&item.video).copied();
    }
    let placed = place(&items, model.temperature, model.lag);
    Ok(items
        .iter()
        .zip(placed)
        .map(|(item, placement)| Answer {
            video: item.video.to_string_lossy().into(),
            start: placement.start.map(|start| (start * 10.0).round() / 10.0),
            confidence: (placement.confidence * 1000.0).round() / 1000.0,
            from: item.times.first().copied().unwrap_or(item.duration),
            duration: item.duration,
        })
        .collect())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::tests::model;
    use crate::reading::Writer;
    use crate::reading::tests::{header, scratch, second};
    use crate::textboxes::TextBox;

    // An episode of 400 seconds whose answers say the titles start
    // `length` seconds before the end, or say nothing clear at all.
    fn episode(name: &str, length: usize, clear: bool) -> Item {
        let times: Vec<f64> = (100..400).map(|t| t as f64).collect();
        let probabilities = times
            .iter()
            .map(|t| match (clear, *t >= 400.0 - length as f64) {
                (true, true) => 0.95,
                (true, false) => 0.05,
                (false, _) => 0.5,
            })
            .collect();
        Item {
            video: PathBuf::from(format!("/series/Season 01/{name}.mkv")),
            duration: 400.0,
            times,
            probabilities,
            mark: None,
        }
    }

    #[test]
    fn an_unsure_episode_takes_the_length_of_its_season() {
        let items = [
            episode("e1", 60, true),
            episode("e2", 60, true),
            episode("e3", 62, true),
            episode("e4", 0, false),
        ];
        let placed = place(&items, 1.0, 0.0);
        assert_eq!(placed[0].start, Some(340.0));
        assert_eq!(placed[3].start, Some(340.0));
    }

    #[test]
    fn a_sure_episode_keeps_its_own_start() {
        let items = [
            episode("e1", 60, true),
            episode("e2", 60, true),
            episode("e3", 60, true),
            episode("e4", 120, true),
        ];
        assert_eq!(place(&items, 1.0, 0.0)[3].start, Some(280.0));
    }

    #[test]
    fn two_episodes_are_too_few_for_a_usual_length() {
        let items = [
            episode("e1", 60, true),
            episode("e2", 60, true),
            episode("e4", 0, false),
        ];
        let alone = changepoint::place(&items[2].times, &items[2].probabilities, 1.0, 0.0, &[]);
        assert_eq!(place(&items, 1.0, 0.0)[2], alone);
    }

    #[test]
    fn a_mark_sets_the_second_of_an_unsure_placement_only() {
        let mut items = [episode("e1", 0, false), episode("e2", 60, true)];
        items[0].mark = Some(300.0);
        items[1].mark = Some(200.0);
        let placed = place(&items, 1.0, 0.0);
        assert_eq!(placed[0].start, Some(300.0));
        assert_eq!(placed[1].start, Some(340.0));
    }

    #[test]
    fn run_places_a_video_from_its_reading_and_the_model() {
        let folder = scratch("place");
        let video = folder.join("film.mkv");
        let mut writer = Writer::create(&reading::path(&video, None), &header()).unwrap();
        for t in 40..100 {
            let mut row = second(t as f64);
            if t >= 70 {
                row.boxes = vec![TextBox(0.5, 0.5, 0.4, 0.05)];
            }
            writer.second(&row).unwrap();
        }
        writer.land().unwrap();
        let path = folder.join("model.json");
        model().save(&path).unwrap();
        let answers = run(
            std::slice::from_ref(&video),
            &path,
            None,
            None,
            &HashMap::new(),
        )
        .unwrap();
        assert_eq!(answers[0].start, Some(70.0));
        assert_eq!((answers[0].from, answers[0].duration), (40.0, 100.0));
        assert_eq!(answers[0].video, video.to_string_lossy());
    }
}
