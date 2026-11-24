//! The rows of a track panel and the value that the strip shows for the
//! track that plays. A row names a track in four parts, the parts that the
//! media browser writes on a film's page, from the same code in
//! `media-screen`: the language, the role, the format, and the title when
//! the title says something that the other parts do not.
//!
//! The rows are in an order that a viewer can scan: the language that
//! plays first, and within a language the usual track before the special
//! ones.

use media_screen::language;
use media_screen::tracks::format;
use media_screen::tracks::subtitle::{self, Role};

use crate::film::{Film, Track as Entry};
use crate::streams::Streams;
use crate::track::Track;

/// The first row of a subtitle list, so a viewer can always turn the
/// subtitles off.
pub const OFF: &str = "Off";

/// One row of a track panel.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Row {
    /// The track's mpv id, or `None` for the `Off` row.
    pub id: Option<i64>,
    /// The English name of the track's language. A track with no language
    /// shows its title, and a track with neither shows `Track 3`.
    pub language: String,
    /// The role word, drawn in italics: `SDH`, `forced`, `commentary`, or
    /// `described`. A full track or a usual audio track has none.
    pub role: &'static str,
    /// The format, such as `E-AC3 Atmos 5.1` or `SRT file`.
    pub format: String,
    /// The title, when it has a word that the other parts do not say.
    pub title: String,
    /// The track's number, when another row reads the same.
    pub number: Option<i64>,
    pub playing: bool,
    /// Whether the track names a language. A track with none shows its
    /// title or its number in `language`, and that text is not a group.
    pub has_language: bool,
    /// The language's code, such as `ENG`, which the strip shows when
    /// the language's name does not fit.
    pub(crate) code: String,
    /// The channels of an audio track, such as `5.1`.
    pub(crate) channels: String,
}

impl Row {
    /// The texts that the strip can show for this track, longest first:
    /// the language with the role and the format, the language with the
    /// role, or with the channels when the track has no role, the
    /// language alone, and the language's code. The strip takes the first
    /// that fits. A subtitle value has no format, because the viewer
    /// chooses subtitles by their language and role, and the panel shows
    /// the format.
    pub fn values(&self, kind: Track) -> Vec<Value> {
        let value = |name: &str, role: &'static str, detail: &str| Value {
            name: name.to_string(),
            role,
            detail: detail.to_string(),
        };
        let language = self.language.as_str();
        let mut values = match (kind, self.role) {
            (Track::Audio, "") => vec![
                value(language, "", &self.format),
                value(language, "", &self.channels),
            ],
            (Track::Audio, role) => vec![
                value(language, role, &self.format),
                value(language, role, ""),
            ],
            (Track::Subtitles, role) => vec![value(language, role, "")],
            (Track::Video, _) => Vec::new(),
        };
        values.extend([value(language, "", ""), value(&self.code, "", "")]);
        values.dedup();
        values.retain(|value| !value.name.is_empty());
        values
    }
}

/// One text that the strip can show for a track: the name, the role in
/// italics, and a detail in smaller type, the format or the channels.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Value {
    pub name: String,
    pub role: &'static str,
    pub detail: String,
}

#[cfg(test)]
impl Value {
    /// The parts joined by spaces, as a person reads them, which the tests
    /// compare.
    pub fn text(&self) -> String {
        [self.name.as_str(), self.role, &self.detail]
            .iter()
            .filter(|part| !part.is_empty())
            .copied()
            .collect::<Vec<_>>()
            .join(" ")
    }
}

/// The languages of `rows` in their order, each once, and whether a track
/// of that language plays. A long panel names the languages of the rows
/// that are scrolled out of view, so the viewer knows what is above and
/// below.
pub fn languages(rows: &[Row]) -> Vec<(String, bool)> {
    let mut languages: Vec<(String, bool)> = Vec::new();
    for row in rows {
        match languages.last_mut() {
            Some((language, plays)) if *language == row.language => *plays |= row.playing,
            _ => languages.push((row.language.clone(), row.playing)),
        }
    }
    languages
}

/// Whether the row at `at` writes its language. A language is written
/// once for its group of rows, on the first row of the group that shows,
/// and the rows under it show only what differs.
pub fn names_its_language(rows: &[Row], at: usize, first_shown: usize) -> bool {
    rows[at].has_language
        && (at == first_shown || at == 0 || rows[at - 1].language != rows[at].language)
}

/// Whether every track of the row's language has the row's format. Then
/// the format is written once, beside the language, and the other rows of
/// the group leave it out. When the formats differ, each row shows its own,
/// so no row's format is only implied.
pub fn one_format(rows: &[Row], at: usize) -> bool {
    let row = &rows[at];
    row.has_language
        && rows
            .iter()
            .filter(|other| other.has_language && other.language == row.language)
            .all(|other| other.format == row.format)
}

/// The rows of one kind of track, in the order that the panel lists them.
/// A subtitle list starts with `Off`, and a subtitle track with no events
/// is left out unless it plays, because it shows nothing.
pub fn rows(kind: Track, film: &Film, streams: &Streams) -> Vec<Row> {
    let entries: Vec<&Entry> = film.tracks_of(kind.kind()).collect();
    let mut rows = match kind {
        Track::Audio => audio(&entries, streams),
        Track::Subtitles => subtitles(&entries, film, streams),
        Track::Video => entries.iter().map(|entry| video(entry)).collect(),
    };
    number_the_same(&mut rows);
    if kind == Track::Subtitles {
        let off = Row {
            language: OFF.to_string(),
            code: OFF.to_string(),
            playing: !entries.iter().any(|entry| entry.selected),
            ..Row::default()
        };
        rows.insert(0, off);
    }
    rows
}

/// The row of the track that plays, or `None` when none plays.
pub fn playing(rows: &[Row]) -> Option<&Row> {
    rows.iter().find(|row| row.playing)
}

/// The base language of a tag, as the subtitle rule reads it, or nothing
/// for a code that names no one language: `mul` for several, `zxx` for no
/// language at all, as on a music track, and `mis` for a language with no
/// code. A track tagged with one of these reads as a track with no
/// language, by its title or its number.
fn spoken(tag: &str) -> String {
    match language::canonical(tag).as_str() {
        "mul" | "zxx" | "mis" => String::new(),
        code => code.to_string(),
    }
}

/// A row's place in the order: the group of its language, the language's
/// English name, the rank of its role, and the track's place in the file.
type Order = (u8, String, u8, usize);

fn sorted(mut rows: Vec<(Order, Row)>) -> Vec<Row> {
    rows.sort_by(|a, b| a.0.cmp(&b.0));
    rows.into_iter().map(|(_, row)| row).collect()
}

/// The group of a language: the languages in `first`, in that order, then
/// every other named language, then the tracks with no language.
fn group(code: &str, first: &[&str]) -> u8 {
    match first.iter().position(|one| !one.is_empty() && *one == code) {
        Some(at) => at as u8,
        None if code.is_empty() => first.len() as u8 + 1,
        None => first.len() as u8,
    }
}

/// The audio rows: the language that plays first, then the others by
/// name. Within a language, the described tracks and the commentaries
/// follow the others, and tracks of one role keep the file's order.
fn audio(entries: &[&Entry], streams: &Streams) -> Vec<Row> {
    let heard = entries
        .iter()
        .find(|entry| entry.selected)
        .map(|entry| spoken(&entry.lang.clone().unwrap_or_default()))
        .unwrap_or_default();
    let rows = entries
        .iter()
        .enumerate()
        .map(|(at, entry)| {
            let code = spoken(&entry.lang.clone().unwrap_or_default());
            let role = audio_role(entry);
            let (format, channels) = match streams.sound(entry) {
                Some(sound) => (
                    format::audio(&sound.codec, &sound.profile, &sound.layout, sound.channels),
                    format::audio("", "", &sound.layout, sound.channels),
                ),
                None => {
                    let channels = entry.channels.unwrap_or(0);
                    let codec = entry.codec.as_deref().unwrap_or_default();
                    (
                        format::audio(codec, "", "", channels),
                        format::audio("", "", "", channels),
                    )
                }
            };
            let rank = match role {
                "" => 0,
                "described" => 1,
                _ => 2,
            };
            let row = named(entry, &code, role, format, channels);
            let order = (
                group(&code, &[&heard]),
                row.language.to_lowercase(),
                rank,
                at,
            );
            (order, row)
        })
        .collect();
    sorted(rows)
}

/// The role of an audio track: `commentary` from its title, and
/// `described` from the visual-impaired flag or its title.
fn audio_role(entry: &Entry) -> &'static str {
    let title = entry.title.as_deref().unwrap_or_default().to_lowercase();
    if title.contains("commentary") {
        "commentary"
    } else if entry.visual_impaired || title.contains("descri") {
        "described"
    } else {
        ""
    }
}

/// The subtitle rows: the language of the subtitles that play, then the
/// language of the audio that plays, then the others by name. Within a
/// language the order is full, SDH, forced, and commentary, and tracks of
/// one role keep the file's order. Each role comes from the subtitle rule
/// of root plan 90, which the command sidecar also applies.
fn subtitles(entries: &[&Entry], film: &Film, streams: &Streams) -> Vec<Row> {
    let rule: Vec<subtitle::Track> = entries
        .iter()
        .map(|entry| subtitle::Track {
            lang: entry.lang.clone().unwrap_or_default(),
            title: entry.title.clone().unwrap_or_default(),
            codec: entry.codec.clone().unwrap_or_default(),
            forced: entry.forced,
            hearing_impaired: entry.hearing_impaired,
            file: file_name(entry).map(str::to_string),
            events: streams.events(entry),
            ..subtitle::Track::default()
        })
        .collect();
    let classified = subtitle::classify(&rule);
    let shown = entries
        .iter()
        .zip(&classified)
        .find(|(entry, _)| entry.selected)
        .map(|(_, one)| spoken(&one.language))
        .unwrap_or_default();
    let heard = film
        .tracks_of("audio")
        .find(|entry| entry.selected)
        .map(|entry| spoken(&entry.lang.clone().unwrap_or_default()))
        .unwrap_or_default();
    let rows = entries
        .iter()
        .zip(&classified)
        .enumerate()
        .filter(|(_, (entry, one))| one.role != Role::Empty || entry.selected)
        .map(|(at, (entry, one))| {
            let format = match file_name(entry) {
                Some(name) => format::subtitle(extension(name), true),
                None => format::subtitle(entry.codec.as_deref().unwrap_or_default(), false),
            };
            let rank = match one.role {
                Role::Full | Role::Empty => 0,
                Role::Sdh => 1,
                Role::Forced => 2,
                Role::Commentary => 3,
            };
            let row = named(
                entry,
                &spoken(&one.language),
                one.role.label(),
                format,
                String::new(),
            );
            let order = (
                group(&spoken(&one.language), &[&shown, &heard]),
                row.language.to_lowercase(),
                rank,
                at,
            );
            (order, row)
        })
        .collect();
    sorted(rows)
}

/// A video row: the track's title, or `Track 2`.
fn video(entry: &Entry) -> Row {
    let name = entry
        .title
        .clone()
        .unwrap_or_else(|| format!("Track {}", entry.id));
    Row {
        id: Some(entry.id),
        language: name.clone(),
        code: name,
        playing: entry.selected,
        ..Row::default()
    }
}

/// A row with its language, its code, and the title that adds a word. A
/// track with no language shows its title, or `Track 3`, in place of the
/// language.
fn named(entry: &Entry, code: &str, role: &'static str, format: String, channels: String) -> Row {
    let title = own_title(entry);
    let has_language = !code.is_empty();
    let (language, code, title) = match (code.is_empty(), title) {
        (false, title) => {
            let name = language::name(code).to_string();
            let tag = entry.lang.clone().unwrap_or_else(|| code.to_string());
            let said = [name.as_str(), tag.as_str(), code, role, format.as_str()];
            let title = title
                .map(|title| without_role(title, role))
                .filter(|title| adds_a_word(title, &said));
            (name, tag.to_uppercase(), title)
        }
        (true, Some(title)) => (title.to_string(), title.to_string(), None),
        (true, None) => {
            let name = format!("Track {}", entry.id);
            (name.clone(), name, None)
        }
    };
    Row {
        id: Some(entry.id),
        language,
        role,
        format,
        title: title.unwrap_or_default().to_string(),
        number: None,
        playing: entry.selected,
        has_language,
        code,
        channels,
    }
}

/// The track's title, or `None` when it has none. mpv titles an external
/// file that has no title of its own with the end of the file's name, so
/// such a title says nothing that the name has not said.
fn own_title(entry: &Entry) -> Option<&str> {
    let title = entry.title.as_deref().filter(|title| !title.is_empty())?;
    match file_name(entry) {
        Some(name) if name.ends_with(title) => None,
        _ => Some(title),
    }
}

/// The title without the role word it starts with, as in "Commentary by
/// Ann Example", which says the role a second time beside the italic role.
/// A ":" or "-" after the word goes too, and then a "by" or "with". The
/// rest starts with a capital letter.
fn without_role(title: &str, role: &str) -> String {
    let words = match role {
        "commentary" => ["commentaries", "commentary"].as_slice(),
        "described" => ["audio description", "described", "description"].as_slice(),
        _ => [].as_slice(),
    };
    let lower = title.to_lowercase();
    let Some(word) = words.iter().find(|word| lower.starts_with(*word)) else {
        return title.to_string();
    };
    let rest = title[word.len()..].trim_start_matches([' ', ':', '-', '\u{2013}']);
    let rest = ["by ", "with "]
        .iter()
        .find_map(|joint| {
            rest.get(..joint.len())
                .filter(|start| start.eq_ignore_ascii_case(joint))
                .map(|_| &rest[joint.len()..])
        })
        .unwrap_or(rest);
    // What remains starts a phrase of its own, as in "Novelist Ann
    // Example", so its first letter is a capital.
    let mut letters = rest.trim().chars();
    letters
        .next()
        .map(|first| first.to_uppercase().chain(letters).collect())
        .unwrap_or_default()
}

/// The words that a title can have and still say nothing that the
/// language, the role, and the format do not: the markers of a role, the
/// names of codecs and of channel layouts in the forms that release groups
/// write, and the words that only say it is a track.
const SAID: [&str; 47] = [
    "sdh",
    "cc",
    "hi",
    "forced",
    "foreign",
    "full",
    "commentary",
    "described",
    "description",
    "descriptive",
    "ad",
    "dd",
    "ddp",
    "dolby",
    "digital",
    "plus",
    "atmos",
    "truehd",
    "dts",
    "hd",
    "ma",
    "hra",
    "x",
    "ac3",
    "eac3",
    "e",
    "aac",
    "flac",
    "pcm",
    "lpcm",
    "opus",
    "mp3",
    "stereo",
    "mono",
    "surround",
    "ch",
    "srt",
    "subrip",
    "pgs",
    "sup",
    "ass",
    "vobsub",
    "subtitles",
    "subtitle",
    "subs",
    "track",
    "audio",
];

/// Whether a title has a word, letters or digits, that none of the
/// `said` parts and none of `SAID` has. A number on its own, such as the
/// 5 and 1 of 5.1, says only the channels.
fn adds_a_word(title: &str, said: &[&str]) -> bool {
    let words = |text: &str| -> Vec<String> {
        text.split(|c: char| !c.is_alphanumeric())
            .filter(|word| !word.is_empty())
            .map(str::to_lowercase)
            .collect()
    };
    let known: Vec<String> = said.iter().flat_map(|part| words(part)).collect();
    words(title).iter().any(|word| {
        !word.chars().all(|c| c.is_ascii_digit())
            && !known.contains(word)
            && !SAID.contains(&word.as_str())
    })
}

/// When two rows read the same, each shows its track number, so the
/// viewer can tell them apart.
fn number_the_same(rows: &mut [Row]) {
    let reads = |row: &Row| {
        (
            row.language.clone(),
            row.role,
            row.format.clone(),
            row.title.clone(),
        )
    };
    let all: Vec<_> = rows.iter().map(reads).collect();
    for row in rows.iter_mut() {
        if all.iter().filter(|one| **one == reads(row)).count() > 1 {
            row.number = row.id;
        }
    }
}

/// The base name of an external file, and `None` for a track of the
/// video's own container.
fn file_name(entry: &Entry) -> Option<&str> {
    let path = entry
        .external_filename
        .as_deref()
        .filter(|_| entry.external)?;
    Some(path.rsplit('/').next().unwrap_or(path))
}

fn extension(name: &str) -> &str {
    name.rsplit_once('.').map_or("", |(_, extension)| extension)
}

#[cfg(test)]
mod tests;
