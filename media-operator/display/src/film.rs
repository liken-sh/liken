//! The properties of the playing film that the display observes from mpv.
//! After `observe_property`, mpv sends the property's current value and
//! then a new value on every change, so no timer reads them again.

use serde_json::Value;

use crate::subtitles::copy::{self, source};
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
    /// The container's forced flag. mpv also sets it for an external
    /// file whose name has a `forced` part.
    pub forced: bool,
    /// The flag of a subtitle track that also describes the sounds, for
    /// a viewer who cannot hear them.
    pub hearing_impaired: bool,
    /// The flag of an audio track that also describes the picture, for
    /// a viewer who cannot see it.
    pub visual_impaired: bool,
    /// How many channels an audio track has, as the container states it.
    /// mpv 0.41 also reports a profile and a channel layout, but only for
    /// the audio track that it decodes, so the display reads neither: a
    /// format that changed when a track was selected would move the
    /// columns of the track panel.
    pub channels: Option<i64>,
    /// The frame rate of a video track, which times the cues of a MicroDVD
    /// subtitle file.
    pub fps: Option<f64>,
    /// The id of the track in its container: the `TrackNumber` of a
    /// Matroska track, which mpv's own demuxer reads, and the `track_ID` of
    /// an MP4 track, which FFmpeg reports as the stream's id.
    pub src_id: Option<i64>,
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
    /// The display's copies of subtitle tracks, which `tracks` leaves out.
    /// Each is the track that mpv reports and the identity of its original.
    pub copies: Vec<(String, Track)>,
    /// How many times mpv has sent `track-list`.
    pub track_lists: u64,
    /// The time of the first packet of the file, from
    /// `demuxer-start-time`. mpv plays the file from that time as 0.
    pub start_time: Option<f64>,
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
            "track-list" => {
                (self.tracks, self.copies) = fold(tracks(value));
                self.track_lists += 1;
            }
            "media-title" => self.media_title = text(value),
            "path" => self.path = text(value),
            "playlist-pos" => self.playlist_pos = value.as_i64(),
            "playlist-count" => self.playlist_count = value.as_i64(),
            "pause" => self.paused = value.as_bool().unwrap_or(false),
            "audio-delay" => self.audio_delay = value.as_f64().unwrap_or(0.0),
            "sub-delay" => self.sub_delay = value.as_f64().unwrap_or(0.0),
            "osd-dimensions" => self.screen = screen(value),
            "demuxer-start-time" => self.start_time = value.as_f64(),
            // mpv reads the header from the track's decoder, and an ASS
            // track always has one. So a value of null means that mpv read
            // the property before it opened the track, and the display keeps
            // waiting for the header.
            "sub-ass-extradata" => self.script = value.as_str().map(Script::parse),
            _ => {}
        }
    }

    /// The subtitle track that a person chose, if any. When mpv draws the
    /// display's copy of a track, this is the original, which the track
    /// panel shows in its place.
    pub fn selected_subtitle(&self) -> Option<&Track> {
        self.tracks_of("sub").find(|track| track.selected)
    }

    /// The subtitle track that mpv draws, which is the copy when mpv draws
    /// one.
    pub fn drawn_subtitle(&self) -> Option<&Track> {
        self.copies
            .iter()
            .map(|(_, copy)| copy)
            .find(|copy| copy.selected)
            .or_else(|| self.selected_subtitle())
    }

    /// Whether `track` is the display's copy of a text track that is not
    /// ASS, such as SubRip.
    pub fn is_converted_copy(&self, track: &Track) -> bool {
        let Some((identity, _)) = self.copies.iter().find(|(_, copy)| copy.id == track.id) else {
            return false;
        };
        self.tracks_of("sub")
            .find(|original| source::identity(original) == *identity)
            .is_some_and(|original| !matches!(original.codec.as_deref(), Some("ass" | "ssa")))
    }

    /// The frame rate of the selected video track.
    pub fn fps(&self) -> Option<f64> {
        self.tracks_of("video")
            .find(|track| track.selected)
            .and_then(|track| track.fps)
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

/// Whether a chapter title is only a time, such as `00:07:40.835` or
/// `12:30`, which some files write in place of a name. The scrubber shows
/// the time already, so such a title says nothing.
fn is_a_time(title: &str) -> bool {
    let (clock, fraction) = title.split_once(['.', ',']).unwrap_or((title, "0"));
    let fields: Vec<&str> = clock.split(':').collect();
    (2..=3).contains(&fields.len())
        && fields
            .iter()
            .chain([&fraction])
            .all(|field| !field.is_empty() && field.chars().all(|c| c.is_ascii_digit()))
}

fn chapters(value: &Value) -> Vec<Chapter> {
    value
        .as_array()
        .map(|list| {
            list.iter()
                .map(|entry| Chapter {
                    time: entry.get("time").and_then(Value::as_f64).unwrap_or(0.0),
                    title: entry
                        .get("title")
                        .and_then(text)
                        .filter(|title| !is_a_time(title)),
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
                    selected: flag(entry, "selected"),
                    codec: entry.get("codec").and_then(text),
                    ff_index: entry.get("ff-index").and_then(Value::as_i64),
                    external: flag(entry, "external"),
                    external_filename: entry.get("external-filename").and_then(text),
                    forced: flag(entry, "forced"),
                    hearing_impaired: flag(entry, "hearing-impaired"),
                    visual_impaired: flag(entry, "visual-impaired"),
                    channels: entry.get("demux-channel-count").and_then(Value::as_i64),
                    fps: entry.get("demux-fps").and_then(Value::as_f64),
                    src_id: entry.get("src-id").and_then(Value::as_i64),
                })
                .collect()
        })
        .unwrap_or_default()
}

/// Splits the display's copies of subtitle tracks from the tracks of the
/// file. The name of a copy's file names its original, and mpv reports the
/// file as the copy's `external-filename`. When mpv
/// draws a copy, the original counts as selected, so the track panel
/// shows one entry for the two.
fn fold(all: Vec<Track>) -> (Vec<Track>, Vec<(String, Track)>) {
    let (copies, mut tracks): (Vec<Track>, Vec<Track>) =
        all.into_iter().partition(|track| copy_of(track).is_some());
    let copies: Vec<(String, Track)> = copies
        .into_iter()
        .filter_map(|copy| Some((copy_of(&copy)?.to_string(), copy)))
        .collect();
    for (identity, copy) in &copies {
        if !copy.selected {
            continue;
        }
        for track in tracks.iter_mut().filter(|track| track.kind == "sub") {
            if source::identity(track) == *identity {
                track.selected = true;
            }
        }
    }
    (tracks, copies)
}

/// The identity of the original of a copy, or `None` for a track that is
/// not a copy.
fn copy_of(track: &Track) -> Option<&str> {
    copy::identity(track.external_filename.as_deref()?)
}

/// A boolean field of a `track-list` entry. A missing field is false.
fn flag(entry: &Value, name: &str) -> bool {
    entry.get(name).and_then(Value::as_bool).unwrap_or(false)
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

    /// The entries are in the shape that mpv 0.41 sends. The profile and
    /// the layout of the decoded audio track are not read.
    #[test]
    fn a_track_keeps_its_flags_and_its_channel_count() {
        let mut film = Film::default();
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "audio", "codec": "eac3", "selected": true,
                  "codec-profile": "Dolby Digital Plus + Dolby Atmos",
                  "demux-channel-count": 6, "demux-channels": "5.1(side)" },
                { "id": 2, "type": "audio", "visual-impaired": true,
                  "demux-channel-count": 2, "demux-channels": "unknown2" },
                { "id": 1, "type": "sub", "forced": true, "default": true },
                { "id": 2, "type": "sub", "hearing-impaired": true },
            ]),
        );
        let read: Vec<_> = film
            .tracks
            .iter()
            .map(|track| {
                (
                    track.kind.as_str(),
                    track.forced,
                    track.hearing_impaired,
                    track.visual_impaired,
                    track.channels,
                )
            })
            .collect();
        assert_eq!(
            read,
            vec![
                ("audio", false, false, false, Some(6)),
                ("audio", false, false, true, Some(2)),
                ("sub", true, false, false, None),
                ("sub", false, true, false, None),
            ]
        );
    }

    /// Some files title each chapter with its start time. A title that is
    /// only a time says nothing that the scrubber does not, so it counts
    /// as no title.
    #[test]
    fn a_chapter_titled_with_a_time_has_no_title() {
        let mut film = Film::default();
        film.apply(
            "chapter-list",
            &json!([
                { "title": "00:07:40.835", "time": 460.835 },
                { "title": "1:02:03", "time": 3723.0 },
                { "title": "12:30", "time": 750.0 },
                { "title": "Chapter 3", "time": 900.0 },
                { "title": "The road at 10:30", "time": 1000.0 },
            ]),
        );
        let titles: Vec<Option<&str>> = film
            .chapters
            .iter()
            .map(|chapter| chapter.title.as_deref())
            .collect();
        assert_eq!(
            titles,
            vec![
                None,
                None,
                None,
                Some("Chapter 3"),
                Some("The road at 10:30")
            ]
        );
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

    /// The track panel lists the original of a copy once, as selected
    /// while mpv draws the copy, and the placement reads the copy.
    #[test]
    fn a_copy_takes_the_place_of_its_original() {
        let mut film = Film::default();
        film.apply(
            "track-list",
            &json!([
                { "id": 1, "type": "sub", "codec": "subrip", "ff-index": 3, "lang": "eng" },
                { "id": 2, "type": "sub", "codec": "subrip", "ff-index": 4 },
                { "id": 3, "type": "sub", "codec": "ass", "ff-index": 0, "external": true,
                  "external-filename": "/ipc/media-display-copy.ff-index-3.1.ass",
                  "lang": "eng", "selected": true },
            ]),
        );
        let listed: Vec<(i64, bool)> = film
            .tracks_of("sub")
            .map(|track| (track.id, track.selected))
            .collect();
        assert_eq!(listed, [(1, true), (2, false)]);
        assert_eq!(film.selected_subtitle().map(|track| track.id), Some(1));
        assert_eq!(film.drawn_subtitle().map(|track| track.id), Some(3));
        assert_eq!(film.copies.len(), 1);
        assert_eq!(film.track_lists, 1);
    }

    /// The `src-id` of a track and the file's start time come from mpv
    /// 0.41.0's properties.
    #[test]
    fn a_track_keeps_its_id_in_the_container_and_the_file_its_start() {
        let mut film = Film::default();
        film.apply(
            "track-list",
            &json!([{ "id": 1, "type": "sub", "codec": "subrip", "src-id": 2, "ff-index": 1 }]),
        );
        film.apply("demuxer-start-time", &json!(0.0));
        assert_eq!(film.tracks[0].src_id, Some(2));
        assert_eq!(film.start_time, Some(0.0));
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
