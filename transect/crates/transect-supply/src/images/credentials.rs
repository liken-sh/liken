//! The credentials transect sends to a registry that refuses to answer
//! without them, read from a Docker `config.json` given to transect
//! itself.  transect never reads a pod's `imagePullSecrets`, because it
//! reads no `Secret` at all.
//!
//! transect reads only the `auths` entries with an `auth` field.  A
//! credential store or helper runs a program to return the credential,
//! and a read-only map must not run programs.  So transect ignores
//! `credsStore` and `credHelpers`, and reports that it ignores them when
//! it starts.

use std::collections::HashMap;
use std::fmt;
use std::path::Path;

use anyhow::Context;
use base64::Engine;
use serde::Deserialize;

use super::reference::registry_name;

/// A username and password for one registry.
#[derive(Clone, PartialEq, Eq)]
pub struct Credential {
    pub username: String,
    pub password: String,
}

/// Never prints the password, so no log line or panic message can
/// contain it.
impl fmt::Debug for Credential {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "Credential({}, <redacted>)", self.username)
    }
}

/// The credential for each registry, by the registry's name.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Credentials {
    by_registry: HashMap<String, Credential>,
    /// Whether the file names a credential store or helpers, which
    /// transect does not run.
    pub ignored_helpers: bool,
}

impl Credentials {
    pub fn of(&self, registry: &str) -> Option<&Credential> {
        self.by_registry.get(registry)
    }

    /// The registries the file holds a credential for.
    pub fn registries(&self) -> Vec<&str> {
        let mut names: Vec<&str> = self.by_registry.keys().map(String::as_str).collect();
        names.sort();
        names
    }

    pub fn load(path: &Path) -> anyhow::Result<Self> {
        let text = std::fs::read_to_string(path)
            .with_context(|| format!("reading registry credentials from {}", path.display()))?;
        parse(&text).with_context(|| format!("reading {} as a Docker config.json", path.display()))
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Config {
    #[serde(default)]
    auths: HashMap<String, Auth>,
    #[serde(default)]
    creds_store: Option<String>,
    #[serde(default)]
    cred_helpers: Option<HashMap<String, String>>,
}

#[derive(Deserialize)]
struct Auth {
    #[serde(default)]
    auth: Option<String>,
}

/// The credentials in a `config.json`.  An entry whose `auth` does not
/// decode to `username:password` is skipped with a warning that names
/// its registry, never its content.
pub fn parse(text: &str) -> anyhow::Result<Credentials> {
    let config: Config = serde_json::from_str(text).context("not a config.json")?;
    let mut by_registry = HashMap::new();
    for (key, auth) in config.auths {
        let Some(encoded) = auth.auth else { continue };
        let registry = registry_name(host(&key));
        let decoded = base64::engine::general_purpose::STANDARD
            .decode(encoded.trim())
            .ok()
            .and_then(|bytes| String::from_utf8(bytes).ok());
        let Some((username, password)) = decoded.as_deref().and_then(|d| d.split_once(':')) else {
            tracing::warn!(
                registry,
                "skipping a credential whose auth is not base64 of user:password"
            );
            continue;
        };
        let credential = Credential {
            username: username.into(),
            password: password.into(),
        };
        by_registry.insert(registry, credential);
    }
    let helpers = config
        .cred_helpers
        .is_some_and(|helpers| !helpers.is_empty());
    Ok(Credentials {
        by_registry,
        ignored_helpers: config.creds_store.is_some() || helpers,
    })
}

/// The host an `auths` key names, which Docker writes as a bare host or
/// as a URL such as `https://index.docker.io/v1/`.
fn host(key: &str) -> &str {
    let key = key.split_once("://").map_or(key, |(_, rest)| rest);
    key.split('/').next().unwrap_or(key)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn encoded(text: &str) -> String {
        base64::engine::general_purpose::STANDARD.encode(text)
    }

    fn credential(username: &str, password: &str) -> Credential {
        Credential {
            username: username.into(),
            password: password.into(),
        }
    }

    #[rstest]
    #[case::a_bare_host("registry.example", "registry.example")]
    #[case::docker_hubs_v1_url("https://index.docker.io/v1/", "docker.io")]
    #[case::a_url_with_a_port("https://registry.example:5000", "registry.example:5000")]
    fn reads_each_registrys_credential(#[case] key: &str, #[case] registry: &str) {
        let text = format!(
            r#"{{"auths": {{"{key}": {{"auth": "{}"}}}}}}"#,
            encoded("robot:s3:cret")
        );
        let found = parse(&text).unwrap();
        assert_eq!(found.of(registry), Some(&credential("robot", "s3:cret")));
    }

    #[rstest]
    #[case::no_auth(r#"{"auths": {"registry.example": {}}}"#)]
    #[case::not_base64(r#"{"auths": {"registry.example": {"auth": "%%%"}}}"#)]
    #[case::no_colon(r#"{"auths": {"registry.example": {"auth": "cm9ib3Q="}}}"#)]
    fn skips_an_entry_with_no_usable_auth(#[case] text: &str) {
        assert_eq!(parse(text).unwrap().registries(), Vec::<&str>::new());
    }

    #[rstest]
    #[case::a_store(r#"{"credsStore": "desktop"}"#, true)]
    #[case::helpers(r#"{"credHelpers": {"registry.example": "ecr-login"}}"#, true)]
    #[case::neither(r#"{"auths": {}}"#, false)]
    fn notes_the_helpers_it_ignores(#[case] text: &str, #[case] ignored: bool) {
        assert_eq!(parse(text).unwrap().ignored_helpers, ignored);
    }

    #[rstest]
    fn never_prints_a_password() {
        let printed = format!("{:?}", credential("robot", "s3cret"));
        assert_eq!(printed, "Credential(robot, <redacted>)");
    }
}
