// The choice of the subtitle rule of root plan 90, as media-operator's
// command sidecar applies it in `subtitletracks.go`. This is a port of that
// file's choice, step for step. The roles that the choice reads are in
// media-screen's `tracks::subtitle`, because the film's display draws them
// too. The tests of both programs read their cases from
// `media-operator/testdata/subtitle-rule.json`, so a change to one rule
// fails a test until the other agrees.
//
// A forced track translates the parts of the audio that are not in the
// audio's own language, so it belongs to the audio track. A full or SDH
// track belongs to the reader. The rule takes a full or SDH track in the
// order of the subtitle languages first. When no such track exists, it
// takes the forced track of the audio that plays, if a person in the
// household hears that language. Among tracks of the same role, a text
// track goes before an image track. A commentary track is the last choice,
// because its text follows the commentary audio and not the film. An empty
// track is never a choice.

pub use media_screen::tracks::subtitle::{Classified, Role, Track, classify};

use media_screen::language;

use super::preferences::{AUTO, AVOID, PREFER, Preferences};

/// The track the rule chose, by its index in the list. `None` shows no
/// subtitles, and `Keep` sets nothing, so mpv's own choice stands.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Choice {
    Track(usize),
    None,
    Keep,
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
// roles: by the rank of its role, then a text track before an image track,
// then the default flag, then track order. The display draws a text track
// in the house style and an image track as the disc made it, so a text
// track is the better pick when the role is the same. The role still
// outranks the kind, so with `sdh: prefer` an SDH image track goes before
// a full text track.
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
        .min_by_key(|(_, one)| {
            (
                rank(sdh, one.role),
                one.track.is_image(),
                !one.track.default,
            )
        })
        .map(|(index, _)| index)
}
