//! The cues of a `mov_text` track inside an MP4 file, read through the
//! track's sample table. `moov` lists the time, the size, and the place of
//! every sample of every track, so the display reads `moov` and then each
//! sample of the track, many at once. A `mov_text` sample is a 16-bit length
//! and the text, which style boxes can follow. The copy takes the text and
//! the bold, italic, and underline of the `styl` box, which FFmpeg's
//! `movtextdec` writes inline. mpv draws a `mov_text` track as a converted
//! track: `configure_ass` puts the house style and the house alignment on
//! the default style, so the track's own default style and justification
//! do not reach the screen, and the copy leaves them out.
//!
//! FFmpeg moves a track's samples by its edit list. The copy does the same
//! for the usual list: empty edits before the first edit add their time,
//! and the first edit's media time is subtracted. A list that repeats or
//! skips parts of the track is not followed.

use super::at::{self, At};
use super::source::Why;
use super::{Copy, Dialogue};

/// The most bytes that the reader takes for `moov`: the limit of every
/// read. The sample tables of a long film are a few megabytes.
const LARGEST_MOOV: u64 = at::LARGEST_READ as u64;

/// The most samples of one track. A cue each second for a day is 86,400,
/// and a sample count that a table states beyond this is refused before
/// the reader builds a list of that length.
const MOST_SAMPLES: usize = 1_000_000;

/// The most bytes of one sample. A `mov_text` sample is a 16-bit length
/// and its text, with a few style boxes.
const LARGEST_SAMPLE: u32 = 1 << 20;

/// The boxes in `body`, each as its type and body. A box that runs past the
/// end of `body` ends the list.
fn boxes(body: &[u8]) -> Vec<([u8; 4], &[u8])> {
    let mut found = Vec::new();
    let mut at = 0usize;
    while at + 8 <= body.len() {
        let size = u32::from_be_bytes(body[at..at + 4].try_into().unwrap_or_default()) as usize;
        let kind: [u8; 4] = body[at + 4..at + 8].try_into().unwrap_or_default();
        let (header, size) = match size {
            0 => (8, body.len() - at),
            1 if at + 16 <= body.len() => (
                16,
                u64::from_be_bytes(body[at + 8..at + 16].try_into().unwrap_or_default()) as usize,
            ),
            size => (8, size),
        };
        if size < header || at + size > body.len() {
            break;
        }
        found.push((kind, &body[at + header..at + size]));
        at += size;
    }
    found
}

fn find<'a>(body: &'a [u8], path: &[&[u8; 4]]) -> Option<&'a [u8]> {
    let mut at = body;
    for kind in path {
        at = boxes(at).into_iter().find(|(found, _)| found == *kind)?.1;
    }
    Some(at)
}

fn u32_at(bytes: &[u8], at: usize) -> Option<u32> {
    Some(u32::from_be_bytes(bytes.get(at..at + 4)?.try_into().ok()?))
}

fn u64_at(bytes: &[u8], at: usize) -> Option<u64> {
    Some(u64::from_be_bytes(bytes.get(at..at + 8)?.try_into().ok()?))
}

/// The timescale of an `mvhd` or `mdhd` box.
fn timescale(header: &[u8]) -> Option<u64> {
    let at = if header.first() == Some(&1) { 20 } else { 12 };
    u32_at(header, at).map(u64::from).filter(|scale| *scale > 0)
}

/// The `moov` box, found among the top-level boxes of the file.
fn moov<F: At + ?Sized>(file: &F) -> Result<Vec<u8>, Why> {
    let mut at = 0u64;
    loop {
        let head = file.at(at, 16).map_err(Why::io)?;
        if head.len() < 8 {
            return Err(Why::NoIndex);
        }
        let size = u64::from(u32_at(&head, 0).unwrap_or(0));
        let (header, size) = match size {
            1 => (16, u64_at(&head, 8).ok_or(Why::NoIndex)?),
            0 => return Err(Why::NoIndex),
            size => (8, size),
        };
        if size < header {
            return Err(Why::NoIndex);
        }
        if &head[4..8] == b"moov" {
            if size > LARGEST_MOOV {
                return Err(Why::Broken(format!(
                    "moov states {size} bytes, over the limit of {LARGEST_MOOV}"
                )));
            }
            let body = file
                .at(at + header, (size - header) as usize)
                .map_err(Why::io)?;
            if body.len() as u64 != size - header {
                return Err(Why::Broken("the file ends inside moov".to_string()));
            }
            return Ok(body);
        }
        at += size;
    }
}

/// The milliseconds that the edit list adds to a sample's time, and the
/// media time that it subtracts.
fn edits(trak: &[u8], movie: u64) -> (u64, u64) {
    let Some(list) = find(trak, &[b"edts", b"elst"]) else {
        return (0, 0);
    };
    let wide = list.first() == Some(&1);
    let count = u32_at(list, 4).unwrap_or(0) as usize;
    let entry = if wide { 20 } else { 12 };
    let mut empty = 0u64;
    for index in 0..count {
        let at = 8 + index * entry;
        let (duration, media) = match wide {
            true => (
                u64_at(list, at),
                u64_at(list, at + 8).map(|time| time as i64),
            ),
            false => (
                u32_at(list, at).map(u64::from),
                u32_at(list, at + 4).map(|time| i64::from(time as i32)),
            ),
        };
        let (Some(duration), Some(media)) = (duration, media) else {
            break;
        };
        if media == -1 {
            empty += duration;
            continue;
        }
        return (empty * 1000 / movie, media.max(0) as u64);
    }
    (empty * 1000 / movie, 0)
}

/// The offset, the size, the start, and the duration of each sample of a
/// sample table, in the units of the media's timescale. It is `None` when
/// a table states more entries than its box holds, more than
/// `MOST_SAMPLES` samples, or a number of times or places that is not the
/// number of samples.
fn samples(table: &[u8]) -> Option<Vec<(u64, u32, u64, u64)>> {
    let stsz = find(table, &[b"stsz"])?;
    let fixed = u32_at(stsz, 4)?;
    let count = u32_at(stsz, 8)? as usize;
    if count > MOST_SAMPLES || (fixed == 0 && stsz.len() < 12 + count * 4) {
        return None;
    }
    let sizes: Vec<u32> = match fixed {
        0 => (0..count)
            .map(|index| u32_at(stsz, 12 + index * 4))
            .collect::<Option<_>>()?,
        size => vec![size; count],
    };
    let stts = find(table, &[b"stts"])?;
    let entries = u32_at(stts, 4)? as usize;
    if stts.len() < 8 + entries.checked_mul(8)? {
        return None;
    }
    let mut times = Vec::with_capacity(count);
    let mut clock = 0u64;
    let mut timed = 0usize;
    for index in 0..entries {
        let runs = u32_at(stts, 8 + index * 8)? as usize;
        let delta = u64::from(u32_at(stts, 12 + index * 8)?);
        timed = timed.checked_add(runs)?;
        if timed > count {
            return None;
        }
        for _ in 0..runs {
            times.push((clock, delta));
            clock = clock.checked_add(delta)?;
        }
    }
    if timed != count {
        return None;
    }
    let (table_box, wide) = match (find(table, &[b"stco"]), find(table, &[b"co64"])) {
        (Some(stco), _) => (stco, false),
        (None, Some(co64)) => (co64, true),
        _ => return None,
    };
    let entries = u32_at(table_box, 4)? as usize;
    let width = if wide { 8 } else { 4 };
    if table_box.len() < 8 + entries.checked_mul(width)? {
        return None;
    }
    let chunks: Vec<u64> = (0..entries)
        .map(|index| match wide {
            true => u64_at(table_box, 8 + index * 8),
            false => u32_at(table_box, 8 + index * 4).map(u64::from),
        })
        .collect::<Option<_>>()?;
    let stsc = find(table, &[b"stsc"])?;
    let entries = u32_at(stsc, 4)? as usize;
    if stsc.len() < 8 + entries.checked_mul(12)? {
        return None;
    }
    let runs: Vec<(usize, usize)> = (0..entries)
        .map(|index| {
            Some((
                u32_at(stsc, 8 + index * 12)? as usize,
                u32_at(stsc, 12 + index * 12)? as usize,
            ))
        })
        .collect::<Option<_>>()?;
    let mut found = Vec::with_capacity(count);
    for (chunk, offset) in chunks.iter().enumerate() {
        let per_chunk = runs
            .iter()
            .take_while(|(first, _)| *first <= chunk + 1)
            .last()
            .map_or(0, |(_, per_chunk)| *per_chunk);
        let mut at = *offset;
        for _ in 0..per_chunk.min(count - found.len()) {
            let sample = found.len();
            let (start, duration) = times[sample];
            found.push((at, sizes[sample], start, duration));
            at = at.checked_add(u64::from(sizes[sample]))?;
        }
    }
    (found.len() == count).then_some(found)
}

/// The style of a run of a sample's text: bold, italic, and underline, as
/// the flags of a `StyleRecord`.
fn face_tags(face: u8, on: bool) -> String {
    let mut out = String::new();
    for (bit, tag) in [(1, "b"), (2, "i"), (4, "u")] {
        if face & bit != 0 {
            out.push_str(&format!("{{\\{tag}{}}}", u8::from(on)));
        }
    }
    out
}

/// The text of one `mov_text` sample as ASS, or `Ok(None)` for an empty
/// sample, which marks a time with no cue. A sample whose text is not
/// UTF-8 or UTF-16, or whose length runs past the sample, is an error.
fn text(sample: &[u8]) -> Result<Option<String>, ()> {
    let length = usize::from(u16::from_be_bytes([
        *sample.first().ok_or(())?,
        *sample.get(1).ok_or(())?,
    ]));
    let bytes = sample.get(2..2 + length).ok_or(())?;
    if bytes.is_empty() {
        return Ok(None);
    }
    let text = match bytes.strip_prefix(&[0xFE, 0xFF]) {
        Some(wide) => String::from_utf16(
            &wide
                .as_chunks::<2>()
                .0
                .iter()
                .map(|pair| u16::from_be_bytes(*pair))
                .collect::<Vec<_>>(),
        )
        .map_err(|_| ())?,
        None => String::from_utf8(bytes.to_vec()).map_err(|_| ())?,
    };
    let chars: Vec<char> = text.chars().collect();
    let mut before: Vec<String> = vec![String::new(); chars.len() + 1];
    for (kind, body) in boxes(&sample[2 + length..]) {
        if &kind != b"styl" {
            continue;
        }
        let count = usize::from(u16::from_be_bytes([
            *body.first().ok_or(())?,
            *body.get(1).ok_or(())?,
        ]));
        for index in 0..count {
            let record = body.get(2 + index * 12..14 + index * 12).ok_or(())?;
            let start = usize::from(u16::from_be_bytes([record[0], record[1]])).min(chars.len());
            let end = usize::from(u16::from_be_bytes([record[2], record[3]])).min(chars.len());
            before[start].push_str(&face_tags(record[6], true));
            before[end].insert_str(0, &face_tags(record[6], false));
        }
    }
    let mut out = String::new();
    for (index, character) in chars.iter().enumerate() {
        out.push_str(&before[index]);
        out.push(*character);
    }
    out.push_str(&before[chars.len()]);
    let out = out.replace("\r\n", "\n");
    Ok(Some(out.trim_matches('\n').replace('\n', "\\N")))
}

/// The `track_ID` of a `trak`, from its `tkhd`.
fn track_id(trak: &[u8]) -> Option<u32> {
    let tkhd = find(trak, &[b"tkhd"])?;
    u32_at(tkhd, if tkhd.first() == Some(&1) { 20 } else { 12 })
}

/// The copy of the `mov_text` track whose `src-id` is `src_id`. mpv plays
/// MP4 through FFmpeg and reports a stream's `id`, the `track_ID` of its
/// `trak`, as `src-id`. Every sample must be read, so a sample that cannot
/// be read fails the whole copy.
pub fn read<F: At + ?Sized>(file: &F, src_id: i64) -> Result<Copy, Why> {
    let moov = moov(file)?;
    let movie = find(&moov, &[b"mvhd"]).and_then(timescale).unwrap_or(1000);
    let trak = boxes(&moov)
        .into_iter()
        .filter(|(kind, _)| kind == b"trak")
        .map(|(_, trak)| trak)
        .find(|trak| track_id(trak).map(i64::from) == Some(src_id))
        .ok_or(Why::Mismatch)?;
    let handler = find(trak, &[b"mdia", b"hdlr"]).and_then(|hdlr| hdlr.get(8..12));
    let stsd = find(trak, &[b"mdia", b"minf", b"stbl", b"stsd"]).ok_or(Why::Mismatch)?;
    if !matches!(handler, Some(b"sbtl" | b"text")) || stsd.get(12..16) != Some(b"tx3g") {
        return Err(Why::Mismatch);
    }
    let scale = find(trak, &[b"mdia", b"mdhd"])
        .and_then(timescale)
        .ok_or(Why::NoIndex)?;
    let table = find(trak, &[b"mdia", b"minf", b"stbl"]).ok_or(Why::NoIndex)?;
    let samples = samples(table)
        .ok_or_else(|| Why::Broken("the track's sample table is not whole".to_string()))?;
    if samples.is_empty() {
        return Err(Why::NoIndex);
    }
    let (shift, media) = edits(trak, movie);
    let texts = at::each(samples.len(), |index| {
        let (offset, size, _, _) = samples[index];
        if size > LARGEST_SAMPLE {
            return Err(());
        }
        let bytes = file.at(offset, size as usize).map_err(|_| ())?;
        text(&bytes)
    })
    .ok_or_else(|| Why::Broken("a read of the file stopped".to_string()))?;
    let ms = |units: u64| units.saturating_sub(media) * 1000 / scale + shift;
    let mut events = Vec::with_capacity(samples.len());
    for ((offset, _, start, duration), text) in samples.iter().zip(texts) {
        let text = text
            .map_err(|_| Why::Broken(format!("the sample at byte {offset} could not be read")))?;
        if let Some(text) = text {
            events.push(Dialogue::text(ms(*start), ms(start + duration), text));
        }
    }
    Ok(Copy::converted(events))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn mp4_box(kind: &[u8; 4], body: &[u8]) -> Vec<u8> {
        let mut out = ((body.len() + 8) as u32).to_be_bytes().to_vec();
        out.extend_from_slice(kind);
        out.extend_from_slice(body);
        out
    }

    fn full(kind: &[u8; 4], body: &[u8]) -> Vec<u8> {
        mp4_box(kind, &[&[0, 0, 0, 0], body].concat())
    }

    fn words(values: &[u32]) -> Vec<u8> {
        values
            .iter()
            .flat_map(|value| value.to_be_bytes())
            .collect()
    }

    fn sample(text: &str) -> Vec<u8> {
        [&(text.len() as u16).to_be_bytes()[..], text.as_bytes()].concat()
    }

    /// The tables and the entry of the text track of a fixture. `stts` and
    /// `stsz` are the words of those boxes, so a test can state counts that
    /// a hostile file states.
    struct Tables {
        stts: Vec<u32>,
        stsz: Vec<u32>,
        vertical: u8,
        face: u8,
    }

    /// The tables of the three samples of `file`: an empty one, then two of
    /// text.
    fn tables(sizes: [u32; 3]) -> Tables {
        Tables {
            stts: vec![2, 1, 500, 2, 1500],
            stsz: vec![0, 3, sizes[0], sizes[1], sizes[2]],
            vertical: 0xFF,
            face: 0,
        }
    }

    /// A `tx3g` entry, centred, with a vertical justification and the face
    /// flags of its default style.
    fn tx3g(vertical: u8, face: u8) -> Vec<u8> {
        let mut body = vec![0; 38];
        body[12] = 1;
        body[13] = vertical;
        body[32] = face;
        mp4_box(b"tx3g", &body)
    }

    fn trak(id: u32, rest: &[u8]) -> Vec<u8> {
        mp4_box(
            b"trak",
            &[full(b"tkhd", &words(&[0, 0, id])), rest.to_vec()].concat(),
        )
    }

    /// The `moov` of a video track, `track_ID` 1, and a `mov_text` track,
    /// `track_ID` 2, whose samples are at `offsets`, with an empty edit of
    /// one second before the track.
    fn moov(offsets: [u32; 2], tables: &Tables) -> Vec<u8> {
        let header = |scale: u32| full(b"mdhd", &words(&[0, 0, scale, 0]));
        let video = trak(
            1,
            &mp4_box(
                b"mdia",
                &[
                    header(1000),
                    full(b"hdlr", &[&[0; 4], b"vide".as_slice()].concat()),
                ]
                .concat(),
            ),
        );
        let table = mp4_box(
            b"stbl",
            &[
                full(
                    b"stsd",
                    &[words(&[1]), tx3g(tables.vertical, tables.face)].concat(),
                ),
                full(b"stts", &words(&tables.stts)),
                full(b"stsz", &words(&tables.stsz)),
                full(b"stsc", &words(&[1, 1, 2, 1])),
                full(b"stco", &words(&[2, offsets[0], offsets[1]])),
            ]
            .concat(),
        );
        let text = trak(
            2,
            &[
                mp4_box(
                    b"edts",
                    &full(b"elst", &words(&[1, 1000, u32::MAX, 0x0001_0000])),
                ),
                mp4_box(
                    b"mdia",
                    &[
                        header(1000),
                        full(b"hdlr", &[&[0; 4], b"sbtl".as_slice()].concat()),
                        mp4_box(b"minf", &table),
                    ]
                    .concat(),
                ),
            ]
            .concat(),
        );
        mp4_box(
            b"moov",
            &[full(b"mvhd", &words(&[0, 0, 1000, 0])), video, text].concat(),
        )
    }

    /// The file: `ftyp`, `moov`, and `mdat` with an empty sample, then two
    /// samples of text in two chunks. `change` alters the tables.
    fn file_with(samples: [Vec<u8>; 3], change: impl Fn(&mut Tables)) -> Vec<u8> {
        let sizes = samples.each_ref().map(|sample| sample.len() as u32);
        let mut tables = tables(sizes);
        change(&mut tables);
        let ftyp = mp4_box(b"ftyp", b"isom\0\0\0\0");
        let start = (ftyp.len() + moov([0, 0], &tables).len() + 8) as u32;
        let offsets = [start, start + sizes[0] + sizes[1]];
        [
            ftyp,
            moov(offsets, &tables),
            mp4_box(b"mdat", &samples.concat()),
        ]
        .concat()
    }

    fn file() -> Vec<u8> {
        file_with(
            [sample(""), sample("One line"), sample("Two\r\nlines")],
            |_| {},
        )
    }

    #[test]
    fn a_mov_text_track_is_read_through_its_sample_table() {
        let bytes = file();
        assert_eq!(
            read(bytes.as_slice(), 2),
            Ok(Copy::converted(vec![
                Dialogue::text(1500, 3000, "One line".to_string()),
                Dialogue::text(3000, 4500, "Two\\Nlines".to_string()),
            ]))
        );
    }

    /// mpv's `src-id` is the `track_ID`, so the reader does not count the
    /// `trak` boxes.
    #[test]
    fn a_track_that_is_not_mov_text_is_refused() {
        let bytes = file();
        assert_eq!(read(bytes.as_slice(), 1), Err(Why::Mismatch));
        assert_eq!(read(bytes.as_slice(), 3), Err(Why::Mismatch));
    }

    /// A table that states more samples or entries than its box holds, or
    /// more than `MOST_SAMPLES`, is refused before a list of that length is
    /// built.
    #[test]
    fn a_hostile_sample_table_is_refused() {
        type Change = fn(&mut Tables);
        let cases: [(&str, Change); 4] = [
            ("stts with 2^32 − 1 runs", |tables| {
                tables.stts = vec![1, u32::MAX, 1]
            }),
            ("stts with 2^32 − 1 entries", |tables| {
                tables.stts[0] = u32::MAX
            }),
            ("stsz with 2^32 − 1 samples", |tables| {
                tables.stsz[1] = u32::MAX
            }),
            ("a fixed size for 2^32 − 1 samples", |tables| {
                tables.stsz = vec![8, u32::MAX]
            }),
        ];
        for (name, change) in cases {
            let bytes = file_with([sample(""), sample("One"), sample("Two")], change);
            assert!(
                matches!(read(bytes.as_slice(), 2), Err(Why::Broken(_))),
                "{name}"
            );
        }
    }

    /// A sample whose size is over the limit, or whose text runs past the
    /// sample, fails the whole copy.
    #[test]
    fn a_broken_sample_fails_the_copy() {
        let huge = file_with([sample(""), sample("One"), sample("Two")], |tables| {
            tables.stsz[3] = 1 << 30;
        });
        let truncated = file_with([sample(""), vec![0, 40, b'x'], sample("Two")], |_| {});
        let not_utf8 = file_with([sample(""), vec![0, 2, 0xC3, 0x28], sample("Two")], |_| {});
        for bytes in [huge, truncated, not_utf8] {
            assert!(matches!(read(bytes.as_slice(), 2), Err(Why::Broken(_))));
        }
    }

    /// mpv draws a `mov_text` track as a converted track, with the house
    /// style and the house alignment, so the copy keeps only the runs of a
    /// `styl` box, as FFmpeg writes them inline. The track's justification
    /// and its default face do not reach the screen today, and the copy
    /// leaves them out.
    #[test]
    fn only_the_styl_runs_are_kept() {
        let styled = [
            &sample("Bold word")[..],
            &mp4_box(
                b"styl",
                &[&[0, 1][..], &[0, 0, 0, 4, 0, 1, 1, 18, 255, 255, 255, 255]].concat(),
            ),
        ]
        .concat();
        assert_eq!(text(&styled), Ok(Some("{\\b1}Bold{\\b0} word".to_string())));

        let bytes = file_with([sample(""), sample("One"), sample("Two")], |tables| {
            tables.vertical = 0;
            tables.face = 4;
        });
        let copy = read(bytes.as_slice(), 2).expect("the copy");
        assert_eq!(copy.events[0].text, "One");
    }

    /// A file cut off inside `moov` gives no copy and does not panic.
    #[test]
    fn a_truncated_file_gives_no_copy() {
        let bytes = file();
        for cut in [10, 60, bytes.len() / 2] {
            assert!(read(&bytes[..cut], 2).is_err(), "cut at {cut}");
        }
    }

    #[test]
    fn a_wide_sample_is_read_as_utf16() {
        let wide = [0, 6, 0xFE, 0xFF, 0, b'H', 0, b'i'];
        assert_eq!(text(&wide), Ok(Some("Hi".to_string())));
        assert_eq!(text(&[0, 0]), Ok(None));
    }
}
