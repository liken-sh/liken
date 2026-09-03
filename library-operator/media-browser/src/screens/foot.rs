// The foot of a title's page: the studios, and one line for every video
// file the title holds. Every function here is pure over the rows, so the
// two pages share one model and the tests need no window.

use crate::catalog::FileFacts;
use crate::look;
use crate::screens::facts;
use crate::views::text;

/// The size the production line draws at, and the smaller size the file
/// lines draw at under it, so the technical detail reads second.
pub const PRODUCTION: f32 = look::CAPTION;
pub const DETAIL: f32 = look::FACE;

// The space between the production line and the first file line.
const GAP: f32 = 8.0;

// The space after the leading words of a line, because the average
// advance the width is measured by undercounts a trailing space.
const AFTER_PREFIX: f32 = 4.0;

/// The words that lead the studios on the production line, in the faint
/// face, so the names read first.
pub const PRODUCED_BY: &str = "Produced by ";

// The two categories of the files table the foot reads, and the role a
// title's own video file holds. A trailer is a video of the title too,
// and the foot names the files the title itself is.
const VIDEO: &str = "video";
const SUBTITLE: &str = "subtitle";
const PRIMARY: &str = "primary";

// The word that leads the subtitle languages on a file's line.
const SUBTITLES: &str = "Subtitles: ";

/// The foot of one title: the studios on one line, then one line for every
/// video file.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Foot {
    studios: String,
    files: Vec<String>,
}

/// One line of the foot as a page draws it: the faint words that lead it,
/// its text, its size, whether the text draws in the faint face rather
/// than the full one, and the space over it. The names are values and
/// draw at full brightness; the words that name them, and the technical
/// detail of the files, are asides and draw faint.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Row<'a> {
    pub prefix: &'static str,
    pub content: &'a str,
    pub size: f32,
    pub faint: bool,
    pub lead: f32,
}

impl Row<'_> {
    /// The width the leading words take, which the text starts after.
    pub fn indent(&self) -> f32 {
        match self.prefix.is_empty() {
            true => 0.0,
            false => text::width(self.prefix, self.size) + AFTER_PREFIX,
        }
    }

    /// The height the row takes at this width, its lead included.
    pub fn height(&self, width: f32) -> f32 {
        let lines = text::lines(self.content, self.size, width - self.indent());
        self.lead + text::height(lines, self.size)
    }
}

impl Foot {
    /// The foot of one title, from the studios its body names and the files
    /// the catalog holds for it.
    pub fn of(studios: &[String], files: &[FileFacts]) -> Self {
        let subtitles = subtitles(files);
        let files = files
            .iter()
            .filter(|file| file.kind == VIDEO && file.role == PRIMARY)
            .map(|file| {
                facts::joined(&[
                    &frame(file),
                    &file.video_codec,
                    &file.audio_codec,
                    &size(file.size_bytes),
                    &subtitles,
                ])
            })
            .filter(|line| !line.is_empty())
            .collect();
        Self {
            studios: studios.join(", "),
            files,
        }
    }

    /// The lines the block draws, in order: the production line, then a
    /// gap, then the file lines. A line the title carries nothing for is
    /// left out, and so is the gap over a file line with no production
    /// line above it.
    pub fn rows(&self) -> impl Iterator<Item = Row<'_>> {
        let production = (!self.studios.is_empty()).then_some(Row {
            prefix: PRODUCED_BY,
            content: self.studios.as_str(),
            size: PRODUCTION,
            faint: false,
            lead: 0.0,
        });
        let files = self.files.iter().enumerate().map(move |(index, file)| Row {
            prefix: "",
            content: file.as_str(),
            size: DETAIL,
            faint: true,
            lead: match index == 0 && production.is_some() {
                true => GAP,
                false => 0.0,
            },
        });
        production.into_iter().chain(files)
    }

    /// The height the block takes at this width, and zero where the title
    /// carries no line at all.
    pub fn height(&self, width: f32) -> f32 {
        self.rows().map(|row| row.height(width)).sum()
    }
}

// The frame of one file, and nothing where the scanner read no size.
fn frame(file: &FileFacts) -> String {
    match file.width > 0 && file.height > 0 {
        true => format!("{}\u{d7}{}", file.width, file.height),
        false => String::new(),
    }
}

// One file's size, in the units a person reads a film's size in, and
// nothing where the scanner read none.
fn size(bytes: i64) -> String {
    const GB: f64 = 1_000_000_000.0;
    const MB: f64 = 1_000_000.0;
    if bytes <= 0 {
        return String::new();
    }
    let bytes = bytes as f64;
    match bytes >= GB {
        true => format!("{:.1} GB", bytes / GB),
        false => format!("{:.0} MB", bytes / MB),
    }
}

// The languages the title's subtitle files carry, in the order the
// files came back, each named once.
fn subtitles(files: &[FileFacts]) -> String {
    let mut named: Vec<&str> = Vec::new();
    for file in files {
        if file.kind != SUBTITLE || file.language.is_empty() {
            continue;
        }
        let language = named_language(&file.language);
        if !named.contains(&language) {
            named.push(language);
        }
    }
    match named.is_empty() {
        true => String::new(),
        false => format!("{SUBTITLES}{}", named.join(", ")),
    }
}

// The English name of a language tag. A tag this table does not name
// draws as the file itself carries it, because a tag a person can read
// is better than nothing at all.
fn named_language(tag: &str) -> &str {
    const NAMES: [(&str, &str); 40] = [
        ("ar", "Arabic"),
        ("ara", "Arabic"),
        ("cs", "Czech"),
        ("ces", "Czech"),
        ("cze", "Czech"),
        ("da", "Danish"),
        ("dan", "Danish"),
        ("de", "German"),
        ("deu", "German"),
        ("ger", "German"),
        ("el", "Greek"),
        ("en", "English"),
        ("eng", "English"),
        ("es", "Spanish"),
        ("spa", "Spanish"),
        ("fi", "Finnish"),
        ("fin", "Finnish"),
        ("fr", "French"),
        ("fra", "French"),
        ("fre", "French"),
        ("he", "Hebrew"),
        ("hi", "Hindi"),
        ("hu", "Hungarian"),
        ("it", "Italian"),
        ("ita", "Italian"),
        ("ja", "Japanese"),
        ("jpn", "Japanese"),
        ("ko", "Korean"),
        ("kor", "Korean"),
        ("nl", "Dutch"),
        ("nld", "Dutch"),
        ("no", "Norwegian"),
        ("pl", "Polish"),
        ("pt", "Portuguese"),
        ("por", "Portuguese"),
        ("ru", "Russian"),
        ("rus", "Russian"),
        ("sv", "Swedish"),
        ("tr", "Turkish"),
        ("zh", "Chinese"),
    ];
    NAMES
        .iter()
        .find(|(code, _)| *code == tag)
        .map_or(tag, |(_, name)| *name)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn video() -> FileFacts {
        FileFacts {
            role: "primary".into(),
            kind: "video".into(),
            container: "mkv".into(),
            video_codec: "x265".into(),
            audio_codec: "AC3".into(),
            width: 1_920,
            height: 804,
            size_bytes: 4_200_000_000,
            language: String::new(),
        }
    }

    fn subtitle(language: &str) -> FileFacts {
        FileFacts {
            role: "subtitle".into(),
            kind: "subtitle".into(),
            language: language.into(),
            ..FileFacts::default()
        }
    }

    #[test]
    fn a_title_names_its_studios_and_then_every_video_file_it_holds() {
        let foot = Foot::of(
            &["A Studio".to_string(), "Another".to_string()],
            &[video(), subtitle("en"), subtitle("fr")],
        );
        let rows: Vec<&str> = foot.rows().map(|row| row.content).collect();
        assert_eq!(
            rows,
            [
                "A Studio, Another",
                "1920×804 · x265 · AC3 · 4.2 GB · Subtitles: English, French",
            ]
        );
    }

    #[test]
    fn a_language_the_table_does_not_name_draws_as_the_file_carries_it() {
        let foot = Foot::of(&[], &[video(), subtitle("qq"), subtitle("eng")]);
        let rows: Vec<&str> = foot.rows().map(|row| row.content).collect();
        assert_eq!(
            rows,
            ["1920×804 · x265 · AC3 · 4.2 GB · Subtitles: qq, English"]
        );
    }

    #[test]
    fn a_file_the_scanner_read_no_frame_or_codec_of_shows_what_it_has() {
        let bare = FileFacts {
            size_bytes: 700_000_000,
            ..video()
        };
        let foot = Foot::of(
            &[],
            &[FileFacts {
                width: 0,
                height: 0,
                video_codec: String::new(),
                ..bare
            }],
        );
        let rows: Vec<&str> = foot.rows().map(|row| row.content).collect();
        assert_eq!(rows, ["AC3 · 700 MB"]);
    }

    #[test]
    fn the_foot_names_the_title_s_own_video_files_and_no_other_file() {
        let trailer = FileFacts {
            role: "trailer".into(),
            ..video()
        };
        let art = FileFacts {
            role: "backdrop".into(),
            kind: "image".into(),
            ..FileFacts::default()
        };
        let foot = Foot::of(&[], &[video(), trailer, art]);
        assert_eq!(foot.rows().count(), 1);
    }

    #[test]
    fn a_title_with_no_studio_and_no_file_takes_no_height() {
        let foot = Foot::of(&[], &[]);
        assert_eq!(foot.rows().count(), 0);
        assert_eq!(foot.height(900.0), 0.0);
    }

    #[test]
    fn a_foot_is_as_tall_as_its_lines_and_the_gap_between_the_two_kinds() {
        let foot = Foot::of(&["A Studio".to_string()], &[video()]);
        assert_eq!(
            foot.height(1_680.0),
            text::height(1, PRODUCTION) + GAP + text::height(1, DETAIL)
        );
    }

    #[test]
    fn the_file_lines_draw_smaller_and_fainter_than_the_production_line() {
        let foot = Foot::of(&["A Studio".to_string()], &[video()]);
        let rows: Vec<Row> = foot.rows().collect();
        assert_eq!(rows[0].prefix, PRODUCED_BY);
        assert!(!rows[0].faint);
        assert_eq!(rows[1].prefix, "");
        assert!(rows[1].faint);
        assert!(rows[1].size < rows[0].size);
        assert_eq!(rows[1].lead, GAP);
    }

    #[test]
    fn a_file_line_with_no_production_line_over_it_takes_no_gap() {
        let foot = Foot::of(&[], &[video()]);
        let rows: Vec<Row> = foot.rows().collect();
        assert_eq!(rows[0].lead, 0.0);
        assert_eq!(foot.height(1_680.0), text::height(1, DETAIL));
    }
}
