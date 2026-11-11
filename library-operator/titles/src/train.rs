// Fits the model on the readings of the videos that have a mark for their
// end titles already, and measures it on videos it did not see.
//
// The marks come from people who typed a time into a database, and some
// are wrong by minutes. The wrong ones are wrong in unrelated ways and
// the picture is consistent, so a model fitted on all of them still learns
// what end titles look like. It then shows which marks it disagrees with,
// and a second fit without those is a little better. A mark is never
// taken as true for the measure either: the score is how close the
// placements are to the marks, and a model that is right where a mark is
// wrong loses that point.

use std::collections::{BTreeSet, HashSet};
use std::path::{Path, PathBuf};
use std::thread;

use crate::Error;
use crate::changepoint::Placement;
use crate::model::{self, Model, Score, Trained};
use crate::place::{self, Item};
use crate::reading;
use crate::signals::{self, WIDTH};
use crate::trees::{self, Forest};

pub struct Settings {
    pub records: Option<PathBuf>,
    pub out: PathBuf,
    // Where to write the placement of each held-out video, for a person
    // who wants to look at the ones that are far from their mark.
    pub placements: Option<PathBuf>,
    pub trees: trees::Settings,
}

// One line of the labels file: a video, the second its end titles start
// by its mark, and its group. The videos of one group are held out
// together, so the model is never measured on an episode of a series it
// was fitted on.
#[derive(Clone, Debug, PartialEq)]
pub struct Label {
    pub video: PathBuf,
    pub start: f64,
    pub group: String,
}

pub fn labels(text: &str) -> Result<Vec<Label>, Error> {
    text.lines()
        .filter(|line| !line.trim().is_empty())
        .map(|line| {
            let mut fields = line.split('\t');
            let (Some(video), Some(start), Some(group)) =
                (fields.next(), fields.next(), fields.next())
            else {
                return Err(
                    format!("a label needs a video, a start, and a group: {line:?}").into(),
                );
            };
            Ok(Label {
                video: PathBuf::from(video),
                start: start.parse()?,
                group: group.to_string(),
            })
        })
        .collect()
}

// A labeled video with a reading: where its rows are in the binned matrix.
struct Video {
    label: Label,
    duration: f64,
    times: Vec<f64>,
    first_row: usize,
    fold: usize,
}

const FOLDS: usize = 5;
// The model is the mean of this many forests, each fitted on this share,
// in hundredths, of the videos.
const BAGS: usize = 4;
const BAG_SHARE: u64 = 70;
// The fit takes one second in this many, and every second within a
// minute of the mark. Neighboring seconds are nearly the same row, so the
// rest add time to the fit and little to the model. It takes the seconds
// next to the mark too, though a mark is seldom exact to the second: a
// fit without the 5 seconds each side of every mark placed 46% of 76
// checked videos within 3 seconds, and a fit with them placed 63%.
const THIN: usize = 3;
// A mark is suspect when a model that did not see the video places the
// titles more than this many seconds from it, with at least this
// confidence at the temperature below.
const FAR: f64 = 30.0;
const SUSPECT_CONFIDENCE: f64 = 0.6;
const FIRST_TEMPERATURE: f64 = 8.0;
const TEMPERATURES: [f64; 6] = [2.0, 4.0, 8.0, 16.0, 32.0, 64.0];

// The fold of a group: a hash of its name, so a group stays in its fold
// from one training to the next and the scores of two models compare.
fn fold(group: &str) -> usize {
    let hash = group.bytes().fold(0xcbf29ce484222325u64, |hash, byte| {
        (hash ^ byte as u64).wrapping_mul(0x100000001b3)
    });
    (hash % FOLDS as u64) as usize
}

fn matrix_of(label: &Label, records: Option<&Path>) -> Option<(signals::Matrix, f64)> {
    let reading = reading::load(&reading::path(&label.video, records)).ok()?;
    let names = signals::people(&label.video, None, records).unwrap_or_default();
    (reading.seconds.len() >= 60)
        .then(|| (signals::matrix(&reading, &names), reading.header.duration))
}

// Runs `work` on each label on the trainer's threads and returns the
// results in the labels' order.
fn each<T: Send>(labels: &[Label], threads: usize, work: impl Fn(&Label) -> T + Sync) -> Vec<T> {
    let chunk = labels.len().div_ceil(threads.max(1)).max(1);
    thread::scope(|scope| {
        let parts: Vec<_> = labels
            .chunks(chunk)
            .map(|part| scope.spawn(|| part.iter().map(&work).collect::<Vec<T>>()))
            .collect();
        parts
            .into_iter()
            .flat_map(|part| part.join().expect("a reading thread panicked"))
            .collect()
    })
}

struct Set {
    videos: Vec<Video>,
    bins: Vec<u8>,
    edges: Vec<Vec<f32>>,
    // For each row of the matrix: whether the titles have started by the
    // mark, and whether the fit takes the row.
    started: Vec<bool>,
    fitted: Vec<bool>,
}

impl Set {
    fn load(labels: &[Label], records: Option<&Path>, threads: usize) -> Result<Set, Error> {
        // The columns' edges come from one row in seven of every reading.
        // The whole matrix as floats would be 600 bytes a second, and as
        // bins it is 146.
        let sample: Vec<f32> = each(labels, threads, |label| {
            matrix_of(label, records).map_or(Vec::new(), |(matrix, _)| {
                (0..matrix.times.len())
                    .step_by(7)
                    .flat_map(|row| matrix.row(row).to_vec())
                    .collect()
            })
        })
        .concat();
        if sample.is_empty() {
            return Err("no labeled video has a reading".into());
        }
        let edges = trees::edges(&sample, WIDTH);
        drop(sample);
        let binned = each(labels, threads, |label| {
            matrix_of(label, records).map(|(matrix, duration)| {
                (trees::bins(&matrix.values, &edges), matrix.times, duration)
            })
        });
        let mut set = Set {
            videos: Vec::new(),
            bins: Vec::new(),
            edges,
            started: Vec::new(),
            fitted: Vec::new(),
        };
        for (label, binned) in labels.iter().zip(binned) {
            let Some((bins, times, duration)) = binned else {
                continue;
            };
            for (index, time) in times.iter().enumerate() {
                let distance = (time - label.start).abs();
                set.started.push(*time >= label.start);
                set.fitted.push(distance <= 60.0 || index % THIN == 0);
            }
            set.videos.push(Video {
                label: label.clone(),
                duration,
                first_row: set.bins.len() / WIDTH,
                times,
                fold: fold(&label.group),
            });
            set.bins.extend(bins);
        }
        Ok(set)
    }

    fn rows(&self, keep: impl Fn(&Video) -> bool) -> Vec<u32> {
        self.videos
            .iter()
            .filter(|video| keep(video))
            .flat_map(|video| video.first_row..video.first_row + video.times.len())
            .filter(|row| self.fitted[*row])
            .map(|row| row as u32)
            .collect()
    }

    // The mean of BAGS forests, each fitted on its own share of the videos
    // that `keep` names. A video is in a forest's share or out of it by a
    // hash of its place and the forest's number, so two trainings on the
    // same videos fit the same forests.
    fn fit(&self, keep: impl Fn(&Video) -> bool, settings: &trees::Settings) -> Forest {
        let forests = (0..BAGS)
            .map(|bag| {
                let rows = self.rows(|video| {
                    let draw = (video.first_row as u64 ^ (bag as u64) << 40)
                        .wrapping_mul(0x9e3779b97f4a7c15)
                        >> 33;
                    keep(video) && (BAGS == 1 || draw % 100 < BAG_SHARE)
                });
                trees::fit(&self.bins, &self.edges, &self.started, &rows, settings)
            })
            .collect();
        trees::mean(forests)
    }

    // The model's answers for one video. A forest splits on a column's
    // edges, so the upper edge of a value's bin goes down the same
    // branches as the value did.
    fn item(&self, video: &Video, forest: &Forest) -> Item {
        let mut row = vec![0f32; WIDTH];
        let probabilities = (video.first_row..video.first_row + video.times.len())
            .map(|index| {
                for (column, bin) in self.bins[index * WIDTH..(index + 1) * WIDTH]
                    .iter()
                    .enumerate()
                {
                    let edges = &self.edges[column];
                    row[column] = edges.get(*bin as usize).copied().unwrap_or(f32::MAX);
                }
                forest.probability(&row)
            })
            .collect();
        Item {
            video: video.label.video.clone(),
            duration: video.duration,
            times: video.times.clone(),
            probabilities,
            mark: None,
        }
    }

    // Every video's answers from a forest fitted without its fold and
    // without the videos in `left_out`.
    fn held_out(&self, left_out: &HashSet<usize>, settings: &trees::Settings) -> Vec<Item> {
        let mut items: Vec<Option<Item>> = self.videos.iter().map(|_| None).collect();
        for fold in 0..FOLDS {
            let rows =
                self.rows(|video| video.fold != fold && !left_out.contains(&video.first_row));
            if rows.is_empty() {
                continue;
            }
            let forest = self.fit(
                |video| video.fold != fold && !left_out.contains(&video.first_row),
                settings,
            );
            for (index, video) in self.videos.iter().enumerate() {
                if video.fold == fold {
                    items[index] = Some(self.item(video, &forest));
                }
            }
            eprintln!(
                "fold {} of {FOLDS}: fitted on {} seconds",
                fold + 1,
                rows.len()
            );
        }
        // A fold that holds every video has nothing to fit on, and its
        // videos then have no answers.
        self.videos
            .iter()
            .zip(items)
            .map(|(video, item)| {
                item.unwrap_or_else(|| Item {
                    video: video.label.video.clone(),
                    duration: video.duration,
                    times: video.times.clone(),
                    probabilities: vec![0.5; video.times.len()],
                    mark: None,
                })
            })
            .collect()
    }

    // The distance from each placement to its video's mark. A reading
    // with no titles counts as a start at the end of the video.
    fn errors(&self, placed: &[Placement]) -> Vec<f64> {
        self.videos
            .iter()
            .zip(placed)
            .map(|(video, placement)| {
                (placement.start.unwrap_or(video.duration) - video.label.start).abs()
            })
            .collect()
    }
}

// The temperature whose confidence is closest to the truth: the mean
// squared distance from each confidence to 1 for a placement within 10
// seconds of its mark and to 0 for the others.
fn temperature(set: &Set, items: &[Item]) -> f64 {
    let miss = |temperature: f64| -> f64 {
        let placed = place::place(items, temperature, 0.0);
        set.errors(&placed)
            .iter()
            .zip(&placed)
            .map(|(error, placement)| (placement.confidence - f64::from(*error <= 10.0)).powi(2))
            .sum()
    };
    TEMPERATURES
        .into_iter()
        .min_by(|a, b| miss(*a).total_cmp(&miss(*b)))
        .expect("the list is not empty")
}

// How many seconds the placements are behind the marks: the median of
// placement less mark, over the placements within 30 seconds of their
// mark. The marks are a second or two from the cut that starts the
// titles as often early as late, so the median distance from them is the
// model's own lag. A placement far from its mark says the mark or the
// model is wrong, and neither tells the lag.
fn lag(set: &Set, placed: &[Placement]) -> f64 {
    let mut near: Vec<f64> = set
        .videos
        .iter()
        .zip(placed)
        .filter_map(|(video, placement)| Some(placement.start? - video.label.start))
        .filter(|distance| distance.abs() <= FAR)
        .collect();
    if near.is_empty() {
        return 0.0;
    }
    near.sort_by(f64::total_cmp);
    (near[near.len() / 2] * 10.0).round() / 10.0
}

pub fn run(labels: &[Label], settings: &Settings) -> Result<Model, Error> {
    let set = Set::load(labels, settings.records.as_deref(), settings.trees.threads)?;
    eprintln!(
        "{} of {} labeled videos have a reading",
        set.videos.len(),
        labels.len()
    );

    let first = set.held_out(&HashSet::new(), &settings.trees);
    let placed = place::place(&first, FIRST_TEMPERATURE, 0.0);
    let left_out: HashSet<usize> = set
        .videos
        .iter()
        .zip(set.errors(&placed).iter().zip(&placed))
        .filter(|(_, (error, placement))| {
            **error > FAR && placement.confidence >= SUSPECT_CONFIDENCE
        })
        .map(|(video, _)| video.first_row)
        .collect();
    eprintln!(
        "{} marks are far from a sure placement, and the next fits leave them out",
        left_out.len()
    );

    let mut second = set.held_out(&left_out, &settings.trees);
    let temperature = temperature(&set, &second);
    let lag = lag(&set, &place::place(&second, temperature, 0.0));
    let placed = place::place(&second, temperature, lag);
    let errors = set.errors(&placed);
    let held_out = Score::of(&errors);
    // The same placements with each video's own mark as a prior, which is
    // how the operator places a video that has a mark. They are no
    // measure of the model against the marks, so they go only to the
    // placements file, for a person to compare with starts they checked.
    for (item, video) in second.iter_mut().zip(&set.videos) {
        item.mark = Some(video.label.start);
    }
    let with_marks = place::place(&second, temperature, lag);
    if let Some(path) = &settings.placements {
        let lines: Vec<String> = set
            .videos
            .iter()
            .zip(placed.iter().zip(&with_marks))
            .map(|(video, (placement, with_mark))| {
                let start = placement.start.unwrap_or(video.duration);
                let with_mark = with_mark.start.unwrap_or(video.duration);
                format!(
                    "{}\t{}\t{start}\t{:.3}\t{with_mark}\n",
                    video.label.video.display(),
                    video.label.start,
                    placement.confidence
                )
            })
            .collect();
        std::fs::write(path, lines.concat())?;
    }

    let rows = set.rows(|video| !left_out.contains(&video.first_row));
    let forest = set.fit(
        |video| !left_out.contains(&video.first_row),
        &settings.trees,
    );
    let groups: BTreeSet<&str> = set
        .videos
        .iter()
        .map(|video| video.label.group.as_str())
        .collect();
    let model = Model {
        format: model::FORMAT.into(),
        trained: Trained {
            videos: set.videos.len() - left_out.len(),
            groups: groups.len(),
            seconds: rows.len(),
            left_out: left_out.len(),
        },
        held_out,
        temperature,
        lag,
        columns: signals::columns(),
        forest,
    };
    model.save(&settings.out)?;
    Ok(model)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::reading::tests::{header, scratch, second};
    use crate::reading::{Header, Writer};
    use crate::textboxes::TextBox;

    #[test]
    fn labels_reads_a_video_a_start_and_a_group() {
        let read =
            labels("/a/film.mkv\t5400.5\tFilm [2000]\textra\n\n/b/e1.mkv\t1200\tSeries\n").unwrap();
        assert_eq!(read.len(), 2);
        let expected = Label {
            video: "/a/film.mkv".into(),
            start: 5400.5,
            group: "Film [2000]".into(),
        };
        assert_eq!(read[0], expected);
        assert!(labels("/a/film.mkv\t5400.5\n").is_err());
    }

    #[test]
    fn fold_keeps_a_group_in_one_fold() {
        assert_eq!(fold("Fringe"), fold("Fringe"));
        assert!(fold("Fringe") < FOLDS);
    }

    // A library of 40 videos of 300 seconds, each in a group of its own.
    // Each has text on a dark frame from its own start to the end, and a
    // few seconds of text in the story. The mark of every tenth video is
    // 100 seconds late.
    fn library(folder: &Path) -> Vec<Label> {
        (0..40)
            .map(|n| {
                let video = folder.join(format!("video-{n}")).join("video.mkv");
                let start = 150 + (n * 7) % 100;
                let header = Header {
                    duration: 300.0,
                    start: 0.0,
                    ..header()
                };
                let mut writer = Writer::create(&reading::path(&video, None), &header).unwrap();
                for t in 0..300 {
                    let mut row = second(t as f64);
                    if t >= start || (t + n) % 50 < 3 {
                        row.boxes = vec![TextBox(0.5, 0.5, 0.4, 0.05)];
                        row.coverage = 0.02;
                    }
                    row.dark = if t >= start { 0.9 } else { 0.2 };
                    writer.second(&row).unwrap();
                }
                writer.land().unwrap();
                Label {
                    video,
                    start: (start + if n % 10 == 0 { 100 } else { 0 }) as f64,
                    group: format!("group-{n}"),
                }
            })
            .collect()
    }

    fn settings(folder: &Path) -> Settings {
        Settings {
            records: None,
            out: folder.join("model.json"),
            placements: Some(folder.join("placements.tsv")),
            trees: trees::Settings {
                trees: 30,
                ..trees::Settings::default()
            },
        }
    }

    #[test]
    fn run_fits_a_model_that_places_videos_it_did_not_see() {
        let folder = scratch("train");
        let model = run(&library(&folder), &settings(&folder)).unwrap();
        assert_eq!(model.held_out.videos, 40);
        // The four late marks are the only placements far from a mark.
        assert_eq!(model.held_out.within_10, 0.9);
        assert_eq!(model.trained.left_out, 4);
        assert_eq!((model.trained.videos, model.trained.groups), (36, 40));
        assert_eq!(Model::load(&folder.join("model.json")).unwrap(), model);
        let placements = std::fs::read_to_string(folder.join("placements.tsv")).unwrap();
        assert_eq!(placements.lines().count(), 40);
    }

    #[test]
    fn run_needs_a_reading() {
        let folder = scratch("train-empty");
        let absent = Label {
            video: folder.join("absent.mkv"),
            start: 10.0,
            group: "absent".into(),
        };
        assert!(run(&[absent], &settings(&folder)).is_err());
    }
}
