//! Matches an image reference with an image name that a
//! `ProgramKnowledge` states.
//!
//! A reference names a registry host, a path, and a tag or a digest.
//! The tag and the digest change on every release, so a program's
//! knowledge names the image without them, and the match ignores them.
//! The host stays, so two registries that serve the same path are
//! different images.  A cluster can pull an image through a mirror
//! that puts its own prefix in front of the path, so the knowledge's
//! name matches when it is the whole name or the last path segments of
//! it.

/// The name of `image`: the reference without its tag and its digest,
/// with Docker Hub's defaults written out, as Docker's reference format
/// reads them.  A first segment is a registry host when it contains a
/// `.` or a `:`, or when it is `localhost`.  A name with no host is on
/// `docker.io`, `index.docker.io` is `docker.io`, and a one-segment
/// path on `docker.io` is in `library`.  So `nginx:1` is
/// `docker.io/library/nginx`, and `atcr.io/guid.foo/gofer:1.4` is
/// `atcr.io/guid.foo/gofer`.
pub fn name(image: &str) -> String {
    let image = image.split_once('@').map_or(image, |(image, _)| image);
    let image = match image.rfind(':') {
        Some(colon) if !image[colon..].contains('/') => &image[..colon],
        _ => image,
    };
    let (host, path) = match image.split_once('/') {
        Some((first, rest)) if first.contains(['.', ':']) || first == "localhost" => (first, rest),
        _ => ("docker.io", image),
    };
    let host = match host {
        "index.docker.io" => "docker.io",
        other => other,
    };
    match (host, path.contains('/')) {
        ("docker.io", false) => format!("docker.io/library/{path}"),
        _ => format!("{host}/{path}"),
    }
}

/// Whether `entry`, as a program's knowledge writes it, has a tag or a
/// digest, which would match one release only.
pub fn pinned(entry: &str) -> bool {
    let last = entry.rsplit('/').next().unwrap_or(entry);
    entry.contains('@') || last.contains(':')
}

/// Whether `image` is the image that `entry` names: the name of the
/// entry is the name of the image, or the last path segments of it.
pub fn matches(image: &str, entry: &str) -> bool {
    let (image, entry) = (name(image), name(entry));
    image == entry || image.ends_with(&format!("/{entry}"))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::a_host_and_a_tag("atcr.io/guid.foo/gofer:1.4", "atcr.io/guid.foo/gofer")]
    #[case::a_tag_and_a_digest(
        "atcr.io/guid.foo/midden:2026@sha256:8710",
        "atcr.io/guid.foo/midden"
    )]
    #[case::a_host_with_a_port("registry.local:5000/team/web:2", "registry.local:5000/team/web")]
    #[case::localhost("localhost/web:dev", "localhost/web")]
    #[case::a_docker_hub_organization("example/web:1", "docker.io/example/web")]
    #[case::an_official_image("nginx", "docker.io/library/nginx")]
    #[case::the_index_host("index.docker.io/nginx:1", "docker.io/library/nginx")]
    #[case::a_host_named_like_a_domain("guid.foo/gofer:1", "guid.foo/gofer")]
    fn writes_the_name_of_an_image(#[case] image: &str, #[case] expected: &str) {
        assert_eq!(name(image), expected);
    }

    #[rstest]
    #[case::the_same_registry("atcr.io/guid.foo/gofer:1.4", "atcr.io/guid.foo/gofer", true)]
    #[case::a_suffix_of_segments("atcr.io/guid.foo/gofer:1.4", "guid.foo/gofer", true)]
    #[case::the_entry_is_the_whole_name("guid.foo/gofer:1", "guid.foo/gofer", true)]
    #[case::a_mirror_prefix("mirror.local/atcr.io/guid.foo/gofer:1", "guid.foo/gofer", true)]
    #[case::another_program("atcr.io/guid.foo/gofer-sidecar:1", "guid.foo/gofer", false)]
    #[case::not_on_a_segment("atcr.io/xguid.foo/gofer:1", "guid.foo/gofer", false)]
    #[case::another_registry("ghcr.io/example/web:1", "docker.io/example/web", false)]
    #[case::a_docker_hub_short_name("nginx:1.27", "nginx", true)]
    #[case::a_docker_hub_name_against_another_host("ghcr.io/library/nginx:1", "nginx", false)]
    #[case::a_one_segment_entry_on_another_host("example.test/gardener:1", "gardener", false)]
    fn matches_an_image_by_its_name(
        #[case] image: &str,
        #[case] entry: &str,
        #[case] expected: bool,
    ) {
        assert_eq!(matches(image, entry), expected);
    }

    #[rstest]
    #[case::a_name("guid.foo/gofer", false)]
    #[case::a_tag("guid.foo/gofer:1.4", true)]
    #[case::a_digest("guid.foo/gofer@sha256:0a1b", true)]
    #[case::a_port("registry.local:5000/web", false)]
    fn finds_a_tag_or_a_digest_in_an_entry(#[case] entry: &str, #[case] expected: bool) {
        assert_eq!(pinned(entry), expected);
    }
}
