use std::fs;
use std::sync::Arc;

use tempfile::TempDir;

use super::*;

const STAMP: SourceStamp = SourceStamp {
    size: 123,
    modified_ns: -456,
};

fn key(width: u32, height: u32, fit: Fit) -> Key {
    Key {
        library: "movies".to_owned(),
        art: "poster.png".to_owned(),
        width,
        height,
        fit,
    }
}

fn poster(width: u32, height: u32, pixel: [u8; 4]) -> Poster {
    Poster::new(
        width,
        height,
        Arc::from(pixel.repeat((width * height) as usize)),
    )
}

fn encoded(key: &Key, poster: &Poster) -> Vec<u8> {
    encode(key, STAMP, poster).expect("an encoded entry")
}

fn hit(bytes: &[u8], key: &Key) -> Poster {
    let ReadOutcome::Hit(poster) = parse(bytes, key, Some(STAMP)) else {
        panic!("a cache hit");
    };
    poster
}

#[test]
fn transparent_pixels_use_png_and_keep_their_exact_alpha() {
    let key = key(2, 1, Fit::Cover);
    let bytes = encoded(&key, &poster(2, 1, [10, 20, 30, 117]));
    assert_eq!(bytes[52], Encoding::Png as u8);
    assert_eq!(
        &*hit(&bytes, &key).rgba,
        &[10, 20, 30, 117, 10, 20, 30, 117]
    );
}

#[test]
fn opaque_pixels_use_quality_ninety_jpeg() {
    let key = key(16, 16, Fit::Cover);
    let bytes = encoded(&key, &poster(16, 16, [30, 80, 140, 255]));
    assert_eq!(bytes[52], Encoding::Jpeg as u8);
    let decoded = hit(&bytes, &key);
    assert!(decoded.rgba[0].abs_diff(30) <= 4);
    assert!(decoded.rgba[1].abs_diff(80) <= 4);
    assert!(decoded.rgba[2].abs_diff(140) <= 4);
    assert_eq!(decoded.rgba[3], 255);
}

#[test]
fn a_contain_entry_keeps_its_smaller_output_dimensions() {
    let key = key(60, 60, Fit::Contain);
    let bytes = encoded(&key, &poster(60, 20, [10, 20, 30, 255]));
    let decoded = hit(&bytes, &key);
    assert_eq!((decoded.width, decoded.height), (60, 20));
}

#[test]
fn decoded_dimensions_must_match_the_header() {
    let key = key(60, 60, Fit::Contain);
    let mut bytes = encoded(&key, &poster(60, 20, [10, 20, 30, 255]));
    bytes[48..52].copy_from_slice(&19u32.to_be_bytes());
    let header_len = usize::try_from(u32::from_be_bytes(bytes[8..12].try_into().unwrap())).unwrap();
    let checksum = entry_checksum(
        &bytes[..57],
        &bytes[FIXED_HEADER..header_len],
        &bytes[header_len..],
    );
    bytes[57..89].copy_from_slice(&checksum);
    assert!(matches!(
        parse(&bytes, &key, Some(STAMP)),
        ReadOutcome::Invalid
    ));
}

#[test]
fn a_cover_entry_must_have_the_requested_dimensions() {
    assert!(
        encode(
            &key(60, 60, Fit::Cover),
            STAMP,
            &poster(60, 20, [0, 0, 0, 255])
        )
        .is_none()
    );
}

#[test]
fn a_zero_sized_contain_entry_is_refused() {
    assert!(
        encode(
            &key(60, 60, Fit::Contain),
            STAMP,
            &poster(0, 0, [0, 0, 0, 255])
        )
        .is_none()
    );
}

#[test]
fn changed_source_metadata_is_a_miss() {
    let key = key(8, 8, Fit::Cover);
    let bytes = encoded(&key, &poster(8, 8, [0, 0, 0, 255]));
    let changed = SourceStamp {
        size: STAMP.size + 1,
        ..STAMP
    };
    assert!(matches!(
        parse(&bytes, &key, Some(changed)),
        ReadOutcome::MetadataMiss
    ));
}

#[test]
fn the_full_key_in_the_header_guards_a_digest_collision() {
    let key = key(8, 8, Fit::Cover);
    let bytes = encoded(&key, &poster(8, 8, [0, 0, 0, 255]));
    let other = Key {
        art: "other.png".to_owned(),
        ..key.clone()
    };
    assert!(matches!(
        parse(&bytes, &other, Some(STAMP)),
        ReadOutcome::Invalid
    ));
}

#[test]
fn a_changed_header_fails_its_digest_for_a_deleted_source() {
    let key = key(8, 8, Fit::Cover);
    let mut bytes = encoded(&key, &poster(8, 8, [0, 0, 0, 255]));
    bytes[27] ^= 1;
    assert!(matches!(parse(&bytes, &key, None), ReadOutcome::Invalid));
}

#[test]
fn a_changed_payload_fails_its_digest() {
    let key = key(8, 8, Fit::Cover);
    let mut bytes = encoded(&key, &poster(8, 8, [0, 0, 0, 255]));
    let last = bytes.len() - 1;
    bytes[last] ^= 1;
    assert!(matches!(
        parse(&bytes, &key, Some(STAMP)),
        ReadOutcome::Invalid
    ));
}

#[test]
fn a_truncated_entry_is_invalid() {
    let key = key(8, 8, Fit::Cover);
    let mut bytes = encoded(&key, &poster(8, 8, [0, 0, 0, 255]));
    bytes.pop();
    assert!(matches!(
        parse(&bytes, &key, Some(STAMP)),
        ReadOutcome::Invalid
    ));
}

#[test]
fn an_unbounded_header_is_invalid() {
    let key = key(8, 8, Fit::Cover);
    let mut bytes = encoded(&key, &poster(8, 8, [0, 0, 0, 255]));
    bytes[8..12].copy_from_slice(&u32::MAX.to_be_bytes());
    assert!(matches!(
        parse(&bytes, &key, Some(STAMP)),
        ReadOutcome::Invalid
    ));
}

#[test]
fn an_unbounded_payload_claim_is_invalid() {
    let key = key(8, 8, Fit::Cover);
    let mut bytes = encoded(&key, &poster(8, 8, [0, 0, 0, 255]));
    bytes[12..20].copy_from_slice(&u64::MAX.to_be_bytes());
    assert!(matches!(
        parse(&bytes, &key, Some(STAMP)),
        ReadOutcome::Invalid
    ));
}

#[test]
fn read_file_refuses_an_oversized_file_before_reading_it() {
    let dir = TempDir::new().unwrap();
    let path = dir.path().join("entry");
    fs::write(
        &path,
        vec![0; MAX_HEADER + 4 * 8 * 8 + ENCODED_OVERHEAD as usize + 1],
    )
    .unwrap();
    assert!(matches!(
        read_file(&path, &key(8, 8, Fit::Cover)).unwrap(),
        FileRead::Invalid
    ));
}
