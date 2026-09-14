// What mpv reports about the film that is playing. The display observes
// each of these properties; mpv pushes it once at registration and then on
// every change, so the display runs no timer of its own for these values.

use serde_json::Value;

// One chapter of the film, as chapter-list carries it.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Chapter {
    pub time: f64,
    pub title: Option<String>,
}

// One track of the file, as track-list carries it. kind is the list's own
// type field.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Track {
    pub id: i64,
    pub kind: String,
    pub title: Option<String>,
    pub lang: Option<String>,
    pub selected: bool,
}

impl Track {
    /// Name a track by what a viewer recognizes: the title and the language.
    /// When the file gives neither, the id is the only handle left, so the
    /// label falls back to it.
    pub fn label(&self) -> String {
        let lang = self.lang.as_ref().map(|lang| lang.to_uppercase());
        match (
            self.title.as_deref().filter(|title| !title.is_empty()),
            lang,
        ) {
            (Some(title), Some(lang)) => format!("{title} ({lang})"),
            (Some(title), None) => title.to_string(),
            (None, Some(lang)) => lang,
            (None, None) => format!("Track {}", self.id),
        }
    }
}

// The properties the display holds, one field per observed property. Every
// module reads its film state here instead of asking mpv inside a frame,
// because an IPC client cannot read a property inside a frame the way a
// script can.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Film {
    pub duration: Option<f64>,
    pub position: Option<f64>,
    pub chapter: Option<i64>,
    pub chapters: Vec<Chapter>,
    /// The title of the chapter mpv plays now, which a music item draws as its
    /// track name.
    pub chapter_title: Option<String>,
    pub tracks: Vec<Track>,
    pub metadata: Vec<(String, String)>,
    pub media_title: Option<String>,
    pub playlist_pos: Option<i64>,
    pub playlist_count: Option<i64>,
    pub paused: bool,
    pub audio_delay: f64,
    pub sub_delay: f64,
}

impl Film {
    // Take one property push. The name is the property the display observed,
    // and the value is what mpv pushed.
    pub fn apply(&mut self, name: &str, value: &Value) {
        match name {
            "duration" => self.duration = value.as_f64(),
            "time-pos" => self.position = value.as_f64(),
            "chapter" => self.chapter = value.as_i64(),
            "chapter-list" => self.chapters = chapters(value),
            "chapter-metadata/by-key/title" => self.chapter_title = text(value),
            "metadata" => self.metadata = pairs(value),
            "track-list" => self.tracks = tracks(value),
            "media-title" => self.media_title = text(value),
            "playlist-pos" => self.playlist_pos = value.as_i64(),
            "playlist-count" => self.playlist_count = value.as_i64(),
            "pause" => self.paused = value.as_bool().unwrap_or(false),
            "audio-delay" => self.audio_delay = value.as_f64().unwrap_or(0.0),
            "sub-delay" => self.sub_delay = value.as_f64().unwrap_or(0.0),
            _ => {}
        }
    }

    // The tracks of one kind, in the order track-list carries them.
    pub fn tracks_of(&self, kind: &str) -> Vec<&Track> {
        self.tracks
            .iter()
            .filter(|track| track.kind == kind)
            .collect()
    }

    /// A standalone track plays as a plain file, so mpv reads its tags. An
    /// album plays as one timeline, and mpv reads the first track's tags and
    /// keeps them for the whole run.
    pub fn tag(&self, name: &str) -> Option<&str> {
        self.metadata
            .iter()
            .find(|(key, _)| key.eq_ignore_ascii_case(name))
            .map(|(_, value)| value.as_str())
            .filter(|value| !value.is_empty())
    }

    /// The chapter mpv plays now, which is the one the bar marks and the one
    /// the line below the bar names.
    pub fn current_chapter(&self) -> Option<&Chapter> {
        let at = self.chapter.filter(|at| *at >= 0)?;
        self.chapters.get(usize::try_from(at).ok()?)
    }
}

fn text(value: &Value) -> Option<String> {
    value
        .as_str()
        .filter(|found| !found.is_empty())
        .map(str::to_string)
}

fn chapters(value: &Value) -> Vec<Chapter> {
    value
        .as_array()
        .map(|list| {
            list.iter()
                .map(|entry| Chapter {
                    time: entry.get("time").and_then(Value::as_f64).unwrap_or(0.0),
                    title: entry.get("title").and_then(text),
                })
                .collect()
        })
        .unwrap_or_default()
}

fn tracks(value: &Value) -> Vec<Track> {
    value
        .as_array()
        .map(|list| {
            list.iter()
                .map(|entry| Track {
                    id: entry.get("id").and_then(Value::as_i64).unwrap_or(0),
                    kind: entry
                        .get("type")
                        .and_then(Value::as_str)
                        .unwrap_or_default()
                        .to_string(),
                    title: entry.get("title").and_then(text),
                    lang: entry.get("lang").and_then(text),
                    selected: entry
                        .get("selected")
                        .and_then(Value::as_bool)
                        .unwrap_or(false),
                })
                .collect()
        })
        .unwrap_or_default()
}

fn pairs(value: &Value) -> Vec<(String, String)> {
    value
        .as_object()
        .map(|map| {
            map.iter()
                .filter_map(|(key, value)| Some((key.clone(), value.as_str()?.to_string())))
                .collect()
        })
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn film() -> Film {
        let mut film = Film::default();
        film.apply("duration", &json!(6000.0));
        film.apply("time-pos", &json!(1199.0));
        film.apply("chapter", &json!(1));
        film.apply(
            "chapter-list",
            &json!([
                { "title": "Opening", "time": 0.0 },
                { "title": "The road", "time": 600.0 },
                { "title": "", "time": 2400.0 },
            ]),
        );
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "video", "title": "Main", "selected": true },
                { "id": 1, "type": "audio", "lang": "eng", "selected": true },
                { "id": 2, "type": "audio", "title": "Commentary", "lang": "eng" },
                { "id": 1, "type": "sub", "lang": "spa" },
            ]),
        );
        film
    }

    #[test]
    fn a_push_lands_on_the_property_it_names() {
        let film = film();
        assert_eq!(film.duration, Some(6000.0));
        assert_eq!(film.position, Some(1199.0));
        assert_eq!(film.chapter, Some(1));
        assert_eq!(film.chapters.len(), 3);
        assert_eq!(film.chapters[1].time, 600.0);
        assert_eq!(film.chapters[1].title.as_deref(), Some("The road"));
        assert_eq!(film.chapters[2].title, None);
        assert_eq!(film.tracks.len(), 4);
        assert_eq!(film.tracks_of("audio").len(), 2);
        assert_eq!(film.tracks_of("sub").len(), 1);
        assert_eq!(film.tracks_of("video").len(), 1);
    }

    #[test]
    fn a_property_with_no_value_reads_as_nothing() {
        let mut film = film();
        film.apply("duration", &Value::Null);
        film.apply("chapter-list", &Value::Null);
        film.apply("track-list", &Value::Null);
        film.apply("media-title", &json!(""));
        film.apply("volume", &json!(50));
        assert_eq!(film.duration, None);
        assert!(film.chapters.is_empty());
        assert!(film.tracks.is_empty());
        assert_eq!(film.media_title, None);
    }

    #[test]
    fn a_track_reads_by_its_title_its_language_or_its_id() {
        let label = |title: Option<&str>, lang: Option<&str>| {
            Track {
                id: 3,
                kind: "audio".to_string(),
                title: title.map(str::to_string),
                lang: lang.map(str::to_string),
                selected: false,
            }
            .label()
        };

        assert_eq!(label(Some("Commentary"), Some("eng")), "Commentary (ENG)");
        assert_eq!(label(Some("Commentary"), None), "Commentary");
        assert_eq!(label(None, Some("spa")), "SPA");
        assert_eq!(label(None, None), "Track 3");
        assert_eq!(label(Some(""), Some("eng")), "ENG");
    }

    #[test]
    fn the_current_chapter_is_the_one_mpv_plays() {
        let mut film = film();
        assert_eq!(
            film.current_chapter().and_then(|at| at.title.as_deref()),
            Some("The road")
        );

        film.apply("chapter", &json!(-1));
        assert_eq!(film.current_chapter(), None);
        film.apply("chapter", &json!(9));
        assert_eq!(film.current_chapter(), None);
    }

    #[test]
    fn a_tag_reads_whatever_case_the_file_wrote_it_in() {
        let mut film = film();
        film.apply(
            "metadata",
            &json!({ "ARTIST": "Someone", "album": "", "date": "1979-10-05" }),
        );
        assert_eq!(film.tag("artist"), Some("Someone"));
        assert_eq!(film.tag("album"), None);
        assert_eq!(film.tag("date"), Some("1979-10-05"));
        assert_eq!(film.tag("composer"), None);
    }

    #[test]
    fn a_delay_with_no_value_reads_as_no_delay() {
        let mut film = Film::default();
        film.apply("audio-delay", &json!(-0.05));
        film.apply("sub-delay", &Value::Null);
        assert_eq!(film.audio_delay, -0.05);
        assert_eq!(film.sub_delay, 0.0);
    }
}
