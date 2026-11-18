//! Image provenance: the repository and commit each running image was
//! built from, read from its OCI labels in the registry that serves it.
//!
//! An image's labels are in its config blob, not in the Kubernetes API,
//! so transect reads each registry's API directly: the manifest for the
//! digest a container runs, the manifest for its machine's platform when
//! that is an index, then the config blob.  A digest names immutable
//! content, so transect reads its labels once and keeps them while a pod
//! runs that digest.
//!
//! The reads run in a background task, a few at a time.  A render never
//! waits for a read, so a page shows the labels read so far.  Each
//! completed read marks the cluster changed, so open pages redraw.

mod api_server;
mod auth;
mod census;
mod challenge;
mod client;
mod credentials;
mod digest;
pub mod labels;
mod manifest;
pub mod mirror;
mod origin;
mod reference;
mod runs;
mod sentence;
mod state;
mod task;

use std::collections::HashMap;
use std::sync::Arc;

pub use census::{Census, Gap, Unreadable, census};
pub use client::Registries;
pub use credentials::Credentials;
pub use labels::Provenance;
pub use manifest::Platform;
#[cfg(any(test, feature = "fixtures"))]
pub use origin::Built;
pub use origin::{Origin, Origins};
pub use reference::{ImageRef, parse};
pub use runs::Runs;
pub use task::watch;

/// Why transect could not read an image's labels.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Failure {
    /// No connection, or no response in time.
    Unreachable,
    /// The host responded, but not as a registry.
    NotARegistry,
    /// The registry responded 429.
    RateLimited,
    /// The registry requires credentials that transect was not given.
    NeedsCredentials,
    /// The registry refused the credentials transect sent.
    Refused,
    /// The registry has no manifest for the digest.
    NotFound,
    /// The image's index has no manifest for the machine's platform.
    NoPlatform(String),
    /// A manifest of a kind that holds no config blob, such as Docker's
    /// schema 1.
    Unsupported,
    /// A response that did not parse.
    Malformed,
    /// A response larger than transect reads.
    TooLarge,
    /// A response whose content does not hash to the requested digest.
    Mismatch,
    /// The token service responded with a status other than 2xx, 401,
    /// or 403, or with a body that has no token.  The failure keeps
    /// neither the status nor the body, because the registry's challenge
    /// sets the token service's URL.  Anyone who can run a pod from a
    /// registry they control could point transect at an internal address
    /// and read its response on a page.
    NoToken,
    /// Any other status the registry responded with.
    Status(u16),
}

impl Failure {
    /// Whether the failure applies to the whole registry, not to one
    /// image.  After such a failure, every image that the registry serves
    /// waits for the same backoff, and the map shows one note for the
    /// registry.
    pub fn registry_wide(&self) -> bool {
        matches!(
            self,
            Self::Unreachable | Self::NotARegistry | Self::RateLimited
        )
    }

    /// The reason, as a card or a finding shows it.
    pub fn note(&self) -> String {
        match self {
            Self::Unreachable => "the registry is unreachable".into(),
            Self::NotARegistry => "the host does not respond as a registry".into(),
            Self::RateLimited => "the registry is limiting how often transect reads it".into(),
            Self::NeedsCredentials => {
                "the registry requires credentials that transect does not have".into()
            }
            Self::Refused => "the registry refused transect's credentials".into(),
            Self::NotFound => "the registry no longer has this digest".into(),
            Self::NoPlatform(platform) => format!("the image has no manifest for {platform}"),
            Self::Unsupported => "the manifest is of a kind that has no labels".into(),
            Self::Malformed => "the registry's answer did not parse".into(),
            Self::TooLarge => "the registry's answer is larger than transect reads".into(),
            Self::Mismatch => "the registry's answer does not match its digest".into(),
            Self::NoToken => "the registry's token service gave transect no token".into(),
            Self::Status(code) => format!("the registry returned {code}"),
        }
    }
}

/// The state of transect's read of one image's labels.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Lookup {
    /// Not read yet, or being read.
    Reading,
    Read(Arc<Provenance>),
    Failed(Failure),
}

/// The labels read so far, as a snapshot for one page render.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Known {
    digests: HashMap<String, Lookup>,
    /// The registries that failed as a whole, and why.
    registries: HashMap<String, Failure>,
    /// The mirror each host was read from.
    mirrors: mirror::Mirrors,
}

impl Known {
    /// The state of `image`'s labels: the result recorded for its
    /// digest, or else its registry's failure, or else
    /// `Lookup::Reading` when transect has not read them yet.
    pub fn lookup(&self, image: &ImageRef) -> Lookup {
        if let Some(lookup) = self.digests.get(&image.digest) {
            return lookup.clone();
        }
        match self.registries.get(&image.registry) {
            Some(failure) => Lookup::Failed(failure.clone()),
            None => Lookup::Reading,
        }
    }

    pub fn with_mirrors(self, mirrors: mirror::Mirrors) -> Self {
        Self { mirrors, ..self }
    }

    /// The mirror `registry` was read from, if any.
    pub fn mirror_of(&self, registry: &str) -> Option<&mirror::Mirror> {
        self.mirrors.get(registry)
    }

    #[cfg(any(test, feature = "fixtures"))]
    pub fn with(mut self, image: &ImageRef, lookup: Lookup) -> Self {
        self.digests.insert(image.digest.clone(), lookup);
        self
    }
}

/// One image to read, for the platform of a machine that runs it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Want {
    pub image: ImageRef,
    pub platform: Platform,
}
