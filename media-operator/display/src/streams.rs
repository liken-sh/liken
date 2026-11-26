//! What `library-operator`'s probe recorded about the streams of the
//! item's file, which the media browser sends in the item's
//! `presentation`: the event count of each subtitle track, in
//! `subtitles`, the codec, the profile, and the channels of each audio
//! track, in `audio`, and the active area of each video track's frame, in
//! `video`. mpv reports the profile and the channel layout only of the
//! audio track it decodes, it cannot count a subtitle track's events, and
//! it draws the black rows of a frame as picture, so the display reads
//! these facts when the block has them.

use serde_json::Value;

use crate::film::Track;

/// One entry of `presentation.subtitles` with its event count.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Counted {
    stream: Option<i64>,
    file: Option<String>,
    events: Option<i64>,
}

/// One entry of `presentation.audio`: ffprobe's codec name, profile, and
/// channel layout, and the channel count. The media browser leaves out a
/// value that ffprobe did not report, which reads here as empty or zero.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Sound {
    stream: Option<i64>,
    pub codec: String,
    pub profile: String,
    pub channels: i64,
    pub layout: String,
}

/// One entry of `presentation.video`: the height of the stream's frame,
/// and the first row and the height of its `activeArea`, when the entry
/// has one.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Frame {
    stream: Option<i64>,
    height: Option<i64>,
    area: Option<(i64, i64)>,
}

impl Frame {
    /// The share of the frame's height that is black below its active
    /// area, or 0 when the entry has no area that fits in its frame.
    fn below(&self) -> f64 {
        match (self.height, self.area) {
            (Some(frame), Some((top, height)))
                if frame > 0 && top >= 0 && height > 0 && top + height <= frame =>
            {
                (frame - top - height) as f64 / frame as f64
            }
            _ => 0.0,
        }
    }
}

/// The facts of one item's streams.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Streams {
    counted: Vec<Counted>,
    sounds: Vec<Sound>,
    frames: Vec<Frame>,
}

/// No facts, for an item whose block has no lists or describes another
/// item.
pub static NONE: Streams = Streams {
    counted: Vec::new(),
    sounds: Vec::new(),
    frames: Vec::new(),
};

impl Streams {
    /// Reads the block's `subtitles`, `audio`, and `video` lists. An entry
    /// that is not an object is left out.
    pub fn parse(subtitles: Option<&Value>, audio: Option<&Value>, video: Option<&Value>) -> Self {
        let entries = |list: Option<&Value>| -> Vec<Value> {
            list.and_then(Value::as_array)
                .into_iter()
                .flatten()
                .filter(|entry| entry.is_object())
                .cloned()
                .collect()
        };
        let text = |entry: &Value, name: &str| {
            entry
                .get(name)
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_string()
        };
        Self {
            counted: entries(subtitles)
                .iter()
                .map(|entry| Counted {
                    stream: entry.get("stream").and_then(Value::as_i64),
                    file: Some(text(entry, "file")).filter(|file| !file.is_empty()),
                    events: entry.get("events").and_then(Value::as_i64),
                })
                .collect(),
            sounds: entries(audio)
                .iter()
                .map(|entry| Sound {
                    stream: entry.get("stream").and_then(Value::as_i64),
                    codec: text(entry, "codec"),
                    profile: text(entry, "profile"),
                    channels: entry.get("channels").and_then(Value::as_i64).unwrap_or(0),
                    layout: text(entry, "layout"),
                })
                .collect(),
            frames: entries(video)
                .iter()
                .map(|entry| {
                    let number = |value: Option<&Value>, name: &str| {
                        value
                            .and_then(|value| value.get(name))
                            .and_then(Value::as_i64)
                    };
                    let area = entry.get("activeArea");
                    Frame {
                        stream: number(Some(entry), "stream"),
                        height: number(Some(entry), "height"),
                        area: number(area, "top").zip(number(area, "height")),
                    }
                })
                .collect(),
        }
    }

    /// The share of the frame of the video track `track` that is black
    /// below the frame's active area, which adds to the lower bar of the
    /// screen. It is 0 when no entry matches the track or its entry has no
    /// area, so the bar is the one that mpv reports.
    pub fn encoded_bar(&self, track: &Track) -> f64 {
        self.frames
            .iter()
            .find(|entry| same_stream(entry.stream, None, track))
            .map_or(0.0, Frame::below)
    }

    /// The event count of a subtitle track, or `None` when no entry
    /// matches it or its entry has no count.
    pub fn events(&self, track: &Track) -> Option<i64> {
        self.counted
            .iter()
            .find(|entry| same_stream(entry.stream, entry.file.as_deref(), track))
            .and_then(|entry| entry.events)
    }

    /// The facts of an audio track, or `None` when no entry matches it.
    /// An audio file beside the video has no entry, because the probe
    /// reads only the video's own container.
    pub fn sound(&self, track: &Track) -> Option<&Sound> {
        self.sounds
            .iter()
            .find(|entry| same_stream(entry.stream, None, track))
    }
}

/// Whether an entry of the block describes `track`. An embedded track
/// matches by its index in the container, and an external file by its
/// base name, because the pod mounts the media at another path than the
/// library's. An external file is its own container, so its index is 0
/// and never matches an entry's `stream`. `subtitleEvents` in the command
/// sidecar matches a track to its event count the same way.
pub fn same_stream(stream: Option<i64>, file: Option<&str>, track: &Track) -> bool {
    match track.external {
        true => {
            let name = track
                .external_filename
                .as_deref()
                .map(|path| path.rsplit('/').next().unwrap_or(path));
            file.is_some() && file == name
        }
        false => stream.is_some() && stream == track.ff_index,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn streams() -> Streams {
        Streams::parse(
            Some(&json!([
                { "stream": 2, "events": 14 },
                { "stream": 3, "events": 620,
                  "image": { "canvasHeight": 1080, "lineHeight": 61, "inkBottom": 918 } },
                { "stream": 4 },
                { "file": "a-film.en.hi.srt", "events": 700 },
                "not an entry",
            ])),
            Some(&json!([
                { "stream": 1, "codec": "eac3", "profile": "Dolby Digital Plus + Dolby Atmos",
                  "channels": 6, "layout": "5.1(side)" },
                { "stream": 5, "codec": "aac" },
            ])),
            None,
        )
    }

    fn embedded(kind: &str, ff_index: i64) -> Track {
        Track {
            kind: kind.to_string(),
            ff_index: Some(ff_index),
            ..Track::default()
        }
    }

    fn external(kind: &str, path: &str) -> Track {
        Track {
            kind: kind.to_string(),
            ff_index: Some(0),
            external: true,
            external_filename: Some(path.to_string()),
            ..Track::default()
        }
    }

    #[test]
    fn a_subtitle_track_finds_its_count_by_stream_or_by_file_name() {
        let cases = [
            ("the embedded stream 2", embedded("sub", 2), Some(14)),
            (
                "a stream whose entry has an image block",
                embedded("sub", 3),
                Some(620),
            ),
            (
                "a stream whose entry has no count",
                embedded("sub", 4),
                None,
            ),
            ("a stream with no entry", embedded("sub", 9), None),
            (
                "the external file",
                external("sub", "/media/a-film.en.hi.srt"),
                Some(700),
            ),
            (
                "another external file",
                external("sub", "/media/a-film.fr.srt"),
                None,
            ),
        ];
        for (name, track, want) in cases {
            assert_eq!(streams().events(&track), want, "{name}");
        }
    }

    /// An entry gives only the values that ffprobe reported, so a missing
    /// profile or layout reads as empty and a missing count as zero.
    #[test]
    fn an_audio_track_finds_its_facts_by_stream() {
        let streams = streams();
        assert_eq!(
            streams.sound(&embedded("audio", 1)),
            Some(&Sound {
                stream: Some(1),
                codec: "eac3".to_string(),
                profile: "Dolby Digital Plus + Dolby Atmos".to_string(),
                channels: 6,
                layout: "5.1(side)".to_string(),
            })
        );
        assert_eq!(
            streams
                .sound(&embedded("audio", 5))
                .map(|sound| (sound.channels, sound.layout.as_str())),
            Some((0, ""))
        );
        assert_eq!(streams.sound(&embedded("audio", 2)), None);
        assert_eq!(streams.sound(&external("audio", "/media/a-film.ac3")), None);
    }

    #[test]
    fn a_block_with_no_lists_has_no_facts() {
        let empty = Streams::parse(None, Some(&json!({ "stream": 1 })), None);
        assert_eq!(empty, Streams::default());
        assert_eq!(empty.events(&embedded("sub", 2)), None);
        assert_eq!(empty.encoded_bar(&embedded("video", 0)), 0.0);
    }

    /// The video track finds its frame by stream, and the black rows below
    /// its active area are a share of the frame's height. An entry with no
    /// area, or an area that does not fit in its frame, adds nothing.
    #[test]
    fn a_video_track_finds_the_black_rows_below_its_active_area_by_stream() {
        let streams = Streams::parse(
            None,
            None,
            Some(&json!([
                { "stream": 0, "width": 1920, "height": 1080,
                  "activeArea": { "left": 0, "top": 131, "width": 1920, "height": 817 } },
                { "stream": 1, "width": 1920, "height": 1080 },
                { "stream": 2, "width": 1920, "height": 1080,
                  "activeArea": { "left": 0, "top": 300, "width": 1920, "height": 900 } },
                { "stream": 3, "activeArea": { "left": 0, "top": 0, "width": 1920, "height": 800 } },
            ])),
        );
        let cases = [
            ("a stream with an area", 0, 132.0 / 1080.0),
            ("a stream with no area", 1, 0.0),
            ("an area below the foot of its frame", 2, 0.0),
            ("an entry with no frame height", 3, 0.0),
            ("a stream with no entry", 9, 0.0),
        ];
        for (name, stream, want) in cases {
            assert_eq!(
                streams.encoded_bar(&embedded("video", stream)),
                want,
                "{name}"
            );
        }
    }
}
