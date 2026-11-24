// The format of an audio or subtitle track, in the words on a disc's box.
// ffprobe and mpv give their own names, such as `truehd` and
// `hdmv_pgs_subtitle`, and a person reads TrueHD and PGS. The functions take
// ffprobe's values, because the probe's rows in the catalog and the audio
// entries of a `Play`'s presentation both have them. mpv reports the same
// codec names, but it reports the profile and the layout only for the
// audio track it decodes.

/// One audio track's format: its codec, Atmos where its profile names it,
/// and its channels, such as `E-AC3 Atmos 5.1`, `DTS-HD MA 7.1`, or
/// `AAC 2.0`. `codec` and `profile` are ffprobe's `codec_name` and
/// `profile`, `layout` is its `channel_layout`, such as `5.1(side)`, and
/// `channels` is its channel count. An empty value or a count of zero adds
/// nothing.
pub fn audio(codec: &str, profile: &str, layout: &str, channels: i64) -> String {
    let atmos = profile.to_lowercase().contains("atmos");
    let codec = audio_codec(codec, profile);
    let parts = [
        codec.as_str(),
        if atmos { "Atmos" } else { "" },
        &audio_channels(layout, channels),
    ];
    parts
        .iter()
        .filter(|part| !part.is_empty())
        .copied()
        .collect::<Vec<_>>()
        .join(" ")
}

fn audio_codec(codec: &str, profile: &str) -> String {
    let named = match codec {
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
        "dts" if profile.starts_with("DTS") => {
            return profile.split(" + ").next().unwrap_or("DTS").to_string();
        }
        "dts" => "DTS",
        pcm if pcm.starts_with("pcm_") => "PCM",
        other => other,
    };
    named.to_string()
}

// The channels in the form a box states them: 7.1, 5.1, 2.0, or 1.0. The
// layout says it where ffprobe names one, and the count says it otherwise.
fn audio_channels(layout: &str, channels: i64) -> String {
    let layout = layout.split('(').next().unwrap_or_default();
    match layout {
        "mono" => "1.0".to_string(),
        "stereo" => "2.0".to_string(),
        layout if layout.starts_with(|c: char| c.is_ascii_digit()) => layout.to_string(),
        _ => match channels {
            1 => "1.0".to_string(),
            2 => "2.0".to_string(),
            6 => "5.1".to_string(),
            8 => "7.1".to_string(),
            0 => String::new(),
            count => format!("{count} ch"),
        },
    }
}

/// One subtitle track's format: the codec of an embedded stream, such as
/// `PGS`, or the extension of a file beside the video, which reads as a
/// file, such as `SRT file`. `codec` is ffprobe's codec name for an
/// embedded stream and the file's extension when `file` is true.
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_audio_track_reads_as_its_codec_atmos_and_its_channels() {
        let cases = [
            (
                "eac3",
                "Dolby Digital Plus + Dolby Atmos",
                "5.1(side)",
                6,
                "E-AC3 Atmos 5.1",
            ),
            (
                "truehd",
                "Dolby TrueHD + Dolby Atmos",
                "7.1",
                8,
                "TrueHD Atmos 7.1",
            ),
            ("dts", "DTS-HD MA + DTS:X", "7.1", 8, "DTS-HD MA 7.1"),
            ("dts", "", "5.1(side)", 6, "DTS 5.1"),
            ("ac3", "", "5.1(side)", 6, "AC3 5.1"),
            ("aac", "LC", "stereo", 2, "AAC 2.0"),
            ("mp3", "", "", 2, "MP3 2.0"),
            ("alac", "", "", 0, "ALAC"),
            ("pcm_s16le", "", "", 1, "PCM 1.0"),
        ];
        for (codec, profile, layout, channels, format) in cases {
            assert_eq!(audio(codec, profile, layout, channels), format, "{codec}");
        }
    }

    #[test]
    fn an_audio_track_reads_its_channels_from_its_layout_or_its_count() {
        let cases = [
            ("flac", "", 1, "FLAC 1.0"),
            ("opus", "", 8, "Opus 7.1"),
            ("eac3", "", 6, "E-AC3 5.1"),
            ("pcm_s24le", "", 3, "PCM 3 ch"),
            ("aac", "mono", 1, "AAC 1.0"),
            ("dts", "5.1", 6, "DTS 5.1"),
            ("vorbis", "", 0, "Vorbis"),
            ("wmapro", "", 0, "wmapro"),
        ];
        for (codec, layout, channels, format) in cases {
            assert_eq!(audio(codec, "", layout, channels), format, "{codec}");
        }
    }

    #[test]
    fn a_subtitle_format_reads_as_its_common_name() {
        let cases = [
            ("subrip", false, "SRT"),
            ("srt", true, "SRT file"),
            ("ass", false, "ASS"),
            ("ssa", false, "SSA"),
            ("webvtt", false, "VTT"),
            ("vtt", true, "VTT file"),
            ("mov_text", false, "Timed Text"),
            ("hdmv_pgs_subtitle", false, "PGS"),
            ("dvd_subtitle", false, "VobSub"),
            ("dvb_subtitle", false, "DVB"),
            ("sup", true, "SUP file"),
        ];
        for (codec, file, format) in cases {
            assert_eq!(subtitle(codec, file), format, "{codec}");
        }
    }
}
