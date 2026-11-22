//! The `image` block of an image subtitle track: three numbers that
//! `library-operator`'s probe measures from the track's bitmaps, in rows
//! of the track's canvas. mpv cannot report them, because it receives an
//! image track one cue at a time. The media browser sends them in the
//! item's `presentation.subtitles`, and the display reads them from the
//! presentation block.

use serde_json::Value;

use crate::film::Track;

/// The numbers of one image track.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Measurement {
    /// The height of the track's canvas.
    pub canvas_height: u32,
    /// The height of one line of text with ascenders and descenders.
    pub line_height: u32,
    /// The row just below the last row of text of a usual bottom cue.
    pub ink_bottom: u32,
}

/// One entry of `presentation.subtitles` that has an `image` block.
#[derive(Debug, Clone, PartialEq)]
pub struct Measured {
    stream: Option<i64>,
    file: Option<String>,
    measurement: Measurement,
}

/// Reads the entries of `presentation.subtitles` that have an `image`
/// block.
pub fn parse(subtitles: Option<&Value>) -> Vec<Measured> {
    subtitles
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(|entry| {
            Some(Measured {
                stream: entry.get("stream").and_then(Value::as_i64),
                file: entry
                    .get("file")
                    .and_then(Value::as_str)
                    .filter(|file| !file.is_empty())
                    .map(str::to_string),
                measurement: measurement(entry.get("image")?)?,
            })
        })
        .collect()
}

/// The three numbers of an `image` block, each an integer of 1 or more.
fn measurement(image: &Value) -> Option<Measurement> {
    let number = |name: &str| {
        image
            .get(name)
            .and_then(Value::as_u64)
            .filter(|rows| *rows >= 1)
            .and_then(|rows| u32::try_from(rows).ok())
    };
    Some(Measurement {
        canvas_height: number("canvasHeight")?,
        line_height: number("lineHeight")?,
        ink_bottom: number("inkBottom")?,
    })
}

/// The measurement of `track`, or `None` when no entry matches it. An
/// embedded track matches an entry by its index in the container, and an
/// external file by its base name, because the pod mounts the media at
/// another path than the library's. `subtitleEvents` in the command
/// sidecar matches a track to its event count the same way.
pub fn find(measured: &[Measured], track: &Track) -> Option<Measurement> {
    let name = track
        .external_filename
        .as_deref()
        .map(|path| path.rsplit('/').next().unwrap_or(path));
    measured
        .iter()
        .find(|entry| match track.external {
            true => entry.file.is_some() && entry.file.as_deref() == name,
            false => entry.stream.is_some() && entry.stream == track.ff_index,
        })
        .map(|entry| entry.measurement)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    const FILM: Measurement = Measurement {
        canvas_height: 1080,
        line_height: 61,
        ink_bottom: 918,
    };

    const DVD: Measurement = Measurement {
        canvas_height: 480,
        line_height: 31,
        ink_bottom: 434,
    };

    fn subtitles() -> Vec<Measured> {
        parse(Some(&json!([
            { "stream": 2, "events": 900 },
            { "stream": 3, "events": 1382,
              "image": { "canvasHeight": 1080, "lineHeight": 61, "inkBottom": 918 } },
            { "file": "a-film.en.sup",
              "image": { "canvasHeight": 480, "lineHeight": 31, "inkBottom": 434 } },
        ])))
    }

    fn embedded(ff_index: i64) -> Track {
        Track {
            kind: "sub".to_string(),
            codec: Some("hdmv_pgs_subtitle".to_string()),
            ff_index: Some(ff_index),
            ..Track::default()
        }
    }

    fn external(path: &str) -> Track {
        Track {
            kind: "sub".to_string(),
            codec: Some("hdmv_pgs_subtitle".to_string()),
            ff_index: Some(0),
            external: true,
            external_filename: Some(path.to_string()),
            ..Track::default()
        }
    }

    /// An embedded track matches by its index in the container, and an
    /// external file by its base name, because the pod mounts the media
    /// at another path than the library's.
    #[test]
    fn a_track_finds_its_measurement_by_stream_or_by_file_name() {
        let cases = [
            ("the embedded stream 3", embedded(3), Some(FILM)),
            ("an embedded stream with no image block", embedded(2), None),
            ("an embedded stream with no entry", embedded(5), None),
            (
                "the external file",
                external("/media/films/a-film.en.sup"),
                Some(DVD),
            ),
            (
                "another external file",
                external("/media/films/a-film.fr.sup"),
                None,
            ),
        ];
        for (name, track, want) in cases {
            assert_eq!(find(&subtitles(), &track), want, "{name}");
        }
    }

    /// An external file is its own container, so its index is 0 and
    /// matches no entry's `stream`.
    #[test]
    fn an_external_file_does_not_match_by_its_index() {
        let measured = parse(Some(&json!([
            { "stream": 0,
              "image": { "canvasHeight": 1080, "lineHeight": 61, "inkBottom": 918 } },
        ])));
        assert_eq!(find(&measured, &external("/media/a-film.sup")), None);
    }

    /// The `Play`'s schema requires three integers of 1 or more, so an
    /// entry that breaks that rule is not a measurement.
    #[test]
    fn an_image_block_that_breaks_the_schema_is_ignored() {
        let measured = parse(Some(&json!([
            { "stream": 1, "image": { "canvasHeight": 1080, "lineHeight": 61 } },
            { "stream": 2, "image": { "canvasHeight": 1080, "lineHeight": 0, "inkBottom": 918 } },
            { "stream": 3, "image": { "canvasHeight": "1080", "lineHeight": 61, "inkBottom": 918 } },
            { "stream": 4, "image": null },
            "not an entry",
        ])));
        assert_eq!(measured, []);
        assert_eq!(parse(None), []);
        assert_eq!(parse(Some(&json!({ "stream": 3 }))), []);
    }
}
