//! Attribution by package: the workload whose labels show it was
//! installed with a CRD is taken, as a guess, for the controller of
//! that CRD's kind.
//!
//! An operator's chart or manifests often label its CRDs and its
//! `Deployment` alike with the `app.kubernetes.io/` labels Kubernetes
//! recommends: `part-of`, `instance`, and `name`, narrowed by
//! `component`.  So a kind whose objects run no pods, and so have no
//! `ServiceAccount` to follow from RBAC, can still lead to the workload
//! that reconciles it.  Shared labels show that two objects were
//! installed together, not that one reconciles the other, so every
//! attribution made here is drawn as a guess.  No package manager's own
//! keys are read here.

use std::collections::{BTreeMap, HashMap};

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use transect_graph::edges::Object;

/// The labels that name the application an object is part of.  A CRD's
/// controller has every one of these that the CRD has.
const IDENTITY: [&str; 3] = [
    "app.kubernetes.io/part-of",
    "app.kubernetes.io/instance",
    "app.kubernetes.io/name",
];

/// The label that names the part of the application an object is,
/// which narrows several workloads of one application to the one
/// whose component matches the CRD's.
const COMPONENT: &str = "app.kubernetes.io/component";

/// What an object's labels and annotations state about the package it
/// came from, by key.
pub type Package = BTreeMap<String, String>;

/// `meta`'s package keys, from its labels and its annotations.
pub fn package(meta: &ObjectMeta) -> Package {
    let labels = meta.labels.iter().flatten();
    let annotations = meta.annotations.iter().flatten();
    labels
        .chain(annotations)
        .filter(|(key, value)| kept(key) && !value.is_empty())
        .map(|(key, value)| (key.clone(), value.clone()))
        .collect()
}

/// Whether `key` is one of the package keys, which a CRD keeps in its
/// store when its other labels and annotations are dropped.
pub fn kept(key: &str) -> bool {
    IDENTITY.contains(&key) || key == COMPONENT
}

/// Each custom kind's CRD, by group and kind: its name and its package.
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

    /// The CRD of `group`/`kind`, by name, with its package.
    pub fn of(&self, group: &str, kind: &str) -> Option<&(String, Package)> {
        self.0.get(&(group.to_string(), kind.to_string()))
    }
}

/// The one workload of `workloads` that `crd`'s package names, with the
/// labels that matched it, `key=value`: the workload that has every
/// identity label the CRD has, narrowed by component when several
/// match.  None when no workload matches, or several are left.
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
    let said = keys.iter().map(|key| format!("{key}={}", crd[*key]));
    Some(((*workload).clone(), said.collect()))
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

    /// Three workloads of one suite, each its own component, two of one
    /// instance, each its own name, and one whose package names no
    /// application.
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
        let found = found.map(|(workload, said)| (workload.name, said));
        let expected = expected.map(|(name, said)| {
            (
                name.to_string(),
                said.into_iter().map(String::from).collect(),
            )
        });
        assert_eq!(found, expected);
    }
}
