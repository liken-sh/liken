// The files section of a movie's page: one block for each video file the
// movie holds, with its picture, its sound tracks, and its subtitle tracks.
// A title can hold several encodings of one film, and a person reads here
// which one plays and what it plays with. The block of the file a play
// takes, and the audio and subtitle tracks in it that the play takes,
// carry the accent's dot, and every other row draws fainter.
//
// Every function here is pure over the rows, so the tests need no window.

mod draw;
mod labels;

pub use draw::draw;

use crate::catalog::FileFacts;
use crate::catalog::stream::{Stream, Tracks};
use crate::catalog::{Subtitle, Track as Entry};
use crate::language;
use crate::look;
use crate::screens::{facts, foot};
use crate::tracks::subtitle::{self, Choice, Role};
use crate::tracks::{Preferences, audio};
use crate::views::text;

/// The heading over the section, in the words the stripes' headings use.
pub const HEADING: &str = "Files";

// The two labels of a block's groups of rows.
const SOUND: &str = "Sound";
const SUBTITLES: &str = "Subtitles";

// The kinds of file and stream the section reads.
const VIDEO: &str = "video";
const AUDIO: &str = "audio";
const SUBTITLE: &str = "subtitle";
const PRIMARY: &str = "primary";

// The word that names a track whose tag names no language.
const UNKNOWN: &str = "Unknown";

/// One row of a group: a track's language, what it is, its role, and
/// whether a play takes it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Row {
    pub language: String,
    pub detail: String,
    pub role: &'static str,
    pub plays: bool,
}

/// One group of rows under its label.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Group {
    pub label: &'static str,
    pub rows: Vec<Row>,
}

/// One video file's block.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Block {
    /// A short name for the encoding, such as `4K` or `Bluray-1080p`.
    pub name: String,
    /// The container and the size, as in `MKV · 14.3 GB`, drawn faint at
    /// the right of the name.
    pub aside: String,
    /// The frame, the codec, the dynamic range, and the frame rate.
    pub picture: String,
    pub groups: Vec<Group>,
    /// Whether a play of the movie takes this file.
    pub plays: bool,
}

/// The section: every primary video file's block, in path order.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Files {
    pub blocks: Vec<Block>,
}

impl Files {
    /// The section of one movie. `videos` are the movie's files with the
    /// tracks of each, `playing` is the path a play of the movie takes, and
    /// `preferences` are the `Player`'s. A file that is not a primary
    /// video, such as a trailer or a subtitle file, takes no block.
    pub fn of(
        videos: &[(FileFacts, Tracks)],
        playing: Option<&str>,
        preferences: &Preferences,
    ) -> Self {
        let blocks = videos
            .iter()
            .filter(|(file, _)| file.kind == VIDEO && file.role == PRIMARY)
            .map(|(file, tracks)| {
                let plays = playing == Some(file.path.as_str());
                block(file, tracks, plays.then_some(preferences))
            })
            .collect();
        Self { blocks }
    }

    /// The height the section takes, and zero where the movie holds no
    /// video file.
    pub fn height(&self) -> f32 {
        if self.blocks.is_empty() {
            return 0.0;
        }
        let rows = self.blocks.chunks(COLUMNS);
        let tall = rows
            .map(|pair| pair.iter().map(Block::height).fold(0.0, f32::max))
            .sum::<f32>();
        let gaps = (self.blocks.len().div_ceil(COLUMNS) - 1) as f32 * ROW_GAP;
        text::height(1, look::HEADING) + HEADING_GAP + tall + gaps
    }
}

/// The section's measures, in logical pixels.
pub(crate) const COLUMNS: usize = 2;
// The space between the heading and the first row of blocks.
pub(crate) const HEADING_GAP: f32 = 26.0;
// The space between two columns, and between two rows of blocks.
pub(crate) const COLUMN_GAP: f32 = 96.0;
pub(crate) const ROW_GAP: f32 = 48.0;
// The widest the two columns grow, so a row of a group stays a line a person
// reads across on a wide panel.
pub(crate) const MOST_WIDTH: f32 = 1_560.0;
// The space under a block's name, over its rule.
pub(crate) const UNDER_NAME: f32 = 14.0;
// The space over and under the picture line.
pub(crate) const OVER_PICTURE: f32 = 12.0;
pub(crate) const UNDER_PICTURE: f32 = 6.0;
// The space over and under the rows of a group, and between two rows.
pub(crate) const GROUP_PAD: f32 = 12.0;
pub(crate) const ROW_SPACE: f32 = 6.0;
// The width of a rule.
pub(crate) const RULE: f32 = 1.0;

// The sizes of the block's lines.
pub(crate) const NAME: f32 = look::FACTS;
pub(crate) const ROW: f32 = look::CAPTION;
pub(crate) const SMALL: f32 = look::FACE;

impl Block {
    /// The height the block takes: the name and its rule, the picture
    /// line, and each group with its rule.
    pub fn height(&self) -> f32 {
        let head = text::height(1, NAME) + UNDER_NAME + RULE;
        let picture = OVER_PICTURE + text::height(1, SMALL) + UNDER_PICTURE;
        let groups: f32 = self.groups.iter().map(Group::height).sum();
        head + picture + groups
    }
}

impl Group {
    /// The height the group takes, its rule included.
    pub fn height(&self) -> f32 {
        let rows = self.rows.len() as f32;
        2.0 * GROUP_PAD + rows * text::height(1, ROW) + (rows - 1.0).max(0.0) * ROW_SPACE + RULE
    }
}

// One file's block. `preferences` are there only for the file a play takes,
// because only its tracks carry a dot.
fn block(file: &FileFacts, tracks: &Tracks, preferences: Option<&Preferences>) -> Block {
    let video = tracks.streams.iter().find(|stream| stream.kind == VIDEO);
    let sounds: Vec<&Stream> = of_kind(tracks, AUDIO);
    let heard = preferences.and_then(|preferences| {
        let candidates: Vec<audio::Track> = sounds
            .iter()
            .map(|stream| audio::Track {
                lang: stream.language.clone(),
                default: stream.default,
            })
            .collect();
        audio::choose(&candidates, &preferences.audio_languages)
    });
    let sound = sounds
        .iter()
        .enumerate()
        .map(|(index, stream)| Row {
            language: named(&stream.language),
            detail: labels::sound(stream),
            role: match stream.title.to_lowercase().contains("commentary") {
                true => "commentary",
                false => "",
            },
            plays: heard == Some(index),
        })
        .collect();
    let audio_language = heard.map_or("", |index| sounds[index].language.as_str());
    let subtitles = subtitle_rows(tracks, audio_language, preferences);
    let groups = [(SOUND, sound), (SUBTITLES, subtitles)]
        .into_iter()
        .filter(|(_, rows): &(_, Vec<Row>)| !rows.is_empty())
        .map(|(label, rows)| Group { label, rows })
        .collect();
    Block {
        name: labels::short_name(file, video),
        aside: facts::joined(&[&file.container.to_uppercase(), &foot::size(file.size_bytes)]),
        picture: labels::picture(file, video),
        groups,
        plays: preferences.is_some(),
    }
}

fn of_kind<'a>(tracks: &'a Tracks, kind: &str) -> Vec<&'a Stream> {
    tracks
        .streams
        .iter()
        .filter(|stream| stream.kind == kind)
        .collect()
}

// The subtitle rows of one file: its embedded subtitle streams in container
// order, then the text files beside it that mpv loads, the order of mpv's
// track list. The rule classifies and chooses over every track, and an
// empty track then takes no row, because it shows nothing.
//
// The catalog records no hearing-impaired flag of an embedded stream, so a
// track whose only SDH marker is that flag reads as a full track here, and
// as SDH in media-operator, which reads the flag from mpv.
fn subtitle_rows(
    tracks: &Tracks,
    audio_language: &str,
    preferences: Option<&Preferences>,
) -> Vec<Row> {
    let embedded = of_kind(tracks, SUBTITLE);
    let rule: Vec<subtitle::Track> = embedded
        .iter()
        .map(|stream| subtitle::Track {
            lang: stream.language.clone(),
            title: stream.title.clone(),
            default: stream.default,
            forced: stream.forced,
            events: stream.events,
            ..subtitle::Track::default()
        })
        .chain(tracks.beside.iter().filter_map(external))
        .collect();
    let classified = subtitle::classify(&rule);
    let shown = preferences
        .filter(|preferences| preferences.chooses_subtitles())
        .map(|preferences| subtitle::choose(&classified, audio_language, preferences));
    let details = embedded
        .iter()
        .map(|stream| labels::subtitle(&stream.codec, false))
        .chain(tracks.beside.iter().filter_map(|entry| match &entry.track {
            Entry::File(name) => Some(labels::subtitle(extension(name), true)),
            Entry::Stream(_) => None,
        }));
    classified
        .iter()
        .zip(details)
        .enumerate()
        .filter(|(_, (one, _))| one.role != Role::Empty)
        .map(|(index, (one, format))| Row {
            language: named(&one.language),
            detail: facts::joined(&[&format, &labels::lines(one.track.events)]),
            role: match one.role {
                Role::Sdh => "SDH",
                Role::Forced => "forced",
                Role::Commentary => "commentary",
                Role::Full | Role::Empty => "",
            },
            plays: shown == Some(Choice::Track(index)),
        })
        .collect()
}

// A subtitle file beside the video, as the rule reads it. mpv reads the
// language and the markers from the file's name, and the rule reads the
// same name when the track's own fields are empty, so the track carries the
// name alone. mpv titles such a file by the end of its name, which the rule
// reads as no title.
fn external(entry: &Subtitle) -> Option<subtitle::Track> {
    match &entry.track {
        Entry::File(name) => Some(subtitle::Track {
            file: Some(name.clone()),
            events: entry.events,
            ..subtitle::Track::default()
        }),
        Entry::Stream(_) => None,
    }
}

fn extension(name: &str) -> &str {
    name.rsplit_once('.').map_or("", |(_, extension)| extension)
}

// A track's language as a person reads it.
fn named(tag: &str) -> String {
    match tag.is_empty() {
        true => UNKNOWN.to_string(),
        false => language::name(tag).to_string(),
    }
}

#[cfg(test)]
mod tests;
