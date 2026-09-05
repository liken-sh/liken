use std::fs;
use std::io::Cursor;
use std::path::Path;
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use image::codecs::jpeg::JpegEncoder;
use image::codecs::png::PngEncoder;
use image::{ExtendedColorType, ImageEncoder, ImageFormat, ImageReader, Limits};
use sha2::{Digest, Sha256};

use super::super::Fit;
use super::super::key::Key;
use super::super::store::Poster;

const MAGIC: &[u8; 8] = b"LPSTRV1\0";
const FIXED_HEADER: usize = 89;
const MAX_HEADER: usize = 16 * 1024;
const ENCODED_OVERHEAD: u64 = 64 * 1024;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) struct SourceStamp {
    pub(super) size: u64,
    pub(super) modified_ns: i128,
}

impl SourceStamp {
    pub(super) fn from_metadata(metadata: &fs::Metadata) -> Option<Self> {
        Some(Self {
            size: metadata.len(),
            modified_ns: system_time_ns(metadata.modified().ok()?)?,
        })
    }
}

fn system_time_ns(time: SystemTime) -> Option<i128> {
    match time.duration_since(UNIX_EPOCH) {
        Ok(after) => i128::try_from(after.as_nanos()).ok(),
        Err(before) => i128::try_from(before.duration().as_nanos())
            .ok()?
            .checked_neg(),
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Encoding {
    Jpeg = 0,
    Png = 1,
}

pub(super) enum ReadOutcome {
    Hit(Poster),
    MetadataMiss,
    Invalid,
}

pub(super) enum FileRead {
    Bytes(Vec<u8>),
    Invalid,
}

pub(super) fn read_file(path: &Path, key: &Key) -> std::io::Result<FileRead> {
    let Some(max_payload) = max_payload(key) else {
        return Ok(FileRead::Invalid);
    };
    let metadata = path.symlink_metadata()?;
    if !metadata.file_type().is_file() {
        return Ok(FileRead::Invalid);
    }
    let max_file = u64::try_from(MAX_HEADER)
        .ok()
        .and_then(|header| header.checked_add(max_payload));
    if Some(metadata.len()) > max_file {
        return Ok(FileRead::Invalid);
    }
    Ok(FileRead::Bytes(fs::read(path)?))
}

pub(super) fn parse(bytes: &[u8], key: &Key, source: Option<SourceStamp>) -> ReadOutcome {
    let Some(max_payload) = max_payload(key) else {
        return ReadOutcome::Invalid;
    };
    let Some(fixed) = bytes.get(..FIXED_HEADER) else {
        return ReadOutcome::Invalid;
    };
    if &fixed[..8] != MAGIC {
        return ReadOutcome::Invalid;
    }
    let header_len = usize::try_from(u32::from_be_bytes(fixed[8..12].try_into().unwrap())).unwrap();
    let payload_len = u64::from_be_bytes(fixed[12..20].try_into().unwrap());
    if !(FIXED_HEADER..=MAX_HEADER).contains(&header_len) || payload_len > max_payload {
        return ReadOutcome::Invalid;
    }
    let Some(payload_len) = usize::try_from(payload_len).ok() else {
        return ReadOutcome::Invalid;
    };
    if header_len.checked_add(payload_len) != Some(bytes.len()) {
        return ReadOutcome::Invalid;
    }

    let stamp = SourceStamp {
        size: u64::from_be_bytes(fixed[20..28].try_into().unwrap()),
        modified_ns: i128::from_be_bytes(fixed[28..44].try_into().unwrap()),
    };
    if source.is_some_and(|current| current != stamp) {
        return ReadOutcome::MetadataMiss;
    }
    let width = u32::from_be_bytes(fixed[44..48].try_into().unwrap());
    let height = u32::from_be_bytes(fixed[48..52].try_into().unwrap());
    if !valid_dimensions(key, width, height) {
        return ReadOutcome::Invalid;
    }
    let encoding = match fixed[52] {
        0 => Encoding::Jpeg,
        1 => Encoding::Png,
        _ => return ReadOutcome::Invalid,
    };
    let key_len = usize::try_from(u32::from_be_bytes(fixed[53..57].try_into().unwrap())).unwrap();
    if FIXED_HEADER.checked_add(key_len) != Some(header_len) {
        return ReadOutcome::Invalid;
    }
    let expected_key = match key.bytes() {
        Some(bytes) => bytes,
        None => return ReadOutcome::Invalid,
    };
    if bytes.get(FIXED_HEADER..header_len) != Some(expected_key.as_slice()) {
        return ReadOutcome::Invalid;
    }
    let payload = &bytes[header_len..];
    let checksum = entry_checksum(&fixed[..57], &expected_key, payload);
    if fixed[57..89] != checksum {
        return ReadOutcome::Invalid;
    }
    let Some(poster) = decode(payload, encoding, width, height, key) else {
        return ReadOutcome::Invalid;
    };
    ReadOutcome::Hit(poster)
}

pub(super) fn encode(key: &Key, stamp: SourceStamp, poster: &Poster) -> Option<Vec<u8>> {
    if !valid_dimensions(key, poster.width, poster.height) {
        return None;
    }
    let expected_rgba = rgba_len(poster.width, poster.height)?;
    if poster.rgba.len() != expected_rgba {
        return None;
    }
    let (encoding, payload) = encode_pixels(poster)?;
    if u64::try_from(payload.len()).ok()? > max_payload(key)? {
        return None;
    }
    let key_bytes = key.bytes()?;
    let header_len = FIXED_HEADER.checked_add(key_bytes.len())?;
    if header_len > MAX_HEADER {
        return None;
    }
    let mut bytes = Vec::with_capacity(header_len.checked_add(payload.len())?);
    bytes.extend_from_slice(MAGIC);
    bytes.extend_from_slice(&u32::try_from(header_len).ok()?.to_be_bytes());
    bytes.extend_from_slice(&u64::try_from(payload.len()).ok()?.to_be_bytes());
    bytes.extend_from_slice(&stamp.size.to_be_bytes());
    bytes.extend_from_slice(&stamp.modified_ns.to_be_bytes());
    bytes.extend_from_slice(&poster.width.to_be_bytes());
    bytes.extend_from_slice(&poster.height.to_be_bytes());
    bytes.push(encoding as u8);
    bytes.extend_from_slice(&u32::try_from(key_bytes.len()).ok()?.to_be_bytes());
    let checksum = entry_checksum(&bytes, &key_bytes, &payload);
    bytes.extend_from_slice(&checksum);
    bytes.extend_from_slice(&key_bytes);
    bytes.extend_from_slice(&payload);
    Some(bytes)
}

fn entry_checksum(prefix: &[u8], key: &[u8], payload: &[u8]) -> [u8; 32] {
    let mut digest = Sha256::new();
    digest.update(prefix);
    digest.update(key);
    digest.update(payload);
    digest.finalize().into()
}

fn encode_pixels(poster: &Poster) -> Option<(Encoding, Vec<u8>)> {
    let mut payload = Vec::new();
    if poster.rgba.chunks_exact(4).any(|pixel| pixel[3] < u8::MAX) {
        PngEncoder::new(&mut payload)
            .write_image(
                &poster.rgba,
                poster.width,
                poster.height,
                ExtendedColorType::Rgba8,
            )
            .ok()?;
        return Some((Encoding::Png, payload));
    }
    let rgb: Vec<u8> = poster
        .rgba
        .chunks_exact(4)
        .flat_map(|pixel| pixel[..3].iter().copied())
        .collect();
    JpegEncoder::new_with_quality(&mut payload, 90)
        .write_image(&rgb, poster.width, poster.height, ExtendedColorType::Rgb8)
        .ok()?;
    Some((Encoding::Jpeg, payload))
}

fn decode(
    payload: &[u8],
    encoding: Encoding,
    width: u32,
    height: u32,
    key: &Key,
) -> Option<Poster> {
    let mut reader = ImageReader::new(Cursor::new(payload));
    reader.set_format(match encoding {
        Encoding::Jpeg => ImageFormat::Jpeg,
        Encoding::Png => ImageFormat::Png,
    });
    let mut limits = Limits::default();
    limits.max_image_width = Some(key.width);
    limits.max_image_height = Some(key.height);
    limits.max_alloc = u64::try_from(rgba_len(width, height)?).ok();
    reader.limits(limits);
    let decoded = reader.decode().ok()?;
    if decoded.width() != width || decoded.height() != height {
        return None;
    }
    let rgba = decoded.to_rgba8().into_raw();
    if rgba.len() != rgba_len(width, height)? {
        return None;
    }
    Some(Poster::new(width, height, Arc::from(rgba)))
}

fn valid_dimensions(key: &Key, width: u32, height: u32) -> bool {
    match key.fit {
        Fit::Cover => width == key.width && height == key.height,
        Fit::Contain => width > 0 && height > 0 && width <= key.width && height <= key.height,
    }
}

fn rgba_len(width: u32, height: u32) -> Option<usize> {
    usize::try_from(
        u64::from(width)
            .checked_mul(u64::from(height))?
            .checked_mul(4)?,
    )
    .ok()
}

fn max_payload(key: &Key) -> Option<u64> {
    u64::from(key.width)
        .checked_mul(u64::from(key.height))?
        .checked_mul(4)?
        .checked_add(ENCODED_OVERHEAD)
}

#[cfg(test)]
mod tests;
