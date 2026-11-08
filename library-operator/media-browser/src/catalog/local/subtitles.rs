// The subtitle tracks of one video: its embedded subtitle streams from the
// `streams` table, and the text subtitle files beside it from the `files`
// table, each with the event count the probe read. The display matches an
// entry to a track of mpv's `track-list` by the stream's `ff-index` or by the
// external file's name, so only a file that mpv loads gets an entry.

use rusqlite::Connection;

use super::collect;
use crate::catalog::{Subtitle, Track};

// The subtitle formats whose cues the probe counts, as the library operator's
// `textSubtitleExtensions` names them. A picture format carries no cue the
// probe reads, so it has no count to send.
const TEXT_EXTENSIONS: [&str; 4] = ["srt", "vtt", "ass", "ssa"];

/// Every subtitle track of one video file: the embedded streams in container
/// order, then the external files in name order. The ordinal of a stream row
/// is its position in ffprobe's answer, which lists every stream of every
/// kind by its index, so the ordinal is the container index mpv reads.
pub fn subtitles(
    connection: &Connection,
    library: &str,
    path: &str,
) -> rusqlite::Result<Vec<Subtitle>> {
    let mut tracks = collect(
        connection,
        "SELECT ordinal, events FROM streams \
         WHERE library = ? AND path = ? AND kind = 'subtitle' ORDER BY ordinal",
        &[&library, &path],
        |row| {
            Ok(Subtitle {
                track: Track::Stream(row.get(0)?),
                events: row.get(1)?,
            })
        },
    )?;
    tracks.extend(beside(connection, library, path)?);
    Ok(tracks)
}

// The text subtitle files in the video's folder that mpv loads for it. The
// read is a range of the primary key, every path under the folder, and the
// files of a deeper folder are left out after it, because mpv reads only the
// video's own folder.
pub(super) fn beside(
    connection: &Connection,
    library: &str,
    path: &str,
) -> rusqlite::Result<Vec<Subtitle>> {
    let (folder, video) = match path.rsplit_once('/') {
        Some((folder, video)) => (format!("{folder}/"), video),
        None => (String::new(), path),
    };
    // '0' is the character after '/', so every path that starts with the
    // folder sorts below the folder with its slash replaced by it.
    let past = match folder.strip_suffix('/') {
        Some(name) => format!("{name}0"),
        None => String::new(),
    };
    let files = collect(
        connection,
        "SELECT path, events FROM files \
         WHERE library = ? AND type = 'subtitle' AND path >= ? AND (? = '' OR path < ?) \
         ORDER BY path",
        &[&library, &folder, &past, &past],
        |row| Ok((row.get::<_, String>(0)?, row.get::<_, Option<i64>>(1)?)),
    )?;
    Ok(files
        .into_iter()
        .filter_map(|(file, events)| {
            let name = file.strip_prefix(&folder)?;
            (!name.contains('/') && text(name) && loads(video, name)).then(|| Subtitle {
                track: Track::File(name.into()),
                events,
            })
        })
        .collect())
}

fn text(name: &str) -> bool {
    name.rsplit_once('.').is_some_and(|(_, extension)| {
        TEXT_EXTENSIONS
            .iter()
            .any(|t| t.eq_ignore_ascii_case(extension))
    })
}

/// Whether mpv's default `--sub-auto=exact` loads a subtitle file for a
/// video in the same folder. mpv loads a file whose name without its
/// extension is the video's name without its extension, and a file whose
/// name is the video's name followed by a language tag, with any of the
/// `forced`, `sdh`, `hi`, `cc`, and `default` segments after the tag. Both
/// comparisons ignore ASCII case. This is `append_dir_subtitles` in mpv's
/// `player/external_files.c`.
fn loads(video: &str, file: &str) -> bool {
    let video = stem(video).as_bytes();
    let file = stem(file).as_bytes();
    if file.eq_ignore_ascii_case(video) {
        return true;
    }
    file.len() > video.len()
        && file[..video.len()].eq_ignore_ascii_case(video)
        && language_start(file) == Some(video.len())
}

// A name without its extension and without the spaces around it, as mpv's
// `bstr_strip_ext` and `bstr_strip` make it.
fn stem(name: &str) -> &str {
    name.rsplit_once('.')
        .map_or(name, |(stem, _)| stem)
        .trim_matches(|c: char| c.is_ascii_whitespace())
}

// The segments mpv reads as flags of a track and not as its language.
const FLAG_SEGMENTS: [&[u8]; 5] = [b"sdh", b"hi", b"cc", b"forced", b"default"];

/// Where the language tag at the end of a name without its extension
/// starts: the position of the delimiter before it, or nothing when the
/// name ends in no tag. A tag is a primary subtag of two or three letters
/// and optional subtags of one to eight letters after a hyphen, as in `en`,
/// `eng`, and `pt-BR`. Flag segments after the tag are skipped. This is
/// `mp_guess_lang_from_filename` in mpv's `misc/language.c`, step for step,
/// so a name mpv reads no tag from, such as `Film.eng.0`, reads none here.
fn language_start(name: &[u8]) -> Option<usize> {
    if name.len() < 2 {
        return None;
    }
    let at = |i: isize| (i >= 0).then(|| name[i as usize]);
    let mut i = name.len() as isize - 1;
    let mut delimiter = b'.';
    if at(i) == Some(b')') {
        delimiter = b'(';
        i -= 1;
    }
    if at(i) == Some(b']') {
        delimiter = b'[';
        i -= 1;
    }
    let mut length = 0;
    let mut subtags = 0;
    loop {
        while at(i).is_some_and(|c| c.is_ascii_alphabetic()) {
            length += 1;
            i -= 1;
        }
        if length >= 2 && at(i) == Some(delimiter) {
            let start = (i + 1) as usize;
            let segment = &name[start..start + length];
            if FLAG_SEGMENTS
                .iter()
                .any(|flag| segment.eq_ignore_ascii_case(flag))
            {
                length = 0;
                i -= if delimiter == b'.' { 1 } else { 2 };
                continue;
            }
        }
        if length < subtags + 1 || length > subtags + 8 {
            return None;
        }
        if at(i) == Some(b'-') {
            length += 1;
            i -= 1;
            subtags = length;
        } else {
            break;
        }
    }
    if length < subtags + 2 || length > subtags + 3 || i <= 0 || at(i) != Some(delimiter) {
        return None;
    }
    Some(i as usize)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn mpv_loads_the_files_named_as_the_video_or_as_the_video_and_a_language() {
        let cases = [
            ("Film.srt", true),
            ("film.SRT", true),
            ("Film.en.srt", true),
            ("Film.eng.srt", true),
            ("Film.en.hi.srt", true),
            ("Film.eng.forced.srt", true),
            ("Film.en.sdh.cc.srt", true),
            ("Film.pt-BR.srt", true),
            ("Film.zh-Hant-TW.srt", true),
            ("Film.eng.0.srt", false),
            ("Film.forced.srt", false),
            ("Film.english.srt", false),
            ("Film.e.srt", false),
            ("Film Extended.en.srt", false),
            ("Film2.en.srt", false),
            ("Other.en.srt", false),
            ("Film.en-toolongsubtag.srt", false),
        ];
        for (file, loaded) in cases {
            assert_eq!(loads("Film.mkv", file), loaded, "{file}");
        }
    }

    #[test]
    fn a_tag_in_brackets_or_parentheses_is_a_language() {
        let cases = [
            ("Film[en]", Some(4)),
            ("Film(eng)", Some(4)),
            ("Film(en)(forced)", Some(4)),
            ("Film", None),
            ("x", None),
            (".en", None),
        ];
        for (name, start) in cases {
            assert_eq!(language_start(name.as_bytes()), start, "{name}");
        }
    }
}
