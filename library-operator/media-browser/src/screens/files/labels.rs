// The words the files section writes for a file and its streams. The probe
// records ffprobe's own names, such as `hevc`, `truehd`, and
// `hdmv_pgs_subtitle`, and a person reads the names on a disc's box: HEVC,
// TrueHD, and PGS.

use crate::catalog::FileFacts;
use crate::catalog::stream::Stream;
use crate::screens::facts;

// The widths at which a frame is 4K, 1080p, and 720p. A film's frame is
// often cropped to its own aspect, such as 1920×804 for 2.39:1, so the class
// reads the width.
const UHD: i64 = 3_200;
const FULL_HD: i64 = 1_800;
const HD: i64 = 1_200;

// The words a quality tag names its source with, in the names Radarr and
// Sonarr give a file, such as `[Bluray-1080p]` or `[WEBDL-2160p]`.
const SOURCES: [&str; 8] = [
    "remux", "bluray", "web", "hdtv", "dvd", "sdtv", "br-disk", "raw-hd",
];

/// A short name for one encoding: its quality tag where the file name
/// carries one, such as `Bluray-1080p`, and its resolution class otherwise,
/// such as `4K`. A tag that names no resolution follows the class, as in
/// `1080p WEBDL`.
pub fn short_name(file: &FileFacts, video: Option<&Stream>) -> String {
    let class = class(video.map_or(file.width, |video| video.width));
    match quality(&file.path) {
        Some(tag) if resolution_in(tag) || class.is_empty() => tag.to_string(),
        Some(tag) => format!("{class} {tag}"),
        None => class.to_string(),
    }
}

fn class(width: i64) -> &'static str {
    match width {
        width if width >= UHD => "4K",
        width if width >= FULL_HD => "1080p",
        width if width >= HD => "720p",
        width if width > 0 => "SD",
        _ => "",
    }
}

// The first bracketed part of the file's name that names a source.
fn quality(path: &str) -> Option<&str> {
    let name = path.rsplit('/').next().unwrap_or(path);
    name.split('[')
        .skip(1)
        .filter_map(|part| part.split_once(']').map(|(tag, _)| tag))
        .find(|tag| {
            let lower = tag.to_lowercase();
            SOURCES.iter().any(|source| lower.contains(source))
        })
}

// Whether a tag names a resolution, such as the 1080p of Bluray-1080p.
fn resolution_in(tag: &str) -> bool {
    tag.to_lowercase()
        .split(|c: char| !c.is_ascii_alphanumeric())
        .any(|word| {
            word.strip_suffix('p').is_some_and(|digits| {
                (3..=4).contains(&digits.len()) && digits.chars().all(|c| c.is_ascii_digit())
            })
        })
}

/// The picture line: the frame, the codec with its depth, the dynamic
/// range, and the frame rate. A file the probe has not read shows the frame
/// and the codec the scanner read from its name.
pub fn picture(file: &FileFacts, video: Option<&Stream>) -> String {
    let Some(video) = video else {
        return facts::joined(&[&frame(file.width, file.height), &file.video_codec]);
    };
    let codec = match video.depth >= 10 {
        true => format!("{} {}-bit", video_codec(&video.codec), video.depth),
        false => video_codec(&video.codec).to_string(),
    };
    facts::joined(&[
        &frame(video.width, video.height),
        &codec,
        &range(video),
        &frame_rate(&video.frame_rate),
    ])
}

fn frame(width: i64, height: i64) -> String {
    match width > 0 && height > 0 {
        true => format!("{width}\u{d7}{height}"),
        false => String::new(),
    }
}

fn video_codec(codec: &str) -> &str {
    match codec {
        "hevc" => "HEVC",
        "h264" => "H.264",
        "av1" => "AV1",
        "vp9" => "VP9",
        "mpeg2video" => "MPEG-2",
        "mpeg4" => "MPEG-4",
        "vc1" => "VC-1",
        other => other,
    }
}

// The dynamic range: Dolby Vision from its configuration record, HDR10 from
// the PQ transfer, and HLG from its own. A Dolby Vision file with an HDR10
// base layer plays as HDR10 on a panel without Dolby Vision, so the line
// names both.
fn range(video: &Stream) -> String {
    let mut parts = Vec::new();
    if video.dolby_vision {
        parts.push("Dolby Vision");
    }
    match video.color_transfer.as_str() {
        "smpte2084" => parts.push("HDR10"),
        "arib-std-b67" => parts.push("HLG"),
        _ => {}
    }
    match parts.is_empty() {
        true => "SDR".to_string(),
        false => parts.join(", "),
    }
}

// A frame rate as ffprobe writes it, such as 24000/1001, in frames per
// second to three places at most.
fn frame_rate(rate: &str) -> String {
    let (numerator, denominator) = rate.split_once('/').unwrap_or((rate, "1"));
    let (Ok(numerator), Ok(denominator)) = (numerator.parse::<f64>(), denominator.parse::<f64>())
    else {
        return String::new();
    };
    if numerator <= 0.0 || denominator <= 0.0 {
        return String::new();
    }
    let fixed = format!("{:.3}", numerator / denominator);
    format!("{} fps", fixed.trim_end_matches('0').trim_end_matches('.'))
}

/// One sound track: its codec, Atmos where its profile names it, and its
/// channels.
pub fn sound(stream: &Stream) -> String {
    let atmos = stream.profile.to_lowercase().contains("atmos");
    let codec = audio_codec(stream);
    let parts = [
        codec.as_str(),
        if atmos { "Atmos" } else { "" },
        &channels(stream),
    ];
    parts
        .iter()
        .filter(|part| !part.is_empty())
        .copied()
        .collect::<Vec<_>>()
        .join(" ")
}

fn audio_codec(stream: &Stream) -> String {
    let named = match stream.codec.as_str() {
        "truehd" => "TrueHD",
        "ac3" => "AC3",
        "eac3" => "E-AC3",
        "aac" => "AAC",
        "flac" => "FLAC",
        "opus" => "Opus",
        "mp3" => "MP3",
        "vorbis" => "Vorbis",
        "alac" => "ALAC",
        // ffprobe names the DTS extensions in the profile: DTS-HD MA,
        // DTS-HD HRA, and DTS Express.
        "dts" if stream.profile.starts_with("DTS") => {
            return stream
                .profile
                .split(" + ")
                .next()
                .unwrap_or("DTS")
                .to_string();
        }
        "dts" => "DTS",
        pcm if pcm.starts_with("pcm_") => "PCM",
        other => other,
    };
    named.to_string()
}

// The channels in the form a box states them: 7.1, 5.1, 2.0, or 1.0.
fn channels(stream: &Stream) -> String {
    let layout = stream.layout.split('(').next().unwrap_or_default();
    match layout {
        "mono" => "1.0".to_string(),
        "stereo" => "2.0".to_string(),
        layout if layout.starts_with(|c: char| c.is_ascii_digit()) => layout.to_string(),
        _ => match stream.channels {
            1 => "1.0".to_string(),
            2 => "2.0".to_string(),
            6 => "5.1".to_string(),
            8 => "7.1".to_string(),
            0 => String::new(),
            count => format!("{count} ch"),
        },
    }
}

/// One subtitle track's format: the codec of an embedded stream, or the
/// extension of a file beside the video, which reads as a file.
pub fn subtitle(codec: &str, file: bool) -> String {
    let named = match codec.to_lowercase().as_str() {
        "subrip" | "srt" => "SRT".to_string(),
        "ass" => "ASS".to_string(),
        "ssa" => "SSA".to_string(),
        "webvtt" | "vtt" => "VTT".to_string(),
        "mov_text" => "Timed Text".to_string(),
        "hdmv_pgs_subtitle" => "PGS".to_string(),
        "dvd_subtitle" => "VobSub".to_string(),
        "dvb_subtitle" => "DVB".to_string(),
        _ => codec.to_uppercase(),
    };
    match file {
        true => format!("{named} file"),
        false => named,
    }
}

/// How many events a subtitle track holds, as lines, and nothing where the
/// probe read no count.
pub fn lines(events: Option<i64>) -> String {
    match events {
        Some(1) => "1 line".to_string(),
        Some(count) => format!("{} lines", grouped(count)),
        None => String::new(),
    }
}

// A count with a comma between each group of three digits.
fn grouped(count: i64) -> String {
    let digits = count.to_string();
    let mut out = String::new();
    for (index, digit) in digits.chars().enumerate() {
        if index > 0 && (digits.len() - index).is_multiple_of(3) {
            out.push(',');
        }
        out.push(digit);
    }
    out
}
