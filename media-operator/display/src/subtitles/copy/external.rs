//! A subtitle file beside the video, which mpv loads as an external track.
//! The file is small, so the display reads it whole and converts it by the
//! codec that mpv reports for the track.

use super::source::Why;
use super::{Copy, ass, microdvd, subrip, webvtt};

/// The copy of a file whose bytes are `bytes`.
pub fn convert(bytes: &[u8], codec: &str, fps: Option<f64>) -> Result<Copy, Why> {
    let text = decode(bytes);
    match codec {
        "subrip" => Ok(Copy::converted(subrip::parse(&text))),
        "webvtt" => Ok(Copy::converted(webvtt::parse(&text))),
        "microdvd" => microdvd::parse(&text, fps)
            .map(Copy::converted)
            .ok_or_else(|| {
                Why::Broken("the MicroDVD file states no usable frame rate".to_string())
            }),
        "ass" | "ssa" => Ok(ass::parse(&text)),
        _ => Err(Why::Codec(codec.to_string())),
    }
}

/// The characters that Windows-1252 gives the bytes 0x80 to 0x9F, where
/// it differs from Latin-1. A byte that the encoding leaves undefined
/// reads as the control character of the same number, as in Latin-1.
const WINDOWS_1252: [char; 32] = [
    '\u{20ac}', '\u{81}', '\u{201a}', '\u{192}', '\u{201e}', '\u{2026}', '\u{2020}', '\u{2021}',
    '\u{2c6}', '\u{2030}', '\u{160}', '\u{2039}', '\u{152}', '\u{8d}', '\u{17d}', '\u{8f}',
    '\u{90}', '\u{2018}', '\u{2019}', '\u{201c}', '\u{201d}', '\u{2022}', '\u{2013}', '\u{2014}',
    '\u{2dc}', '\u{2122}', '\u{161}', '\u{203a}', '\u{153}', '\u{9d}', '\u{17e}', '\u{178}',
];

/// The text of a file. A byte-order mark names UTF-8, UTF-16 LE, or UTF-16
/// BE. A file without one is UTF-8 when it decodes as UTF-8, and otherwise
/// Windows-1252, the usual encoding of an older Western file. mpv guesses
/// an encoding with uchardet, so a file in another legacy encoding can read
/// differently here, but its line breaks, which place the cue, do not
/// change.
pub fn decode(bytes: &[u8]) -> String {
    if let Some(rest) = bytes.strip_prefix(b"\xef\xbb\xbf") {
        return String::from_utf8_lossy(rest).into_owned();
    }
    let wide = |rest: &[u8], unit: fn([u8; 2]) -> u16| {
        let units: Vec<u16> = rest
            .as_chunks::<2>()
            .0
            .iter()
            .map(|pair| unit(*pair))
            .collect();
        String::from_utf16_lossy(&units)
    };
    if let Some(rest) = bytes.strip_prefix(b"\xff\xfe") {
        return wide(rest, u16::from_le_bytes);
    }
    if let Some(rest) = bytes.strip_prefix(b"\xfe\xff") {
        return wide(rest, u16::from_be_bytes);
    }
    match std::str::from_utf8(bytes) {
        Ok(text) => text.to_string(),
        Err(_) => bytes
            .iter()
            .map(|byte| match byte {
                0x80..=0x9F => WINDOWS_1252[usize::from(byte - 0x80)],
                _ => char::from(*byte),
            })
            .collect(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::subtitles::copy::Dialogue;

    #[test]
    fn each_text_format_is_read_by_its_codec() {
        let cases: [(&[u8], &str); 4] = [
            (b"1\n00:00:01,000 --> 00:00:02,000\nTwo\nlines\n", "subrip"),
            (b"WEBVTT\n\n00:01.000 --> 00:02.000\nTwo\nlines\n", "webvtt"),
            (b"{25}{50}Two|lines\n", "microdvd"),
            (
                b"[Script Info]\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Two\\Nlines\n",
                "ass",
            ),
        ];
        for (bytes, codec) in cases {
            let copy = convert(bytes, codec, Some(25.0)).expect("a text codec");
            assert_eq!(
                copy.events,
                [Dialogue::text(1000, 2000, "Two\\Nlines".to_string())],
                "{codec}"
            );
        }
        assert_eq!(
            convert(b"", "dvd_subtitle", None),
            Err(Why::Codec("dvd_subtitle".to_string()))
        );
    }

    /// The encodings of a subtitle file: UTF-8 with or without a mark,
    /// UTF-16 by its mark, and Windows-1252 for anything else.
    #[test]
    fn each_encoding_is_decoded() {
        let utf16 = |text: &str, le: bool| -> Vec<u8> {
            let mut bytes = match le {
                true => vec![0xFF, 0xFE],
                false => vec![0xFE, 0xFF],
            };
            for unit in text.encode_utf16() {
                bytes.extend(match le {
                    true => unit.to_le_bytes(),
                    false => unit.to_be_bytes(),
                });
            }
            bytes
        };
        let cases: [(Vec<u8>, &str); 5] = [
            (b"caf\xc3\xa9".to_vec(), "café"),
            (b"\xef\xbb\xbfcaf\xc3\xa9".to_vec(), "café"),
            (utf16("café", true), "café"),
            (utf16("café", false), "café"),
            (
                b"It\x92s caf\xe9 \x80".to_vec(),
                "It\u{2019}s café \u{20ac}",
            ),
        ];
        for (bytes, want) in cases {
            assert_eq!(decode(&bytes), want, "{bytes:?}");
        }
    }

    /// A MicroDVD file with a frame rate of 0 times nothing.
    #[test]
    fn a_microdvd_file_without_a_rate_is_refused() {
        assert!(matches!(
            convert(b"{1}{1}0\n{10}{20}x\n", "microdvd", None),
            Err(Why::Broken(_))
        ));
    }
}
