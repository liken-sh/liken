// A reading as the numbers the model reads: for each second, sixteen
// signals, and for each signal its mean over the 10, 30, and 90 seconds
// before and after the second, over everything before it, and over
// everything from it to the end of the file. The model answers one
// question for each second, "have the end titles started?", and the
// answer depends more on what follows the second than on the second
// itself: a credit roll is text on a dark frame that goes on to the end.

use std::path::Path;

use serde::Deserialize;

use crate::Error;
use crate::reading::Reading;
use crate::words;

pub const SIGNALS: [&str; 16] = [
    "text",
    "coverage",
    "dark",
    "bright",
    "change",
    "centered",
    "boxheight",
    "spread",
    "boxes",
    "lines",
    "name",
    "names",
    "role",
    "roles",
    "textdark",
    "read",
];
const SPANS: [usize; 3] = [10, 30, 90];
// The signal itself, two means for each span, and the two means to the
// ends of the reading.
const VIEWS: usize = 1 + SPANS.len() * 2 + 2;
pub const WIDTH: usize = SIGNALS.len() * VIEWS + 2;

// The name of each column, in order. A model names the columns it was
// fitted on, and `place` refuses a model whose columns differ from these.
pub fn columns() -> Vec<String> {
    let mut columns = Vec::with_capacity(WIDTH);
    for signal in SIGNALS {
        columns.push(signal.to_string());
        for span in SPANS {
            columns.push(format!("{signal}-before{span}"));
            columns.push(format!("{signal}-after{span}"));
        }
        columns.push(format!("{signal}-before"));
        columns.push(format!("{signal}-after"));
    }
    columns.push("to-end".into());
    columns.push("window".into());
    columns
}

pub struct Matrix {
    pub times: Vec<f64>,
    // One row of WIDTH values for each second, row after row.
    pub values: Vec<f32>,
}

impl Matrix {
    pub fn row(&self, index: usize) -> &[f32] {
        &self.values[index * WIDTH..(index + 1) * WIDTH]
    }
}

// The sixteen signals of each second.
fn base(reading: &Reading, names: &[String]) -> Vec<[f32; SIGNALS.len()]> {
    let seconds = &reading.seconds;
    let mut base = vec![[0f32; SIGNALS.len()]; seconds.len()];
    for (row, second) in base.iter_mut().zip(seconds) {
        let boxes = &second.boxes;
        row[1] = second.coverage;
        row[2] = second.dark;
        row[3] = second.bright;
        row[4] = second.change;
        row[8] = boxes.len() as f32;
        if boxes.is_empty() {
            continue;
        }
        let count = boxes.len() as f32;
        row[0] = 1.0;
        // How close the lines are to the middle of the frame, from 0 to 1.
        // A title card and a credit roll are centered, and a subtitle is
        // too, but a sign in the story seldom is.
        row[5] = 1.0
            - 2.0
                * boxes
                    .iter()
                    .map(|b| (b.center_x() - 0.5).abs())
                    .sum::<f32>()
                / count;
        let mut heights: Vec<f32> = boxes.iter().map(|b| b.height()).collect();
        heights.sort_by(f32::total_cmp);
        row[6] = heights[heights.len() / 2];
        let top = boxes.iter().map(|b| b.center_y()).fold(f32::MAX, f32::min);
        let bottom = boxes.iter().map(|b| b.center_y()).fold(f32::MIN, f32::max);
        row[7] = bottom - top;
        row[14] = second.dark;
    }
    // A second takes the words of the nearest read within 4 seconds, when
    // it holds text itself. The reader reads one frame in 8 seconds, and
    // only a frame with text, so a second with text and no read near it
    // is rare, and its words stay zero.
    for (index, second) in seconds.iter().enumerate() {
        let Some(lines) = &second.lines else {
            continue;
        };
        let lines: Vec<String> = lines
            .iter()
            .map(|line| words::plain(&line.0))
            .filter(|line| line.len() >= 4)
            .collect();
        let named = words::named(&lines, names) as f32;
        let roles = words::roles(&lines) as f32;
        let from = index.saturating_sub(4);
        let to = (index + 5).min(seconds.len());
        for row in base[from..to].iter_mut().filter(|row| row[0] > 0.0) {
            row[9] = lines.len() as f32;
            row[10] = f32::from(named > 0.0);
            row[11] = named;
            row[12] = f32::from(roles > 0.0);
            row[13] = roles;
            row[15] = 1.0;
        }
    }
    base
}

// The matrix of a reading. `names` are the plain names of the video's
// people, from `words::names`.
pub fn matrix(reading: &Reading, names: &[String]) -> Matrix {
    let base = base(reading, names);
    let count = base.len();
    let mut values = vec![0f32; count * WIDTH];
    for signal in 0..SIGNALS.len() {
        // total[i] is the sum of the signal over the seconds before i.
        let mut total = vec![0f64; count + 1];
        for (index, row) in base.iter().enumerate() {
            total[index + 1] = total[index] + row[signal] as f64;
        }
        let mean =
            |from: usize, to: usize| ((total[to] - total[from]) / (to - from).max(1) as f64) as f32;
        for index in 0..count {
            let row = &mut values[index * WIDTH + signal * VIEWS..][..VIEWS];
            row[0] = base[index][signal];
            for (place, span) in SPANS.iter().enumerate() {
                row[1 + place * 2] = mean(index.saturating_sub(*span), index);
                row[2 + place * 2] = mean(index, (index + span).min(count));
            }
            row[VIEWS - 2] = mean(0, index);
            row[VIEWS - 1] = mean(index, count);
        }
    }
    let header = &reading.header;
    let window = (header.duration - header.end_start()) as f32;
    for (index, second) in reading.seconds.iter().enumerate() {
        values[index * WIDTH + WIDTH - 2] = (header.duration - second.t) as f32;
        values[index * WIDTH + WIDTH - 1] = window;
    }
    Matrix {
        times: reading.seconds.iter().map(|second| second.t).collect(),
        values,
    }
}

#[derive(Deserialize)]
struct CreditsFile {
    #[serde(default)]
    credits: Vec<Credit>,
}

#[derive(Deserialize)]
struct Credit {
    name: String,
}

#[derive(Deserialize)]
struct BillingFile {
    #[serde(default)]
    billing: Vec<Credit>,
}

// The names of the people the video credits, from two records of the
// credits fact. `credits.yaml` holds the people a screen shows: the billed
// cast, the directors, and the writers. `billing.yaml` holds every person
// the provider credited, with the producers, the composer, the
// cinematographer, and the rest of the crew, who are the names on most
// cards of a title sequence. The names of both count.
//
// A film's records are in the film's folder. An episode's are in the
// series' folder, one above the season's, because the credits fact
// credits a series and not each episode. With `records`, a billing record
// under that directory counts too, for a library that only reads. A video
// with no record has no names, and the model then works from the job
// words and the picture.
pub fn people(
    video: &Path,
    credits: Option<&Path>,
    records: Option<&Path>,
) -> Result<Vec<String>, Error> {
    let folders: Vec<&Path> = video.ancestors().skip(1).take(2).collect();
    let read =
        |file: &Path| std::fs::read_to_string(file).map_err(|e| format!("{}: {e}", file.display()));
    let mut names = Vec::new();
    let credits = match credits {
        Some(file) => Some(file.to_path_buf()),
        None => folders
            .iter()
            .map(|folder| folder.join(".liken").join("credits.yaml"))
            .find(|file| file.exists()),
    };
    if let Some(file) = credits {
        let parsed: CreditsFile = serde_yaml_ng::from_str(&read(&file)?)?;
        names.extend(parsed.credits.into_iter().map(|credit| credit.name));
    }
    for folder in &folders {
        let beside = folder.join(".liken").join("billing.yaml");
        let kept = records.map(|records| {
            records
                .join(folder.strip_prefix("/").unwrap_or(folder))
                .join(".liken")
                .join("billing.yaml")
        });
        for file in [Some(beside), kept]
            .into_iter()
            .flatten()
            .filter(|file| file.exists())
        {
            let parsed: BillingFile = serde_yaml_ng::from_str(&read(&file)?)?;
            names.extend(parsed.billing.into_iter().map(|credit| credit.name));
        }
    }
    Ok(words::names(&names))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::reading::tests::{header, scratch, second};
    use crate::reading::{Line, Second};
    use crate::textboxes::TextBox;

    fn column(name: &str) -> usize {
        columns().iter().position(|c| c == name).unwrap()
    }

    fn with_text(t: f64) -> Second {
        let mut second = second(t);
        second.coverage = 0.2;
        second.dark = 0.9;
        second.boxes = vec![TextBox(0.5, 0.4, 0.3, 0.04), TextBox(0.5, 0.6, 0.3, 0.06)];
        second
    }

    // A reading of 60 seconds from second 40 of a video of 100: story for
    // 30 seconds, then text on a dark frame, read once at second 80.
    fn reading() -> Reading {
        let mut seconds: Vec<Second> = (0..30).map(|i| second(40.0 + i as f64)).collect();
        seconds.extend((30..60).map(|i| with_text(40.0 + i as f64)));
        seconds[40].lines = Some(vec![
            Line("Directed by".into(), 0.9, 0.4, 0.04),
            Line("EVA GREEN".into(), 0.9, 0.6, 0.06),
        ]);
        Reading {
            header: header(),
            seconds,
        }
    }

    #[test]
    fn columns_has_the_width_of_a_row() {
        assert_eq!(columns().len(), WIDTH);
    }

    #[test]
    fn matrix_gives_each_second_its_signals() {
        let matrix = matrix(&reading(), &["eva green".into()]);
        let (story, titles) = (matrix.row(10), matrix.row(40));
        assert_eq!(story[column("text")], 0.0);
        assert_eq!(titles[column("text")], 1.0);
        assert_eq!(titles[column("boxes")], 2.0);
        assert_eq!(titles[column("centered")], 1.0);
        assert_eq!(titles[column("boxheight")], 0.06);
        assert!((titles[column("spread")] - 0.2).abs() < 1e-6);
        assert_eq!(titles[column("textdark")], 0.9);
        assert_eq!(titles[column("to-end")], 20.0);
        assert_eq!(titles[column("window")], 60.0);
    }

    #[test]
    fn matrix_means_look_before_and_after_the_second() {
        let matrix = matrix(&reading(), &[]);
        let edge = matrix.row(30);
        assert_eq!(edge[column("text-before10")], 0.0);
        assert_eq!(edge[column("text-after10")], 1.0);
        assert_eq!(edge[column("text-before")], 0.0);
        assert_eq!(edge[column("text-after")], 1.0);
        assert_eq!(matrix.row(25)[column("text-after10")], 0.5);
        assert_eq!(matrix.row(0)[column("text-after")], 0.5);
    }

    #[test]
    fn a_read_gives_its_words_to_the_seconds_around_it() {
        let matrix = matrix(&reading(), &["eva green".into()]);
        for index in [36, 40, 44] {
            let row = matrix.row(index);
            assert_eq!(
                (
                    row[column("lines")],
                    row[column("names")],
                    row[column("roles")]
                ),
                (2.0, 1.0, 1.0)
            );
            assert_eq!(
                (
                    row[column("name")],
                    row[column("role")],
                    row[column("read")]
                ),
                (1.0, 1.0, 1.0)
            );
        }
        assert_eq!(matrix.row(35)[column("read")], 0.0);
        assert_eq!(matrix.row(45)[column("read")], 0.0);
    }

    #[test]
    fn people_reads_the_record_of_the_film_or_of_the_series() {
        let folder = scratch("people");
        let season = folder.join("Series").join("Season 01");
        std::fs::create_dir_all(folder.join("Series").join(".liken")).unwrap();
        std::fs::create_dir_all(&season).unwrap();
        let record = "credits:\n  - name: Eva Green\n    part: actor\n  - name: Ade\n";
        std::fs::write(
            folder.join("Series").join(".liken").join("credits.yaml"),
            record,
        )
        .unwrap();
        assert_eq!(
            people(&season.join("e1.mkv"), None, None).unwrap(),
            vec!["eva green"]
        );
        assert!(
            people(&folder.join("film.mkv"), None, None)
                .unwrap()
                .is_empty()
        );
    }

    #[test]
    fn people_adds_the_billing_beside_the_video_and_under_the_records() {
        let folder = scratch("billing");
        let film = folder.join("Film");
        let records = folder.join("records");
        let kept = records.join(film.strip_prefix("/").unwrap()).join(".liken");
        std::fs::create_dir_all(film.join(".liken")).unwrap();
        std::fs::create_dir_all(&kept).unwrap();
        std::fs::write(
            film.join(".liken").join("credits.yaml"),
            "credits:\n  - name: Eva Green\n",
        )
        .unwrap();
        let beside = "billing:\n  - name: Eva Green\n    job: actress\n  - name: Steven Price\n    job: composer\n";
        std::fs::write(film.join(".liken").join("billing.yaml"), beside).unwrap();
        std::fs::write(
            kept.join("billing.yaml"),
            "billing:\n  - name: Steve Arnold\n",
        )
        .unwrap();
        let video = film.join("film.mkv");
        assert_eq!(
            people(&video, None, None).unwrap(),
            vec!["eva green", "steven price"]
        );
        assert_eq!(
            people(&video, None, Some(&records)).unwrap(),
            vec!["eva green", "steve arnold", "steven price"]
        );
    }
}
