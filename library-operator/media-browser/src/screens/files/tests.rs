use super::*;
use crate::tracks::preferences::{AUTO, ON, PREFER};

const REMUX: &str = "Film (2024)/Film (2024) [Remux-2160p].mkv";
const ENCODE: &str = "Film (2024)/Film (2024) [Bluray-1080p].mkv";

fn video_file(path: &str, size_bytes: i64) -> FileFacts {
    FileFacts {
        path: path.into(),
        role: "primary".into(),
        kind: "video".into(),
        size_bytes,
        ..FileFacts::default()
    }
}

fn picture(width: i64, height: i64, codec: &str) -> Stream {
    Stream {
        kind: "video".into(),
        codec: codec.into(),
        width,
        height,
        frame_rate: "24000/1001".into(),
        ..Stream::default()
    }
}

fn sound(language: &str, codec: &str, layout: &str) -> Stream {
    Stream {
        kind: "audio".into(),
        codec: codec.into(),
        layout: layout.into(),
        language: language.into(),
        ..Stream::default()
    }
}

fn text_track(language: &str, title: &str, events: Option<i64>) -> Stream {
    Stream {
        kind: "subtitle".into(),
        codec: "hdmv_pgs_subtitle".into(),
        language: language.into(),
        title: title.into(),
        events,
        ..Stream::default()
    }
}

fn beside(name: &str, events: Option<i64>) -> Subtitle {
    Subtitle {
        track: Entry::File(name.into()),
        events,
    }
}

// The remux: Dolby Vision over HDR10 in 10-bit HEVC, English TrueHD Atmos,
// an English commentary, and Spanish. Its subtitles are a full English
// track, an SDH one, a forced one, an empty one, and an SDH file beside it.
fn remux() -> (FileFacts, Tracks) {
    let mut video = picture(3_840, 2_076, "hevc");
    video.depth = 10;
    video.dolby_vision = true;
    video.color_transfer = "smpte2084".into();
    let mut atmos = sound("eng", "truehd", "7.1");
    atmos.profile = "Dolby TrueHD + Dolby Atmos".into();
    atmos.default = true;
    let mut commentary = sound("eng", "ac3", "stereo");
    commentary.title = "Director's Commentary".into();
    let tracks = Tracks {
        streams: vec![
            video,
            atmos,
            commentary,
            sound("spa", "ac3", "5.1(side)"),
            text_track("eng", "", Some(1_298)),
            text_track("eng", "SDH", Some(1_412)),
            text_track("eng", "Forced", Some(31)),
            text_track("eng", "", Some(0)),
            text_track("spa", "", Some(1_240)),
        ],
        beside: vec![beside("Film (2024) [Remux-2160p].en.sdh.srt", Some(1_415))],
    };
    (video_file(REMUX, 58_400_000_000), tracks)
}

// The encode: SDR H.264, DTS-HD MA and a commentary, and SRT tracks.
fn encode() -> (FileFacts, Tracks) {
    let mut dts = sound("eng", "dts", "5.1(side)");
    dts.profile = "DTS-HD MA".into();
    let tracks = Tracks {
        streams: vec![
            picture(1_920, 1_038, "h264"),
            dts,
            sound("eng", "ac3", "stereo"),
            Stream {
                codec: "subrip".into(),
                ..text_track("eng", "", Some(1_298))
            },
        ],
        beside: Vec::new(),
    };
    (video_file(ENCODE, 14_200_000_000), tracks)
}

fn english(subtitles: &str, sdh: &str) -> Preferences {
    Preferences {
        audio_languages: vec!["en".into()],
        subtitle_languages: vec!["en".into()],
        subtitles: subtitles.into(),
        sdh: sdh.into(),
    }
}

fn section(preferences: &Preferences) -> Files {
    Files::of(&[remux(), encode()], Some(REMUX), preferences)
}

fn group<'a>(block: &'a Block, label: &str) -> &'a Group {
    block
        .groups
        .iter()
        .find(|group| group.label == label)
        .expect("the block holds the group")
}

fn playing(group: &Group) -> Vec<usize> {
    group
        .rows
        .iter()
        .enumerate()
        .filter(|(_, row)| row.plays)
        .map(|(index, _)| index)
        .collect()
}

#[test]
fn every_encoding_takes_a_block_named_for_its_quality() {
    let files = section(&english(ON, ""));
    let heads: Vec<(&str, &str, &str)> = files
        .blocks
        .iter()
        .map(|block| {
            (
                block.name.as_str(),
                block.aside.as_str(),
                block.picture.as_str(),
            )
        })
        .collect();
    assert_eq!(
        heads,
        [
            (
                "Remux-2160p",
                "58.4 GB",
                "3840\u{d7}2076 \u{b7} HEVC 10-bit \u{b7} Dolby Vision, HDR10 \u{b7} 23.976 fps"
            ),
            (
                "Bluray-1080p",
                "14.2 GB",
                "1920\u{d7}1038 \u{b7} H.264 \u{b7} SDR \u{b7} 23.976 fps"
            ),
        ]
    );
}

#[test]
fn only_the_file_a_play_takes_carries_a_dot() {
    let files = section(&english(ON, ""));
    assert!(files.blocks[0].plays);
    assert!(!files.blocks[1].plays);
    assert!(
        files.blocks[1]
            .groups
            .iter()
            .all(|group| playing(group).is_empty())
    );
}

#[test]
fn a_sound_row_names_its_language_its_codec_and_its_channels() {
    let files = section(&english(ON, ""));
    let rows: Vec<(&str, &str, &str)> = group(&files.blocks[0], "Sound")
        .rows
        .iter()
        .map(|row| (row.language.as_str(), row.detail.as_str(), row.role))
        .collect();
    assert_eq!(
        rows,
        [
            ("English", "TrueHD Atmos 7.1", ""),
            ("English", "AC3 2.0", "commentary"),
            ("Spanish", "AC3 5.1", ""),
        ]
    );
    assert_eq!(
        group(&files.blocks[1], "Sound").rows[0].detail,
        "DTS-HD MA 5.1"
    );
}

#[test]
fn the_dot_marks_the_audio_track_of_the_first_language() {
    let spanish = Preferences {
        audio_languages: vec!["es".into()],
        ..Preferences::default()
    };
    assert_eq!(
        playing(group(&section(&english(ON, "")).blocks[0], "Sound")),
        [0]
    );
    assert_eq!(playing(group(&section(&spanish).blocks[0], "Sound")), [2]);
}

#[test]
fn a_subtitle_row_names_its_format_its_lines_and_its_role_and_an_empty_track_takes_none() {
    let files = section(&english(ON, ""));
    let rows: Vec<(&str, &str, &str)> = group(&files.blocks[0], "Subtitles")
        .rows
        .iter()
        .map(|row| (row.language.as_str(), row.detail.as_str(), row.role))
        .collect();
    assert_eq!(
        rows,
        [
            ("English", "PGS \u{b7} 1,298 lines", ""),
            ("English", "PGS \u{b7} 1,412 lines", "SDH"),
            ("English", "PGS \u{b7} 31 lines", "forced"),
            ("Spanish", "PGS \u{b7} 1,240 lines", ""),
            ("English", "SRT file \u{b7} 1,415 lines", "SDH"),
        ]
    );
}

#[test]
fn the_dot_marks_the_subtitle_track_the_rule_chooses() {
    let subtitles =
        |preferences: &Preferences| playing(group(&section(preferences).blocks[0], "Subtitles"));
    assert_eq!(subtitles(&english(ON, "")), [0]);
    assert_eq!(subtitles(&english(ON, PREFER)), [1]);
    assert_eq!(subtitles(&english(AUTO, "")), [2]);
}

#[test]
fn no_subtitle_track_carries_a_dot_while_the_mode_chooses_none() {
    for mode in ["off", ""] {
        let files = section(&english(mode, ""));
        assert!(
            playing(group(&files.blocks[0], "Subtitles")).is_empty(),
            "{mode}"
        );
        assert_eq!(playing(group(&files.blocks[0], "Sound")), [0], "{mode}");
    }
}

#[test]
fn a_movie_no_play_resolves_for_carries_no_dot() {
    let files = Files::of(&[remux()], None, &english(ON, ""));
    assert!(!files.blocks[0].plays);
    assert!(playing(group(&files.blocks[0], "Sound")).is_empty());
}

#[test]
fn a_file_the_probe_has_not_read_shows_what_the_scanner_read() {
    let bare = FileFacts {
        width: 1_280,
        height: 720,
        video_codec: "x264".into(),
        container: "mkv".into(),
        ..video_file("Film/Film.mkv", 700_000_000)
    };
    let files = Files::of(&[(bare, Tracks::default())], None, &Preferences::default());
    let block = &files.blocks[0];
    assert_eq!(
        (
            block.name.as_str(),
            block.picture.as_str(),
            block.aside.as_str()
        ),
        ("720p", "1280\u{d7}720 \u{b7} x264", "MKV \u{b7} 700 MB")
    );
    assert!(block.groups.is_empty());
}

#[test]
fn a_short_name_is_the_quality_tag_or_the_resolution_class() {
    let cases = [
        ("Film [Bluray-1080p].mkv", 1_920, "Bluray-1080p"),
        ("Film [WEBDL].mkv", 3_840, "4K WEBDL"),
        ("Film [x265].mkv", 3_840, "4K"),
        ("Film.mkv", 1_280, "720p"),
        ("Film.mkv", 720, "SD"),
        ("Film [HDTV].mkv", 0, "HDTV"),
        ("Film [2026, WEBDL-1080p].mkv", 1_920, "WEBDL-1080p"),
        (
            "Film [1999, Bluray-1080p Proper].mkv",
            1_920,
            "Bluray-1080p Proper",
        ),
        ("Film [2026, WEBDL].mkv", 3_840, "4K WEBDL"),
    ];
    for (path, width, name) in cases {
        let file = FileFacts {
            width,
            ..video_file(path, 0)
        };
        assert_eq!(labels::short_name(&file, None), name, "{path}");
    }
}

#[test]
fn the_dynamic_range_and_the_frame_rate_read_as_a_box_states_them() {
    let cases = [
        ("arib-std-b67", false, "25/1", "HLG \u{b7} 25 fps"),
        ("smpte2084", false, "24/1", "HDR10 \u{b7} 24 fps"),
        ("bt709", true, "30000/1001", "Dolby Vision \u{b7} 29.97 fps"),
        ("", false, "0/0", "SDR"),
    ];
    for (transfer, dolby_vision, rate, line) in cases {
        let video = Stream {
            color_transfer: transfer.into(),
            dolby_vision,
            frame_rate: rate.into(),
            ..Stream::default()
        };
        assert_eq!(
            labels::picture(&FileFacts::default(), Some(&video)),
            line,
            "{transfer}"
        );
    }
}

#[test]
fn a_sound_track_reads_its_channels_from_its_layout_or_its_count() {
    let cases = [
        ("flac", "", 1, "FLAC 1.0"),
        ("opus", "", 8, "Opus 7.1"),
        ("eac3", "", 6, "E-AC3 5.1"),
        ("pcm_s24le", "", 3, "PCM 3 ch"),
        ("aac", "mono", 1, "AAC 1.0"),
        ("dts", "5.1", 6, "DTS 5.1"),
        ("vorbis", "", 0, "Vorbis"),
    ];
    for (codec, layout, channels, detail) in cases {
        let stream = Stream {
            channels,
            ..sound("eng", codec, layout)
        };
        assert_eq!(labels::sound(&stream), detail, "{codec}");
    }
}

#[test]
fn a_subtitle_format_reads_as_its_common_name() {
    let cases = [
        ("ass", false, "ASS"),
        ("webvtt", false, "VTT"),
        ("dvd_subtitle", false, "VobSub"),
        ("vtt", true, "VTT file"),
        ("sup", true, "SUP file"),
    ];
    for (codec, file, named) in cases {
        assert_eq!(labels::subtitle(codec, file), named, "{codec}");
    }
    assert_eq!(labels::lines(Some(1)), "1 line");
    assert_eq!(labels::lines(None), "");
}

#[test]
fn a_track_that_names_no_language_reads_as_unknown() {
    let tracks = Tracks {
        streams: vec![sound("", "aac", "stereo")],
        beside: Vec::new(),
    };
    let files = Files::of(
        &[(video_file("Film.mkv", 0), tracks)],
        None,
        &Preferences::default(),
    );
    assert_eq!(files.blocks[0].groups[0].rows[0].language, "Unknown");
}

#[test]
fn two_blocks_stand_side_by_side_and_a_third_wraps_under_them() {
    let one = Files::of(&[remux()], None, &Preferences::default());
    let two = section(&Preferences::default());
    let three = Files::of(
        &[remux(), encode(), encode()],
        None,
        &Preferences::default(),
    );
    let head = text::height(1, look::HEADING) + HEADING_GAP;
    let tall = one.blocks[0].height();
    assert_eq!(one.height(), head + tall);
    assert_eq!(two.height(), head + tall);
    assert_eq!(
        three.height(),
        head + tall + ROW_GAP + three.blocks[2].height()
    );
    assert_eq!(Files::default().height(), 0.0);
}

#[test]
fn a_trailer_and_a_subtitle_file_take_no_block() {
    let trailer = FileFacts {
        role: "trailer".into(),
        ..video_file("Film-trailer.mkv", 0)
    };
    let subtitle = FileFacts {
        kind: "subtitle".into(),
        role: "subtitle".into(),
        ..FileFacts::default()
    };
    let files = Files::of(
        &[(trailer, Tracks::default()), (subtitle, Tracks::default())],
        None,
        &Preferences::default(),
    );
    assert!(files.blocks.is_empty());
}
