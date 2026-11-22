//! The properties of the playing film that the display observes from mpv.
//! After `observe_property`, mpv sends the property's current value and
//! then a new value on every change, so no timer reads them again.

use serde_json::Value;

use crate::subtitles::script::Script;

/// One entry of mpv's `chapter-list`.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Chapter {
    pub time: f64,
    pub title: Option<String>,
}

/// One entry of mpv's `track-list`. `kind` is the entry's `type` field:
/// `video`, `audio`, or `sub`.
///
/// `ff_index` is the stream's index in its container, the index that
/// ffprobe reports and that `presentation.subtitles` calls `stream`. An
/// external file is its own container, so its index is 0 and only
/// `external_filename` identifies it.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Track {
    pub id: i64,
    pub kind: String,
    pub title: Option<String>,
    pub lang: Option<String>,
    pub selected: bool,
    pub codec: Option<String>,
    pub ff_index: Option<i64>,
    pub external: bool,
    pub external_filename: Option<String>,
}

/// mpv's `osd-dimensions`: the size of the window that mpv draws into, in
/// pixels, and the black bars around the picture.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct Screen {
    pub width: i64,
    pub height: i64,
    pub top: i64,
    pub bottom: i64,
    pub left: i64,
    pub right: i64,
}

impl Track {
    /// The name a viewer sees for the track: its title and language. When
    /// the file sets neither, the label is `Track <id>`.
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

/// The last value of each observed mpv property. Drawing code reads these
/// fields and never queries mpv, because a `get_property` request over the
/// IPC socket returns its reply in a later message, not during the frame.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Film {
    pub duration: Option<f64>,
    pub position: Option<f64>,
    pub chapter: Option<i64>,
    pub chapters: Vec<Chapter>,
    /// The title of the playing chapter, which a music item shows as its
    /// track name.
    pub chapter_title: Option<String>,
    pub tracks: Vec<Track>,
    pub metadata: Vec<(String, String)>,
    pub media_title: Option<String>,
    /// The path of the playing file, as the playlist gave it. The music
    /// cover reads the picture embedded in this file, or a cover file
    /// beside it, when the item's block gives no cover.
    pub path: Option<String>,
    pub playlist_pos: Option<i64>,
    pub playlist_count: Option<i64>,
    pub paused: bool,
    pub audio_delay: f64,
    pub sub_delay: f64,
    /// `None` until mpv has configured its video output, because mpv
    /// reports a size of 0 by 0 before that.
    pub screen: Option<Screen>,
    /// The header of the selected ASS track. `None` until mpv sends it,
    /// and again after `forget_script`.
    pub script: Option<Script>,
}

impl Film {
    /// Stores the value that mpv sent for the property `name`. It ignores a
    /// property the display does not observe. A missing or null value
    /// becomes the field's empty value.
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
            "path" => self.path = text(value),
            "playlist-pos" => self.playlist_pos = value.as_i64(),
            "playlist-count" => self.playlist_count = value.as_i64(),
            "pause" => self.paused = value.as_bool().unwrap_or(false),
            "audio-delay" => self.audio_delay = value.as_f64().unwrap_or(0.0),
            "sub-delay" => self.sub_delay = value.as_f64().unwrap_or(0.0),
            "osd-dimensions" => self.screen = screen(value),
            // mpv reads the header from the track's decoder, and an ASS
            // track always has one. So a value of null means that mpv read
            // the property before it opened the track, and the display keeps
            // waiting for the header.
            "sub-ass-extradata" => self.script = value.as_str().map(Script::parse),
            _ => {}
        }
    }

    /// The subtitle track that mpv draws, if any.
    pub fn selected_subtitle(&self) -> Option<&Track> {
        self.tracks_of("sub").find(|track| track.selected)
    }

    /// Marks the header as unknown. The display calls it when it observes
    /// `sub-ass-extradata` again, so a header of the previous track is not
    /// used for the next one.
    pub fn forget_script(&mut self) {
        self.script = None;
    }

    /// The tracks whose `kind` is `kind`, in `track-list` order.
    pub fn tracks_of<'a>(&'a self, kind: &'a str) -> impl Iterator<Item = &'a Track> + 'a {
        self.tracks.iter().filter(move |track| track.kind == kind)
    }

    /// The value of the file tag `name`, matched without regard to case.
    /// An empty value counts as no tag. A standalone track plays as its own
    /// file, so `metadata` holds that file's tags. An album plays as one
    /// mpv EDL timeline, and mpv reports the first file's tags for the
    /// whole timeline.
    pub fn tag(&self, name: &str) -> Option<&str> {
        self.metadata
            .iter()
            .find(|(key, _)| key.eq_ignore_ascii_case(name))
            .map(|(_, value)| value.as_str())
            .filter(|value| !value.is_empty())
    }

    /// The chapter that is playing, which the bar marks and the line below
    /// it names. Returns `None` when mpv reports no chapter or an index
    /// outside `chapter-list`.
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
                    codec: entry.get("codec").and_then(text),
                    ff_index: entry.get("ff-index").and_then(Value::as_i64),
                    external: entry
                        .get("external")
                        .and_then(Value::as_bool)
                        .unwrap_or(false),
                    external_filename: entry.get("external-filename").and_then(text),
                })
                .collect()
        })
        .unwrap_or_default()
}

fn screen(value: &Value) -> Option<Screen> {
    let field = |name: &str| value.get(name).and_then(Value::as_i64).unwrap_or(0);
    let screen = Screen {
        width: field("w"),
        height: field("h"),
        top: field("mt"),
        bottom: field("mb"),
        left: field("ml"),
        right: field("mr"),
    };
    (screen.width > 0 && screen.height > 0).then_some(screen)
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
    fn a_property_push_updates_its_own_field() {
        let film = film();
        assert_eq!(film.duration, Some(6000.0));
        assert_eq!(film.position, Some(1199.0));
        assert_eq!(film.chapter, Some(1));
        assert_eq!(film.chapters.len(), 3);
        assert_eq!(film.chapters[1].time, 600.0);
        assert_eq!(film.chapters[1].title.as_deref(), Some("The road"));
        assert_eq!(film.chapters[2].title, None);
        assert_eq!(film.tracks.len(), 4);
        assert_eq!(film.tracks_of("audio").count(), 2);
        assert_eq!(film.tracks_of("sub").count(), 1);
        assert_eq!(film.tracks_of("video").count(), 1);
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
                ..Track::default()
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

    /// The entries are in the shape that mpv 0.41.0 sends: an external
    /// file has an `ff-index` of 0 and its path.
    #[test]
    fn the_selected_subtitle_track_keeps_what_identifies_its_stream() {
        let mut film = Film::default();
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "video", "selected": true },
                { "id": 1, "type": "sub", "codec": "subrip", "ff-index": 4 },
                { "id": 2, "type": "sub", "codec": "hdmv_pgs_subtitle", "ff-index": 0,
                  "external": true, "external-filename": "/media/a-film.sup", "selected": true },
            ]),
        );
        assert_eq!(
            film.selected_subtitle(),
            Some(&Track {
                id: 2,
                kind: "sub".to_string(),
                selected: true,
                codec: Some("hdmv_pgs_subtitle".to_string()),
                ff_index: Some(0),
                external: true,
                external_filename: Some("/media/a-film.sup".to_string()),
                ..Track::default()
            })
        );

        film.apply("track-list", &json!([{ "id": 1, "type": "sub" }]));
        assert_eq!(film.selected_subtitle(), None);
    }

    /// mpv reports a size of 0 by 0 until its video output is configured,
    /// and that is no screen to place a subtitle on.
    #[test]
    fn the_screen_is_known_once_mpv_reports_a_size() {
        let mut film = Film::default();
        film.apply(
            "osd-dimensions",
            &json!({ "w": 0, "h": 0, "par": 0.0, "aspect": 0.0, "mt": 0, "mb": 0, "ml": 0, "mr": 0 }),
        );
        assert_eq!(film.screen, None);
        film.apply(
            "osd-dimensions",
            &json!({ "w": 1920, "h": 1080, "par": 1.0, "aspect": 1.7778, "mt": 138, "mb": 138, "ml": 0, "mr": 0 }),
        );
        assert_eq!(
            film.screen,
            Some(Screen {
                width: 1920,
                height: 1080,
                top: 138,
                bottom: 138,
                left: 0,
                right: 0
            })
        );
    }

    /// A null header means that mpv had not opened the track, so the film
    /// still has no header.
    #[test]
    fn the_script_is_read_from_the_header_and_forgotten_on_request() {
        let mut film = Film::default();
        film.apply("sub-ass-extradata", &Value::Null);
        assert_eq!(film.script, None);
        film.apply(
            "sub-ass-extradata",
            &json!("[Script Info]\nPlayResY: 804\n"),
        );
        assert_eq!(film.script.map(|script| script.play_res_y), Some(804));
        film.forget_script();
        assert_eq!(film.script, None);
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
