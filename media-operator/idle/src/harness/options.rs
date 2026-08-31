// The flags the idle screen accepts, and the parsers behind them. A headless
// run has no keyboard and no screenshot tool, so the flags stand in for both.

#[cfg(feature = "measure")]
use std::path::PathBuf;
use std::time::Duration;

/// The help the binary prints for `--help`.
pub fn help() -> String {
    [HEAD, MEASURE, TAIL].concat()
}

const HEAD: &str = "\
idle-screen [FLAGS]

  --script \"0.0:p,3.0:o\"   key events at seconds from the first frame
";

/// The flags the `measure` feature carries. A build without it parses none of
/// the three and lists none of them, so a person reading this help reads what
/// the binary in front of them accepts.
#[cfg(feature = "measure")]
const MEASURE: &str = "  --capture DIR            where captured PNGs go
  --capture-at \"0.5,3.2\"   one PNG of the rendered frame at each second listed;
                           the run ends after the last one
  --stats FILE             the JSON measurements, written at exit
";

#[cfg(not(feature = "measure"))]
const MEASURE: &str = "";

const TAIL: &str = "  --quit-after SECONDS     when to exit
  --size WxH               the window size to ask for; the default is 1920x1080
  --help                   print this and exit

The binary takes the same keys from a real keyboard, so it runs on a
workstation with no flags at all. Quit with q.

Under IDLE_PREVIEW=1 the idle screen binds the keys that stand in for the
bus, and names them in a legend at the bottom right. local/idle sets that
variable.
";

/// What the command line asked for.
#[derive(Debug, PartialEq)]
pub enum Invocation {
    /// Open a window with these options.
    Run(Options),
    /// Print the help from [`help`] and stop.
    Help,
}

/// Everything the harness needs for a run: the flags below, and the two window
/// facts the container's environment carries. The command line states none of
/// those two, and [`Options::parse`] leaves both as the default, so the binary
/// reads the wiring once and fills them in.
#[derive(Debug, PartialEq)]
pub struct Options {
    /// Key events at seconds from the first frame, in the order they fire.
    pub script: Vec<(f64, String)>,
    /// Where captured PNGs go, and at what seconds.
    #[cfg(feature = "measure")]
    pub capture_dir: Option<PathBuf>,
    #[cfg(feature = "measure")]
    pub capture_at: Vec<f64>,
    /// Where the JSON measurements go at exit.
    #[cfg(feature = "measure")]
    pub stats: Option<PathBuf>,
    /// When to exit, in seconds. A capture run also exits after its last frame.
    pub quit_after: Option<f64>,
    /// The window size to ask the compositor for.
    pub size: (u32, u32),
    /// The Wayland app-id every window this run maps asks for, from
    /// `DISPLAY_APP_ID`. An empty id asks for none.
    pub app_id: String,
    /// How long the run waits for a window before it exits, from
    /// `IDLE_WINDOW_GRACE_SECONDS`. Nothing leaves the watchdog off.
    pub window_grace: Option<Duration>,
}

impl Default for Options {
    fn default() -> Self {
        Self {
            script: Vec::new(),
            #[cfg(feature = "measure")]
            capture_dir: None,
            #[cfg(feature = "measure")]
            capture_at: Vec::new(),
            #[cfg(feature = "measure")]
            stats: None,
            quit_after: None,
            size: (1920, 1080),
            app_id: String::new(),
            window_grace: None,
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
                "--script" => options.script = parse_script(&value()?)?,
                #[cfg(feature = "measure")]
                "--capture" => options.capture_dir = Some(PathBuf::from(value()?)),
                #[cfg(feature = "measure")]
                "--capture-at" => options.capture_at = parse_times(&value()?)?,
                #[cfg(feature = "measure")]
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

        Ok(Invocation::Run(options))
    }
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
#[cfg(feature = "measure")]
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
            Ok(Invocation::Run(Options::default()))
        );
    }

    #[test]
    fn the_default_size_is_1920x1080() {
        assert_eq!(Options::default().size, (1920, 1080));
    }

    #[test]
    fn the_help_lists_every_flag() {
        for flag in [
            "--script",
            "--capture",
            "--capture-at",
            "--stats",
            "--quit-after",
            "--size",
            "--help",
        ] {
            // Each flag stands in an indented column, and the three parts of
            // the text join without disturbing it.
            assert!(help().contains(&format!("\n  {flag} ")), "{flag}");
        }
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
            Options::parse(args("--wallpaper")),
            Err("unknown flag --wallpaper".to_string())
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
    fn the_command_line_states_neither_window_fact() {
        let Ok(Invocation::Run(options)) = Options::parse(args("--size 1280x720")) else {
            panic!("the flags parse");
        };

        assert_eq!(options.app_id, "");
        assert_eq!(options.window_grace, None);
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
