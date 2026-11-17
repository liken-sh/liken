//! One read of a pod's logs, in the form the page shows.  A read records
//! the last lines of each container with the level of each line, or the
//! reason a container has no lines, and the place transect read them
//! from.

use k8s_openapi::jiff::Timestamp;

use super::level::{Level, level};
use super::logsql;
use super::patterns::Severity;
use super::query::Row;
use super::source::LogSource;

/// How many lines transect requests from the API for each container.
pub const TAIL_LINES: i64 = 50;

/// How many bytes of a container's tail the page keeps.  The API's
/// `limitBytes` counts from the start of the tail, so it would cut the
/// newest lines.  This module cuts the tail instead, from the oldest
/// end.
pub const KEEP_BYTES: usize = 16 * 1024;

/// The most bytes the API may return, so one container that writes
/// megabyte lines cannot make a page load slow.  It is larger than
/// `KEEP_BYTES`, so that usually the cut to `KEEP_BYTES`, not the API,
/// decides which lines are kept.
pub const LIMIT_BYTES: i64 = 64 * 1024;

/// How many characters of one line the page shows.
const LINE_CHARS: usize = 400;

#[derive(Clone, Debug, Default, PartialEq)]
pub struct Tails {
    pub read_at: Timestamp,
    pub from: Origin,
    /// transect may not read `pods/log`, so no container's tail was read.
    pub forbidden: bool,
    pub containers: Vec<Tail>,
    /// The pod's lines that each log pattern matches, from the pod's
    /// start, for each pattern that matches at least one line.  Only a
    /// store can return these.
    pub matching: Vec<Matching>,
}

/// One pattern's lines in one pod, newest last.
#[derive(Clone, Debug, PartialEq)]
pub struct Matching {
    pub tag: Tag,
    /// Each line with the container that wrote it.
    pub lines: Vec<(String, Line)>,
}

/// Where a read's lines came from.
#[derive(Clone, Debug, Default, PartialEq)]
pub enum Origin {
    /// The Kubernetes API, with no log source declared.
    #[default]
    Api,
    /// The log source of this name.
    Store(String),
    /// The Kubernetes API, because the log source of this name returned
    /// no lines, for the reason in `why`.
    Fallback { source: String, why: String },
}

#[derive(Clone, Debug, PartialEq)]
pub struct Tail {
    pub container: String,
    pub read: Read,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Read {
    Lines(Vec<Line>),
    /// The container has written nothing yet.
    Empty,
    /// The API refused the read, as it does for a container that waits
    /// to start.  The value is the API's message.
    Failed(String),
}

#[derive(Clone, Debug, PartialEq)]
pub struct Line {
    pub text: String,
    pub level: Level,
    /// When the line was written.  A store records this time, and the
    /// API's tail does not.
    pub time: Option<Timestamp>,
    /// The patterns the line matches, which only a store can return.
    pub tags: Vec<Tag>,
}

/// A pattern that a line matches, in the form the page shows on the
/// line.
#[derive(Clone, Debug, PartialEq)]
pub struct Tag {
    pub pattern: String,
    pub meaning: String,
    pub severity: Severity,
}

impl Line {
    /// When the line was written, as text for the line's title.
    pub fn time_text(&self) -> Option<String> {
        self.time
            .map(|at| at.strftime("%Y-%m-%d %H:%M:%S UTC").to_string())
    }

    /// The line's CSS class.  The page colors an error or a warning with
    /// `--warm`, as health does, and a plain line in the panel's text
    /// color.
    pub fn class(&self) -> &'static str {
        match self.level {
            Level::Plain => "log-line",
            Level::Warning => "log-line log-warning",
            Level::Error => "log-line log-error",
        }
    }
}

impl Tails {
    /// How many lines the read holds across its containers, and how
    /// many of them are errors.
    pub fn errors(&self) -> (usize, usize) {
        let lines = self.containers.iter().flat_map(|tail| match &tail.read {
            Read::Lines(lines) => lines.as_slice(),
            Read::Empty | Read::Failed(_) => &[],
        });
        lines.fold((0, 0), |(errors, all), line| {
            (errors + usize::from(line.level == Level::Error), all + 1)
        })
    }
}

/// The lines of `text`, a container's tail, newest last.  When the tail
/// is over `KEEP_BYTES`, this function drops the oldest lines first,
/// and it cuts a long line to `LINE_CHARS` with an ellipsis.
pub fn lines(text: &str) -> Read {
    let mut kept: Vec<&str> = Vec::new();
    let mut bytes = 0;
    for line in text.lines().rev().filter(|line| !line.trim().is_empty()) {
        bytes += line.len() + 1;
        if bytes > KEEP_BYTES && !kept.is_empty() {
            break;
        }
        kept.push(line);
    }
    if kept.is_empty() {
        return Read::Empty;
    }
    let lines = kept
        .into_iter()
        .rev()
        .map(|line| Line {
            level: level(line),
            text: cut(line),
            time: None,
            tags: Vec::new(),
        })
        .collect();
    Read::Lines(lines)
}

/// The lines of a store's response to a tail query.  The store returns
/// them newest first, and this function puts them newest last, in the
/// order of the API's lines.  A line's level is the level that its
/// level field names, when the field names one, and otherwise the level
/// that `level` guesses from its text.  The query's own limit bounds the
/// number of lines, and `KEEP_BYTES`, the same byte limit as the API's
/// tail, bounds their size.
pub fn from_rows(rows: &[Row], source: &LogSource) -> Read {
    let fields = &source.fields;
    let mut kept = Vec::new();
    let mut bytes = 0;
    for row in rows {
        let text = row.get(&fields.message).map_or("", String::as_str);
        if blank(text) {
            continue;
        }
        bytes += text.len() + 1;
        if bytes > KEEP_BYTES && !kept.is_empty() {
            break;
        }
        let named = fields.level.as_ref().and_then(|field| row.get(field));
        kept.push(Line {
            level: named
                .and_then(|value| source.levels.named(value))
                .unwrap_or_else(|| level(text)),
            text: cut(text),
            time: row.get(&fields.time).and_then(|time| time.parse().ok()),
            tags: Vec::new(),
        });
    }
    if kept.is_empty() {
        return Read::Empty;
    }
    kept.reverse();
    Read::Lines(kept)
}

/// Whether a store's `message` contains no line, because it is empty or
/// it is the text that VictoriaLogs writes in place of a missing
/// message.
pub fn blank(message: &str) -> bool {
    message.trim().is_empty() || logsql::no_message(message)
}

/// The text of `line` on one line, without control characters or
/// direction marks, cut to `LINE_CHARS` with an ellipsis.  A store
/// keeps a line's own newlines, such as a stack trace's, and the panel
/// shows them as spaces.
pub fn cut(line: &str) -> String {
    let line: String = line
        .chars()
        .map(|c| if c == '\n' || c == '\t' { ' ' } else { c })
        .filter(|c| transect_graph::untrusted::shown(*c))
        .collect();
    match line.char_indices().nth(LINE_CHARS) {
        Some((end, _)) => format!("{}…", &line[..end]),
        None => line,
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn texts(read: &Read) -> Vec<String> {
        match read {
            Read::Lines(lines) => lines.iter().map(|line| line.text.clone()).collect(),
            _ => Vec::new(),
        }
    }

    #[rstest]
    #[case::nothing("", Read::Empty)]
    #[case::blank_lines("\n  \n", Read::Empty)]
    fn an_empty_tail_says_so(#[case] text: &str, #[case] read: Read) {
        assert_eq!(lines(text), read);
    }

    fn classes(read: &Read) -> Vec<&'static str> {
        match read {
            Read::Lines(lines) => lines.iter().map(Line::class).collect(),
            _ => Vec::new(),
        }
    }

    fn tail(container: &str, text: &str) -> Tail {
        Tail {
            container: container.into(),
            read: lines(text),
        }
    }

    #[rstest]
    #[case::no_containers(vec![], (0, 0))]
    #[case::one_error(vec![tail("web", "ready\nERROR dial failed\n")], (1, 2))]
    #[case::across_containers(
        vec![tail("web", "ERROR one\nok\n"), tail("proxy", "FATAL two\n")],
        (2, 3)
    )]
    #[case::an_empty_tail(vec![tail("web", "")], (0, 0))]
    fn counts_the_error_lines_it_read(
        #[case] containers: Vec<Tail>,
        #[case] expected: (usize, usize),
    ) {
        let tails = Tails {
            containers,
            ..Tails::default()
        };
        assert_eq!(tails.errors(), expected);
    }

    #[rstest]
    fn lines_keep_their_order_and_level() {
        let read = lines("starting\nERROR dial failed\n\nlevel=warn slow\n");
        assert_eq!(
            texts(&read),
            ["starting", "ERROR dial failed", "level=warn slow"]
        );
        assert_eq!(
            classes(&read),
            ["log-line", "log-line log-error", "log-line log-warning"]
        );
    }

    #[rstest]
    fn the_oldest_lines_go_past_the_byte_limit() {
        let line = "x".repeat(1023);
        let text = format!("first\n{}", vec![line.as_str(); 20].join("\n"));
        let kept = texts(&lines(&text));
        assert_eq!(kept.len(), KEEP_BYTES / 1024);
        assert!(!kept.contains(&"first".to_string()));
    }

    fn row(time: &str, level: &str, message: &str) -> Row {
        [("_time", time), ("severity_text", level), ("_msg", message)]
            .into_iter()
            .filter(|(_, value)| !value.is_empty())
            .map(|(key, value)| (key.to_string(), value.to_string()))
            .collect()
    }

    fn levels(read: &Read) -> Vec<(String, Level, Option<String>)> {
        match read {
            Read::Lines(lines) => lines
                .iter()
                .map(|line| (line.text.clone(), line.level, line.time_text()))
                .collect(),
            _ => Vec::new(),
        }
    }

    #[rstest]
    fn a_store_answer_reads_newest_last_with_the_level_field_first() {
        let rows = [
            row("2026-01-01T12:00:02Z", "Unspecified", "ERROR dial failed"),
            row("2026-01-01T12:00:01Z", "WARN", "slow\nand steady"),
            row("2026-01-01T12:00:00Z", "", "ready"),
            row("2026-01-01T11:59:59Z", "INFO", ""),
            row(
                "2026-01-01T11:59:58Z",
                "INFO",
                "missing _msg field; see https://docs.victoriametrics.com/victorialogs/keyconcepts/#message-field",
            ),
        ];
        let source = crate::logs::source::fixtures::source();
        let at = |s: &str| Some(format!("2026-01-01 {s} UTC"));
        assert_eq!(
            levels(&from_rows(&rows, &source)),
            vec![
                ("ready".into(), Level::Plain, at("12:00:00")),
                ("slow and steady".into(), Level::Warning, at("12:00:01")),
                ("ERROR dial failed".into(), Level::Error, at("12:00:02")),
            ]
        );
    }

    #[rstest]
    fn a_store_answer_with_no_lines_is_empty() {
        let source = crate::logs::source::fixtures::source();
        assert_eq!(from_rows(&[], &source), Read::Empty);
    }

    #[rstest]
    fn a_long_line_is_cut() {
        let kept = texts(&lines(&"é".repeat(1000)));
        assert_eq!(kept[0].chars().count(), LINE_CHARS + 1);
        assert!(kept[0].ends_with('…'));
    }

    #[rstest]
    #[case::a_stack_trace("panic\n\tat main", "panic  at main")]
    #[case::a_terminal_color("\u{1b}[31mERROR\u{1b}[0m", "[31mERROR[0m")]
    #[case::a_right_to_left_override("user \u{202E}nimda logged in", "user nimda logged in")]
    #[case::an_isolate("a\u{2067}b\u{2069}", "ab")]
    fn a_line_shows_no_control_or_direction_marks(#[case] line: &str, #[case] expected: &str) {
        assert_eq!(cut(line), expected);
    }
}
