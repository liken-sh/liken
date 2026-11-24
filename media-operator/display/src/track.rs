//! The three kinds of track that the strip has a stop for. Each kind
//! filters mpv's `track-list` by one `type` and plays a track by setting
//! one property, so the kinds share one type.

use serde_json::json;

use crate::film::Film;
use crate::ipc::Command;
use crate::timing::Delay;

/// The kind of track a stop picks.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Track {
    Audio,
    Subtitles,
    Video,
}

impl Track {
    /// The `track-list` `type` value of this kind.
    pub fn kind(self) -> &'static str {
        match self {
            Track::Audio => "audio",
            Track::Subtitles => "sub",
            Track::Video => "video",
        }
    }

    /// The heading of the strip's stop.
    pub fn heading(self) -> &'static str {
        match self {
            Track::Audio => "Audio",
            Track::Subtitles => "Subtitles",
            Track::Video => "Video",
        }
    }

    /// The mpv property that plays a track of this kind.
    fn property(self) -> &'static str {
        match self {
            Track::Audio => "aid",
            Track::Subtitles => "sid",
            Track::Video => "vid",
        }
    }

    /// The delay that the panel of this kind sets. A video track has none.
    pub fn delay(self) -> Option<Delay> {
        match self {
            Track::Audio => Some(Delay::Audio),
            Track::Subtitles => Some(Delay::Subtitles),
            Track::Video => None,
        }
    }

    /// Whether the strip has a stop for this kind. The video stop shows
    /// only when the file has more than one video track, such as an
    /// alternate angle. The other stops show when the file has a track of
    /// their kind.
    pub fn available(self, film: &Film) -> bool {
        let count = film.tracks_of(self.kind()).count();
        match self {
            Track::Video => count > 1,
            _ => count >= 1,
        }
    }

    /// The mpv command that plays the track `id`, or turns the subtitles
    /// off for `None`.
    pub fn play(self, id: Option<i64>) -> Vec<Command> {
        let value = id.map_or_else(|| "no".to_string(), |id| id.to_string());
        vec![vec![
            json!("set_property"),
            json!(self.property()),
            json!(value),
        ]]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_stop_shows_when_the_file_has_its_tracks() {
        let mut film = Film::default();
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "video", "selected": true },
                { "id": 1, "type": "audio", "selected": true },
                { "id": 1, "type": "sub" },
            ]),
        );
        assert!(Track::Audio.available(&film));
        assert!(Track::Subtitles.available(&film));
        assert!(!Track::Video.available(&film));
        assert!(!Track::Audio.available(&Film::default()));

        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "video", "selected": true },
                { "id": 2, "type": "video" },
            ]),
        );
        assert!(Track::Video.available(&film));
    }

    #[test]
    fn a_select_sets_the_property_of_its_kind() {
        let cases = [
            (Track::Audio, Some(2), "aid", "2"),
            (Track::Video, Some(1), "vid", "1"),
            (Track::Subtitles, Some(3), "sid", "3"),
            (Track::Subtitles, None, "sid", "no"),
        ];
        for (track, id, property, value) in cases {
            assert_eq!(
                track.play(id),
                vec![vec![json!("set_property"), json!(property), json!(value)]],
                "{track:?} {id:?}"
            );
        }
    }
}
