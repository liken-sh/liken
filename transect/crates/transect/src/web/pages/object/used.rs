//! The objects that name a `ConfigMap`, an `IngressClass`, or a
//! `PriorityClass`: the workloads whose pods read it, and the `Ingress`
//! objects of a class.  A `ServiceAccount`'s page is its access drawing.

use std::collections::BTreeSet;

use k8s_openapi::api::core::v1::PodSpec;
use kube::ResourceExt;

use super::super::super::snapshot::Snapshot;
use super::refer_object;
use transect_graph::edges::Object;
use transect_map::objects::{Section, Side, words};

/// The top owners of the pods in `namespace`, or in every namespace,
/// whose spec names the object, as `reads` decides.
fn workloads(
    snapshot: &Snapshot,
    namespace: Option<&str>,
    reads: impl Fn(&PodSpec) -> bool,
) -> BTreeSet<Object> {
    let pods = snapshot
        .pods
        .iter()
        .filter(|pod| namespace.is_none() || pod.namespace().as_deref() == namespace);
    pods.filter(|pod| pod.spec.as_ref().is_some_and(&reads))
        .map(|pod| {
            let object = Object::new("", "Pod", pod.namespace().as_deref(), &pod.name_any());
            snapshot.ownership.top_owner(&object)
        })
        .collect()
}

fn refer_all(
    snapshot: &Snapshot,
    objects: BTreeSet<Object>,
    here: Option<&str>,
) -> Vec<transect_graph::links::Reference> {
    objects
        .iter()
        .map(|object| refer_object(snapshot, object, here))
        .collect()
}

/// Whether `spec` reads the `ConfigMap` `name`: as a volume, a projected
/// source, or a container's environment.
fn reads_config_map(spec: &PodSpec, name: &str) -> bool {
    let volumes = spec.volumes.iter().flatten();
    let mounted = volumes.clone().any(|volume| {
        volume
            .config_map
            .as_ref()
            .is_some_and(|source| source.name == name)
    });
    let projected = volumes.filter_map(|volume| volume.projected.as_ref());
    let projected = projected.flat_map(|projected| projected.sources.iter().flatten());
    let projected = projected
        .filter_map(|source| source.config_map.as_ref())
        .any(|source| source.name == name);
    let containers = spec
        .containers
        .iter()
        .chain(spec.init_containers.iter().flatten());
    let environment = containers.clone().any(|container| {
        let from = container.env_from.iter().flatten();
        let from = from.filter_map(|from| from.config_map_ref.as_ref());
        let env = container.env.iter().flatten();
        let keys = env.filter_map(|var| var.value_from.as_ref()?.config_map_key_ref.as_ref());
        from.map(|from| &from.name)
            .chain(keys.map(|key| &key.name))
            .any(|read| read == name)
    });
    mounted || projected || environment
}

pub fn config_map(snapshot: &Snapshot, object: &Object) -> Vec<Section> {
    let here = object.namespace.as_deref();
    let readers = workloads(snapshot, here, |spec| reads_config_map(spec, &object.name));
    let depends = snapshot
        .intents
        .of(object)
        .and_then(|said| said.depends_on.as_ref());
    let depends = depends
        .into_iter()
        .flat_map(|listed| listed.entries.iter().map(words));
    vec![
        Section::new("Read by", Side::Into)
            .fact("workloads", refer_all(snapshot, readers, here))
            .fact("depends on", depends.collect())
            .or("No running pod reads it."),
    ]
}

pub fn ingress_class(snapshot: &Snapshot, object: &Object) -> Vec<Section> {
    let ingresses = snapshot.ingresses.iter().filter(|ingress| {
        let named = ingress
            .spec
            .as_ref()
            .and_then(|spec| spec.ingress_class_name.as_deref());
        let annotated = ingress
            .annotations()
            .get("kubernetes.io/ingress.class")
            .map(String::as_str);
        named.or(annotated) == Some(object.name.as_str())
    });
    let ingresses = ingresses.map(|ingress| {
        Object::new(
            "networking.k8s.io",
            "Ingress",
            ingress.namespace().as_deref(),
            &ingress.name_any(),
        )
    });
    vec![
        Section::new("Referred to by", Side::Into)
            .fact("Ingresses", refer_all(snapshot, ingresses.collect(), None))
            .or("No Ingress names it."),
    ]
}

pub fn priority_class(snapshot: &Snapshot, object: &Object) -> Vec<Section> {
    let runs = workloads(snapshot, None, |spec| {
        spec.priority_class_name.as_deref() == Some(object.name.as_str())
    });
    vec![
        Section::new("Referred to by", Side::Into)
            .fact("workloads", refer_all(snapshot, runs, None))
            .or("No running pod names it."),
    ]
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn spec(yaml: &str) -> PodSpec {
        serde_yaml::from_str(yaml).unwrap()
    }

    #[rstest]
    #[case::a_volume(
        "{containers: [], volumes: [{name: v, configMap: {name: settings}}]}",
        true
    )]
    #[case::projected(
        "{containers: [], volumes: [{name: v, projected: {sources: [{configMap: {name: settings}}]}}]}",
        true
    )]
    #[case::env_from(
        "{containers: [{name: a, envFrom: [{configMapRef: {name: settings}}]}]}",
        true
    )]
    #[case::one_key(
        "{containers: [{name: a, env: [{name: X, valueFrom: {configMapKeyRef: {name: settings, key: x}}}]}]}",
        true
    )]
    #[case::another(
        "{containers: [{name: a, envFrom: [{configMapRef: {name: other}}]}]}",
        false
    )]
    fn finds_the_pods_that_read_a_config_map(#[case] yaml: &str, #[case] expected: bool) {
        assert_eq!(reads_config_map(&spec(yaml), "settings"), expected);
    }
}
