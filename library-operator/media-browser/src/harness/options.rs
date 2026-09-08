// The flags the media browser accepts, and the parsers behind them. A
// headless run has no keyboard and no screenshot tool, so the flags stand in
// for both.

use std::path::PathBuf;
use std::time::Duration;

use crate::audience;

/// The Wayland app-id the surface must ask for. The display claim
/// delivers it into the container at run time, and the compositor places the
/// window on the claimed output by it. An empty value asks for no app-id,
/// which is a run on a workstation where no claim named one.
pub const APP_ID: &str = "DISPLAY_APP_ID";

/// The seconds the browser waits for a window before it exits. An unset
/// or non-positive value leaves the watchdog off, so a run outside a pod never
/// exits for a missing window. The operator sets it on the browser container of
/// every screen pod.
pub const WINDOW_GRACE: &str = "WINDOW_GRACE_SECONDS";

/// The topic the library operator reads this `Player`'s play requests
/// on. It is the library operator's own variable, not media-operator's,
/// because that operator names the topic, and the browser knows neither
/// the topic base nor the `Player`'s name.
pub const PLAY_TOPIC: &str = "LIBRARY_PLAY_TOPIC";

/// The topic the browser keeps who is watching on. It is this operator's
/// own variable for the same reason the play topic is, and the browser is
/// the only program that writes it.
pub const AUDIENCE_TOPIC: &str = "LIBRARY_AUDIENCE_TOPIC";

/// The help the binary prints for `--help`.
pub const HELP: &str = "\
media-browser [FLAGS]

  --catalog PATH           the sidecar's SQLite file; without it, the sample
  --updates URL            the agent's HTTP API base
  --progress PATH          the progress store's SQLite file
  --progress-updates URL   the progress agent's HTTP API base
  --people FILE            the Person list, as JSON
  --audience NAMES         who is watching, comma separated
  --library-root NAME=PATH where a library's volume is read; repeatable
  --cache-dir PATH         where scaled art is cached; without it, no disk cache
  --cache-budget BYTES     the bytes the disk cache keeps; without it, 512 MiB
  --script \"0.0:p,3.0:o\"   key events at seconds from the first frame
  --capture DIR            where captured PNGs go
  --capture-at \"0.5,3.2\"   one PNG of the rendered frame at each second listed;
                           the run ends after the last one
  --stats FILE             the JSON measurements, written at exit
  --quit-after SECONDS     when to exit
  --size WxH               the window size to ask for; the default is 1920x1080
  --scale FACTOR           the scale to lay out at, in place of the compositor's
  --print-progress         print what the audience is watching, then exit
  --help                   print this and exit

The binary takes the same keys from a real keyboard, so it runs on a
workstation with no flags at all. Escape ends the run, the forward slash
is back, and the backtick is home.
";

/// What the command line asked for.
#[derive(Debug, PartialEq)]
pub enum Invocation {
    // The options are boxed because they are much the larger of the two
    // answers, and every caller of the parse would carry that size.
    /// Open a window with these options.
    Run(Box<Options>),
    /// Print [`HELP`] and stop.
    Help,
}

/// The flags the media browser accepts.
#[derive(Debug, PartialEq)]
pub struct Options {
    /// The sidecar's SQLite file. Without it the binary browses the
    /// sample catalog and reads no volume.
    pub catalog: Option<PathBuf>,
    /// The base of the agent's HTTP API, where the update streams are.
    pub updates: Option<String>,
    /// The progress store's SQLite file, beside the catalog's. Without it
    /// the browser reads no progress.
    pub progress: Option<PathBuf>,
    /// The base of the progress agent's HTTP API. The progress agent is a
    /// second agent with an API of its own, not the catalog agent's.
    pub progress_updates: Option<String>,
    /// Every `Person` the cluster holds. An empty list means no name is
    /// checked against it.
    pub people: Vec<audience::Person>,
    /// Where that list was read from, so the browser reads it again each
    /// time it asks who is watching. A list in a pod is a projected
    /// ConfigMap that the kubelet rewrites when a `Person` is added.
    pub people_file: Option<PathBuf>,
    /// The people watching at the start of the run, by `Person` name. An
    /// empty audience reads the plays that name nobody.
    pub audience: Vec<String>,
    /// Print the audience's continue-watching rows and exit, with no window.
    pub print_progress: bool,
    /// Where each library's volume is read, keyed by the catalog's
    /// library column, `namespace/name`.
    pub library_roots: Vec<(String, PathBuf)>,
    /// Where scaled art is cached. `None` leaves the disk cache off.
    pub cache_dir: Option<PathBuf>,
    /// The bytes the disk cache keeps under. The operator sets it from
    /// the art claim's size, and `None` keeps the cache's own default.
    pub cache_budget: Option<usize>,
    /// Key events at seconds from the first frame, in the order they fire.
    pub script: Vec<(f64, String)>,
    /// Where captured PNGs go, and at what seconds.
    pub capture_dir: Option<PathBuf>,
    pub capture_at: Vec<f64>,
    /// Where the JSON measurements go at exit.
    pub stats: Option<PathBuf>,
    /// When to exit, in seconds. A capture run also exits after its last frame.
    pub quit_after: Option<f64>,
    /// The window size to ask the compositor for.
    pub size: (u32, u32),
    /// The scale to lay out at, in place of the one the compositor states
    /// for the output. A pod never sets it: it is the knob a run on a
    /// workstation turns to see a 4K panel's layout under a compositor that
    /// states 1.
    pub scale: Option<f32>,
    /// The Wayland app-id every window this run maps asks for, from
    /// [`APP_ID`]. Nothing on the command line sets it.
    pub app_id: String,
    /// How long the run waits for a window before it exits, from
    /// [`WINDOW_GRACE`]. Nothing leaves the watchdog off.
    pub window_grace: Option<Duration>,
    /// The play topic, from [`PLAY_TOPIC`]. A run that misses it browses
    /// and starts nothing.
    pub play_topic: String,
    /// The audience topic, from [`AUDIENCE_TOPIC`]. A run that misses it
    /// keeps who is watching to itself, and asks again after a restart.
    pub audience_topic: String,
}

impl Default for Options {
    fn default() -> Self {
        Self {
            catalog: None,
            updates: None,
            progress: None,
            progress_updates: None,
            people: Vec::new(),
            people_file: None,
            audience: Vec::new(),
            print_progress: false,
            library_roots: Vec::new(),
            cache_dir: None,
            cache_budget: None,
            script: Vec::new(),
            capture_dir: None,
            capture_at: Vec::new(),
            stats: None,
            quit_after: None,
            size: (1920, 1080),
            scale: None,
            app_id: String::new(),
            window_grace: None,
            play_topic: String::new(),
            audience_topic: String::new(),
        }
    }
}

impl Options {
    /// Parse the command line. The flags are few and fixed, so the parser is a
    /// loop over the arguments rather than a dependency.
    pub fn parse<I>(args: I) -> Result<Invocation, String>
    where
        I: IntoIterator<Item = String>,
    {
        let mut options = Options::default();
        let mut args = args.into_iter();

        while let Some(arg) = args.next() {
            let mut value = || args.next().ok_or_else(|| format!("{arg} needs a value"));

            match arg.as_str() {
                "--help" => return Ok(Invocation::Help),
                "--catalog" => options.catalog = Some(PathBuf::from(value()?)),
                "--updates" => options.updates = Some(value()?),
                "--progress" => options.progress = Some(PathBuf::from(value()?)),
                "--progress-updates" => options.progress_updates = Some(value()?),
                "--people" => {
                    let path = value()?;
                    options.people = read_people(&path)?;
                    options.people_file = Some(PathBuf::from(path));
                }
                "--audience" => options.audience = parse_audience(&value()?),
                "--print-progress" => options.print_progress = true,
                "--library-root" => options.library_roots.push(parse_root(&value()?)?),
                "--cache-dir" => options.cache_dir = Some(PathBuf::from(value()?)),
                "--cache-budget" => {
                    let raw = value()?;
                    options.cache_budget = Some(
                        raw.trim()
                            .parse()
                            .map_err(|_| format!("bad --cache-budget {raw}"))?,
                    );
                }
                "--script" => options.script = parse_script(&value()?)?,
                "--capture" => options.capture_dir = Some(PathBuf::from(value()?)),
                "--capture-at" => options.capture_at = parse_times(&value()?)?,
                "--stats" => options.stats = Some(PathBuf::from(value()?)),
                "--quit-after" => {
                    let raw = value()?;
                    options.quit_after = Some(
                        raw.trim()
                            .parse()
                            .map_err(|_| format!("bad --quit-after {raw}"))?,
                    );
                }
                "--size" => options.size = parse_size(&value()?)?,
                "--scale" => options.scale = Some(parse_scale(&value()?)?),
                other => return Err(format!("unknown flag {other}")),
            }
        }

        // The stream and the volumes are read for a catalog, so
        // either flag without one is a run that could not do what it asked
        // for.
        if options.catalog.is_none() {
            if options.updates.is_some() {
                return Err("--updates needs --catalog".to_string());
            }
            if !options.library_roots.is_empty() {
                return Err("--library-root needs --catalog".to_string());
            }
            // The print names each work from the catalog, so it needs one.
            if options.print_progress {
                return Err("--print-progress needs --catalog".to_string());
            }
        }

        // The progress stream wakes re-reads of one file, so a stream
        // without that file has nothing to wake.
        if options.progress.is_none() && options.progress_updates.is_some() {
            return Err("--progress-updates needs --progress".to_string());
        }

        // The people list is the closed set of names an audience may hold,
        // so a name outside it is a mistake in the flag.
        if !options.people.is_empty()
            && let Some(unknown) = options
                .audience
                .iter()
                .find(|name| !options.people.iter().any(|person| &person.name == *name))
        {
            return Err(format!("no person named {unknown}"));
        }

        Ok(Invocation::Run(Box::new(options)))
    }
}

impl Options {
    /// Read what the container was told. A pod cannot discover the
    /// app-id its display claim delivered, the grace the operator set,
    /// or the two topics this operator names, so all four arrive in the
    /// environment and none is a flag. The bus wiring arrives the same
    /// way and `media-screen` reads it, so none of it is here.
    pub fn from_environment(&mut self) {
        self.read_environment(|name| std::env::var(name).ok());
    }

    /// The same read against any source of values. The environment is
    /// global to a process, so a test states the variables here instead of
    /// setting them and racing every other test in the binary.
    pub fn read_environment(&mut self, value: impl Fn(&str) -> Option<String>) {
        self.app_id = value(APP_ID).unwrap_or_default();
        self.window_grace = grace(&value(WINDOW_GRACE).unwrap_or_default());
        self.play_topic = value(PLAY_TOPIC).unwrap_or_default();
        self.audience_topic = value(AUDIENCE_TOPIC).unwrap_or_default();
    }
}

/// The window grace, in seconds. Anything but a positive number leaves
/// the watchdog off.
fn grace(text: &str) -> Option<Duration> {
    let seconds: f64 = text.trim().parse().ok()?;
    if seconds <= 0.0 || !seconds.is_finite() {
        return None;
    }
    Some(Duration::from_secs_f64(seconds))
}

/// The `Person` list the file holds. A file that does not parse stops the
/// run, because a run that quietly held no people would take every audience
/// name as good.
pub fn read_people(path: &str) -> Result<Vec<audience::Person>, String> {
    let bytes = std::fs::read(path).map_err(|error| format!("bad --people {path}: {error}"))?;

    audience::people_from_json(&bytes).map_err(|error| format!("bad --people {path}: {error}"))
}

/// The audience, written as `Person` names separated by commas. An empty
/// name is dropped, so a trailing comma names nobody extra.
pub fn parse_audience(raw: &str) -> Vec<String> {
    raw.split(',')
        .map(str::trim)
        .filter(|name| !name.is_empty())
        .map(str::to_string)
        .collect()
}

/// One library root, written `NAME=PATH`, where the name is the
/// catalog's library column and the path is where that volume is read.
pub fn parse_root(raw: &str) -> Result<(String, PathBuf), String> {
    let (name, path) = raw
        .split_once('=')
        .ok_or_else(|| format!("bad --library-root {raw}"))?;
    if name.is_empty() || path.is_empty() {
        return Err(format!("bad --library-root {raw}"));
    }

    Ok((name.to_string(), PathBuf::from(path)))
}

/// A scripted timeline: `SECONDS:KEY` steps, comma separated, sorted by time so
/// the frame loop reads them in order and never looks back.
pub fn parse_script(raw: &str) -> Result<Vec<(f64, String)>, String> {
    let mut script = Vec::new();

    for step in raw.split(',') {
        let step = step.trim();
        if step.is_empty() {
            continue;
        }
        let (at, key) = step
            .split_once(':')
            .ok_or_else(|| format!("bad script step {step}"))?;
        let at: f64 = at
            .trim()
            .parse()
            .map_err(|_| format!("bad script time {at}"))?;
        let key = key.trim();
        if key.is_empty() {
            return Err(format!("bad script step {step}"));
        }
        script.push((at, key.to_string()));
    }

    script.sort_by(|a, b| a.0.total_cmp(&b.0));
    Ok(script)
}

/// A list of seconds, comma separated, sorted for the same reason.
pub fn parse_times(raw: &str) -> Result<Vec<f64>, String> {
    let mut times = Vec::new();

    for at in raw.split(',') {
        let at = at.trim();
        if at.is_empty() {
            continue;
        }
        times.push(at.parse().map_err(|_| format!("bad capture time {at}"))?);
    }

    times.sort_by(f64::total_cmp);
    Ok(times)
}

/// A window size, written `WIDTHxHEIGHT`.
/// One scale factor. A factor at or under zero would lay out nothing, so
/// it is refused with the size parse's own kind of message.
pub fn parse_scale(raw: &str) -> Result<f32, String> {
    let scale: f32 = raw
        .trim()
        .parse()
        .map_err(|_| format!("bad --scale {raw}"))?;
    if scale <= 0.0 {
        return Err(format!("bad --scale {raw}"));
    }
    Ok(scale)
}

pub fn parse_size(raw: &str) -> Result<(u32, u32), String> {
    let (width, height) = raw
        .trim()
        .split_once('x')
        .ok_or_else(|| format!("bad size {raw}"))?;

    Ok((
        width.parse().map_err(|_| format!("bad width {width}"))?,
        height.parse().map_err(|_| format!("bad height {height}"))?,
    ))
}

#[cfg(test)]
#[path = "options/tests.rs"]
mod tests;
