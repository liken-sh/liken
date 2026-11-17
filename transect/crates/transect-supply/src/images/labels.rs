//! Reads the labels in an image's config blob into the facts that a card
//! shows: where the image was built from, its release, its description,
//! and its documentation.
//!
//! Anyone who builds an image can write its labels, so transect passes
//! each label through `untrusted` before a card shows it.

use std::collections::HashMap;

use serde::Deserialize;

use super::Failure;
use transect_graph::untrusted::markdown;
use transect_graph::untrusted::{capped, link};

pub const SOURCE: &str = "org.opencontainers.image.source";
pub const REVISION: &str = "org.opencontainers.image.revision";
pub const VERSION: &str = "org.opencontainers.image.version";
pub const DESCRIPTION: &str = "org.opencontainers.image.description";
pub const DOCUMENTATION: &str = "org.opencontainers.image.documentation";

/// The most characters of a description that a card shows.
const DESCRIPTION_CHARS: usize = 200;
/// The longest a revision or a version may be, in characters.
const SHORT_CHARS: usize = 300;

/// The facts that an image's labels state about the image.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Provenance {
    /// The repository it was built from, as a clone URL.
    pub source: Option<String>,
    /// The commit it was built from.
    pub revision: Option<String>,
    pub version: Option<String>,
    pub description: Option<String>,
    /// An `http` or `https` URL.
    pub documentation: Option<String>,
}

#[derive(Deserialize)]
struct Config {
    #[serde(default)]
    config: Option<Inner>,
}

#[derive(Deserialize)]
struct Inner {
    #[serde(rename = "Labels", default)]
    labels: Option<HashMap<String, String>>,
}

/// Reads the provenance from the labels in an image's config blob.
pub fn provenance(body: &[u8]) -> Result<Provenance, Failure> {
    let config: Config = serde_json::from_slice(body).map_err(|_| Failure::Malformed)?;
    let labels = config
        .config
        .and_then(|inner| inner.labels)
        .unwrap_or_default();
    let get = |key: &str| labels.get(key).map(String::as_str);
    Ok(Provenance {
        source: get(SOURCE).and_then(|text| capped(text, SHORT_CHARS)),
        revision: get(REVISION).and_then(|text| capped(text, SHORT_CHARS)),
        version: get(VERSION).and_then(|text| capped(text, SHORT_CHARS)),
        description: get(DESCRIPTION)
            .and_then(|text| capped(&markdown::plain(text), DESCRIPTION_CHARS)),
        documentation: get(DOCUMENTATION).and_then(link),
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    /// A config blob with `labels` as a JSON object.
    fn config(labels: &str) -> Vec<u8> {
        format!(r#"{{"architecture": "amd64", "os": "linux", "config": {{"Env": [], "Labels": {labels}}}}}"#)
            .into_bytes()
    }

    #[rstest]
    fn reads_the_labels_a_card_shows() {
        let body = config(
            r#"{
  "org.opencontainers.image.source": "https://code.example.com/team/app",
  "org.opencontainers.image.revision": "0a1b2c3d",
  "org.opencontainers.image.version": "1.4.2",
  "org.opencontainers.image.description": "A media server",
  "org.opencontainers.image.documentation": "https://docs.example.com/app",
  "maintainer": "someone"
}"#,
        );
        let expected = Provenance {
            source: Some("https://code.example.com/team/app".into()),
            revision: Some("0a1b2c3d".into()),
            version: Some("1.4.2".into()),
            description: Some("A media server".into()),
            documentation: Some("https://docs.example.com/app".into()),
        };
        assert_eq!(provenance(&body), Ok(expected));
    }

    #[rstest]
    #[case::no_labels("null")]
    #[case::empty_labels("{}")]
    fn reads_none_from_an_image_without_labels(#[case] labels: &str) {
        assert_eq!(provenance(&config(labels)), Ok(Provenance::default()));
    }

    #[rstest]
    fn reads_none_from_a_config_without_a_config() {
        assert_eq!(provenance(b"{}"), Ok(Provenance::default()));
    }

    #[rstest]
    fn a_blob_that_is_not_json_is_malformed() {
        assert_eq!(provenance(b"<html>"), Err(Failure::Malformed));
    }
}
