//! Registry mirrors: where transect reads the labels of a registry host
//! that does not answer as a registry from where transect runs.
//!
//! A machine's container runtime can pull a host's images from a
//! mirror, or store images that no registry serves at all, such as
//! images that an operating system imports from its own image.  The
//! mirror is configured on the machines, not in the Kubernetes API, and
//! its address differs between a workstation and the cluster.  So
//! transect reads the mirror from a flag or from a `RegistryMirror`
//! resource, and a flag takes precedence over a resource.
//!
//! transect requests from a mirror the way containerd does, with the
//! image's host in the `ns` query parameter, so one mirror can serve
//! several hosts.  A digest names the same content on the host and on
//! its mirror, so labels that transect read from either one stay valid.

use std::collections::BTreeMap;
use std::fmt;

use serde::Deserialize;

use transect_resources::resources::DocId;

/// The value that names the API server's own address as a mirror.
pub const API_SERVER: &str = "api-server";

/// The address of a mirror.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Mirror {
    /// A registry at this base URL, with no trailing slash.
    Url(String),
    /// The registry that the API server's address also serves, as k3s's
    /// embedded registry does.  transect reads it with its own
    /// credentials for the API, because the registry refuses a client
    /// that does not authenticate to the cluster.
    ApiServer,
}

impl Mirror {
    /// The mirror `value` names: `api-server`, or an `http` or `https`
    /// URL.
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

    /// The host whose entry in the registry credentials transect sends to
    /// this mirror, or `None` for the API server.
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
    /// The mirror as a finding names it.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Url(url) => write!(f, "the mirror {url}"),
            Self::ApiServer => f.write_str("the API server's registry"),
        }
    }
}

/// The mirror of each registry host.
pub type Mirrors = BTreeMap<String, Mirror>;

/// A host and its mirror from `--registry-mirror <host>=<mirror>`.
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

/// The `RegistryMirror` `value` declares, or why it does not validate.
pub fn parse(doc: DocId, value: serde_yaml::Value) -> Result<RegistryMirror, String> {
    let document: Document = serde_yaml::from_value(value).map_err(|error| error.to_string())?;
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

/// The mirror of each host.  A flag's mirror replaces a resource's
/// mirror for the same host.
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
    fn sends_a_mirrors_credentials_for_its_host(
        #[case] mirror: Mirror,
        #[case] host: Option<&str>,
    ) {
        assert_eq!(mirror.host(), host);
    }

    #[rstest]
    fn reads_a_resource() {
        let text = "metadata: {name: images}\nspec: {host: Images.Example, mirror: api-server}\n";
        let found = parse(DocId::default(), serde_yaml::from_str(text).unwrap()).unwrap();
        assert_eq!(
            (found.host.as_str(), found.mirror),
            ("images.example", Mirror::ApiServer)
        );
    }

    #[rstest]
    fn a_flag_wins_over_a_resource() {
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
