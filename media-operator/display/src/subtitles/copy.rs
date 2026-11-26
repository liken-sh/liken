//! The display's copy of a text subtitle track: an ASS script with every
//! cue of the track, in which each cue of two or more lines has a
//! `MarginV` of its own. The display writes the script to a file beside
//! mpv's socket and gives it to mpv with `sub-add`, so libass places each
//! cue from the first frame that draws it. `media-operator` plan 49
//! describes the design.
//!
//! Each source of cues is its own module: `matroska` reads the index of a
//! Matroska file, `mp4` reads the sample table of an MP4 file, and
//! `external` reads a subtitle file beside the video. `source` chooses
//! the module for a track. `subrip`, `ass`, `webvtt`, and `microdvd`
//! convert each format to ASS events as mpv's converters do.

pub mod ass;
pub mod at;
pub mod converted;
pub mod external;
pub mod matroska;
pub mod microdvd;
pub mod mp4;
pub mod source;
pub mod subrip;
pub mod webvtt;

use super::cue;
use super::script::{self, Script};
use crate::film::Screen;

/// The start of the name of a copy's file. The name is
/// `media-display-copy.<identity>.<number>.ass`, where the identity names
/// the original and the number counts the copies that the display wrote.
/// mpv reports the file as the copy's `external-filename`, so the display
/// finds the original of a copy from the name. The command sidecar leaves
/// a track with this name out of its choice, by `displayCopyPrefix` in
/// `subtitletracks.go`.
const FILE_PREFIX: &str = "media-display-copy.";

/// The name of the file of copy `number` of the original `identity`.
pub fn file_name(identity: &str, number: u64) -> String {
    format!("{FILE_PREFIX}{identity}.{number}.ass")
}

/// The identity of the original of the copy at `path`, or `None` when the
/// file is not a copy.
pub fn identity(path: &str) -> Option<&str> {
    let name = path.rsplit('/').next()?;
    let rest = name.strip_prefix(FILE_PREFIX)?.strip_suffix(".ass")?;
    Some(rest.rsplit_once('.')?.0)
}

/// One `Dialogue` event. The times are in milliseconds, and the other
/// fields are as the script wrote them.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Dialogue {
    pub start: u64,
    pub end: u64,
    pub layer: String,
    pub style: String,
    pub name: String,
    pub margin_l: String,
    pub margin_r: String,
    pub margin_v: String,
    pub effect: String,
    pub text: String,
}

impl Dialogue {
    /// An event of a converted text track, in the default style.
    pub fn text(start: u64, end: u64, text: String) -> Self {
        Self {
            start,
            end,
            layer: "0".to_string(),
            style: "Default".to_string(),
            name: String::new(),
            margin_l: "0".to_string(),
            margin_r: "0".to_string(),
            margin_v: "0".to_string(),
            effect: String::new(),
            text,
        }
    }

    /// Whether the script places the event itself: with a margin of its
    /// own, a position, a move, or an alignment at the top or in the
    /// middle of the frame.
    fn placed(&self, raised: &[String]) -> bool {
        let text = self.text.as_str();
        let margin = self.margin_v.trim().parse::<i32>().unwrap_or(0);
        margin != 0
            || raised
                .iter()
                .any(|style| style.eq_ignore_ascii_case(self.style.trim_start_matches('*')))
            || text.contains("\\pos(")
            || text.contains("\\move(")
            || (4..=9).any(|row| text.contains(&format!("\\an{row}")))
            || (5..=11).any(|code| text.contains(&format!("\\a{code}")))
    }
}

/// Every cue of a track, ready to be written as a script.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Copy {
    /// The script's header, without an `[Events]` section.
    pub header: String,
    /// Whether the header is an SSA header, whose events start with
    /// `Marked` where an ASS event has `Layer`.
    pub ssa: bool,
    /// Whether the original is a text track that is not ASS, which mpv
    /// draws through its converter. The header of such a copy is written
    /// for each screen by `converted`.
    pub converted: bool,
    pub events: Vec<Dialogue>,
}

impl Copy {
    /// A copy of a converted text track.
    pub fn converted(events: Vec<Dialogue>) -> Self {
        Self {
            header: String::new(),
            ssa: false,
            converted: true,
            events,
        }
    }

    /// The script for `screen`. `encoded` is the share of the frame's
    /// height that is black below its active area, which adds to the
    /// screen's lower bar.
    pub fn script(&self, screen: Option<&Screen>, encoded: f64) -> String {
        let header = match self.converted {
            true => converted::header(screen),
            false => self.header.clone(),
        };
        let rest = screen.map(|screen| match self.converted {
            true => converted::rest(screen),
            false => cue::Rest::ass(&Script::parse(&header), screen),
        });
        let raised = script::raised_styles(&header);
        let mut out = String::with_capacity(header.len() + self.events.len() * 80);
        out.push_str("[Script Info]\n");
        let header = header.trim_start_matches('\u{feff}');
        let body = match header.trim_start().strip_prefix("[Script Info]") {
            Some(rest) => rest.trim_start_matches(['\r', '\n']),
            None => header,
        };
        out.push_str(body.trim_end());
        out.push_str("\n\n[Events]\n");
        out.push_str(match self.ssa {
            true => {
                "Format: Marked, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"
            }
            false => {
                "Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"
            }
        });
        for event in &self.events {
            let margin = match (screen, &rest, event.placed(&raised)) {
                (Some(screen), Some(rest), false) => {
                    cue::margin(rest, screen, encoded, cue::lines(&event.text))
                }
                _ => 0,
            };
            let margin_v = match margin {
                0 => event.margin_v.clone(),
                margin => margin.to_string(),
            };
            out.push_str(&format!(
                "Dialogue: {},{},{},{},{},{},{},{},{},{}\n",
                event.layer,
                time(event.start),
                time(event.end),
                event.style,
                event.name,
                event.margin_l,
                event.margin_r,
                margin_v,
                event.effect,
                event.text
            ));
        }
        out
    }
}

/// An ASS time, `H:MM:SS.cc`, from milliseconds. ASS counts hundredths,
/// and a time is rounded to the nearest one.
pub fn time(ms: u64) -> String {
    let centis = (ms + 5) / 10;
    format!(
        "{}:{:02}:{:02}.{:02}",
        centis / 360_000,
        centis / 6000 % 60,
        centis / 100 % 60,
        centis % 100
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn scope() -> Screen {
        Screen {
            width: 1920,
            height: 1080,
            top: 140,
            bottom: 140,
            left: 0,
            right: 0,
        }
    }

    /// The name of a copy's file gives back the identity of its original,
    /// and a file that is not a copy gives none.
    #[test]
    fn a_copy_is_known_by_its_file_name() {
        let name = file_name("ff-index-3", 7);
        assert_eq!(name, "media-display-copy.ff-index-3.7.ass");
        assert_eq!(identity(&format!("/ipc/{name}")), Some("ff-index-3"));
        assert_eq!(identity("/media/a.en.srt"), None);
        assert_eq!(identity("/ipc/media-display-copy.ass"), None);
    }

    #[test]
    fn a_time_is_written_in_hundredths() {
        let cases = [
            (0, "0:00:00.00"),
            (2500, "0:00:02.50"),
            (3_723_456, "1:02:03.46"),
        ];
        for (ms, want) in cases {
            assert_eq!(time(ms), want, "{ms}");
        }
    }

    /// A cue of one line keeps the style's margin, so it ends on the house
    /// row as the original did. A cue of two lines takes the margin that
    /// centres it in the bar.
    #[test]
    fn the_script_gives_each_cue_of_two_lines_its_margin() {
        let copy = Copy::converted(vec![
            Dialogue::text(500, 2000, "One line".to_string()),
            Dialogue::text(2500, 4000, "The first line\\Nand the second".to_string()),
        ]);
        let script = copy.script(Some(&scope()), 0.0);
        let events: Vec<&str> = script
            .lines()
            .filter(|line| line.starts_with("Dialogue:"))
            .collect();
        assert_eq!(
            events,
            [
                "Dialogue: 0,0:00:00.50,0:00:02.00,Default,,0,0,0,,One line",
                "Dialogue: 0,0:00:02.50,0:00:04.00,Default,,0,0,6,,The first line\\Nand the second",
            ]
        );
        assert!(script.contains("\nPlayResX: 691\nPlayResY: 288\n"));
    }

    /// Before mpv reports the screen, no cue has a margin of its own.
    #[test]
    fn without_a_screen_every_cue_keeps_the_style_margin() {
        let copy = Copy::converted(vec![Dialogue::text(0, 1000, "Two\\Nlines".to_string())]);
        assert!(
            copy.script(None, 0.0)
                .contains("Default,,0,0,0,,Two\\Nlines")
        );
    }

    /// A script's own header and events are kept. Only an event at the foot
    /// of the frame that the script does not place takes a margin.
    #[test]
    fn an_ass_event_that_the_script_places_keeps_its_margin() {
        let header = "\u{feff}[Script Info]\nScriptType: v4.00+\nPlayResY: 288\n\n[V4+ Styles]\n\
            Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n\
            Style: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,10,10,13,1\n\
            Style: Top,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,8,10,10,13,1\n";
        let two = |style: &str, margin: &str, text: &str| Dialogue {
            style: style.to_string(),
            margin_v: margin.to_string(),
            ..Dialogue::text(0, 1000, text.to_string())
        };
        let copy = Copy {
            header: header.to_string(),
            ssa: false,
            converted: false,
            events: vec![
                two("Default", "0", "Free\\Nlines"),
                two("Top", "0", "Top\\Nlines"),
                two("Default", "30", "Own\\Nmargin"),
                two("Default", "0", "{\\pos(10,10)}Placed\\Nlines"),
                two("Default", "0", "{\\an8}Raised\\Nlines"),
            ],
        };
        let script = copy.script(Some(&scope()), 0.0);
        let margins: Vec<&str> = script
            .lines()
            .filter_map(|line| line.strip_prefix("Dialogue: "))
            .map(|event| event.split(',').nth(7).unwrap_or_default())
            .collect();
        assert_eq!(margins, ["1", "0", "30", "0", "0"]);
        assert!(script.starts_with("[Script Info]\nScriptType"));
        assert_eq!(script.matches("[Script Info]").count(), 1);
    }
}
