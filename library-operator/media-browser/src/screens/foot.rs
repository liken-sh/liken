// The foot of a title's page: the studios, and one line for every video
// file the title holds. A series page draws both. A movie's page draws the
// studios alone, and its files section shows each file in full.
//
// Each studio is a stop of its own, which opens the wall of every title
// the catalog holds from that studio. The browser's focus outline goes
// around the focused name, and the outline reaches past the name's box.
// So the names stand apart by more than that reach, from the words that
// lead them and from each other, and the outline never touches a word it
// is not about. The names are measured by the shaper once, at the read,
// so the boxes the outline goes around are the ones the names draw in.

// The More from strip, which takes one studio's place on the production
// line.
pub mod more;

use iced_wgpu::Renderer;
use iced_widget::canvas;
use iced_winit::core::{Point, Rectangle};

use crate::catalog::FileFacts;
use crate::language;
use crate::look;
use crate::screens::facts;
use crate::views::{area, text};

/// The size the production line draws at, and the smaller size the file
/// lines draw at under it, so the technical detail reads second.
pub const PRODUCTION: f32 = look::CAPTION;
pub const DETAIL: f32 = look::FACE;

// The space between the production line and the first file line.
const GAP: f32 = 8.0;

/// The words that lead the studios on the production line, in the faint
/// face, so the names read first.
pub const PRODUCED_BY: &str = "Produced by";

// How far the focus outline reaches past the box of the name it goes
// around: the gap `views::marked` leaves, and the whole stroke.
const REACH: f32 = look::MARK_GAP + look::MARK;

// The space between the leading words and the first name: the outline's
// reach, and room after it, so the outline on the first name stays clear
// of "Produced by".
const AFTER_PREFIX: f32 = REACH + 6.0;

// The space between two names on one line, with the dot that parts them
// in its middle: the outline's reach at each side, and the dot's own room
// between the two reaches.
const BETWEEN: f32 = 2.0 * REACH + 16.0;

// The space between two lines of names, so the outline on a name of the
// lower line stays clear of the line over it.
const LINE_GAP: f32 = REACH + 4.0;

// The mark that parts two names on one line.
const DOT: &str = "\u{b7}";

// The two categories of the files table the foot reads, and the role a
// title's own video file holds. A trailer is a video of the title too,
// and the foot names the files the title itself is.
const VIDEO: &str = "video";
const SUBTITLE: &str = "subtitle";
const PRIMARY: &str = "primary";

// The word that leads the subtitle languages on a file's line.
const SUBTITLES: &str = "Subtitles: ";

/// The foot of one title: the studios on the production line, then one
/// line for every video file.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Foot {
    studios: Vec<Studio>,
    // The width the leading words draw at, and zero on a foot that names
    // no studio.
    prefix: f32,
    files: Vec<String>,
}

// One studio's name and the width the shaper sets it at.
#[derive(Debug, Clone, PartialEq)]
struct Studio {
    name: String,
    width: f32,
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
        let studios: Vec<Studio> = studios
            .iter()
            .filter(|name| !name.is_empty())
            .map(|name| Studio {
                name: name.clone(),
                width: text::measured(name, PRODUCTION),
            })
            .collect();
        let prefix = match studios.is_empty() {
            true => 0.0,
            false => text::measured(PRODUCED_BY, PRODUCTION),
        };
        Self {
            studios,
            prefix,
            files,
        }
    }

    /// The studios, in the order the body names them. Each one is a stop.
    pub fn studios(&self) -> impl Iterator<Item = &str> {
        self.studios.iter().map(|studio| studio.name.as_str())
    }

    /// The name of one studio, or nothing past the last.
    pub fn studio(&self, index: usize) -> Option<&str> {
        self.studios.get(index).map(|studio| studio.name.as_str())
    }

    /// The line of every video file, in the order the catalog answered
    /// them.
    pub fn files(&self) -> &[String] {
        &self.files
    }

    /// The box each studio's name draws in, for a foot whose top left is
    /// at `at` and that is this wide. The names follow the leading words
    /// on one line, and a name that would run past the width starts the
    /// next line under the first name.
    pub fn placed(&self, at: Point, width: f32) -> Vec<Rectangle> {
        let line = text::height(1, PRODUCTION);
        let start = at.x + self.prefix + AFTER_PREFIX;
        let (mut x, mut y) = (start, at.y);
        let mut boxes = Vec::with_capacity(self.studios.len());
        for (index, studio) in self.studios.iter().enumerate() {
            if index > 0 {
                x += BETWEEN;
                if x + studio.width > at.x + width {
                    x = start;
                    y += line + LINE_GAP;
                }
            }
            boxes.push(area(x, y, studio.width, line));
            x += studio.width;
        }
        boxes
    }

    /// The height the block takes at this width, and zero where the title
    /// carries no line at all.
    pub fn height(&self, width: f32) -> f32 {
        let production = self
            .placed(Point::ORIGIN, width)
            .last()
            .map_or(0.0, |last| last.y + last.height);
        let files: f32 = self
            .files
            .iter()
            .map(|file| text::height(text::lines(file, DETAIL, width), DETAIL))
            .sum();
        let gap = match production > 0.0 && !self.files.is_empty() {
            true => GAP,
            false => 0.0,
        };
        production + gap + files
    }

    /// Draw the block with its top left at `at`, this wide: the leading
    /// words faint, the names at full ink with a faint dot between two on
    /// one line, and the file lines faint and smaller under them.
    pub fn draw(&self, frame: &mut canvas::Frame<Renderer>, at: Point, width: f32) {
        let boxes = self.placed(at, width);
        if !boxes.is_empty() {
            text::line(frame, PRODUCED_BY, at, PRODUCTION, look::faint(), width);
        }
        for (index, (studio, place)) in self.studios.iter().zip(&boxes).enumerate() {
            text::line(
                frame,
                &studio.name,
                place.position(),
                PRODUCTION,
                look::text(),
                f32::INFINITY,
            );
            let Some(next) = boxes.get(index + 1).filter(|next| next.y == place.y) else {
                continue;
            };
            let band = area(
                place.x + place.width,
                place.y,
                next.x - place.x - place.width,
                place.height,
            );
            text::centered(frame, DOT, band, PRODUCTION, look::faint());
        }
        let mut y = boxes.last().map_or(at.y, |last| last.y + last.height + GAP);
        for file in &self.files {
            y += text::line(
                frame,
                file,
                Point::new(at.x, y),
                DETAIL,
                look::faint(),
                width,
            );
        }
    }
}

// The frame of one file, and nothing where the scanner read no size.
fn frame(file: &FileFacts) -> String {
    match file.width > 0 && file.height > 0 {
        true => format!("{}\u{d7}{}", file.width, file.height),
        false => String::new(),
    }
}

/// One file's size, in the units a person reads a film's size in, and
/// nothing where the scanner read none. The files section of a movie's page
/// states each file's size the same way.
pub(crate) fn size(bytes: i64) -> String {
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
        let language = language::name(&file.language);
        if !named.contains(&language) {
            named.push(language);
        }
    }
    match named.is_empty() {
        true => String::new(),
        false => format!("{SUBTITLES}{}", named.join(", ")),
    }
}

#[cfg(test)]
mod tests;
