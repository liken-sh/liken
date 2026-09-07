use sha2::{Digest, Sha256};

use super::Fit;

const MAX_KEY_BYTES: usize = 16 * 1024;

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(super) struct Key {
    pub(super) library: String,
    pub(super) art: String,
    pub(super) width: u32,
    pub(super) height: u32,
    pub(super) fit: Fit,
}

impl Key {
    pub(super) fn bytes(&self) -> Option<Vec<u8>> {
        let fields: [&[u8]; 5] = [
            self.library.as_bytes(),
            self.art.as_bytes(),
            &self.width.to_be_bytes(),
            &self.height.to_be_bytes(),
            &[match self.fit {
                Fit::Cover => 0,
                Fit::Contain => 1,
            }],
        ];
        let capacity = fields
            .iter()
            .try_fold(0usize, |size, field| size.checked_add(4 + field.len()))?;
        if capacity > MAX_KEY_BYTES {
            return None;
        }
        let mut bytes = Vec::with_capacity(capacity);
        for field in fields {
            let length = u32::try_from(field.len()).ok()?;
            bytes.extend_from_slice(&length.to_be_bytes());
            bytes.extend_from_slice(field);
        }
        Some(bytes)
    }

    pub(super) fn digest(&self) -> Option<[u8; 32]> {
        Some(Sha256::digest(self.bytes()?).into())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn key(library: &str, art: &str, width: u32, height: u32, fit: Fit) -> Key {
        Key {
            library: library.to_owned(),
            art: art.to_owned(),
            width,
            height,
            fit,
        }
    }

    #[test]
    fn every_field_has_its_own_disk_identity() {
        let base = key("movies", "poster.jpg", 240, 360, Fit::Cover);
        assert_ne!(
            base.digest(),
            key("shows", "poster.jpg", 240, 360, Fit::Cover).digest()
        );
        assert_ne!(
            base.digest(),
            key("movies", "other.jpg", 240, 360, Fit::Cover).digest()
        );
        assert_ne!(
            base.digest(),
            key("movies", "poster.jpg", 241, 360, Fit::Cover).digest()
        );
        assert_ne!(
            base.digest(),
            key("movies", "poster.jpg", 240, 361, Fit::Cover).digest()
        );
        assert_ne!(
            base.digest(),
            key("movies", "poster.jpg", 240, 360, Fit::Contain).digest()
        );
    }

    #[test]
    fn the_disk_identity_has_a_stable_encoding() {
        assert_eq!(
            key("movies", "poster.jpg", 240, 360, Fit::Cover).digest(),
            Some([
                0x9b, 0xbb, 0xbd, 0xf1, 0xb7, 0x26, 0xdd, 0x19, 0xab, 0xae, 0x11, 0x31, 0x30, 0xff,
                0x3d, 0xcb, 0x00, 0x9f, 0xd3, 0x35, 0x09, 0xef, 0xd1, 0xaa, 0x41, 0xb3, 0xba, 0x55,
                0x69, 0xe5, 0x16, 0xf1,
            ])
        );
    }

    #[test]
    fn field_boundaries_cannot_alias() {
        assert_ne!(
            key("ab", "c", 1, 1, Fit::Cover).digest(),
            key("a", "bc", 1, 1, Fit::Cover).digest()
        );
    }

    #[test]
    fn an_unbounded_key_has_no_disk_identity() {
        assert_eq!(
            key(&"x".repeat(MAX_KEY_BYTES), "art", 1, 1, Fit::Cover).digest(),
            None
        );
    }
}
