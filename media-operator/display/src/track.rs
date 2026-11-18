//! The audio, subtitle, and video track controls. Each control's cell shows
//! the selected track, and its chooser lists the tracks in mpv's
//! `track-list` property. Selecting an entry sets the mpv property that
//! picks the track of that kind.
//!
//! The three controls differ only in the `track-list` type they filter, the
//! text in their cell, and the property they set, so they share one type.

use serde_json::json;

use crate::film::Film;
use crate::ipc::Command;

/// The kind of track a control picks.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Track {
    Audio,
    Subtitles,
    Video,
}

/// The cell text when no track of that kind is selected.
const OFF: &str = "off";

/// The first entry of a subtitle list, so a viewer can always turn subtitles
/// off. Entry 0 sets `sid` to `no`, and entry `n` selects the subtitle track
/// at index `n - 1`.
const OFF_ENTRY: &str = "Off";

impl Track {
    /// The `track-list` `type` value this control lists.
    pub fn kind(self) -> &'static str {
        match self {
            Track::Audio => "audio",
            Track::Subtitles => "sub",
            Track::Video => "video",
        }
    }

    /// The mpv property that a select sets.
    pub fn property(self) -> &'static str {
        match self {
            Track::Audio => "aid",
            Track::Subtitles => "sid",
            Track::Video => "vid",
        }
    }

    /// Whether the control shows. The video control shows only when the file
    /// has more than one video track, such as an alternate angle. The other
    /// controls show when the file has at least one track of their kind.
    pub fn available(self, film: &Film) -> bool {
        let count = film.tracks_of(self.kind()).count();
        match self {
            Track::Video => count > 1,
            _ => count >= 1,
        }
    }

    /// The cell text. A subtitle cell shows only the language when the track
    /// has one, because the strip has room for one short line.
    pub fn value(self, film: &Film) -> String {
        let Some(track) = film.tracks_of(self.kind()).find(|track| track.selected) else {
            return OFF.to_string();
        };
        match (self, track.lang.as_ref()) {
            (Track::Subtitles, Some(lang)) => lang.to_uppercase(),
            _ => track.label(),
        }
    }

    /// The chooser entries, in `track-list` order. A subtitle list starts
    /// with an Off entry.
    pub fn entries(self, film: &Film) -> Vec<String> {
        let labels = film.tracks_of(self.kind()).map(|track| track.label());
        match self {
            Track::Subtitles => std::iter::once(OFF_ENTRY.to_string())
                .chain(labels)
                .collect(),
            _ => labels.collect(),
        }
    }

    /// The entry the chooser opens on, which is the playing track, or the
    /// first entry when none plays.
    pub fn opens_on(self, film: &Film) -> usize {
        self.playing(film).unwrap_or(0)
    }

    /// The entry of the playing track, which the chooser marks with a dot.
    /// A subtitle list with no selected track marks its Off entry. Any other
    /// list with no selected track marks nothing.
    pub fn playing(self, film: &Film) -> Option<usize> {
        let at = film.tracks_of(self.kind()).position(|track| track.selected);
        match (self, at) {
            (Track::Subtitles, Some(at)) => Some(at + 1),
            (Track::Subtitles, None) => Some(0),
            (_, at) => at,
        }
    }

    /// The mpv commands that switch to the entry at `selected`. Returns no
    /// commands when the entry is no longer in the list.
    pub fn apply(self, film: &Film, selected: usize) -> Vec<Command> {
        let write = |value: String| {
            vec![vec![
                json!("set_property"),
                json!(self.property()),
                json!(value),
            ]]
        };
        if self == Track::Subtitles {
            if selected == 0 {
                return write("no".to_string());
            }
            return match film.tracks_of(self.kind()).nth(selected - 1) {
                Some(track) => write(track.id.to_string()),
                None => Vec::new(),
            };
        }
        match film.tracks_of(self.kind()).nth(selected) {
            Some(track) => write(track.id.to_string()),
            None => Vec::new(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn film() -> Film {
        let mut film = Film::default();
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "video", "title": "Main", "selected": true },
                { "id": 1, "type": "audio", "lang": "eng", "selected": true },
                { "id": 2, "type": "audio", "title": "Commentary", "lang": "eng" },
                { "id": 1, "type": "sub", "lang": "spa" },
                { "id": 2, "type": "sub", "title": "Forced", "lang": "eng" },
            ]),
        );
        film
    }

    #[test]
    fn a_control_shows_when_the_file_has_its_tracks() {
        assert!(Track::Audio.available(&film()));
        assert!(Track::Subtitles.available(&film()));
        assert!(!Track::Video.available(&film()));
        assert!(!Track::Audio.available(&Film::default()));

        let mut angles = film();
        angles.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "video", "selected": true },
                { "id": 2, "type": "video" },
            ]),
        );
        assert!(Track::Video.available(&angles));
    }

    #[test]
    fn a_cell_shows_the_playing_track() {
        assert_eq!(Track::Audio.value(&film()), "ENG");
        assert_eq!(Track::Video.value(&film()), "Main");
        assert_eq!(Track::Subtitles.value(&film()), OFF);
        assert_eq!(Track::Audio.value(&Film::default()), OFF);
    }

    /// A subtitle cell shows the language, or the whole label when the
    /// track has no language.
    #[test]
    fn a_subtitle_cell_shows_its_language() {
        let mut film = film();
        film.apply(
            "track-list",
            &json!([{ "id": 1, "type": "sub", "lang": "spa", "selected": true }]),
        );
        assert_eq!(Track::Subtitles.value(&film), "SPA");

        film.apply(
            "track-list",
            &json!([{ "id": 1, "type": "sub", "title": "Forced", "selected": true }]),
        );
        assert_eq!(Track::Subtitles.value(&film), "Forced");
    }

    #[test]
    fn a_subtitle_list_starts_with_off() {
        assert_eq!(
            Track::Subtitles.entries(&film()),
            vec![
                "Off".to_string(),
                "SPA".to_string(),
                "Forced (ENG)".to_string()
            ]
        );
        assert_eq!(
            Track::Audio.entries(&film()),
            vec!["ENG".to_string(), "Commentary (ENG)".to_string()]
        );
        assert_eq!(Track::Video.entries(&film()), vec!["Main".to_string()]);
    }

    /// A chooser opens on the playing track, or the first entry when none
    /// plays.
    #[test]
    fn a_chooser_opens_on_the_track_that_plays() {
        let mut film = film();
        assert_eq!(Track::Audio.opens_on(&film), 0);
        assert_eq!(Track::Subtitles.opens_on(&film), 0);

        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "audio", "lang": "eng" },
                { "id": 2, "type": "audio", "lang": "fra", "selected": true },
                { "id": 1, "type": "sub", "lang": "spa" },
                { "id": 2, "type": "sub", "lang": "eng", "selected": true },
            ]),
        );
        assert_eq!(Track::Audio.opens_on(&film), 1);
        assert_eq!(Track::Subtitles.opens_on(&film), 2);
    }

    /// The playing entry is the selected track, Off for a subtitle list with
    /// none selected, and no entry for an audio list with none selected.
    #[test]
    fn the_playing_entry_is_the_track_mpv_plays() {
        let mut film = film();
        assert_eq!(Track::Audio.playing(&film), Some(0));
        assert_eq!(Track::Subtitles.playing(&film), Some(0));

        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "audio", "lang": "eng" },
                { "id": 1, "type": "sub", "lang": "spa" },
                { "id": 2, "type": "sub", "lang": "eng", "selected": true },
            ]),
        );
        assert_eq!(Track::Audio.playing(&film), None);
        assert_eq!(Track::Subtitles.playing(&film), Some(2));
    }

    #[test]
    fn a_select_sets_the_track_property() {
        assert_eq!(
            Track::Audio.apply(&film(), 1),
            vec![vec![json!("set_property"), json!("aid"), json!("2")]]
        );
        assert_eq!(
            Track::Video.apply(&film(), 0),
            vec![vec![json!("set_property"), json!("vid"), json!("1")]]
        );
        assert_eq!(
            Track::Subtitles.apply(&film(), 2),
            vec![vec![json!("set_property"), json!("sid"), json!("2")]]
        );
    }

    #[test]
    fn the_first_subtitle_entry_turns_subtitles_off() {
        assert_eq!(
            Track::Subtitles.apply(&film(), 0),
            vec![vec![json!("set_property"), json!("sid"), json!("no")]]
        );
    }

    /// A track that left the list between opening the chooser and the select
    /// changes nothing.
    #[test]
    fn a_missing_entry_writes_nothing() {
        assert!(Track::Audio.apply(&film(), 9).is_empty());
        assert!(Track::Subtitles.apply(&film(), 9).is_empty());
    }
}
