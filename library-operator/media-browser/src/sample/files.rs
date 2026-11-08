// The invented files of one sample movie: two encodings of the film, each
// with the streams a probe of a real release reads, and an SDH subtitle
// file beside the smaller one. The names follow Radarr's, so the quality
// tag reads off the name as it does in a real library, and the encoding
// whose name sorts first is the one a play takes, as in the local catalog.
// Radarr writes the tag in brackets when its naming format asks for them.

use crate::catalog::stream::{Stream, Tracks};
use crate::catalog::{FileFacts, Subtitle, Track};
use crate::tracks::Preferences;
use crate::tracks::preferences::{ON, PREFER};

// The two encodings, by their quality tags. Bluray-1080p sorts before
// Remux-2160p, so a play of a sample movie takes the 1080p file.
const ENCODE: &str = "Bluray-1080p";
const REMUX: &str = "Remux-2160p";

// The path of one encoding of the movie titled `title`.
fn path(title: &str, quality: &str) -> String {
    format!("{title}/{title} [{quality}].mkv")
}

/// The path a play of the movie takes: the first video file by path.
pub fn played(title: &str) -> String {
    path(title, ENCODE)
}

/// The movie's files in path order: the subtitle file beside the 1080p
/// encoding, then the two encodings.
pub fn files(title: &str, number: i64) -> Vec<FileFacts> {
    let video = |quality: &str, width, height, size_bytes| FileFacts {
        path: path(title, quality),
        role: "primary".into(),
        kind: "video".into(),
        container: "mkv".into(),
        width,
        height,
        size_bytes,
        ..FileFacts::default()
    };
    vec![
        FileFacts {
            path: format!("{title}/{title} [{ENCODE}].en.sdh.srt"),
            role: "subtitle".into(),
            kind: "subtitle".into(),
            container: "srt".into(),
            language: "en".into(),
            ..FileFacts::default()
        },
        video(ENCODE, 1_920, 804, 14_200_000_000 + number * 1_000_000),
        video(REMUX, 3_840, 1_606, 58_400_000_000 + number * 1_000_000),
    ]
}

/// The streams of one encoding, and the subtitle file beside the 1080p
/// one. A path that names neither holds none.
pub fn tracks(title: &str, file: &str) -> Tracks {
    if file == path(title, ENCODE) {
        return encode(title);
    }
    if file == path(title, REMUX) {
        return remux();
    }
    Tracks::default()
}

/// The preferences the sample runs under: a household that hears and
/// reads English and prefers SDH tracks, so a page shows a dot on both
/// kinds of track.
pub fn preferences() -> Preferences {
    Preferences {
        audio_languages: vec!["en".into()],
        subtitle_languages: vec!["en".into()],
        subtitles: ON.into(),
        sdh: PREFER.into(),
    }
}

fn picture(width: i64, height: i64, codec: &str) -> Stream {
    Stream {
        kind: "video".into(),
        codec: codec.into(),
        width,
        height,
        depth: 8,
        frame_rate: "24000/1001".into(),
        color_transfer: "bt709".into(),
        ..Stream::default()
    }
}

fn sound(language: &str, codec: &str, profile: &str, layout: &str, title: &str) -> Stream {
    Stream {
        kind: "audio".into(),
        codec: codec.into(),
        profile: profile.into(),
        layout: layout.into(),
        language: language.into(),
        title: title.into(),
        ..Stream::default()
    }
}

fn text(codec: &str, language: &str, title: &str, events: i64) -> Stream {
    Stream {
        kind: "subtitle".into(),
        codec: codec.into(),
        language: language.into(),
        title: title.into(),
        events: Some(events),
        ..Stream::default()
    }
}

// The streams in container order, each numbered by its place.
fn numbered(streams: Vec<Stream>) -> Vec<Stream> {
    streams
        .into_iter()
        .enumerate()
        .map(|(ordinal, stream)| Stream {
            ordinal: ordinal as i64,
            ..stream
        })
        .collect()
}

fn encode(title: &str) -> Tracks {
    let mut forced = text("subrip", "eng", "Forced", 29);
    forced.forced = true;
    Tracks {
        streams: numbered(vec![
            picture(1_920, 804, "h264"),
            Stream {
                default: true,
                ..sound("eng", "dts", "DTS-HD MA", "5.1(side)", "")
            },
            sound("eng", "ac3", "", "stereo", "Commentary"),
            text("subrip", "eng", "", 1_298),
            forced,
            text("subrip", "spa", "", 1_240),
        ]),
        beside: vec![Subtitle {
            track: Track::File(format!("{title} [{ENCODE}].en.sdh.srt")),
            events: Some(1_415),
        }],
    }
}

fn remux() -> Tracks {
    let mut video = picture(3_840, 1_606, "hevc");
    video.depth = 10;
    video.dolby_vision = true;
    video.color_transfer = "smpte2084".into();
    let mut forced = text("hdmv_pgs_subtitle", "eng", "Forced", 31);
    forced.forced = true;
    forced.default = true;
    Tracks {
        streams: numbered(vec![
            video,
            Stream {
                default: true,
                ..sound("eng", "truehd", "Dolby TrueHD + Dolby Atmos", "7.1", "")
            },
            sound("eng", "ac3", "", "stereo", "Commentary"),
            sound("spa", "ac3", "", "5.1(side)", ""),
            text("hdmv_pgs_subtitle", "eng", "", 1_298),
            text("hdmv_pgs_subtitle", "eng", "SDH", 1_412),
            forced,
            text("hdmv_pgs_subtitle", "eng", "", 0),
            text("hdmv_pgs_subtitle", "spa", "", 1_240),
        ]),
        beside: Vec::new(),
    }
}
