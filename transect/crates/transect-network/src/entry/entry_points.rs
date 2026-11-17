//! An `IngressController`'s `entryPoints`: the ports its controller
//! serves an `Ingress` on, which an `Ingress` may name in an
//! annotation.
//!
//! ```yaml
//! entryPoints:
//!   annotation: example.io/entrypoints
//!   default: [web, websecure]
//!   ports:
//!     - {name: web, port: 80, redirectsTo: websecure}
//!     - {name: websecure, port: 443, tls: true}
//! ```
//!
//! Without them, an `Ingress` is served on port 80, and on 443 when it
//! has `spec.tls`, as the `Ingress` API documents.

use k8s_openapi::api::networking::v1::Ingress;
use serde::Deserialize;

/// The ports a controller serves an `Ingress` on.
#[derive(Clone, Debug, PartialEq)]
pub struct EntryPoints {
    /// The `Ingress` annotation that names entry points, comma-separated.
    pub annotation: Option<String>,
    /// The entry points an `Ingress` without the annotation is served on.
    pub default: Vec<String>,
    pub ports: Vec<EntryPort>,
}

/// One entry point: its name, its port, whether it serves an
/// `Ingress`'s TLS, and the entry point the controller redirects it to.
#[derive(Clone, Debug, PartialEq, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct EntryPort {
    pub name: String,
    pub port: u16,
    #[serde(default)]
    pub tls: bool,
    pub redirects_to: Option<String>,
}

/// One port an `Ingress` is served on, whether it serves the
/// `Ingress`'s TLS there, and the TLS port the controller redirects it
/// to, when it declares one.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Served {
    pub port: u16,
    pub tls: bool,
    pub redirect: Option<u16>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Written {
    annotation: Option<String>,
    #[serde(default)]
    default: Vec<String>,
    ports: Vec<EntryPort>,
}

/// The entry points `written` declares, or why they do not validate.
pub fn read(written: Written) -> Result<EntryPoints, String> {
    if written.ports.is_empty() {
        return Err("entryPoints lists no ports".into());
    }
    let named = |name: &str| written.ports.iter().any(|port| port.name == name);
    let unknown = written.default.iter().find(|name| !named(name));
    if let Some(name) = unknown {
        return Err(format!(
            "entryPoints' default names {name}, which is not the name of a port"
        ));
    }
    let redirects = written
        .ports
        .iter()
        .filter_map(|port| port.redirects_to.as_deref());
    if let Some(name) = redirects.into_iter().find(|name| !named(name)) {
        return Err(format!(
            "entryPoints redirects to {name}, which is not the name of a port"
        ));
    }
    Ok(EntryPoints {
        annotation: written.annotation,
        default: written.default,
        ports: written.ports,
    })
}

impl EntryPoints {
    /// The ports `ingress` is served on: those its annotation names, or
    /// the default ones.
    pub fn of(&self, ingress: &Ingress) -> Vec<Served> {
        let annotations = ingress.metadata.annotations.as_ref();
        let named = self
            .annotation
            .as_ref()
            .and_then(|key| annotations?.get(key));
        let names: Vec<&str> = match named {
            Some(value) => value.split(',').map(str::trim).collect(),
            None => self.default.iter().map(String::as_str).collect(),
        };
        let ports = self
            .ports
            .iter()
            .filter(|port| names.contains(&port.name.as_str()));
        let tls_port = |name: &str| {
            let target = self.ports.iter().find(|port| port.name == name)?;
            target.tls.then_some(target.port)
        };
        ports
            .map(|port| Served {
                port: port.port,
                tls: port.tls,
                redirect: port.redirects_to.as_deref().and_then(tls_port),
            })
            .collect()
    }
}

/// The ports an `Ingress` is served on with no entry points declared:
/// 80, and 443 for its TLS when it has any.
pub fn conventional(ingress: &Ingress) -> Vec<Served> {
    let spec = ingress.spec.as_ref();
    let tls = spec
        .and_then(|spec| spec.tls.as_ref())
        .is_some_and(|tls| !tls.is_empty());
    let mut served = vec![Served {
        port: 80,
        tls: false,
        redirect: None,
    }];
    if tls {
        served.push(Served {
            port: 443,
            tls: true,
            redirect: None,
        });
    }
    served
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn points() -> EntryPoints {
        let written: Written = serde_yaml::from_str(
            "{annotation: example.io/entrypoints, default: [web, websecure], ports: [{name: web, port: 80, redirectsTo: websecure}, {name: websecure, port: 443, tls: true}]}",
        )
        .unwrap();
        read(written).unwrap()
    }

    fn ingress(text: &str) -> Ingress {
        serde_yaml::from_str(text).unwrap()
    }

    #[rstest]
    #[case::the_default("{metadata: {name: a}}", vec![(80, false, Some(443)), (443, true, None)])]
    #[case::named_by_the_annotation("{metadata: {name: a, annotations: {example.io/entrypoints: websecure}}}", vec![(443, true, None)])]
    fn reads_the_ports_an_ingress_is_served_on(
        #[case] text: &str,
        #[case] expected: Vec<(u16, bool, Option<u16>)>,
    ) {
        let served = points().of(&ingress(text));
        let found: Vec<(u16, bool, Option<u16>)> =
            served.iter().map(|s| (s.port, s.tls, s.redirect)).collect();
        assert_eq!(found, expected);
    }

    #[rstest]
    #[case::no_tls("{metadata: {name: a}}", vec![(80, false)])]
    #[case::tls("{metadata: {name: a}, spec: {tls: [{hosts: [a.example.io]}]}}", vec![(80, false), (443, true)])]
    fn reads_the_conventional_ports(#[case] text: &str, #[case] expected: Vec<(u16, bool)>) {
        let found: Vec<(u16, bool)> = conventional(&ingress(text))
            .iter()
            .map(|s| (s.port, s.tls))
            .collect();
        assert_eq!(found, expected);
    }

    #[rstest]
    #[case::no_ports("{ports: []}", "no ports")]
    #[case::an_unknown_default(
        "{default: [web], ports: [{name: websecure, port: 443}]}",
        "default names web"
    )]
    #[case::an_unknown_redirect(
        "{ports: [{name: web, port: 80, redirectsTo: secure}]}",
        "redirects to secure"
    )]
    fn says_why_entry_points_do_not_validate(#[case] text: &str, #[case] says: &str) {
        let written: Written = serde_yaml::from_str(text).unwrap();
        let error = read(written).unwrap_err();
        assert!(error.contains(says), "{error:?}");
    }
}
