//! The image a container runs, read from its status's `imageID`: the
//! registry, the repository, and the digest the kubelet pulled.
//!
//! The spec's `image` names a tag, which can move to other content.  The
//! status's `imageID` names the content itself, so its labels never
//! change and transect reads them once.

/// One image by digest.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct ImageRef {
    /// The registry's name, as an image reference gives it, with Docker
    /// Hub's aliases replaced by `docker.io`.
    pub registry: String,
    pub repository: String,
    /// `sha256:` and its hex.
    pub digest: String,
}

/// Docker Hub's name in image references.  Docker Hub does not serve its
/// API at this host.
pub const DOCKER_HUB: &str = "docker.io";

impl ImageRef {
    /// The host that serves the registry's API.
    pub fn api_host(&self) -> &str {
        match self.registry.as_str() {
            DOCKER_HUB => "registry-1.docker.io",
            other => other,
        }
    }
}

/// The registry that a host or a credential key names, with each of
/// Docker Hub's aliases replaced by `docker.io`.
pub fn registry_name(host: &str) -> String {
    let host = host.to_ascii_lowercase();
    match host.as_str() {
        "index.docker.io" | "registry-1.docker.io" | "registry.hub.docker.com" => DOCKER_HUB.into(),
        _ => host,
    }
}

/// The image an `imageID` names, as the runtimes write it: with or
/// without a scheme such as `docker-pullable://`, and with the registry
/// left out for Docker Hub.  It returns `None` for an ID that is only a
/// local image's ID, such as `sha256:…` alone, because no registry
/// serves it.
pub fn parse(image_id: &str) -> Option<ImageRef> {
    let id = image_id
        .split_once("://")
        .map_or(image_id, |(_, rest)| rest);
    let (name, digest) = id.split_once('@')?;
    if !super::digest::ok(digest) {
        return None;
    }
    let name = strip_tag(name);
    let (registry, repository) = match name.split_once('/') {
        Some((first, rest)) if first.contains(['.', ':']) || first == "localhost" => {
            (registry_name(first), rest.to_string())
        }
        _ => (DOCKER_HUB.to_string(), name.to_string()),
    };
    let repository = match (registry.as_str(), repository.contains('/')) {
        (DOCKER_HUB, false) => format!("library/{repository}"),
        _ => repository,
    };
    let allowed = |c: char| c.is_ascii_lowercase() || c.is_ascii_digit() || "-._/".contains(c);
    let repository_ok = !repository.is_empty()
        && repository.chars().all(allowed)
        && !repository
            .split('/')
            .any(|part| part.is_empty() || part == "..");
    let registry_ok = registry
        .chars()
        .all(|c| c.is_ascii_alphanumeric() || "-.:".contains(c));
    (repository_ok && registry_ok).then(|| ImageRef {
        registry,
        repository,
        digest: digest.into(),
    })
}

/// The name without a tag, which some runtimes leave on an `imageID`.
/// A tag follows the last `/`, and a colon before that `/` is a
/// registry's port.
fn strip_tag(name: &str) -> &str {
    let last = name.rfind('/').map_or(0, |slash| slash + 1);
    match name[last..].find(':') {
        Some(colon) => &name[..last + colon],
        None => name,
    }
}

#[cfg(test)]
pub mod fixtures {
    /// A sha256 digest of `c` repeated.
    pub fn digest(c: char) -> String {
        format!("sha256:{}", c.to_string().repeat(64))
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::digest;
    use super::*;

    fn image(registry: &str, repository: &str) -> Option<ImageRef> {
        Some(ImageRef {
            registry: registry.into(),
            repository: repository.into(),
            digest: digest('a'),
        })
    }

    #[rstest]
    #[case::containerd("registry.example/team/app", image("registry.example", "team/app"))]
    #[case::docker_pullable(
        "docker-pullable://registry.example/team/app",
        image("registry.example", "team/app")
    )]
    #[case::docker_hub_official("docker.io/library/db", image("docker.io", "library/db"))]
    #[case::docker_hub_without_a_registry("team/app", image("docker.io", "team/app"))]
    #[case::docker_hub_single_name("db", image("docker.io", "library/db"))]
    #[case::docker_hub_alias("index.docker.io/team/app", image("docker.io", "team/app"))]
    #[case::a_port(
        "registry.example:5000/team/app",
        image("registry.example:5000", "team/app")
    )]
    #[case::a_tag_left_on(
        "registry.example:5000/team/app:1.2",
        image("registry.example:5000", "team/app")
    )]
    #[case::localhost("localhost/app", image("localhost", "app"))]
    #[case::a_capital_letter("registry.example/Team/app", None)]
    #[case::a_step_up("registry.example/team/../app", None)]
    fn reads_the_image_an_id_names(#[case] name: &str, #[case] expected: Option<ImageRef>) {
        assert_eq!(parse(&format!("{name}@{}", digest('a'))), expected);
    }

    #[rstest]
    #[case::a_local_image_id("sha256:aaaa")]
    #[case::docker_image_id(&format!("docker://{}", digest('a')))]
    #[case::a_short_digest("registry.example/team/app@sha256:abc")]
    #[case::an_unknown_algorithm("registry.example/team/app@md5:0123456789abcdef0123456789abcdef")]
    #[case::empty("")]
    fn reads_no_image_from_an_id_no_registry_serves(#[case] id: &str) {
        assert_eq!(parse(id), None);
    }

    #[rstest]
    #[case::docker_hub("docker.io", "registry-1.docker.io")]
    #[case::any_other("registry.example", "registry.example")]
    fn reads_docker_hubs_api_at_its_own_host(#[case] registry: &str, #[case] host: &str) {
        let image = image(registry, "team/app").unwrap();
        assert_eq!(image.api_host(), host);
    }
}
