//! Each test builds a small Matroska file in memory with the elements that
//! the reader follows: two `SeekHead` elements, `Info`, `Tracks`, one
//! cluster, and `Cues`.

use super::*;

fn id_bytes(id: u32) -> Vec<u8> {
    let bytes = id.to_be_bytes();
    let first = bytes.iter().position(|byte| *byte != 0).unwrap_or(3);
    bytes[first..].to_vec()
}

/// An element with an eight-byte size, so its length does not depend on
/// the values inside it.
fn el(id: u32, body: &[u8]) -> Vec<u8> {
    let mut out = id_bytes(id);
    out.push(0x01);
    out.extend_from_slice(&(body.len() as u64).to_be_bytes()[1..]);
    out.extend_from_slice(body);
    out
}

fn num(id: u32, value: u64) -> Vec<u8> {
    el(id, &value.to_be_bytes())
}

fn cat(parts: &[Vec<u8>]) -> Vec<u8> {
    parts.concat()
}

/// A block of track `track` at `time` units after the cluster's timestamp.
fn frame(track: u8, time: i16, payload: &str) -> Vec<u8> {
    let mut out = vec![0x80 | track];
    out.extend_from_slice(&time.to_be_bytes());
    out.push(0);
    out.extend_from_slice(payload.as_bytes());
    out
}

fn group(track: u8, time: i16, payload: &str, duration: u64) -> Vec<u8> {
    el(
        BLOCK_GROUP,
        &cat(&[
            el(BLOCK, &frame(track, time, payload)),
            num(BLOCK_DURATION, duration),
        ]),
    )
}

fn seek(id: u32, position: u64) -> Vec<u8> {
    el(
        SEEK,
        &cat(&[num(SEEK_ID, u64::from(id)), num(SEEK_POSITION, position)]),
    )
}

struct Fixture {
    codec: &'static str,
    private: &'static str,
    payloads: [&'static str; 2],
    indexed_track: u64,
    /// Builds the first subtitle block from its payload in place of a
    /// usual `BlockGroup`, for a test of a broken block.
    first: Option<fn(&str) -> Vec<u8>>,
}

const SUBRIP: Fixture = Fixture {
    codec: "S_TEXT/UTF8",
    private: "",
    payloads: ["One line", "Two\r\nlines"],
    indexed_track: 3,
    first: None,
};

const HEADER: &str = "[Script Info]\nPlayResY: 288\n\n[V4+ Styles]\n\n[Events]\nFormat: Layer\n";

const ASS: Fixture = Fixture {
    codec: "S_TEXT/ASS",
    private: HEADER,
    payloads: [
        "1,0,Default,,0,0,0,,One line",
        "2,0,Default,,0,0,0,,Two\\Nlines",
    ],
    indexed_track: 3,
    first: None,
};

/// The file. Track 3 is the second `TrackEntry`, whose `src-id` in mpv is
/// its `TrackNumber`, 3.
fn file(fixture: &Fixture) -> Vec<u8> {
    let info = el(INFO, &num(TIMESTAMP_SCALE, 1_000_000));
    let mut entry = cat(&[num(TRACK_NUMBER, 3), el(CODEC_ID, fixture.codec.as_bytes())]);
    if !fixture.private.is_empty() {
        entry.extend(el(CODEC_PRIVATE, fixture.private.as_bytes()));
    }
    let tracks = el(
        TRACKS,
        &cat(&[
            el(
                TRACK_ENTRY,
                &cat(&[num(TRACK_NUMBER, 1), el(CODEC_ID, b"V_MPEG4/ISO/AVC")]),
            ),
            el(TRACK_ENTRY, &entry),
        ]),
    );
    let first = match fixture.first {
        Some(build) => build(fixture.payloads[0]),
        None => group(3, 500, fixture.payloads[0], 1500),
    };
    let video = el(SIMPLE_BLOCK, &frame(1, 600, "picture"));
    let second = group(3, 2000, fixture.payloads[1], 1500);
    let leading = cat(&[el(0xBF, &[0; 4]), num(CLUSTER_TIMESTAMP, 1000)]);
    let cluster = el(
        CLUSTER,
        &cat(&[leading.clone(), first.clone(), video.clone(), second]),
    );
    let relative = [
        leading.len() as u64,
        (leading.len() + first.len() + video.len()) as u64,
    ];

    // The first SeekHead names Info and a second SeekHead after the
    // cluster, which names Tracks and Cues.
    let head_length = el(
        SEEK_HEAD,
        &cat(&[el(0xEC, &[0; 3]), seek(INFO, 0), seek(SEEK_HEAD, 0)]),
    )
    .len();
    let info_at = head_length as u64;
    let tracks_at = info_at + info.len() as u64;
    let cluster_at = tracks_at + tracks.len() as u64;
    let second_head_at = cluster_at + cluster.len() as u64;
    let second_head_length = el(
        SEEK_HEAD,
        &cat(&[el(0xBF, &[0; 4]), seek(TRACKS, 0), seek(CUES, 0)]),
    )
    .len();
    let cues_at = second_head_at + second_head_length as u64;
    let head = el(
        SEEK_HEAD,
        &cat(&[
            el(0xEC, &[0; 3]),
            seek(INFO, info_at),
            seek(SEEK_HEAD, second_head_at),
        ]),
    );
    let second_head = el(
        SEEK_HEAD,
        &cat(&[
            el(0xBF, &[0; 4]),
            seek(TRACKS, tracks_at),
            seek(CUES, cues_at),
        ]),
    );
    let point = |time: u64, track: u64, relative: u64| {
        el(
            CUE_POINT,
            &cat(&[
                num(0xB3, time),
                el(
                    CUE_TRACK_POSITIONS,
                    &cat(&[
                        num(CUE_TRACK, track),
                        num(CUE_CLUSTER_POSITION, cluster_at),
                        num(CUE_RELATIVE_POSITION, relative),
                    ]),
                ),
            ]),
        )
    };
    let cues = el(
        CUES,
        &cat(&[
            el(0xBF, &[0; 4]),
            point(1500, fixture.indexed_track, relative[0]),
            point(1600, 1, relative[0] + first.len() as u64),
            point(3000, fixture.indexed_track, relative[1]),
        ]),
    );
    let segment = cat(&[head, info, tracks, cluster, second_head, cues]);
    cat(&[el(EBML, &num(0x4282, 0)), el(SEGMENT, &segment)])
}

#[test]
fn a_subrip_track_is_read_through_the_index() {
    let bytes = file(&SUBRIP);
    let copy = read(bytes.as_slice(), 3, "subrip").expect("the copy");
    assert_eq!(
        copy,
        Copy::converted(vec![
            Dialogue::text(1500, 3000, "One line".to_string()),
            Dialogue::text(3000, 4500, "Two\\Nlines".to_string()),
        ])
    );
}

#[test]
fn an_ass_track_keeps_the_header_of_its_codec_private() {
    let bytes = file(&ASS);
    let copy = read(bytes.as_slice(), 3, "ass").expect("the copy");
    assert_eq!(
        copy.header,
        "[Script Info]\nPlayResY: 288\n\n[V4+ Styles]\n\n"
    );
    assert_eq!(
        copy.events
            .iter()
            .map(|event| event.text.as_str())
            .collect::<Vec<_>>(),
        ["One line", "Two\\Nlines"]
    );
}

/// The reader finds the track by its `TrackNumber`, which mpv reports as
/// `src-id`, and refuses a track whose codec is not the one mpv reports.
#[test]
fn a_track_that_is_not_the_one_mpv_reports_is_refused() {
    let bytes = file(&SUBRIP);
    let cases = [(1, "subrip"), (3, "ass"), (2, "subrip")];
    for (src_id, codec) in cases {
        assert_eq!(
            read(bytes.as_slice(), src_id, codec),
            Err(Why::Mismatch),
            "{src_id} {codec}"
        );
    }
}

#[test]
fn a_track_with_no_cue_points_has_no_index() {
    let bytes = file(&Fixture {
        indexed_track: 1,
        ..SUBRIP
    });
    assert_eq!(read(bytes.as_slice(), 3, "subrip"), Err(Why::NoIndex));
}

#[test]
fn a_file_that_is_not_matroska_is_refused() {
    let bytes = b"\x00\x00\x00\x18ftypisom".to_vec();
    assert_eq!(read(bytes.as_slice(), 0, "subrip"), Err(Why::Container));
}

/// libass stacks the events that show at once by their order in the
/// script, so an ASS copy keeps the `ReadOrder` of its blocks, whatever
/// their times.
#[test]
fn an_ass_copy_keeps_the_read_order_of_its_events() {
    let bytes = file(&Fixture {
        payloads: ["2,0,Default,,0,0,0,,Second", "1,0,Default,,0,0,0,,First"],
        ..ASS
    });
    let copy = read(bytes.as_slice(), 3, "ass").expect("the copy");
    assert_eq!(
        copy.events
            .iter()
            .map(|event| event.text.as_str())
            .collect::<Vec<_>>(),
        ["First", "Second"]
    );
}

/// A block whose size is 2^52, a laced block, a block that is not UTF-8,
/// and a block of another track each fail the whole copy, before any
/// allocation of the stated size.
#[test]
fn a_broken_block_fails_the_copy() {
    let huge: fn(&str) -> Vec<u8> = |_| {
        let mut out = vec![0xA3, 0x01];
        out.extend_from_slice(&(1u64 << 52).to_be_bytes()[1..]);
        out.extend_from_slice(&frame(3, 500, "x"));
        out
    };
    let laced: fn(&str) -> Vec<u8> = |payload| {
        let mut block = frame(3, 500, payload);
        block[3] = 0x02;
        el(SIMPLE_BLOCK, &block)
    };
    let not_utf8: fn(&str) -> Vec<u8> = |_| {
        let mut block = frame(3, 500, "");
        block.extend_from_slice(&[0xC3, 0x28]);
        el(SIMPLE_BLOCK, &block)
    };
    let other: fn(&str) -> Vec<u8> = |payload| el(SIMPLE_BLOCK, &frame(1, 500, payload));
    for (name, first) in [
        ("huge", huge),
        ("laced", laced),
        ("not UTF-8", not_utf8),
        ("another track", other),
    ] {
        let bytes = file(&Fixture {
            first: Some(first),
            ..SUBRIP
        });
        assert!(
            matches!(read(bytes.as_slice(), 3, "subrip"), Err(Why::Broken(_))),
            "{name}"
        );
    }
}

/// A file cut off inside the index, or with an element that states a size
/// over its limit, gives no copy and does not panic.
#[test]
fn a_truncated_or_hostile_file_gives_no_copy() {
    let bytes = file(&SUBRIP);
    for cut in [8, 40, bytes.len() / 2, bytes.len() - 10] {
        assert!(read(&bytes[..cut], 3, "subrip").is_err(), "cut at {cut}");
    }
    let mut hostile = bytes.clone();
    let tracks = hostile
        .windows(4)
        .position(|window| window == [0x16, 0x54, 0xAE, 0x6B])
        .expect("the Tracks element");
    hostile[tracks + 4..tracks + 12]
        .copy_from_slice(&[0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFE]);
    assert!(read(hostile.as_slice(), 3, "subrip").is_err());
}

/// libass reads `CodecPrivate` as text that ends at its first NUL byte, so
/// a header with a NUL after its styles is that much header to mpv. The
/// copy's header ends at the same byte, and the copy's script has no NUL,
/// which would end the script for libass before its events.
#[test]
fn a_nul_in_the_header_ends_the_header() {
    let bytes = file(&Fixture {
        private: "[Script Info]\nPlayResY: 288\n\n[V4+ Styles]\nStyle: Default\n\0junk\n",
        ..ASS
    });
    let copy = read(bytes.as_slice(), 3, "ass").expect("the copy");
    assert_eq!(
        copy.header,
        "[Script Info]\nPlayResY: 288\n\n[V4+ Styles]\nStyle: Default\n"
    );
    let script = copy.script(None, 0.0);
    assert!(!script.contains('\0'));
    assert!(script.contains("\n[Events]\nFormat: Layer,"));
}
