// The audio track mpv takes when a play starts. media-operator hands mpv
// the `audioLanguages` as `--alang`, and mpv ranks the audio tracks of a
// file by the position of their language in that list, then by the default
// flag, then by their order in the file. A file with no track in any of the
// languages plays its default track, or its first. This is that ranking in
// short: mpv also weighs a track's other flags, which the probe does not
// record, so a file whose tracks differ only in those can play another
// track than the page marks.

use crate::language;

/// One audio track, with the two fields the choice reads.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Track {
    pub lang: String,
    pub default: bool,
}

/// The index of the track a play takes, or nothing for a file with no
/// audio track.
pub fn choose(tracks: &[Track], languages: &[String]) -> Option<usize> {
    let first = |matches: &dyn Fn(&Track) -> bool| {
        tracks
            .iter()
            .enumerate()
            .filter(|(_, track)| matches(track))
            .min_by_key(|(_, track)| !track.default)
            .map(|(index, _)| index)
    };
    languages
        .iter()
        .find_map(|wanted| first(&|track| language::same(&track.lang, wanted)))
        .or_else(|| first(&|_| true))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn track(lang: &str, default: bool) -> Track {
        Track {
            lang: lang.into(),
            default,
        }
    }

    fn english_then_spanish() -> Vec<String> {
        vec!["en".into(), "es".into()]
    }

    #[test]
    fn the_first_language_of_the_list_wins_over_the_default_flag() {
        let tracks = [track("spa", true), track("eng", false)];
        assert_eq!(choose(&tracks, &english_then_spanish()), Some(1));
    }

    #[test]
    fn the_default_flag_ranks_tracks_of_one_language() {
        let tracks = [track("eng", false), track("eng", true)];
        assert_eq!(choose(&tracks, &english_then_spanish()), Some(1));
    }

    #[test]
    fn a_file_with_no_track_in_a_language_plays_its_default_or_its_first() {
        let flagged = [track("jpn", false), track("fre", true)];
        let plain = [track("jpn", false), track("fre", false)];
        assert_eq!(choose(&flagged, &english_then_spanish()), Some(1));
        assert_eq!(choose(&plain, &english_then_spanish()), Some(0));
        assert_eq!(choose(&plain, &[]), Some(0));
    }

    #[test]
    fn a_file_with_no_audio_plays_no_track() {
        assert_eq!(choose(&[], &english_then_spanish()), None);
    }
}
