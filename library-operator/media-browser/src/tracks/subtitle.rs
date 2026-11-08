// The subtitle rule of plan 90, as media-operator's command sidecar applies
// it in `subtitletracks.go`. This is a port of that file, step for step,
// and both programs' tests read their cases from
// `media-operator/testdata/subtitle-rule.json`, so a change to one rule
// fails a test until the other agrees.
//
// A forced track translates the parts of the audio that are not in the
// audio's own language, so it belongs to the audio track. A full or SDH
// track belongs to the reader. The rule takes a full or SDH track in the
// order of the subtitle languages first. When no such track exists, it
// takes the forced track of the audio that plays, if a person in the
// household hears that language. A commentary track is the last choice,
// because its text follows the commentary audio and not the film. An empty
// track is never a choice.

use super::preferences::{AUTO, AVOID, PREFER, Preferences};
use crate::language;

/// The fraction of the largest track in the same language, one fifth,
/// below which a track counts as forced. Plan 90 holds the survey of event
/// counts that the fraction separates.
const FORCED_SHARE: i64 = 5;

/// One subtitle track, with the fields of mpv's `track-list` that the rule
/// reads and the event count the probe recorded.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Track {
    /// The language tag, as the container or mpv's read of a file name
    /// gives it.
    pub lang: String,
    pub title: String,
    pub default: bool,
    pub forced: bool,
    pub hearing_impaired: bool,
    /// The base name of an external file, and nothing for a track of the
    /// video's own container.
    pub file: Option<String>,
    /// How many events the track holds. No count is not a count of zero.
    pub events: Option<i64>,
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

/// The track the rule chose, by its index in the list. `None` shows no
/// subtitles, and `Keep` sets nothing, so mpv's own choice stands.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Choice {
    Track(usize),
    None,
    Keep,
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

// The one role of a track, in the order of plan 90: a track with no events
// is empty whatever its flags say, and a forced marker outranks a commentary
// title and an SDH marker. The count test applies only when both counts are
// known, because an absent count differs from zero.
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
/// one language. The media's own name can hold dots, so the walk stops at
/// the first segment that is none of these.
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

/// The track the rule chooses among the classified tracks of one file.
/// `audio` is the language of the audio track that plays.
///
/// The reading languages are `subtitleLanguages`. A household that states
/// none reads the languages it hears, so `audioLanguages` take their place.
/// With neither list, the rule has no language to choose by, and mpv's own
/// choice stands.
pub fn choose(tracks: &[Classified], audio: &str, preferences: &Preferences) -> Choice {
    let reading = match preferences.subtitle_languages.is_empty() {
        true => &preferences.audio_languages,
        false => &preferences.subtitle_languages,
    };
    if reading.is_empty() {
        return Choice::Keep;
    }
    let sdh = preferences.sdh.as_str();
    let forced = best(
        tracks,
        |language| language::same(language, audio),
        "",
        &[Role::Forced],
    );
    // In auto, audio in the household's first language needs only the
    // forced track that translates the rest of it.
    if preferences.subtitles == AUTO
        && preferences
            .audio_languages
            .first()
            .is_some_and(|first| language::same(audio, first))
    {
        return forced.map_or(Choice::None, Choice::Track);
    }
    let in_language = |wanted: &str, sdh: &str, roles: &[Role]| {
        best(
            tracks,
            |language| language::same(language, wanted),
            sdh,
            roles,
        )
    };
    let readers = [Role::Full, Role::Sdh];
    if let Some(pick) = reading
        .iter()
        .find_map(|wanted| in_language(wanted, sdh, &readers))
    {
        return Choice::Track(pick);
    }
    if let Some(pick) = forced.filter(|_| {
        preferences
            .audio_languages
            .iter()
            .any(|wanted| language::same(audio, wanted))
    }) {
        return Choice::Track(pick);
    }
    // A forced track in a language the household reads, under audio in
    // another language, translates less than the film needs. It still shows
    // some of the film's text, and a track flagged forced is often the full
    // translation of a foreign show with a wrong flag.
    if let Some(pick) = reading
        .iter()
        .find_map(|wanted| in_language(wanted, "", &[Role::Forced]))
    {
        return Choice::Track(pick);
    }
    // A track that names no language is usually in the language of the
    // release. It is a better choice than none, and a commentary track in a
    // known language follows the commentary audio and not the film.
    if let Some(pick) = best(tracks, str::is_empty, sdh, &readers) {
        return Choice::Track(pick);
    }
    reading
        .iter()
        .find_map(|wanted| in_language(wanted, "", &[Role::Commentary]))
        .map_or(Choice::None, Choice::Track)
}

// The rank of the full and SDH roles under the sdh preference. The two rank
// the same with no preference.
fn rank(sdh: &str, role: Role) -> u8 {
    match (sdh, role) {
        (PREFER, Role::Sdh) | (AVOID, Role::Full) => 0,
        (PREFER | AVOID, _) => 1,
        _ => 0,
    }
}

// The first track whose language matches and whose role is one of the
// roles: by the rank of its role, then the default flag, then track order.
fn best(
    tracks: &[Classified],
    matches: impl Fn(&str) -> bool,
    sdh: &str,
    roles: &[Role],
) -> Option<usize> {
    tracks
        .iter()
        .enumerate()
        .filter(|(_, one)| roles.contains(&one.role) && matches(&one.language))
        .min_by_key(|(_, one)| (rank(sdh, one.role), !one.track.default))
        .map(|(index, _)| index)
}
