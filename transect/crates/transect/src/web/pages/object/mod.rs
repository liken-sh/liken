//! The page of any object that has no page of its own: what its kind
//! is, its conditions, its owner and controller, the sections its kind
//! has, and the edges that leave it and reach it.
//!
//! The object is read from the store that holds it.  A kind no store
//! holds, such as a `Lease`, is read once when its page loads, and its
//! page states that.

mod about;
pub mod card;
mod columns;
mod find;
mod lease;
pub mod part_of;
mod rbac;
mod reach;
mod service;
mod storage;
mod used;
mod volume;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use k8s_openapi::jiff::Timestamp;
use kube::core::DynamicObject;
use serde_json::Value;

use super::super::model::Model;
use super::super::snapshot::Snapshot;
use transect_graph::edges::{Endpoint, Object, Shown};
use transect_graph::links::Reference;
use transect_map::objects::kinds::{Catalog, ReadFrom, Served};
use transect_map::objects::reads::{Key, Read, Reads};
use transect_map::objects::{self, ObjectBoard, ObjectPage, Section};
use transect_resources::layers::Layers;
pub use volume::{Asked as VolumeAt, volume_page};

/// What a URL names: a namespace for a namespaced kind, the kind's name
/// in the URL, and the object's name.
#[derive(Clone, Copy, Debug)]
pub struct ObjectAt<'a> {
    pub namespace: Option<&'a str>,
    pub resource: &'a str,
    pub name: &'a str,
}

/// An object as the page reads it: its metadata and its fields, which
/// for a kind of Kubernetes's own hold only its status, and for a
/// custom kind watched trimmed, its status and its printer columns'
/// values.
pub struct Found {
    pub meta: ObjectMeta,
    pub data: Value,
    /// The version it was read at, for a custom kind, which picks the
    /// printer columns its CRD declares.
    pub api_version: Option<String>,
}

impl Found {
    /// `object`, whole or as its store keeps it.
    pub fn of(object: &DynamicObject) -> Self {
        Self {
            meta: object.metadata.clone(),
            data: object.data.clone(),
            api_version: object.types.as_ref().map(|types| types.api_version.clone()),
        }
    }

    /// The object as a `DynamicObject`, which the column reader takes.
    pub fn dynamic(&self) -> DynamicObject {
        DynamicObject {
            types: None,
            metadata: self.meta.clone(),
            data: self.data.clone(),
        }
    }
}

/// The kind `asked` names, when its scope matches the URL's.
fn served(snapshot: &Snapshot, asked: ObjectAt) -> Option<Served> {
    let custom: Vec<_> = snapshot.custom.iter().map(|(api, _)| api.clone()).collect();
    let catalog = Catalog {
        custom: &custom,
        scoped: &snapshot.cluster_scoped,
    };
    let served = catalog.resolve(asked.resource)?;
    (served.namespaced == asked.namespace.is_some()).then_some(served)
}

/// The read the page of `asked` makes when it loads, or none when a
/// store holds its kind, or when its name is one Kubernetes could not
/// hold, which no read is made for.
pub fn read_key(model: &Model, asked: ObjectAt) -> Option<Key> {
    let served = served(&model.snapshot, asked)?;
    (served.read == Some(ReadFrom::Get))
        .then(|| Key::of(&served.resource, asked.namespace, asked.name))?
}

/// The line under the title of a page read once.
pub const READ_ONCE: &str = "Read when the page loaded; reload for a newer copy.";

/// The object `key` names, as `reads` last read it, with the line the
/// page shows, or none when the API server holds no such object.  A
/// read that was refused or failed, or that is no longer kept, draws
/// the object's name and a line that gives the reason.
fn read_once(reads: &Reads, key: &Key) -> Option<(Found, String)> {
    let named = || Found {
        meta: ObjectMeta {
            name: Some(key.name.clone()),
            namespace: key.namespace.clone(),
            ..ObjectMeta::default()
        },
        data: Value::Null,
        api_version: None,
    };
    let capped = |said: &str| transect_graph::untrusted::capped(said, 200).unwrap_or_default();
    let kind = &key.resource.kind;
    Some(match reads.last(key) {
        Some(Read::Found(object)) => (Found::of(&object), READ_ONCE.into()),
        Some(Read::Missing) => return None,
        Some(Read::Refused(said)) => (
            named(),
            format!(
                "The API server refused transect's read of this {kind}. Its message: {}",
                capped(&said)
            ),
        ),
        Some(Read::Failed(said)) => (
            named(),
            format!(
                "Reading this {kind} failed; reload to try again. The error: {}",
                capped(&said)
            ),
        ),
        None => (
            named(),
            format!("This {kind} has not been read; reload to read it."),
        ),
    })
}

/// The page of the object `asked` names, or none when the cluster
/// holds no such object.
pub fn object_page(
    model: &Model,
    layers: &Layers,
    reads: &Reads,
    asked: ObjectAt,
    now: Timestamp,
) -> Option<ObjectPage> {
    let snapshot = &model.snapshot;
    let served = served(snapshot, asked)?;
    let resource = &served.resource;
    if let (Some(namespace), "", "PersistentVolumeClaim") = (
        asked.namespace,
        resource.group.as_str(),
        resource.kind.as_str(),
    ) {
        return volume_page(model, layers, VolumeAt::Claim(namespace, asked.name), now);
    }
    let object = Object::new(&resource.group, &resource.kind, asked.namespace, asked.name);
    let (found, note) = match served.read {
        Some(ReadFrom::Get) => {
            let key = Key::of(resource, asked.namespace, asked.name)?;
            let (found, note) = read_once(reads, &key)?;
            (
                found,
                Some(transect_resources::resources::OffLine::said(note)),
            )
        }
        _ => (find::found(snapshot, &object)?, None),
    };
    let place = match asked.namespace {
        Some(namespace) => Some((namespace.to_string(), snapshot.layer_of(namespace, layers)?)),
        None => None,
    };
    let custom = served.read.is_none();
    let labels = objects::labels(snapshot.custom.iter().map(|(api, _)| api));
    let label = labels
        .get(&(resource.group.clone(), resource.kind.clone()))
        .cloned()
        .unwrap_or_else(|| {
            transect_resources::knowledge::plural_label(&resource.kind, Some(&resource.plural))
        });
    let kind_href = match (custom, asked.namespace) {
        (true, Some(namespace)) => Some(transect_graph::links::objects(namespace, &served.url())),
        _ => None,
    };
    let dynamic = found.dynamic();
    let level = transect_observe::health::reported(&snapshot.kinds, &object, &dynamic);
    let resting = transect_observe::health::resting(&snapshot.kinds, &object, &dynamic);
    let path = transect_graph::links::page(&object, &snapshot.plurals)?;
    let graph = model.graph(&Shown::default());
    let deciding = transect_observe::health::Deciding::of(&snapshot.kinds, &object);
    let center = objects::Center {
        kind: object.kind.clone(),
        name: object.name.clone(),
        level,
        resting: resting.clone(),
        note,
        chips: transect_map::map::every_chip(&found.data, &deciding),
        messages: messages(&objects::conditions(&found.data, &deciding)),
    };
    let sections = sections(model, &object, &found, &graph, now);
    let page = ObjectPage {
        kind: object.kind.clone(),
        name: object.name.clone(),
        place,
        label,
        kind_href,
        path,
        board: ObjectBoard::build(Some(center), sections)
            .with_nesting(part_of::nesting(snapshot, &graph, &object)),
        card: card::card(
            snapshot,
            &object,
            Some(&found.meta),
            &graph,
            level,
            resting.is_some(),
            now,
        ),
    };
    Some(page)
}

/// The message of each condition that takes a warm dot, so the board
/// shows the reason without a hover.
pub fn messages(conditions: &[objects::Condition]) -> Vec<String> {
    let warm = conditions.iter().filter(|condition| {
        condition
            .level
            .is_some_and(|level| level != transect_observe::health::Level::Healthy)
    });
    warm.filter_map(|condition| condition.message.clone())
        .collect()
}

/// Every section of `object`'s page: what it is, what its kind adds, and
/// the edges at either end of it.
pub fn sections(
    model: &Model,
    object: &Object,
    found: &Found,
    graph: &transect_graph::edges::Graph,
    now: Timestamp,
) -> Vec<Section> {
    let snapshot = &model.snapshot;
    let mut sections = vec![about::about(snapshot, object, &found.meta, graph)];
    sections.extend(columns::columns(snapshot, object, found, now));
    sections.extend(kind_sections(snapshot, object, found, now));
    sections.extend(reach::edges(snapshot, object, graph));
    sections
}

/// The sections `object`'s kind adds, when it is one of Kubernetes's
/// own that has some.
fn kind_sections(
    snapshot: &Snapshot,
    object: &Object,
    found: &Found,
    now: Timestamp,
) -> Vec<Section> {
    match (object.group.as_str(), object.kind.as_str()) {
        ("", "Service") => service::service(snapshot, object),
        ("networking.k8s.io", "Ingress") => service::ingress(snapshot, object),
        ("", "ConfigMap") => used::config_map(snapshot, object),
        ("networking.k8s.io", "IngressClass") => used::ingress_class(snapshot, object),
        ("scheduling.k8s.io", "PriorityClass") => used::priority_class(snapshot, object),
        ("rbac.authorization.k8s.io", "Role" | "ClusterRole") => rbac::rules(snapshot, object),
        ("rbac.authorization.k8s.io", "RoleBinding" | "ClusterRoleBinding") => {
            rbac::binding(snapshot, object)
        }
        ("storage.k8s.io", "StorageClass") => storage::class(snapshot, object),
        ("storage.k8s.io", "CSIDriver") => storage::driver(snapshot, object),
        ("resource.k8s.io", "ResourceClaim") => storage::claim(snapshot, object, &found.data),
        ("coordination.k8s.io", "Lease") => lease::lease(snapshot, object, found, now),
        _ => Vec::new(),
    }
}

/// How a section names `end`: its words, and its page when it has one.
pub fn refer(snapshot: &Snapshot, end: &Endpoint, here: Option<&str>) -> Reference {
    let mut reference = Reference::to_end(end, here, &snapshot.plurals);
    if let Endpoint::Device(device) = end {
        reference.text = snapshot.devices.named(device, &snapshot.resources.devices);
    }
    reference.href = match end {
        Endpoint::Object(object) => snapshot.pages().href(object),
        Endpoint::Machine(machine) => Some(transect_graph::links::machine(machine)),
        Endpoint::Outside(name) => Some(snapshot.outsides.page_of(name)),
        Endpoint::Device(device) => transect_graph::links::device(device),
    };
    reference
}

/// How a section names `object`.
pub fn refer_object(snapshot: &Snapshot, object: &Object, here: Option<&str>) -> Reference {
    refer(snapshot, &Endpoint::Object(object.clone()), here)
}

#[cfg(test)]
mod tests;
