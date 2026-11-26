//! The cues of a text track inside a Matroska file, read through the file's
//! index. A film's subtitle blocks are spread among its video clusters, so
//! reading the file through would read the whole film. The `Cues` element
//! lists the cluster and the place in it of every subtitle block that
//! mkvmerge and FFmpeg write, so the display reads the index and then each
//! block, many at once.
//!
//! The path through the file is: the EBML header, the `Segment`, the
//! `SeekHead` elements before the first cluster, which give the place of
//! `Info`, `Tracks`, `Cues`, and any further `SeekHead`, then each
//! `CuePoint` of the track. A cue point gives the cluster's place from the
//! start of the segment's data, and the block's place from the start of the
//! cluster's data. The cluster's own timestamp, which the block's time
//! counts from, is in the cluster's header.

use std::collections::BTreeMap;

use super::at::{self, At};
use super::source::Why;
use super::{Copy, Dialogue, ass, subrip, webvtt};

const EBML: u32 = 0x1A45_DFA3;
const SEGMENT: u32 = 0x1853_8067;
const SEEK_HEAD: u32 = 0x114D_9B74;
const SEEK: u32 = 0x4DBB;
const SEEK_ID: u32 = 0x53AB;
const SEEK_POSITION: u32 = 0x53AC;
const INFO: u32 = 0x1549_A966;
const TIMESTAMP_SCALE: u32 = 0x2A_D7B1;
const TRACKS: u32 = 0x1654_AE6B;
const TRACK_ENTRY: u32 = 0xAE;
const TRACK_NUMBER: u32 = 0xD7;
const CODEC_ID: u32 = 0x86;
const CODEC_PRIVATE: u32 = 0x63A2;
const CONTENT_ENCODINGS: u32 = 0x6D80;
const CLUSTER: u32 = 0x1F43_B675;
const CLUSTER_TIMESTAMP: u32 = 0xE7;
const CUES: u32 = 0x1C53_BB6B;
const CUE_POINT: u32 = 0xBB;
const CUE_TRACK_POSITIONS: u32 = 0xB7;
const CUE_TRACK: u32 = 0xF7;
const CUE_CLUSTER_POSITION: u32 = 0xF1;
const CUE_RELATIVE_POSITION: u32 = 0xF0;
const CUE_DURATION: u32 = 0xB2;
const SIMPLE_BLOCK: u32 = 0xA3;
const BLOCK_GROUP: u32 = 0xA0;
const BLOCK: u32 = 0xA1;
const BLOCK_DURATION: u32 = 0x9B;

/// The nanoseconds in one unit of a timestamp when `Info` gives no scale.
const DEFAULT_SCALE: u64 = 1_000_000;

/// The most bytes that the reader takes for `SeekHead`, `Info`, or
/// `Tracks`. The largest of them is `Tracks`, whose `CodecPrivate` of an ASS
/// track is the script's header, rarely more than a few hundred kilobytes.
/// Fonts are attachments, outside `Tracks`.
const LARGEST_HEADER: u64 = 16 << 20;

/// The most bytes of `Cues`. A cue point is about 20 bytes, so a film of
/// three hours with a cue point each second for the video and one for each
/// subtitle block is under a megabyte. The limit is the reader's own.
const LARGEST_CUES: u64 = at::LARGEST_READ as u64;

/// The most bytes of one subtitle block. A cue of text is a few hundred
/// bytes, and an ASS event with a drawing a few kilobytes.
const LARGEST_BLOCK: u64 = 1 << 20;

/// The top-level elements that the reader reads before it stops at the
/// first cluster. A file puts its `SeekHead`, `Info`, and `Tracks` there.
const HEADER_ELEMENTS: usize = 64;

/// The bytes that the reader takes for one block before it knows the
/// block's size. A subtitle block is usually far smaller.
const BLOCK_READ: usize = 4096;

/// A variable-length integer at the start of `bytes`: its value and its
/// length. An element ID keeps its length marker, and a size drops it.
fn vint(bytes: &[u8], marker: bool) -> Option<(u64, usize)> {
    let first = *bytes.first()?;
    let length = first.leading_zeros() as usize + 1;
    if length > 8 || bytes.len() < length {
        return None;
    }
    let mut value = match marker {
        true => u64::from(first),
        false => u64::from(first) & (0xFF >> length),
    };
    for byte in &bytes[1..length] {
        value = (value << 8) | u64::from(*byte);
    }
    Some((value, length))
}

/// The ID, the size, and the header length of the element at the start of
/// `bytes`. A size of all ones, an unknown size, is `u64::MAX`.
fn element(bytes: &[u8]) -> Option<(u32, u64, usize)> {
    let (id, id_length) = vint(bytes, true)?;
    let (size, size_length) = vint(&bytes[id_length..], false)?;
    let unknown = size == (1u64 << (7 * size_length)) - 1;
    let size = if unknown { u64::MAX } else { size };
    Some((u32::try_from(id).ok()?, size, id_length + size_length))
}

/// The children of an element's body, each as its ID and body. A child
/// that runs past the end of the body ends the list.
fn children(body: &[u8]) -> Vec<(u32, &[u8])> {
    let mut found = Vec::new();
    let mut at = 0;
    while let Some((id, size, header)) = element(&body[at..]) {
        let start = at + header;
        let Some(end) = usize::try_from(size)
            .ok()
            .and_then(|size| start.checked_add(size))
        else {
            break;
        };
        if end > body.len() {
            break;
        }
        found.push((id, &body[start..end]));
        at = end;
    }
    found
}

fn uint(bytes: &[u8]) -> u64 {
    bytes
        .iter()
        .fold(0, |value, byte| (value << 8) | u64::from(*byte))
}

fn child(body: &[u8], id: u32) -> Option<&[u8]> {
    children(body)
        .into_iter()
        .find(|(found, _)| *found == id)
        .map(|(_, body)| body)
}

/// The element at `offset` and its body, which must be at most `largest`
/// bytes.
fn read_element<F: At + ?Sized>(
    file: &F,
    offset: u64,
    largest: u64,
) -> Result<(u32, Vec<u8>), Why> {
    let head = file.at(offset, 12).map_err(Why::io)?;
    let (id, size, header) = element(&head).ok_or(Why::NoIndex)?;
    if size > largest {
        return Err(Why::Broken(format!(
            "an element at byte {offset} states {size} bytes, over the limit of {largest}"
        )));
    }
    let body = file
        .at(offset + header as u64, size as usize)
        .map_err(Why::io)?;
    if body.len() as u64 != size {
        return Err(Why::Broken(format!(
            "the file ends inside the element at byte {offset}"
        )));
    }
    Ok((id, body))
}

/// One subtitle track of the file.
struct Track {
    number: u64,
    codec: String,
    private: Vec<u8>,
    encoded: bool,
}

/// The track whose `TrackNumber` is `number`. mpv plays Matroska with its
/// own demuxer, which reports a track's `TrackNumber` as its `src-id`.
fn track(tracks: &[u8], number: i64) -> Option<Track> {
    let number = u64::try_from(number).ok()?;
    let entry = children(tracks)
        .into_iter()
        .filter(|(id, _)| *id == TRACK_ENTRY)
        .map(|(_, entry)| entry)
        .find(|entry| child(entry, TRACK_NUMBER).map(uint) == Some(number))?;
    Some(Track {
        number: uint(child(entry, TRACK_NUMBER)?),
        codec: String::from_utf8_lossy(child(entry, CODEC_ID)?)
            .trim_end_matches('\0')
            .to_string(),
        private: child(entry, CODEC_PRIVATE)
            .map(<[u8]>::to_vec)
            .unwrap_or_default(),
        encoded: child(entry, CONTENT_ENCODINGS).is_some(),
    })
}

/// The name that mpv reports for a Matroska codec ID, for the text codecs.
fn codec(id: &str) -> Option<&'static str> {
    match id {
        "S_TEXT/UTF8" | "S_TEXT/ASCII" => Some("subrip"),
        "S_TEXT/ASS" | "S_ASS" | "S_TEXT/SSA" | "S_SSA" => Some("ass"),
        "S_TEXT/WEBVTT" | "D_WEBVTT/SUBTITLES" => Some("webvtt"),
        _ => None,
    }
}

/// The place of one block that a cue point names.
#[derive(Debug, Clone, Copy)]
struct Place {
    cluster: u64,
    relative: u64,
    duration: Option<u64>,
}

/// The place of each block of the track `number`, or `None` when a cue
/// point of the track lacks its cluster or its place in the cluster.
fn places(cues: &[u8], number: u64) -> Option<Vec<Place>> {
    let mut found = Vec::new();
    for (id, point) in children(cues) {
        if id != CUE_POINT {
            continue;
        }
        for (id, positions) in children(point) {
            if id != CUE_TRACK_POSITIONS || child(positions, CUE_TRACK).map(uint) != Some(number) {
                continue;
            }
            found.push(Place {
                cluster: child(positions, CUE_CLUSTER_POSITION).map(uint)?,
                relative: child(positions, CUE_RELATIVE_POSITION).map(uint)?,
                duration: child(positions, CUE_DURATION).map(uint),
            });
        }
    }
    Some(found)
}

/// The start of a cluster's data and the cluster's timestamp. A CRC-32 or a
/// `Void` element can come before the timestamp.
fn cluster<F: At + ?Sized>(file: &F, offset: u64) -> Option<(u64, u64)> {
    let head = file.at(offset, 64).ok()?;
    let (id, _, header) = element(&head)?;
    if id != CLUSTER {
        return None;
    }
    let mut at = header;
    while let Some((id, size, length)) = element(&head[at..]) {
        let body = head.get(at + length..at + length + usize::try_from(size).ok()?)?;
        if id == CLUSTER_TIMESTAMP {
            return Some((offset + header as u64, uint(body)));
        }
        at += length + body.len();
    }
    None
}

/// A block's relative time, its payload, and the duration of its group.
struct Block {
    time: i64,
    payload: Vec<u8>,
    duration: Option<u64>,
}

fn block<F: At + ?Sized>(file: &F, offset: u64, number: u64) -> Option<Block> {
    let mut bytes = file.at(offset, BLOCK_READ).ok()?;
    let (id, size, header) = element(&bytes)?;
    if size > LARGEST_BLOCK {
        return None;
    }
    let end = header + usize::try_from(size).ok()?;
    if end > bytes.len() {
        bytes = file.at(offset, end).ok()?;
    }
    let body = bytes.get(header..end)?;
    let (frame, duration) = match id {
        SIMPLE_BLOCK => (body, None),
        BLOCK_GROUP => (child(body, BLOCK)?, child(body, BLOCK_DURATION).map(uint)),
        _ => return None,
    };
    let (track, length) = vint(frame, false)?;
    let flags = *frame.get(length + 2)?;
    if track != number || flags & 0x06 != 0 {
        return None;
    }
    let time = i16::from_be_bytes([*frame.get(length)?, *frame.get(length + 1)?]);
    Some(Block {
        time: i64::from(time),
        payload: frame[length + 3..].to_vec(),
        duration,
    })
}

/// The copy of the track whose `src-id` is `src_id`, whose codec mpv
/// reports as `codec`. Every block that the index names must be read, so a
/// block that cannot be read fails the whole copy, and mpv keeps drawing
/// the original.
pub fn read<F: At + ?Sized>(file: &F, src_id: i64, codec_name: &str) -> Result<Copy, Why> {
    let head = file.at(0, 12).map_err(Why::io)?;
    let (id, size, header) = element(&head).ok_or(Why::Container)?;
    if id != EBML || size == u64::MAX {
        return Err(Why::Container);
    }
    let segment_at = header as u64 + size;
    let head = file.at(segment_at, 12).map_err(Why::io)?;
    let (id, _, header) = element(&head).ok_or(Why::NoIndex)?;
    if id != SEGMENT {
        return Err(Why::NoIndex);
    }
    let segment = segment_at + header as u64;

    let mut found: BTreeMap<u32, Vec<u8>> = BTreeMap::new();
    let mut seek_heads: Vec<u64> = Vec::new();
    let mut at = segment;
    for _ in 0..HEADER_ELEMENTS {
        let Ok(head) = file.at(at, 12) else { break };
        let Some((id, size, header)) = element(&head) else {
            break;
        };
        if id == CLUSTER || size == u64::MAX {
            break;
        }
        match id {
            SEEK_HEAD => seek_heads.push(at - segment),
            INFO | TRACKS => {
                let (_, body) = read_element(file, at, LARGEST_HEADER)?;
                found.insert(id, body);
            }
            _ => {}
        }
        at += header as u64 + size;
    }
    let mut positions: BTreeMap<u32, u64> = BTreeMap::new();
    let mut read_heads: Vec<u64> = Vec::new();
    while let Some(position) = seek_heads.pop() {
        if read_heads.contains(&position) {
            continue;
        }
        read_heads.push(position);
        let (id, body) = read_element(file, segment + position, LARGEST_HEADER)?;
        if id != SEEK_HEAD {
            continue;
        }
        for (id, seek) in children(&body) {
            if id != SEEK {
                continue;
            }
            let (Some(target), Some(position)) = (
                child(seek, SEEK_ID).map(uint),
                child(seek, SEEK_POSITION).map(uint),
            ) else {
                continue;
            };
            let target = u32::try_from(target).unwrap_or(0);
            match target {
                SEEK_HEAD => seek_heads.push(position),
                _ => {
                    positions.entry(target).or_insert(position);
                }
            }
        }
    }
    let body_of = |id: u32, largest: u64| -> Result<Option<Vec<u8>>, Why> {
        if let Some(body) = found.get(&id) {
            return Ok(Some(body.clone()));
        }
        let Some(position) = positions.get(&id) else {
            return Ok(None);
        };
        let (read, body) = read_element(file, segment + position, largest)?;
        Ok((read == id).then_some(body))
    };
    let scale = body_of(INFO, LARGEST_HEADER)?
        .and_then(|info| child(&info, TIMESTAMP_SCALE).map(uint))
        .unwrap_or(DEFAULT_SCALE);
    let tracks = body_of(TRACKS, LARGEST_HEADER)?.ok_or(Why::NoIndex)?;
    let track = track(&tracks, src_id).ok_or(Why::Mismatch)?;
    let kind = codec(&track.codec).ok_or(Why::Mismatch)?;
    let same = kind == codec_name || (kind == "ass" && codec_name == "ssa");
    if !same {
        return Err(Why::Mismatch);
    }
    if track.encoded {
        return Err(Why::Compressed);
    }
    let cues = body_of(CUES, LARGEST_CUES)?.ok_or(Why::NoIndex)?;
    let places = places(&cues, track.number).ok_or(Why::NoIndex)?;
    if places.is_empty() {
        return Err(Why::NoIndex);
    }

    let mut clusters: Vec<u64> = places.iter().map(|place| place.cluster).collect();
    clusters.sort_unstable();
    clusters.dedup();
    let heads = at::each(clusters.len(), |index| {
        cluster(file, segment + clusters[index])
    })
    .ok_or_else(|| Why::Broken("a read of the file stopped".to_string()))?;
    let heads: BTreeMap<u64, (u64, u64)> = clusters
        .iter()
        .zip(heads)
        .map(|(position, head)| {
            let head = head.ok_or_else(|| {
                Why::Broken(format!(
                    "the cluster at byte {position} of the segment has no timestamp"
                ))
            })?;
            Ok((*position, head))
        })
        .collect::<Result<_, Why>>()?;
    let mut located: Vec<(u64, u64, Option<u64>)> = places
        .iter()
        .filter_map(|place| {
            let (data, timestamp) = heads.get(&place.cluster)?;
            Some((data + place.relative, *timestamp, place.duration))
        })
        .collect();
    located.sort_unstable_by_key(|(offset, _, _)| *offset);
    located.dedup_by_key(|(offset, _, _)| *offset);
    let blocks = at::each(located.len(), |index| {
        block(file, located[index].0, track.number)
    })
    .ok_or_else(|| Why::Broken("a read of the file stopped".to_string()))?;

    let ms = |units: i64| (units.max(0) as u64).saturating_mul(scale) / 1_000_000;
    let mut timed: Vec<(u64, Option<u64>, String)> = Vec::with_capacity(located.len());
    for ((offset, timestamp, cue_duration), block) in located.iter().zip(blocks) {
        let block = block
            .ok_or_else(|| Why::Broken(format!("the block at byte {offset} could not be read")))?;
        let text = String::from_utf8(block.payload)
            .map_err(|_| Why::Broken(format!("the block at byte {offset} is not UTF-8")))?;
        let start = ms(*timestamp as i64 + block.time);
        let duration = block
            .duration
            .or(*cue_duration)
            .map(|units| ms(units as i64));
        timed.push((start, duration, text.trim_end_matches('\0').to_string()));
    }
    timed.sort_by_key(|(start, _, _)| *start);
    let ends: Vec<u64> = timed
        .iter()
        .enumerate()
        .map(|(index, (start, duration, _))| match duration {
            Some(duration) => start + duration,
            None => timed.get(index + 1).map_or(start + 3000, |next| next.0),
        })
        .collect();
    // libass stacks the events that show at once by their order in the
    // script, and a Matroska ASS block keeps the event's place in the
    // original script as its `ReadOrder`. So the events of an ASS track are
    // written in that order, and the others in the order of their times.
    let mut ordered: Vec<(u64, Dialogue)> = Vec::with_capacity(timed.len());
    for ((start, _, text), end) in timed.into_iter().zip(ends) {
        let event = match kind {
            "ass" => ass::block(&text, start, end).ok_or_else(|| {
                Why::Broken(format!("the event at {start} ms is not an ASS event"))
            })?,
            "webvtt" => (start, Dialogue::text(start, end, webvtt::markup(&text))),
            _ => (start, Dialogue::text(start, end, subrip::markup(&text))),
        };
        ordered.push(event);
    }
    ordered.sort_by_key(|(order, _)| *order);
    let events: Vec<Dialogue> = ordered.into_iter().map(|(_, event)| event).collect();
    Ok(match kind {
        "ass" => {
            // libass copies `CodecPrivate` into a string that ends at its
            // first NUL byte and reads no further, so mpv's header for the
            // original ends there too.
            let private = track
                .private
                .split(|byte| *byte == 0)
                .next()
                .unwrap_or_default();
            let (header, ssa) = ass::header(&String::from_utf8_lossy(private));
            Copy {
                header,
                ssa,
                converted: false,
                events,
            }
        }
        _ => Copy::converted(events),
    })
}

#[cfg(test)]
mod tests;
