//! A registry's manifest for one digest: an image's, which names its
//! config blob, or an index's, which names one image per platform.

use serde::Deserialize;

use super::{Failure, digest};

/// The media types transect accepts for a manifest, the OCI and the
/// Docker ones for both an index and an image.
pub const ACCEPT: &str = "application/vnd.oci.image.index.v1+json, \
    application/vnd.docker.distribution.manifest.list.v2+json, \
    application/vnd.oci.image.manifest.v1+json, \
    application/vnd.docker.distribution.manifest.v2+json";

/// A machine's operating system and CPU, as an index names the platform
/// of each image in it.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize)]
pub struct Platform {
    pub os: String,
    pub architecture: String,
    #[serde(default)]
    pub variant: Option<String>,
}

impl Platform {
    /// The platform that transect uses for a machine whose `Node`
    /// reports none.  Most clusters run this platform.
    pub fn fallback() -> Self {
        Self::new("linux", "amd64")
    }

    pub fn new(os: &str, architecture: &str) -> Self {
        Self {
            os: os.into(),
            architecture: architecture.into(),
            variant: None,
        }
    }
}

impl std::fmt::Display for Platform {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}/{}", self.os, self.architecture)?;
        match &self.variant {
            Some(variant) => write!(f, "/{variant}"),
            None => Ok(()),
        }
    }
}

#[derive(Debug, PartialEq, Eq)]
pub enum Manifest {
    /// The digest of the image's config blob, which holds its labels.
    Image {
        config: String,
    },
    Index(Vec<Entry>),
}

/// One manifest an index lists.
#[derive(Debug, PartialEq, Eq, Deserialize)]
pub struct Entry {
    pub digest: String,
    #[serde(default)]
    pub platform: Option<Platform>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Raw {
    #[serde(default)]
    schema_version: Option<u32>,
    #[serde(default)]
    manifests: Option<Vec<Entry>>,
    #[serde(default)]
    config: Option<Descriptor>,
}

#[derive(Deserialize)]
struct Descriptor {
    digest: String,
}

/// Parses the manifest in `body` by its shape, not by its media type,
/// because some registries answer with a generic content type.  Docker's
/// schema 1 has no config blob, so it is unsupported.  transect puts
/// every digest that the manifest names into a later request's path, so
/// a manifest that names anything except a digest is malformed.
pub fn parse(body: &[u8]) -> Result<Manifest, Failure> {
    let raw: Raw = serde_json::from_slice(body).map_err(|_| Failure::Malformed)?;
    let manifest = match (raw.schema_version, raw.manifests, raw.config) {
        (Some(1), _, _) => return Err(Failure::Unsupported),
        (_, Some(entries), _) => Manifest::Index(entries),
        (_, None, Some(config)) => Manifest::Image {
            config: config.digest,
        },
        (_, None, None) => return Err(Failure::Unsupported),
    };
    let named_ok = match &manifest {
        Manifest::Image { config } => digest::ok(config),
        Manifest::Index(entries) => entries.iter().all(|entry| digest::ok(&entry.digest)),
    };
    named_ok.then_some(manifest).ok_or(Failure::Malformed)
}

/// The manifest in an index for `platform`: one whose operating system
/// and architecture match, preferring one whose variant matches too.
/// An index lists attestations with the platform `unknown/unknown`, so
/// they never match a machine.
pub fn select<'a>(entries: &'a [Entry], platform: &Platform) -> Option<&'a str> {
    let matching = || {
        entries.iter().filter(|entry| {
            entry.platform.as_ref().is_some_and(|found| {
                found.os == platform.os && found.architecture == platform.architecture
            })
        })
    };
    let variant = |entry: &&Entry| {
        let found = entry.platform.as_ref().and_then(|p| p.variant.as_ref());
        platform.variant.is_some() && found == platform.variant.as_ref()
    };
    let best = matching().find(variant).or_else(|| matching().next());
    best.map(|entry| entry.digest.as_str())
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::reference::fixtures::digest;
    use super::*;

    /// An index of one image per platform, the digest of each a hex
    /// digit repeated: `a` for amd64, `7` and `6` for arm's variants,
    /// and `f` for an attestation.
    fn index() -> String {
        let entry = |c: char, platform: &str| {
            format!(r#"{{"digest": "{}", "platform": {platform}}}"#, digest(c))
        };
        let entries = [
            entry('a', r#"{"os": "linux", "architecture": "amd64"}"#),
            entry(
                '7',
                r#"{"os": "linux", "architecture": "arm", "variant": "v7"}"#,
            ),
            entry(
                '6',
                r#"{"os": "linux", "architecture": "arm", "variant": "v6"}"#,
            ),
            entry('f', r#"{"os": "unknown", "architecture": "unknown"}"#),
        ];
        format!(
            r#"{{"schemaVersion": 2, "manifests": [{}]}}"#,
            entries.join(", ")
        )
    }

    fn image(config: &str) -> String {
        format!(r#"{{"schemaVersion": 2, "config": {{"digest": "{config}"}}}}"#)
    }

    fn entries() -> Vec<Entry> {
        match parse(index().as_bytes()) {
            Ok(Manifest::Index(entries)) => entries,
            other => panic!("not an index: {other:?}"),
        }
    }

    fn arm(variant: Option<&str>) -> Platform {
        Platform {
            variant: variant.map(Into::into),
            ..Platform::new("linux", "arm")
        }
    }

    #[rstest]
    #[case::an_oci_image(
        &format!(r#"{{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
            "config": {{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": "{}", "size": 1}}, "layers": []}}"#, digest('c')),
        Ok(Manifest::Image { config: digest('c') })
    )]
    #[case::a_docker_image_without_a_media_type(
        &image(&digest('c')),
        Ok(Manifest::Image { config: digest('c') })
    )]
    #[case::a_config_that_is_a_path(&image("../../api/v1/secrets"), Err(Failure::Malformed))]
    #[case::a_config_with_a_path_after_it(
        &image(&format!("{}/../../api", digest('c'))),
        Err(Failure::Malformed)
    )]
    #[case::an_index_entry_that_is_a_path(
        r#"{"schemaVersion": 2, "manifests": [{"digest": "../../api/v1/secrets"}]}"#,
        Err(Failure::Malformed)
    )]
    #[case::schema_1(r#"{"schemaVersion": 1, "fsLayers": []}"#, Err(Failure::Unsupported))]
    #[case::neither(r#"{"schemaVersion": 2}"#, Err(Failure::Unsupported))]
    #[case::not_json("<html>", Err(Failure::Malformed))]
    fn reads_a_manifest(#[case] body: &str, #[case] expected: Result<Manifest, Failure>) {
        assert_eq!(parse(body.as_bytes()), expected);
    }

    #[rstest]
    #[case::amd64(Platform::new("linux", "amd64"), Some(digest('a')))]
    #[case::a_variant_that_matches(arm(Some("v6")), Some(digest('6')))]
    #[case::no_variant_takes_the_first(arm(None), Some(digest('7')))]
    #[case::a_variant_the_index_lacks(arm(Some("v5")), Some(digest('7')))]
    #[case::a_platform_the_index_lacks(Platform::new("linux", "arm64"), None)]
    fn selects_the_manifest_for_a_machine(
        #[case] platform: Platform,
        #[case] expected: Option<String>,
    ) {
        assert_eq!(select(&entries(), &platform), expected.as_deref());
    }
}
