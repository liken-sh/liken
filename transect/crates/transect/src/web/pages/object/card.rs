//! The card shown beside an object's board: the kind's description from
//! its CRD, the workloads that reconcile the object, the GitOps objects
//! that apply it, and the kind's knowledge when it has any.

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use k8s_openapi::jiff::Timestamp;

use super::super::super::snapshot::Snapshot;
use super::refer;
use transect_graph::edges::{Endpoint, Graph, Kind, Object};
use transect_graph::links::Reference;
use transect_map::objects::ObjectCard;
use transect_observe::health::Level;
use transect_resources::knowledge::KnownThrough;

pub fn card(
    snapshot: &Snapshot,
    object: &Object,
    meta: Option<&ObjectMeta>,
    graph: &Graph,
    level: Option<Level>,
    resting: bool,
    now: Timestamp,
) -> ObjectCard {
    let here = object.namespace.as_deref();
    let end = Endpoint::Object(object.clone());
    let refer_all = |kind: Kind| -> Vec<_> {
        let ends = graph.leaving(kind, &end).into_iter();
        ends.map(|end| refer(snapshot, end, here)).collect()
    };
    let controllers = refer_all(Kind::ControlledBy);
    let controller = match graph.leaving(Kind::ControlledBy, &end)[..] {
        [Endpoint::Object(one)] => Some(Reference {
            href: snapshot.pages().href(one),
            text: one.name.clone(),
        }),
        _ => None,
    };
    let known = snapshot
        .kinds
        .of_object(object)
        .map(|knowledge| KnownThrough::of(knowledge, controller));
    ObjectCard {
        kind: object.kind.clone(),
        name: object.name.clone(),
        level,
        resting,
        why: snapshot.definitions.of(object),
        controllers,
        code: refer_all(Kind::ReconcilesFrom),
        known,
        facts: facts(object, meta, now),
    }
}

/// The kind with its API group, the namespace, and the age. A custom
/// kind also gets "controlled by: no controller found", because some
/// operator should reconcile it. The card shows these facts only when it
/// has nothing else to show: no CRD description, controller, GitOps
/// object, or knowledge.
fn facts(
    object: &Object,
    meta: Option<&ObjectMeta>,
    now: Timestamp,
) -> Vec<(&'static str, String)> {
    let kind = match object.group.as_str() {
        "" => object.kind.clone(),
        group => format!("{} in {group}", object.kind),
    };
    let namespace = object
        .namespace
        .clone()
        .map(|namespace| ("namespace", namespace));
    let created = meta.and_then(|meta| meta.creation_timestamp.as_ref());
    let age = created.map(|created| {
        let minutes = now.duration_since(created.0).as_secs() / 60;
        ("age", transect_resources::knowledge::age(minutes))
    });
    let custom = transect_map::objects::kinds::built_in(&object.group, &object.kind).is_none();
    let controller = custom.then(|| ("controlled by", "no controller found".to_string()));
    std::iter::once(("kind", kind))
        .chain(namespace)
        .chain(age)
        .chain(controller)
        .collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::namespaced(
        Object::new("networking.k8s.io", "Ingress", Some("shop"), "web"),
        vec![("kind", "Ingress in networking.k8s.io"), ("namespace", "shop"), ("age", "2d")]
    )]
    #[case::a_custom_kind(
        Object::new("caches.example.test", "Cache", Some("shop"), "sessions"),
        vec![("kind", "Cache in caches.example.test"), ("namespace", "shop"), ("age", "2d"), ("controlled by", "no controller found")]
    )]
    #[case::core_and_cluster_scoped(
        Object::new("", "PersistentVolume", None, "pv-1"),
        vec![("kind", "PersistentVolume"), ("age", "2d")]
    )]
    fn lists_kind_namespace_and_age(#[case] object: Object, #[case] expected: Vec<(&str, &str)>) {
        let meta: ObjectMeta =
            serde_yaml::from_str("{creationTimestamp: '2026-01-01T00:00:00Z'}").unwrap();
        let now: Timestamp = "2026-01-03T00:00:00Z".parse().unwrap();
        let found = facts(&object, Some(&meta), now);
        let found: Vec<(&str, &str)> = found.iter().map(|(l, v)| (*l, v.as_str())).collect();
        assert_eq!(found, expected);
    }
}
