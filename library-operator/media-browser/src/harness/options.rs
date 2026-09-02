// The flags the media browser accepts, and the parsers behind them. A
// headless run has no keyboard and no screenshot tool, so the flags stand in
// for both.

use std::path::PathBuf;
use std::time::Duration;

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

/// The help the binary prints for `--help`.
pub const HELP: &str = "\
media-browser [FLAGS]

  --catalog PATH           the sidecar's SQLite file; without it, the sample
  --updates URL            the agent's HTTP API base
  --library-root NAME=PATH where a library's volume is read; repeatable
  --script \"0.0:p,3.0:o\"   key events at seconds from the first frame
  --capture DIR            where captured PNGs go
  --capture-at \"0.5,3.2\"   one PNG of the rendered frame at each second listed;
                           the run ends after the last one
  --stats FILE             the JSON measurements, written at exit
  --quit-after SECONDS     when to exit
  --size WxH               the window size to ask for; the default is 1920x1080
  --help                   print this and exit

The binary takes the same keys from a real keyboard, so it runs on a
workstation with no flags at all. Quit with q.
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
    /// Where each library's volume is read, keyed by the catalog's
    /// library column, `namespace/name`.
    pub library_roots: Vec<(String, PathBuf)>,
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
    /// The Wayland app-id every window this run maps asks for, from
    /// [`APP_ID`]. Nothing on the command line sets it.
    pub app_id: String,
    /// How long the run waits for a window before it exits, from
    /// [`WINDOW_GRACE`]. Nothing leaves the watchdog off.
    pub window_grace: Option<Duration>,
    /// The play topic, from [`PLAY_TOPIC`]. A run that misses it browses
    /// and starts nothing.
    pub play_topic: String,
}

impl Default for Options {
    fn default() -> Self {
        Self {
            catalog: None,
            updates: None,
            library_roots: Vec::new(),
            script: Vec::new(),
            capture_dir: None,
            capture_at: Vec::new(),
            stats: None,
            quit_after: None,
            size: (1920, 1080),
            app_id: String::new(),
            window_grace: None,
            play_topic: String::new(),
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
                "--library-root" => options.library_roots.push(parse_root(&value()?)?),
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
        }

        Ok(Invocation::Run(Box::new(options)))
    }
}

impl Options {
    /// Read what the container was told. A pod cannot discover the
    /// app-id its display claim delivered, the grace the operator set,
    /// or the topic this operator reads play requests on, so all three
    /// arrive in the environment and none is a flag. The bus wiring
    /// arrives the same way and `media-screen` reads it, so none of it
    /// is here.
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
mod tests {
    use super::*;

    fn args(line: &str) -> Vec<String> {
        line.split_whitespace().map(str::to_string).collect()
    }

    #[test]
    fn no_flags_is_a_run_with_the_defaults() {
        assert_eq!(
            Options::parse(Vec::new()),
            Ok(Invocation::Run(Box::default()))
        );
    }

    #[test]
    fn the_default_size_is_1920x1080() {
        assert_eq!(Options::default().size, (1920, 1080));
    }

    #[test]
    fn help_stops_the_parse() {
        assert_eq!(Options::parse(args("--help")), Ok(Invocation::Help));
        assert_eq!(
            Options::parse(args("--size 800x600 --help")),
            Ok(Invocation::Help)
        );
    }

    #[test]
    fn an_unknown_flag_is_an_error() {
        assert_eq!(
            Options::parse(args("--posters")),
            Err("unknown flag --posters".to_string())
        );
    }

    #[test]
    fn a_flag_without_its_value_is_an_error() {
        assert_eq!(
            Options::parse(args("--stats")),
            Err("--stats needs a value".to_string())
        );
    }

    #[test]
    fn the_flags_land_in_the_options() {
        let Ok(Invocation::Run(options)) = Options::parse(args(
            "--script 1.0:p --capture /frames --capture-at 0.5 \
             --stats /stats.json --quit-after 3 --size 1280x720",
        )) else {
            panic!("the flags parse");
        };

        assert_eq!(options.script, vec![(1.0, "p".to_string())]);
        assert_eq!(options.capture_dir, Some(PathBuf::from("/frames")));
        assert_eq!(options.capture_at, vec![0.5]);
        assert_eq!(options.stats, Some(PathBuf::from("/stats.json")));
        assert_eq!(options.quit_after, Some(3.0));
        assert_eq!(options.size, (1280, 720));
    }

    #[test]
    fn the_catalog_flags_land_in_the_options() {
        let Ok(Invocation::Run(options)) = Options::parse(args(
            "--catalog /state/state.db --updates http://127.0.0.1:20081 \
             --library-root local/movies=/films --library-root local/series=/shows",
        )) else {
            panic!("the flags parse");
        };

        assert_eq!(options.catalog, Some(PathBuf::from("/state/state.db")));
        assert_eq!(options.updates, Some("http://127.0.0.1:20081".to_string()));
        assert_eq!(
            options.library_roots,
            vec![
                ("local/movies".to_string(), PathBuf::from("/films")),
                ("local/series".to_string(), PathBuf::from("/shows")),
            ]
        );
    }

    #[test]
    fn the_catalog_flags_are_all_optional() {
        let Ok(Invocation::Run(options)) = Options::parse(Vec::new()) else {
            panic!("no flags parse");
        };

        assert_eq!(options.catalog, None);
        assert_eq!(options.updates, None);
        assert!(options.library_roots.is_empty());
    }

    #[test]
    fn a_stream_or_a_volume_without_a_catalog_is_an_error() {
        assert_eq!(
            Options::parse(args("--updates http://127.0.0.1:20081")),
            Err("--updates needs --catalog".to_string())
        );
        assert_eq!(
            Options::parse(args("--library-root local/movies=/films")),
            Err("--library-root needs --catalog".to_string())
        );
    }

    #[test]
    fn a_library_root_is_a_name_and_a_path_around_an_equals() {
        assert_eq!(
            parse_root("local/movies=/films"),
            Ok(("local/movies".to_string(), PathBuf::from("/films")))
        );
        assert_eq!(
            parse_root("local/movies"),
            Err("bad --library-root local/movies".to_string())
        );
        assert!(parse_root("=/films").is_err());
        assert!(parse_root("local/movies=").is_err());
    }

    fn environment(pairs: &[(&str, &str)]) -> Options {
        let pairs: Vec<(String, String)> = pairs
            .iter()
            .map(|(name, value)| (name.to_string(), value.to_string()))
            .collect();
        let mut options = Options::default();
        options.read_environment(|name| {
            pairs
                .iter()
                .find(|(set, _)| set == name)
                .map(|(_, value)| value.clone())
        });
        options
    }

    #[test]
    fn an_empty_environment_names_no_screen_and_arms_nothing() {
        let options = environment(&[]);
        assert_eq!(options.app_id, "");
        assert_eq!(options.window_grace, None);
    }

    #[test]
    fn an_empty_environment_names_no_play_topic() {
        assert_eq!(environment(&[]).play_topic, "");
    }

    #[test]
    fn this_operator_names_the_play_topic() {
        let options = environment(&[(PLAY_TOPIC, "liken/library/players/house/den-tv/play")]);

        assert_eq!(
            options.play_topic,
            "liken/library/players/house/den-tv/play"
        );
    }

    #[test]
    fn the_claim_names_the_screen_and_the_operator_arms_the_watchdog() {
        let options = environment(&[(APP_ID, "media-den-tv"), (WINDOW_GRACE, "15")]);
        assert_eq!(options.app_id, "media-den-tv");
        assert_eq!(options.window_grace, Some(Duration::from_secs(15)));
    }

    #[test]
    fn a_positive_grace_arms_the_watchdog() {
        assert_eq!(grace("15"), Some(Duration::from_secs(15)));
        assert_eq!(grace(" 1.5 "), Some(Duration::from_secs_f64(1.5)));
    }

    #[test]
    fn anything_but_a_positive_grace_leaves_it_off() {
        assert_eq!(grace(""), None);
        assert_eq!(grace("0"), None);
        assert_eq!(grace("-5"), None);
        assert_eq!(grace("soon"), None);
        assert_eq!(grace("inf"), None);
    }

    #[test]
    fn a_script_sorts_by_time() {
        assert_eq!(
            parse_script("4.0:o, 1.5:p,0.25:down"),
            Ok(vec![
                (0.25, "down".to_string()),
                (1.5, "p".to_string()),
                (4.0, "o".to_string()),
            ])
        );
    }

    #[test]
    fn an_empty_script_is_empty() {
        assert_eq!(parse_script(""), Ok(Vec::new()));
        assert_eq!(parse_script(" , "), Ok(Vec::new()));
    }

    #[test]
    fn a_script_step_needs_a_time_and_a_key() {
        assert!(parse_script("p").is_err());
        assert!(parse_script("later:p").is_err());
        assert!(parse_script("1.0:").is_err());
    }

    #[test]
    fn capture_times_sort() {
        assert_eq!(parse_times("3.2, 0.5 ,1"), Ok(vec![0.5, 1.0, 3.2]));
    }

    #[test]
    fn a_capture_time_must_be_a_number() {
        assert_eq!(
            parse_times("0.5,soon"),
            Err("bad capture time soon".to_string())
        );
    }

    #[test]
    fn a_size_is_two_numbers_around_an_x() {
        assert_eq!(parse_size("1920x1080"), Ok((1920, 1080)));
        assert_eq!(parse_size(" 640x480 "), Ok((640, 480)));
        assert!(parse_size("1920").is_err());
        assert!(parse_size("widexhigh").is_err());
    }
}
