use std::path::PathBuf;
use std::process::ExitCode;

use appearances::matcher::{self, Rule};
use appearances::{detect, matches, review, runtime::Error};

const USAGE: &str = "usage:
  appearances detect <video>... [--models DIR] [--device auto|cpu|gpu] [--threads N]
                     [--cache DIR] [--decode-width PX] [--detect-width PX] [--hwaccel vaapi]
                     [--decode-threads N]
  appearances match <title folder>... [--models DIR] [--device auto|cpu|gpu] [--threads N]
                    [--credits FILE] [--threshold X] [--margin X]
  appearances review <title folder>... [--threshold X] [--margin X] [--out DIR] [--sheets N]
                     [--play]

--models defaults to $APPEARANCES_MODELS. --device defaults to auto, which takes an Intel GPU
when OpenVINO finds one. --threads sets the CPU's inference threads and defaults to 1.

match prints one line of JSON for each title folder on standard output. It reads the cast from
the folder's .liken/credits.yaml, or from the credits file --credits names, such as the series'
file for a season folder.

A face is named when its closest person's cosine similarity reaches --threshold (0.363) and
leads the next closest person by --margin (0.05). A face the headshots leave unnamed takes a
name when at least 2 named faces of other samples are within 0.5 of it, all name one person,
and they are at least 30% of all its faces within 0.5.";

// The flags and the positional arguments of one command. Every flag takes
// one value, except the switches, which take none.
const SWITCHES: [&str; 1] = ["play"];

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
                Some(name) if SWITCHES.contains(&name) => {
                    flags.push((name.to_string(), String::new()))
                }
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

    fn decimal(&self, name: &str, default: f32) -> Result<f32, Error> {
        self.get(name).map_or(Ok(default), |v| Ok(v.parse()?))
    }

    fn rule(&self) -> Result<Rule, Error> {
        Ok(Rule {
            threshold: self.decimal("threshold", matcher::THRESHOLD)?,
            margin: self.decimal("margin", matcher::MARGIN)?,
        })
    }

    fn models(&self) -> Result<PathBuf, Error> {
        self.get("models")
            .map(PathBuf::from)
            .or_else(|| std::env::var_os("APPEARANCES_MODELS").map(PathBuf::from))
            .ok_or_else(|| "name the model directory with --models or $APPEARANCES_MODELS".into())
    }
}

fn run() -> Result<(), Error> {
    let mut raw = std::env::args().skip(1);
    let command = raw.next().ok_or(USAGE)?;
    let args = Args::parse(raw)?;
    let device = args.get("device").unwrap_or("auto").to_string();
    let threads = args.number("threads", 1)?;
    match command.as_str() {
        "detect" => {
            let settings = detect::Settings {
                models: args.models()?,
                device,
                threads,
                cache: args.get("cache").map(PathBuf::from),
                decode_width: args.number("decode-width", 1280)?,
                detect_width: args.number("detect-width", 1280)?,
                hwaccel: args.get("hwaccel").map(String::from),
                decode_threads: args.number("decode-threads", 2)?,
            };
            for video in &args.paths {
                println!("{}", detect::run(video, &settings)?.display());
            }
        }
        "match" => {
            let models = args.models()?;
            let rule = args.rule()?;
            let credits = args.get("credits").map(PathBuf::from);
            for title in &args.paths {
                let found =
                    matches::run(title, credits.as_deref(), &models, &device, threads, rule)?;
                println!("{}", serde_json::to_string(&found)?);
            }
        }
        "review" => {
            let settings = review::Settings {
                rule: args.rule()?,
                out: args.get("out").map(PathBuf::from),
                play: args.get("play").is_some(),
                sheets: args.get("sheets").map(str::parse).transpose()?,
            };
            for title in &args.paths {
                review::run(title, &settings)?;
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
            eprintln!("appearances: {error}");
            ExitCode::FAILURE
        }
    }
}
