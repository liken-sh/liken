//! Chooses where the cues of a track come from: the file beside the video
//! for an external track, the index of a Matroska file, or the sample
//! table of an MP4 file. Every other track keeps the placement of plan 46,
//! and the reason goes in the display's log.

use std::fmt;
use std::fs::File;
use std::io;

use super::at::{At, Current, For};
use super::{Copy, external, matroska, mp4};
use crate::film::Track;

/// Why a track has no copy.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Why {
    /// The codec is not one of the text codecs.
    Codec(String),
    /// The video is not a local Matroska or MP4 file.
    Container,
    /// The file lists no block of the track in its index.
    NoIndex,
    /// The track's entry in the file is not the track that mpv reports.
    Mismatch,
    /// The track's blocks are compressed.
    Compressed,
    /// A read of the file failed.
    Unreadable(String),
    /// A part of the track could not be read or decoded. The copy would
    /// miss cues, so it is not made.
    Broken(String),
    /// The track has no cue.
    Empty,
    /// The copy has a different number of cues from the count in the
    /// item's presentation.
    Count { expected: i64, found: usize },
    /// Another read replaced this one.
    Superseded,
}

impl Why {
    pub fn io(error: io::Error) -> Self {
        match error.kind() {
            io::ErrorKind::Interrupted => Self::Superseded,
            _ => Self::Unreadable(error.to_string()),
        }
    }
}

impl fmt::Display for Why {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Codec(codec) => write!(f, "the codec {codec:?} is not text"),
            Self::Container => write!(f, "the video is not a local Matroska or MP4 file"),
            Self::NoIndex => write!(f, "the file's index lists no cue of the track"),
            Self::Mismatch => write!(f, "the file's track entry does not match the track"),
            Self::Compressed => write!(f, "the track's blocks are compressed"),
            Self::Unreadable(error) => write!(f, "the file could not be read: {error}"),
            Self::Broken(what) => write!(f, "{what}"),
            Self::Empty => write!(f, "the track has no cue"),
            Self::Count { expected, found } => write!(
                f,
                "the copy has {found} cues and the presentation counts {expected}"
            ),
            Self::Superseded => write!(f, "another read replaced this one"),
        }
    }
}

/// The codecs whose cues the display can copy, as mpv names them.
const TEXT: [&str; 6] = ["subrip", "ass", "ssa", "webvtt", "mov_text", "microdvd"];

/// The name of the original track in the name of its copy's file. A track
/// inside the video is known by its `ff-index`, and an external track by
/// the id that mpv gives it, which stays the same while the file plays.
pub fn identity(track: &Track) -> String {
    match track.external {
        true => format!("external-{}", track.id),
        false => format!("ff-index-{}", track.ff_index.unwrap_or(-1)),
    }
}

/// What a read needs besides the track: the video's frame rate for a
/// MicroDVD file, the file's start time, the number of cues that the item's presentation counts
/// for the track, and the read's number, which stops the read once another
/// read is current.
pub struct Ask<'a> {
    pub fps: Option<f64>,
    /// The file's `demuxer-start-time`, in seconds.
    pub start: f64,
    pub events: Option<i64>,
    pub current: &'a Current,
    pub read: u64,
}

/// The number of cues by which a copy can differ from the presentation's
/// count. The count is the `NUMBER_OF_FRAMES` tag that mkvmerge writes, one
/// for each block of the track, and the copy has one event for each block.
/// The slack covers a count that a later edit of the file left behind by a
/// few blocks. A larger difference means that the index misses blocks.
fn tolerance(expected: i64) -> i64 {
    (expected / 100).max(2)
}

/// Reads the copy of `track` from the video at `path`. It blocks on the
/// file, so the display runs it off the thread that draws.
pub fn read(path: &str, track: &Track, ask: &Ask) -> Result<Copy, Why> {
    let mut copy = read_track(path, track, ask)?;
    if !track.external {
        shift(&mut copy, ask.start);
    }
    if copy.events.is_empty() {
        return Err(Why::Empty);
    }
    if !parses(&copy.script(None)) {
        return Err(Why::Broken(
            "the copy's script has a NUL byte or no event format, so libass would read no event"
                .to_string(),
        ));
    }
    if let Some(expected) = ask.events
        && (copy.events.len() as i64 - expected).abs() > tolerance(expected)
    {
        return Err(Why::Count {
            expected,
            found: copy.events.len(),
        });
    }
    Ok(copy)
}

/// Moves each cue of an embedded track back by the file's start time.
/// mpv plays a file from its `demuxer-start-time` as 0, and moves the
/// packets of the file's own tracks by it. It does not move a subtitle file
/// that it adds, and mpv 0.41.0 drew both a `.srt` file beside a video that
/// starts at 10 s and the same cues inside the video at the same playback
/// times. So the copy of an external file keeps its times, and the copy of
/// an embedded track, which mpv adds as a file, moves back here. A cue that
/// ends before the start of playback starts and ends at 0.
fn shift(copy: &mut Copy, start: f64) {
    let start = (start.max(0.0) * 1000.0).round() as u64;
    for event in &mut copy.events {
        event.start = event.start.saturating_sub(start);
        event.end = event.end.saturating_sub(start);
    }
}

/// Whether libass reads the events of `script`. libass stops at the first
/// NUL byte, and it needs the `Format` line of `[Events]` to read a
/// `Dialogue` line.
fn parses(script: &str) -> bool {
    !script.contains('\0') && script.contains("\n[Events]\nFormat: ")
}

fn read_track(path: &str, track: &Track, ask: &Ask) -> Result<Copy, Why> {
    let codec = track.codec.clone().unwrap_or_default();
    if !TEXT.contains(&codec.as_str()) {
        return Err(Why::Codec(codec));
    }
    let open = |path: &str| File::open(path).map_err(Why::io);
    if track.external {
        let path = track.external_filename.as_deref().ok_or(Why::Container)?;
        if path.contains("://") {
            return Err(Why::Container);
        }
        let file = open(path)?;
        let length = file.metadata().map_err(Why::io)?.len();
        let reading = For {
            file: &file,
            current: ask.current,
            read: ask.read,
        };
        let bytes = reading
            .at(0, usize::try_from(length).unwrap_or(usize::MAX))
            .map_err(Why::io)?;
        return external::convert(&bytes, &codec, ask.fps);
    }
    if path.contains("://") {
        return Err(Why::Container);
    }
    let file = open(path)?;
    let reading = For {
        file: &file,
        current: ask.current,
        read: ask.read,
    };
    let src_id = track.src_id.ok_or(Why::Mismatch)?;
    let head = reading.at(0, 12).map_err(Why::io)?;
    match (head.get(..4), head.get(4..8)) {
        (Some([0x1A, 0x45, 0xDF, 0xA3]), _) => matroska::read(&reading, src_id, &codec),
        (_, Some(b"ftyp")) if codec == "mov_text" => mp4::read(&reading, src_id),
        _ => Err(Why::Container),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn track(codec: &str) -> Track {
        Track {
            id: 2,
            kind: "sub".to_string(),
            codec: Some(codec.to_string()),
            ff_index: Some(3),
            src_id: Some(4),
            ..Track::default()
        }
    }

    fn ask(current: &Current, events: Option<i64>) -> Ask<'_> {
        current.set(1);
        Ask {
            fps: None,
            start: 1.4,
            events,
            current,
            read: 1,
        }
    }

    #[test]
    fn a_picture_track_has_no_copy() {
        let current = Current::default();
        assert_eq!(
            read(
                "/media/a.mkv",
                &track("hdmv_pgs_subtitle"),
                &ask(&current, None)
            ),
            Err(Why::Codec("hdmv_pgs_subtitle".to_string()))
        );
    }

    #[test]
    fn a_stream_from_the_network_has_no_copy() {
        let current = Current::default();
        assert_eq!(
            read(
                "https://example.com/a.mkv",
                &track("subrip"),
                &ask(&current, None)
            ),
            Err(Why::Container)
        );
    }

    #[test]
    fn the_identity_names_the_stream_or_the_external_track() {
        let external = Track {
            external: true,
            external_filename: Some("/media/a.en.srt".to_string()),
            ..track("subrip")
        };
        assert_eq!(identity(&track("subrip")), "ff-index-3");
        assert_eq!(identity(&external), "external-2");
    }

    /// An external file with `contents`, read as the display reads it.
    fn external(test: &str, contents: &str, events: Option<i64>) -> Result<usize, Why> {
        let path =
            std::env::temp_dir().join(format!("media-display-{}-{test}.srt", std::process::id()));
        std::fs::write(&path, contents).expect("writing the file");
        let external = Track {
            external: true,
            external_filename: Some(path.display().to_string()),
            ..track("subrip")
        };
        let current = Current::default();
        let copy = read("/media/a.mkv", &external, &ask(&current, events));
        let _ = std::fs::remove_file(&path);
        copy.map(|copy| copy.events.len())
    }

    const TWO_CUES: &str =
        "1\n00:00:01,000 --> 00:00:02,000\nOne\n\n2\n00:00:03,000 --> 00:00:04,000\nTwo\n";

    /// The display reads an external file whole. mpv times an added file
    /// from the start of playback and does not move it by the video's
    /// start time, so the copy of an external file keeps its times. The
    /// fixture's start time is 1.4 s.
    #[test]
    fn an_external_file_is_read_whole() {
        assert_eq!(external("whole", TWO_CUES, None), Ok(2));
    }

    /// mpv plays a file from its start time as 0, so the cues of a track in
    /// the file move back by the start time, and a cue that ends before it
    /// starts at 0.
    #[test]
    fn the_cues_of_an_embedded_track_move_back_by_the_start_time() {
        let mut copy = Copy::converted(vec![
            super::super::Dialogue::text(10_500, 12_000, "One".to_string()),
            super::super::Dialogue::text(500, 2_000, "Early".to_string()),
        ]);
        shift(&mut copy, 10.0);
        let times: Vec<(u64, u64)> = copy
            .events
            .iter()
            .map(|event| (event.start, event.end))
            .collect();
        assert_eq!(times, [(500, 2_000), (0, 0)]);
    }

    /// libass stops reading a script at its first NUL byte, so a copy whose
    /// script has one would show nothing, and it is not made.
    #[test]
    fn a_copy_that_libass_would_stop_reading_is_refused() {
        let file = "[Script Info]\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Bad\0text\n";
        let path =
            std::env::temp_dir().join(format!("media-display-{}-nul.ass", std::process::id()));
        std::fs::write(&path, file).expect("writing the file");
        let external = Track {
            external: true,
            external_filename: Some(path.display().to_string()),
            ..track("ass")
        };
        let current = Current::default();
        let copy = read("/media/a.mkv", &external, &ask(&current, None));
        let _ = std::fs::remove_file(&path);
        assert!(matches!(copy, Err(Why::Broken(_))), "{copy:?}");
    }

    /// A copy with no cue would show nothing, so it is not made.
    #[test]
    fn a_track_with_no_cue_has_no_copy() {
        assert_eq!(
            external("empty", "not a subtitle file\n", None),
            Err(Why::Empty)
        );
    }

    /// The presentation's count of cues must match the copy within its
    /// tolerance, or the copy misses cues.
    #[test]
    fn a_copy_that_misses_cues_is_refused() {
        assert_eq!(external("close", TWO_CUES, Some(4)), Ok(2));
        assert_eq!(
            external("far", TWO_CUES, Some(5)),
            Err(Why::Count {
                expected: 5,
                found: 2
            })
        );
        assert_eq!(tolerance(1000), 10);
    }

    /// A read that another read replaced stops with no copy.
    #[test]
    fn a_superseded_read_stops() {
        let path = std::env::temp_dir().join(format!(
            "media-display-{}-superseded.srt",
            std::process::id()
        ));
        std::fs::write(&path, TWO_CUES).expect("writing the file");
        let external = Track {
            external: true,
            external_filename: Some(path.display().to_string()),
            ..track("subrip")
        };
        let current = Current::default();
        current.set(2);
        let ask = Ask {
            fps: None,
            start: 0.0,
            events: None,
            current: &current,
            read: 1,
        };
        let copy = read("/media/a.mkv", &external, &ask);
        let _ = std::fs::remove_file(&path);
        assert_eq!(copy.map(|copy| copy.events.len()), Err(Why::Superseded));
    }
}
