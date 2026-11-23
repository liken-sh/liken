//! Registry mirrors: where transect reads an image's labels when
//! transect cannot reach the registry in the image's name.
//!
//! A machine's container runtime can pull a host's images from a
//! mirror, or run images that no registry serves, such as the images
//! that an operating system imports from its own image.  The mirror is
//! configured on the machines, not in the Kubernetes API, and the
//! address that reaches it from a workstation is not the address that
//! reaches it from inside the cluster.  So you tell transect the mirror
//! with a `--registry-mirror` flag or a `RegistryMirror` resource.  A
//! flag overrides a resource for the same host.
//!
//! transect requests from a mirror the way containerd does, with the
//! image's host in the `ns` query parameter, so one mirror can serve
//! several hosts.  A digest identifies the same content on the host and
//! on its mirror, so labels read from either are valid for the image.

use std::collections::BTreeMap;
use std::fmt;

use serde::Deserialize;

use transect_resources::resources::DocId;

/// The flag or resource value that selects the API server's address as
/// the mirror.
pub const API_SERVER: &str = "api-server";

/// The address of a mirror.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Mirror {
    /// A registry at this base URL, with no trailing slash.
    Url(String),
    /// The registry that is served at the API server's address, as k3s's
    /// embedded registry is.  transect reads it with its own credentials
    /// for the API, because the registry refuses a client that has not
    /// authenticated to the cluster.
    ApiServer,
}

impl Mirror {
    /// The mirror that `value` selects: `api-server`, or an `http` or
    /// `https` URL.
    pub fn read(value: &str) -> Result<Self, String> {
        let value = value.trim();
        if value == API_SERVER {
            return Ok(Self::ApiServer);
        }
        match transect_graph::upstream::http_base(value) {
            Some(url) => Ok(Self::Url(url)),
            None => Err(format!(
                "a mirror is {API_SERVER} or an http or https URL, not {value:?}"
            )),
        }
    }

    /// The host whose registry credentials transect sends to this
    /// mirror.  It is `None` for the API server.
    pub fn host(&self) -> Option<&str> {
        match self {
            Self::Url(url) => {
                let rest = url.split_once("://").map_or(url.as_str(), |(_, rest)| rest);
                rest.split('/').next()
            }
            Self::ApiServer => None,
        }
    }
}

impl fmt::Display for Mirror {
    /// The mirror as the text of a finding refers to it.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Url(url) => write!(f, "the mirror {url}"),
            Self::ApiServer => f.write_str("the API server's registry"),
        }
    }
}

/// The mirror of each registry host.
pub type Mirrors = BTreeMap<String, Mirror>;

/// The host and mirror of one `--registry-mirror <host>=<mirror>` flag.
pub fn flag(value: &str) -> Result<(String, Mirror), String> {
    let (host, mirror) = value
        .split_once('=')
        .ok_or_else(|| format!("a mirror is written <host>=<mirror>, not {value:?}"))?;
    let host = host.trim().to_ascii_lowercase();
    if host.is_empty() {
        return Err(format!("{value:?} names no host"));
    }
    Ok((host, Mirror::read(mirror)?))
}

/// One `RegistryMirror` resource.
#[derive(Clone, Debug, PartialEq)]
pub struct RegistryMirror {
    pub doc: DocId,
    pub name: String,
    pub host: String,
    pub mirror: Mirror,
}

#[derive(Deserialize)]
struct Document {
    metadata: Metadata,
    spec: Spec,
}

#[derive(Deserialize)]
struct Metadata {
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Spec {
    host: String,
    mirror: String,
}

/// The `RegistryMirror` that `value` declares, or why `value` is not
/// valid.
pub fn parse(doc: DocId, value: serde_yaml_ng::Value) -> Result<RegistryMirror, String> {
    let document: Document = serde_yaml_ng::from_value(value).map_err(|error| error.to_string())?;
    let host = document.spec.host.trim().to_ascii_lowercase();
    if host.is_empty() {
        return Err("host is empty".into());
    }
    Ok(RegistryMirror {
        doc,
        name: document.metadata.name,
        host,
        mirror: Mirror::read(&document.spec.mirror)?,
    })
}

/// The mirror of each host, where a flag overrides a resource for the
/// same host.
pub fn merged(flags: &Mirrors, resources: &[RegistryMirror]) -> Mirrors {
    let mut mirrors: Mirrors = resources
        .iter()
        .map(|one| (one.host.clone(), one.mirror.clone()))
        .collect();
    mirrors.extend(
        flags
            .iter()
            .map(|(host, mirror)| (host.clone(), mirror.clone())),
    );
    mirrors
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn url(text: &str) -> Mirror {
        Mirror::Url(text.into())
    }

    #[rstest]
    #[case::a_url("images.example=https://mirror.example:5000/", Ok(("images.example".into(), url("https://mirror.example:5000"))))]
    #[case::the_api_server("Images.Example=api-server", Ok(("images.example".into(), Mirror::ApiServer)))]
    #[case::no_equals("images.example", Err("a mirror is written <host>=<mirror>, not \"images.example\"".into()))]
    #[case::no_host("=api-server", Err("\"=api-server\" names no host".into()))]
    #[case::not_a_url("images.example=mirror.example", Err("a mirror is api-server or an http or https URL, not \"mirror.example\"".into()))]
    fn reads_a_flag(#[case] value: &str, #[case] expected: Result<(String, Mirror), String>) {
        assert_eq!(flag(value), expected);
    }

    #[rstest]
    #[case::a_url(url("https://mirror.example:5000/v2"), Some("mirror.example:5000"))]
    #[case::the_api_server(Mirror::ApiServer, None)]
    fn takes_the_credentials_host_from_a_mirror(
        #[case] mirror: Mirror,
        #[case] host: Option<&str>,
    ) {
        assert_eq!(mirror.host(), host);
    }

    #[rstest]
    fn reads_a_resource() {
        let text = "metadata: {name: images}\nspec: {host: Images.Example, mirror: api-server}\n";
        let found = parse(DocId::default(), serde_yaml_ng::from_str(text).unwrap()).unwrap();
        assert_eq!(
            (found.host.as_str(), found.mirror),
            ("images.example", Mirror::ApiServer)
        );
    }

    #[rstest]
    fn a_flag_overrides_a_resource_for_the_same_host() {
        let resources = [
            RegistryMirror {
                doc: DocId::default(),
                name: "a".into(),
                host: "images.example".into(),
                mirror: Mirror::ApiServer,
            },
            RegistryMirror {
                doc: DocId::default(),
                name: "b".into(),
                host: "other.example".into(),
                mirror: Mirror::ApiServer,
            },
        ];
        let flags = Mirrors::from([("images.example".into(), url("https://mirror.example"))]);
        assert_eq!(
            merged(&flags, &resources),
            Mirrors::from([
                ("images.example".into(), url("https://mirror.example")),
                ("other.example".into(), Mirror::ApiServer),
            ])
        );
    }
}
