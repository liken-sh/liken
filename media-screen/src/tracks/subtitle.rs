// The roles of the subtitle rule of root plan 90, as media-operator's
// command sidecar gives them in `subtitletracks.go`. This is a port of that
// file's classification, step for step. The tests of both programs read
// their cases from `media-operator/testdata/subtitle-rule.json`, so a change
// to one rule fails a test until the other agrees.
//
// A forced track translates the parts of the audio that are not in the
// audio's own language. A full or SDH track has every line of the film, and
// an SDH track also describes the sounds. A commentary track follows the
// commentary audio and not the film. An empty track has no events, so it
// shows nothing. The media browser chooses a track by these roles, and both
// screens draw the role beside the track's language.

use crate::language;

/// The fraction of the largest track in the same language, one fifth,
/// below which a track counts as forced. Root plan 90 has the survey of
/// event counts that the fraction separates.
const FORCED_SHARE: i64 = 5;

/// One subtitle track, with the fields of mpv's `track-list` that the rule
/// reads and the event count the probe recorded.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Track {
    /// The language tag, as the container or mpv's read of a file name
    /// gives it.
    pub lang: String,
    pub title: String,
    /// ffprobe's codec name, which is also the name mpv reports. An
    /// external file has none, and a track with none counts as text.
    pub codec: String,
    pub default: bool,
    pub forced: bool,
    pub hearing_impaired: bool,
    /// The base name of an external file, and nothing for a track of the
    /// video's own container.
    pub file: Option<String>,
    /// How many events the track has. No count is not a count of zero.
    pub events: Option<i64>,
}

/// The codecs of the subtitle tracks that are pictures: the PGS track of a
/// Blu-ray and the VobSub track of a DVD. mpv draws such a track as it is on
/// the disc, so the display's house style does not change its font. Every
/// other codec is text.
const IMAGE_CODECS: [&str; 2] = ["hdmv_pgs_subtitle", "dvd_subtitle"];

impl Track {
    /// Whether the track is a picture and not text, by its codec.
    pub fn is_image(&self) -> bool {
        IMAGE_CODECS.contains(&self.codec.as_str())
    }
}

/// The one role a track takes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Role {
    Empty,
    Forced,
    Commentary,
    Sdh,
    Full,
}

impl Role {
    /// The word media-operator's log line uses for the role.
    pub fn word(self) -> &'static str {
        match self {
            Self::Empty => "empty",
            Self::Forced => "forced",
            Self::Commentary => "commentary",
            Self::Sdh => "sdh",
            Self::Full => "full",
        }
    }

    /// The word a screen draws beside the track's language. A full track
    /// is the usual case, so it has no word. An empty track shows nothing,
    /// so a screen lists no row for it and has no word to draw.
    pub fn label(self) -> &'static str {
        match self {
            Self::Sdh => "SDH",
            Self::Forced => "forced",
            Self::Commentary => "commentary",
            Self::Full | Self::Empty => "",
        }
    }
}

/// One track with its role. The language is the canonical base language,
/// and it is empty when neither the track nor its file name names one.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Classified {
    pub track: Track,
    pub language: String,
    pub role: Role,
    forced: bool,
    sdh: bool,
    commentary: bool,
}

/// Every track with its role, in track order.
pub fn classify(tracks: &[Track]) -> Vec<Classified> {
    let mut classified: Vec<Classified> = tracks.iter().map(describe).collect();
    // The forced test by count compares each track with the largest track
    // in its language, so it runs after every track has its count.
    let largest = |language: &str, all: &[Classified]| {
        all.iter()
            .filter(|one| one.language == language)
            .filter_map(|one| one.track.events)
            .max()
            .unwrap_or(0)
    };
    let roles: Vec<Role> = classified
        .iter()
        .map(|one| role(one, largest(&one.language, &classified)))
        .collect();
    for (one, role) in classified.iter_mut().zip(roles) {
        one.role = role;
    }
    classified
}

// One track's language and markers. mpv reads the language and the markers
// from the name of an external file, except from a name that ends in a
// number, such as Movie.eng.0.srt. So the rule reads the name too, and uses
// what it finds where the track's own fields are empty.
fn describe(track: &Track) -> Classified {
    let mut language = language::canonical(&track.lang);
    let (mut forced, mut sdh) = (track.forced, track.hearing_impaired);
    let mut title = track.title.as_str();
    if let Some(name) = &track.file {
        let from_name = file_name(name);
        if language.is_empty() {
            language = from_name.language;
        }
        forced |= from_name.forced;
        sdh |= from_name.sdh;
        // mpv titles a file with no title of its own by the end of its
        // name, so the title says nothing that the name has not said, and a
        // word of the film's own name, such as Force, would read as a
        // marker.
        if name.ends_with(title) {
            title = "";
        }
    }
    let lower = title.to_lowercase();
    forced |= lower.contains("forc");
    sdh |= lower
        .split(|c: char| !c.is_alphabetic())
        .any(hearing_impaired_word);
    Classified {
        track: track.clone(),
        language,
        role: Role::Full,
        forced,
        sdh,
        commentary: lower.contains("commentary"),
    }
}

// The one role of a track, in the order of root plan 90: a track with no
// events is empty whatever its flags say, and a forced marker outranks a
// commentary title and an SDH marker. The count test applies only when both
// counts are known, because an absent count differs from zero.
fn role(one: &Classified, largest: i64) -> Role {
    match one.track.events {
        Some(0) => Role::Empty,
        _ if one.forced => Role::Forced,
        Some(events) if events * FORCED_SHARE < largest => Role::Forced,
        _ if one.commentary => Role::Commentary,
        _ if one.sdh => Role::Sdh,
        _ => Role::Full,
    }
}

// The three markers that Bazarr, mpv, and release groups write for a track
// with sound descriptions.
fn hearing_impaired_word(word: &str) -> bool {
    matches!(word.to_lowercase().as_str(), "sdh" | "cc" | "hi")
}

/// What the segments of an external file's name say.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct FileName {
    pub language: String,
    pub forced: bool,
    pub sdh: bool,
}

/// The segments of a name, read from the end, after the extension:
/// numbers, the markers `forced`, `sdh`, `hi`, `cc`, and `default`, and then
/// one language. The media's own name can contain dots, so the walk stops
/// at the first segment that is none of these.
pub fn file_name(name: &str) -> FileName {
    let mut parsed = FileName::default();
    let stem = name.rsplit_once('.').map_or(name, |(stem, _)| stem);
    let segments: Vec<&str> = stem.split('.').collect();
    // The first segment is the media's name, never a language or a marker.
    for segment in segments.iter().skip(1).rev() {
        let segment = segment.to_lowercase();
        match segment.as_str() {
            number if !number.is_empty() && number.chars().all(|c| c.is_ascii_digit()) => {}
            "forced" => parsed.forced = true,
            marker if hearing_impaired_word(marker) => parsed.sdh = true,
            "default" => {}
            _ => {
                parsed.language = file_name_language(&segment);
                return parsed;
            }
        }
    }
    parsed
}

// One segment as a language, when its primary subtag has two or three
// letters, the shape that mpv accepts in a file name.
fn file_name_language(segment: &str) -> String {
    let primary = segment.split('-').next().unwrap_or_default();
    match (2..=3).contains(&primary.len()) {
        true => language::canonical(segment),
        false => String::new(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_screen_draws_a_word_for_every_role_but_full_and_empty() {
        let cases = [
            (Role::Sdh, "SDH"),
            (Role::Forced, "forced"),
            (Role::Commentary, "commentary"),
            (Role::Full, ""),
            (Role::Empty, ""),
        ];
        for (role, label) in cases {
            assert_eq!(role.label(), label, "{role:?}");
        }
    }

    #[test]
    fn a_pgs_or_vobsub_track_is_an_image_and_any_other_codec_is_text() {
        let track = |codec: &str| Track {
            codec: codec.into(),
            ..Track::default()
        };
        assert!(track("hdmv_pgs_subtitle").is_image());
        assert!(track("dvd_subtitle").is_image());
        assert!(!track("subrip").is_image());
        assert!(!track("").is_image());
    }
}
