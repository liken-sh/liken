use std::collections::HashMap;
use std::path::PathBuf;
use std::process::ExitCode;

use titles::{Error, place, read, train, trees};

const USAGE: &str = "usage:
  titles read <video>... [--models DIR] [--device cpu|gpu|auto] [--threads N] [--cache DIR]
                         [--hwaccel vaapi] [--decode-threads N] [--window SECONDS] [--records DIR]

  titles train --labels FILE --out FILE [--records DIR] [--placements FILE] [--threads N]
                         [--trees N]
  titles place <video>... --model FILE [--records DIR] [--credits FILE] [--marks FILE]

read decodes the last --window seconds of each video (900 by default), and writes the text it
finds in each second to .liken/titles/<video>.jsonl beside the video. --records names a
directory that holds the readings in place of the library.

train fits the model on the readings of the videos that --labels names. Each line of the labels
file is a video, the second its end titles start, and the name of its film or series, with tabs
between them. train measures the model on videos it was not fitted on, prints the measure, and
writes the model to --out. --placements names a file for the placement of each of those videos.

place prints, for each video, the second its end titles start and how sure the model is, as a
line of JSON. The videos of one folder are placed together, with the usual length of the
folder's end titles as a prior. The names of a video's people come from the .liken/credits.yaml
of its folder or of the folder above it, or from the file --credits names. --marks names a file
of the labels' form. A video in it takes its mark as a prior, so the picture sets the second
where the two agree and overrules the mark where they do not.

--models defaults to $TITLES_MODELS. --device defaults to cpu. --threads sets the CPU's inference
threads and defaults to 1. --hwaccel vaapi decodes on the GPU.";

struct Args {
    flags: Vec<(String, String)>,
    paths: Vec<PathBuf>,
}

impl Args {
    fn parse(raw: impl Iterator<Item = String>) -> Result<Args, Error> {
        let mut flags = Vec::new();
        let mut paths = Vec::new();
        let mut raw = raw.peekable();
        while let Some(arg) = raw.next() {
            match arg.strip_prefix("--") {
                Some(name) => {
                    let value = raw.next().ok_or(format!("--{name} needs a value"))?;
                    flags.push((name.to_string(), value));
                }
                None => paths.push(PathBuf::from(arg)),
            }
        }
        Ok(Args { flags, paths })
    }

    fn get(&self, name: &str) -> Option<&str> {
        self.flags
            .iter()
            .rev()
            .find(|(n, _)| n == name)
            .map(|(_, v)| v.as_str())
    }

    fn number(&self, name: &str, default: usize) -> Result<usize, Error> {
        self.get(name).map_or(Ok(default), |v| Ok(v.parse()?))
    }

    fn decimal(&self, name: &str, default: f64) -> Result<f64, Error> {
        self.get(name).map_or(Ok(default), |v| Ok(v.parse()?))
    }

    fn path(&self, name: &str) -> Option<PathBuf> {
        self.get(name).map(PathBuf::from)
    }

    fn models(&self) -> Result<PathBuf, Error> {
        self.path("models")
            .or_else(|| std::env::var_os("TITLES_MODELS").map(PathBuf::from))
            .ok_or_else(|| "name the model directory with --models or $TITLES_MODELS".into())
    }
}

fn run() -> Result<(), Error> {
    let mut raw = std::env::args().skip(1);
    let command = raw.next().ok_or(USAGE)?;
    let args = Args::parse(raw)?;
    match command.as_str() {
        "read" => {
            let settings = read::Settings {
                models: args.models()?,
                device: args.get("device").unwrap_or("cpu").to_string(),
                threads: args.number("threads", 1)?,
                cache: args.path("cache"),
                hwaccel: args.get("hwaccel").map(String::from),
                decode_threads: args.number("decode-threads", 2)?,
                window: args.decimal("window", 900.0)?,
                records: args.path("records"),
            };
            for video in &args.paths {
                println!("{}", read::run(video, &settings)?.display());
            }
        }
        "train" => {
            let labels = args
                .path("labels")
                .ok_or("name the labels file with --labels")?;
            let text = std::fs::read_to_string(&labels)
                .map_err(|e| format!("{}: {e}", labels.display()))?;
            let settings = train::Settings {
                records: args.path("records"),
                out: args.path("out").ok_or("name the model file with --out")?,
                placements: args.path("placements"),
                trees: trees::Settings {
                    trees: args.number("trees", trees::Settings::default().trees)?,
                    threads: args.number("threads", trees::Settings::default().threads)?,
                    ..trees::Settings::default()
                },
            };
            let model = train::run(&train::labels(&text)?, &settings)?;
            println!(
                "{}",
                serde_json::to_string(&(&model.trained, &model.held_out, model.temperature))?
            );
        }
        "place" => {
            let model = args
                .path("model")
                .ok_or("name the model file with --model")?;
            let (records, credits) = (args.path("records"), args.path("credits"));
            let marks = match args.path("marks") {
                Some(file) => {
                    let text = std::fs::read_to_string(&file)
                        .map_err(|e| format!("{}: {e}", file.display()))?;
                    train::labels(&text)?
                        .into_iter()
                        .map(|label| (label.video, label.start))
                        .collect()
                }
                None => HashMap::new(),
            };
            for answer in place::run(
                &args.paths,
                &model,
                records.as_deref(),
                credits.as_deref(),
                &marks,
            )? {
                println!("{}", serde_json::to_string(&answer)?);
            }
        }
        _ => return Err(USAGE.into()),
    }
    Ok(())
}

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("titles: {error}");
            ExitCode::FAILURE
        }
    }
}
