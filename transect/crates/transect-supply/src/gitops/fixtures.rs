//! A fake GitOps tool for tests, in the API group `apply.example.test`.
//! A `Tree` applies a directory, a `Release` installs a chart and has
//! higher `precedence` than a `Tree`, and both fetch from a `Repo`.

use std::sync::Arc;

use kube::api::ApiResource;
use kube::core::DynamicObject;

use super::GitOps;
use transect_cluster::cluster::Watch;
use transect_graph::edges::{Object, Plurals};
use transect_resources::knowledge::Kinds;
use transect_resources::knowledge::fixtures::knowledge;

pub const GROUP: &str = "apply.example.test";

/// Each kind of the tool, as (kind, plural, knowledge YAML).
const KNOWLEDGE: [(&str, &str, &str); 3] = [
    (
        "Tree",
        "trees",
        "{applies: {name: {label: tree/name}, namespace: {label: tree/namespace}}, source: [spec.sourceRef], directory: spec.path, revision: {paths: [status.lastAppliedRevision], formats: ['{ref}@sha1:{commit}']}}",
    ),
    (
        "Release",
        "releases",
        "{applies: {name: {label: release/name}, namespace: {label: release/namespace}, precedence: 1}, source: [spec.sourceRef], chart: spec.chart}",
    ),
    (
        "Repo",
        "repos",
        "{repository: ['{spec.url}'], ref: {branch: spec.ref.branch, tag: spec.ref.tag, commit: spec.ref.commit}, revision: {paths: [status.artifact.revision], at: status.artifact.lastUpdateTime}}",
    ),
];

/// An object of the tool's `kind`.
pub fn object(kind: &str, namespace: &str, name: &str) -> Object {
    Object::new(GROUP, kind, Some(namespace), name)
}

/// The tool's kinds, resolved as if the cluster served them.
pub fn kinds() -> Kinds {
    let plurals: Plurals = KNOWLEDGE
        .iter()
        .map(|(kind, plural, _)| ((GROUP.to_string(), kind.to_string()), plural.to_string()))
        .collect();
    let knowledge: Vec<_> = KNOWLEDGE
        .iter()
        .map(|(_, plural, spec)| knowledge(&format!("{plural}.{GROUP}"), spec))
        .collect();
    Kinds::resolve(&knowledge, &plurals, &[])
}

/// An object of the tool's `kind`, with `labels`, the YAML of its
/// labels, and `body`, the YAML of its spec and status.
pub fn dynamic(kind: &str, namespace: &str, name: &str, labels: &str, body: &str) -> DynamicObject {
    serde_yaml_ng::from_str(&format!(
        "apiVersion: {GROUP}/v1\nkind: {kind}\nmetadata: {{namespace: {namespace}, name: {name}, labels: {{{labels}}}}}\n{body}"
    ))
    .unwrap()
}

/// A `GitOps` reader over `objects`, each given as (kind, namespace,
/// name, labels YAML, spec and status YAML), with the watch of every
/// kind listed.
pub fn gitops(objects: &[(&str, &str, &str, &str, &str)]) -> GitOps {
    let custom: Vec<(ApiResource, Vec<Arc<DynamicObject>>)> = KNOWLEDGE
        .iter()
        .map(|(kind, plural, _)| {
            let resource = ApiResource {
                group: GROUP.into(),
                version: "v1".into(),
                api_version: format!("{GROUP}/v1"),
                kind: kind.to_string(),
                plural: plural.to_string(),
            };
            let of_kind = objects.iter().filter(|(of, ..)| of == kind);
            let of_kind = of_kind.map(|(kind, namespace, name, labels, body)| {
                Arc::new(dynamic(kind, namespace, name, labels, body))
            });
            (resource, of_kind.collect())
        })
        .collect();
    GitOps::build(&kinds(), &custom, |_, _| Watch::Listed)
}
