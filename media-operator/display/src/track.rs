//! The track controls. Each one's cell shows the current track, and its
//! chooser lists the tracks `track-list` carries. A select switches the
//! property mpv reads that stream from.
//!
//! The audio control, the subtitle control, and the video control differ in
//! the list they filter, the word their cell reads, and the property a select
//! writes. Everything else they share, so they are one type here.

use serde_json::json;

use crate::film::Film;
use crate::ipc::Command;

/// Which stream a control picks.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Track {
    Audio,
    Subtitles,
    Video,
}

/// The word a cell reads when no track of that kind plays.
const OFF: &str = "off";

/// Off is the first entry of a subtitle list, ahead of the tracks, so a viewer
/// can always turn subtitles off. Entry 1 maps to `sid no`, and every later
/// entry maps to the track one place before it.
const OFF_ENTRY: &str = "Off";

impl Track {
    /// The `track-list` type this control filters on.
    pub fn kind(self) -> &'static str {
        match self {
            Track::Audio => "audio",
            Track::Subtitles => "sub",
            Track::Video => "video",
        }
    }

    /// The property a select writes.
    pub fn property(self) -> &'static str {
        match self {
            Track::Audio => "aid",
            Track::Subtitles => "sid",
            Track::Video => "vid",
        }
    }

    /// The video control shows only when the file carries more than one video
    /// track, like an alternate angle. Most files carry one, so it stays
    /// hidden.
    pub fn available(self, film: &Film) -> bool {
        let count = film.tracks_of(self.kind()).len();
        match self {
            Track::Video => count > 1,
            _ => count >= 1,
        }
    }

    /// The cell's word. A subtitle cell reads the language alone, because the
    /// strip holds one line for it.
    pub fn value(self, film: &Film) -> String {
        let Some(track) = film
            .tracks_of(self.kind())
            .into_iter()
            .find(|track| track.selected)
        else {
            return OFF.to_string();
        };
        match (self, track.lang.as_ref()) {
            (Track::Subtitles, Some(lang)) => lang.to_uppercase(),
            _ => track.label(),
        }
    }

    /// The chooser's entries, in the order the list carries them.
    pub fn entries(self, film: &Film) -> Vec<String> {
        let tracks = film.tracks_of(self.kind());
        let labels = tracks.into_iter().map(|track| track.label());
        match self {
            Track::Subtitles => std::iter::once(OFF_ENTRY.to_string())
                .chain(labels)
                .collect(),
            _ => labels.collect(),
        }
    }

    /// The entry a chooser opens on, which is the track that plays now.
    pub fn opens_on(self, film: &Film) -> usize {
        let at = film
            .tracks_of(self.kind())
            .into_iter()
            .position(|track| track.selected);
        match (self, at) {
            (Track::Subtitles, Some(at)) => at + 1,
            (Track::Subtitles, None) => 0,
            (_, at) => at.unwrap_or(0),
        }
    }

    /// Switch to the entry the chooser stands on. A subtitle list's first
    /// entry turns subtitles off.
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
            return match film.tracks_of(self.kind()).get(selected - 1) {
                Some(track) => write(track.id.to_string()),
                None => Vec::new(),
            };
        }
        match film.tracks_of(self.kind()).get(selected) {
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
    fn a_control_shows_for_the_file_that_carries_its_tracks() {
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
    fn a_cell_reads_the_track_that_plays() {
        assert_eq!(Track::Audio.value(&film()), "ENG");
        assert_eq!(Track::Video.value(&film()), "Main");
        assert_eq!(Track::Subtitles.value(&film()), OFF);
        assert_eq!(Track::Audio.value(&Film::default()), OFF);
    }

    /// A subtitle cell reads the language alone, and falls back to the whole
    /// label when the track carries no language.
    #[test]
    fn a_subtitle_cell_reads_its_language() {
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
    fn a_subtitle_list_carries_off_ahead_of_every_track() {
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

    /// A chooser opens on the track that plays, and on the first entry when
    /// none does.
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

    #[test]
    fn a_select_writes_the_property_mpv_reads_the_stream_from() {
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

    /// An entry the list no longer carries writes nothing, so a track that
    /// went away between the open and the select changes none.
    #[test]
    fn an_entry_the_list_lost_writes_nothing() {
        assert!(Track::Audio.apply(&film(), 9).is_empty());
        assert!(Track::Subtitles.apply(&film(), 9).is_empty());
    }
}
