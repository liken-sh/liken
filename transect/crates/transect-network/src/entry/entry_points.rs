//! An `IngressController`'s `entryPoints`: the ports that its controller
//! serves `Ingress`es on. An `Ingress` can choose among them with an
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
//! When a controller declares none, `conventional` gives the ports.

use k8s_openapi::api::networking::v1::Ingress;
use serde::Deserialize;

/// The ports a controller serves `Ingress`es on.
#[derive(Clone, Debug, PartialEq)]
pub struct EntryPoints {
    /// The key of the `Ingress` annotation whose value lists entry point
    /// names, separated by commas.
    pub annotation: Option<String>,
    /// The entry points that serve an `Ingress` that has no such
    /// annotation.
    pub default: Vec<String>,
    pub ports: Vec<EntryPort>,
}

/// One entry point: its name, its port, whether it terminates the
/// `Ingress`'s TLS, and the name of the entry point that the controller
/// redirects it to.
#[derive(Clone, Debug, PartialEq, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct EntryPort {
    pub name: String,
    pub port: u16,
    #[serde(default)]
    pub tls: bool,
    pub redirects_to: Option<String>,
}

/// One port that serves an `Ingress`, whether it terminates the
/// `Ingress`'s TLS there, and the port it redirects to. `redirect` is
/// set only when the entry point's `redirectsTo` names an entry point
/// that has `tls: true`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Served {
    pub port: u16,
    pub tls: bool,
    pub redirect: Option<u16>,
}

/// The `entryPoints` field as an `IngressController` writes it, before
/// `read` validates it.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Written {
    annotation: Option<String>,
    #[serde(default)]
    default: Vec<String>,
    ports: Vec<EntryPort>,
}

/// The entry points in `written`, or the reason they are invalid.
pub fn read(written: Written) -> Result<EntryPoints, String> {
    if written.ports.is_empty() {
        return Err("entryPoints lists no ports".into());
    }
    let named = |name: &str| written.ports.iter().any(|port| port.name == name);
    let unknown = written.default.iter().find(|name| !named(name));
    if let Some(name) = unknown {
        return Err(format!(
            "the default of entryPoints lists {name:?}, and no port has that name"
        ));
    }
    let redirects = written
        .ports
        .iter()
        .filter_map(|port| port.redirects_to.as_deref());
    if let Some(name) = redirects.into_iter().find(|name| !named(name)) {
        return Err(format!(
            "a port of entryPoints redirects to {name:?}, and no port has that name"
        ));
    }
    Ok(EntryPoints {
        annotation: written.annotation,
        default: written.default,
        ports: written.ports,
    })
}

impl EntryPoints {
    /// The ports that serve `ingress`: the entry points listed in its
    /// annotation, or `default` when the annotation is absent.
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

/// The ports that transect shows for an `Ingress` when its controller
/// declares no entry points: 80, and also 443 when `spec.tls` has an
/// entry.
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
        let written: Written = serde_yaml_ng::from_str(
            "{annotation: example.io/entrypoints, default: [web, websecure], ports: [{name: web, port: 80, redirectsTo: websecure}, {name: websecure, port: 443, tls: true}]}",
        )
        .unwrap();
        read(written).unwrap()
    }

    fn ingress(text: &str) -> Ingress {
        serde_yaml_ng::from_str(text).unwrap()
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
        "default of entryPoints lists"
    )]
    #[case::an_unknown_redirect(
        "{ports: [{name: web, port: 80, redirectsTo: secure}]}",
        "redirects to"
    )]
    fn rejects_invalid_entry_points_with_the_reason(#[case] text: &str, #[case] says: &str) {
        let written: Written = serde_yaml_ng::from_str(text).unwrap();
        let error = read(written).unwrap_err();
        assert!(
            error.contains(says),
            "the error is {error:?}, want it to contain {says:?}"
        );
    }
}
