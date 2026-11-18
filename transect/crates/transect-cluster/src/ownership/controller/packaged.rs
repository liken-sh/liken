//! Guesses the controller of a kind from the labels of its CRD.
//!
//! The chart or manifests of an operator often put the same
//! recommended `app.kubernetes.io/` labels on its CRDs and on its
//! `Deployment`: `part-of`, `instance`, and `name`, and `component` to
//! tell the workloads of one application apart. So a kind whose
//! controller RBAC does not show can still lead to the workload that
//! reconciles it. Shared labels show only that the two were installed
//! together, not that one reconciles the other, so every match is drawn
//! as a guess. The labels of a package manager, such as Helm's, are not
//! read.

use std::collections::{BTreeMap, HashMap};

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use transect_graph::edges::Object;

/// The labels that name the application that an object belongs to.
const IDENTITY: [&str; 3] = [
    "app.kubernetes.io/part-of",
    "app.kubernetes.io/instance",
    "app.kubernetes.io/name",
];

/// The label that names which part of the application an object is.
/// When several workloads match a CRD's identity labels, the controller
/// is the one with the same component as the CRD.
const COMPONENT: &str = "app.kubernetes.io/component";

/// An object's labels and annotations whose keys are in `IDENTITY` or
/// are `COMPONENT`, which this module calls its package keys.
pub type Package = BTreeMap<String, String>;

/// The package of `meta`, without keys whose value is empty.
pub fn package(meta: &ObjectMeta) -> Package {
    let labels = meta.labels.iter().flatten();
    let annotations = meta.annotations.iter().flatten();
    labels
        .chain(annotations)
        .filter(|(key, value)| kept(key) && !value.is_empty())
        .map(|(key, value)| (key.clone(), value.clone()))
        .collect()
}

/// Whether `key` is a package key. The CRD store keeps these labels and
/// annotations and drops the others.
pub fn kept(key: &str) -> bool {
    IDENTITY.contains(&key) || key == COMPONENT
}

/// The name and package of the CRD of each custom kind, by group and
/// kind.
#[derive(Debug, Default)]
pub struct Packages(HashMap<(String, String), (String, Package)>);

impl Packages {
    pub fn read<'a>(crds: impl IntoIterator<Item = &'a CustomResourceDefinition>) -> Self {
        let read = crds.into_iter().map(|crd| {
            let key = (crd.spec.group.clone(), crd.spec.names.kind.clone());
            let name = crd.metadata.name.clone().unwrap_or_default();
            (key, (name, package(&crd.metadata)))
        });
        Self(read.collect())
    }

    pub fn of(&self, group: &str, kind: &str) -> Option<&(String, Package)> {
        self.0.get(&(group.to_string(), kind.to_string()))
    }
}

/// Returns the workload whose package matches `crd`'s, with the
/// `key=value` labels that matched. A workload matches when it has the
/// CRD's value for every identity label that the CRD has. When several
/// match, only those with the CRD's `component` are kept. Returns `None`
/// when no workload matches, or when more than one still does.
pub fn matched<'a>(
    crd: &Package,
    workloads: impl IntoIterator<Item = (&'a Object, &'a Package)>,
) -> Option<(Object, Vec<String>)> {
    let keys: Vec<&str> = IDENTITY
        .iter()
        .copied()
        .filter(|k| crd.contains_key(*k))
        .collect();
    if keys.is_empty() {
        return None;
    }
    let shares = |package: &Package, keys: &[&str]| {
        keys.iter().all(|key| package.get(*key) == crd.get(*key))
    };
    let found: Vec<(&Object, &Package)> = workloads
        .into_iter()
        .filter(|(_, package)| shares(package, &keys))
        .collect();
    let (found, keys) = match found.len() {
        0 => return None,
        1 => (found, keys),
        _ if !crd.contains_key(COMPONENT) => return None,
        _ => {
            let mut keys = keys;
            keys.push(COMPONENT);
            let narrowed = found.into_iter().filter(|(_, p)| shares(p, &keys));
            (narrowed.collect(), keys)
        }
    };
    let [(workload, _)] = found.as_slice() else {
        return None;
    };
    let matched_labels = keys.iter().map(|key| format!("{key}={}", crd[*key]));
    Some(((*workload).clone(), matched_labels.collect()))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn labels(pairs: &[(&str, &str)]) -> Package {
        pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect()
    }

    fn workload(name: &str) -> Object {
        Object::new("apps", "Deployment", Some("system"), name)
    }

    const PART: &str = "app.kubernetes.io/part-of";
    const INSTANCE: &str = "app.kubernetes.io/instance";
    const NAME: &str = "app.kubernetes.io/name";

    /// `sources` and `applier` are components of one suite. `certs` and
    /// `certs-webhook` are of one instance with different names.
    /// `scanner` has none of the package labels.
    fn workloads() -> Vec<(Object, Package)> {
        vec![
            (
                workload("sources"),
                labels(&[(PART, "suite"), (INSTANCE, "suite"), (COMPONENT, "sources")]),
            ),
            (
                workload("applier"),
                labels(&[(PART, "suite"), (INSTANCE, "suite"), (COMPONENT, "applier")]),
            ),
            (
                workload("certs"),
                labels(&[
                    (NAME, "certs"),
                    (INSTANCE, "certs"),
                    (COMPONENT, "controller"),
                ]),
            ),
            (
                workload("certs-webhook"),
                labels(&[
                    (NAME, "webhook"),
                    (INSTANCE, "certs"),
                    (COMPONENT, "webhook"),
                ]),
            ),
            (
                workload("scanner"),
                labels(&[("meta.example.com/release", "scanner")]),
            ),
        ]
    }

    #[rstest]
    #[case::its_identity(
        labels(&[(INSTANCE, "certs"), (NAME, "certs"), (COMPONENT, "crds")]),
        Some(("certs", vec!["app.kubernetes.io/instance=certs", "app.kubernetes.io/name=certs"]))
    )]
    #[case::narrowed_by_component(
        labels(&[(PART, "suite"), (INSTANCE, "suite"), (COMPONENT, "sources")]),
        Some(("sources", vec!["app.kubernetes.io/part-of=suite", "app.kubernetes.io/instance=suite", "app.kubernetes.io/component=sources"]))
    )]
    #[case::a_package_managers_own_keys(
        labels(&[("meta.example.com/release", "scanner")]),
        None
    )]
    #[case::an_instance_of_two_workloads(labels(&[(INSTANCE, "certs")]), None)]
    #[case::a_component_no_workload_is(
        labels(&[(PART, "suite"), (INSTANCE, "suite"), (COMPONENT, "watcher")]),
        None
    )]
    #[case::no_package(labels(&[]), None)]
    fn finds_the_workload_a_crd_was_installed_with(
        #[case] crd: Package,
        #[case] expected: Option<(&str, Vec<&str>)>,
    ) {
        let workloads = workloads();
        let found = matched(&crd, workloads.iter().map(|(o, p)| (o, p)));
        let found = found.map(|(workload, matched)| (workload.name, matched));
        let expected = expected.map(|(name, matched)| {
            (
                name.to_string(),
                matched.into_iter().map(String::from).collect(),
            )
        });
        assert_eq!(found, expected);
    }
}
