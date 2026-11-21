//! The page of an object whose kind has no page of its own: its kind,
//! conditions, owner and controller, the sections its kind adds, and
//! the edges that leave and reach it.
//!
//! The page reads the object from the store of its kind. When no store
//! holds the kind, as with a `Lease`, the page reads the object from the
//! API server when it loads, and shows `READ_ONCE` under the title.

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
pub use find::{stored, typed};
use transect_graph::edges::{Endpoint, Object, Shown};
use transect_graph::links::Reference;
use transect_map::objects::kinds::{Catalog, ReadFrom, Served};
use transect_map::objects::reads::{Key, Read, Reads};
use transect_map::objects::{self, ObjectBoard, ObjectPage, Section};
use transect_resources::layers::Layers;
pub use volume::{VolumeAt, volume_page};

/// The parts of an object's URL: the namespace (for a namespaced
/// kind), the kind's resource name, and the object's name.
#[derive(Clone, Copy, Debug)]
pub struct ObjectAt<'a> {
    pub namespace: Option<&'a str>,
    pub resource: &'a str,
    pub name: &'a str,
}

/// An object as the page reads it: its metadata and `data`. For a
/// built-in kind, `data` holds only the status. For a custom kind, the
/// watch trims the object to its status and the values of its printer
/// columns.
pub struct Found {
    pub meta: ObjectMeta,
    pub data: Value,
    /// The API version the object was read at. For a custom kind it
    /// selects which version's printer columns to use from the CRD.
    pub api_version: Option<String>,
}

impl Found {
    pub fn of(object: &DynamicObject) -> Self {
        Self {
            meta: object.metadata.clone(),
            data: object.data.clone(),
            api_version: object.types.as_ref().map(|types| types.api_version.clone()),
        }
    }

    /// The object as a `DynamicObject`, which the printer column reader
    /// takes.
    pub fn dynamic(&self) -> DynamicObject {
        DynamicObject {
            types: None,
            metadata: self.meta.clone(),
            data: self.data.clone(),
        }
    }
}

/// The kind that `at.resource` names, when the kind's scope matches
/// the URL: namespaced only if the URL has a namespace.
fn served(snapshot: &Snapshot, at: ObjectAt) -> Option<Served> {
    let custom: Vec<_> = snapshot.custom.iter().map(|(api, _)| api.clone()).collect();
    let catalog = Catalog {
        custom: &custom,
        scoped: &snapshot.cluster_scoped,
    };
    let served = catalog.resolve(at.resource)?;
    (served.namespaced == at.namespace.is_some()).then_some(served)
}

/// The read that the page of `at` makes when it loads. It is `None`
/// when a store holds the kind, when the name or the namespace is not a
/// valid Kubernetes name, and for a `Secret` or a `ConfigMap`, which
/// `Key::of` refuses to read.
pub fn read_key(model: &Model, at: ObjectAt) -> Option<Key> {
    let served = served(&model.snapshot, at)?;
    (served.read == Some(ReadFrom::Get))
        .then(|| Key::of(&served.resource, at.namespace, at.name))?
}

/// The line under the title of a page whose object was read once.
pub const READ_ONCE: &str = "Read when the page loaded; reload for a newer copy.";

/// The object of `key` as `reads` last read it, with the line to show
/// under the title, or `None` when the API server has no such object.
/// When the read was refused, failed, or has not happened, the page
/// shows only the object's name and a line with the reason.
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
    let capped =
        |message: &str| transect_graph::untrusted::capped(message, 200).unwrap_or_default();
    let kind = &key.resource.kind;
    Some(match reads.last(key) {
        Some(Read::Found(object)) => (Found::of(&object), READ_ONCE.into()),
        Some(Read::Missing) => return None,
        Some(Read::Refused(message)) => (
            named(),
            format!(
                "The API server refused transect's read of this {kind}. Its message: {}",
                capped(&message)
            ),
        ),
        Some(Read::Failed(message)) => (
            named(),
            format!(
                "Reading this {kind} failed; reload to try again. The error: {}",
                capped(&message)
            ),
        ),
        None => (
            named(),
            format!("This {kind} has not been read; reload to read it."),
        ),
    })
}

/// The page of the object in `at`, or `None` when the cluster has no
/// such object.
pub fn object_page(
    model: &Model,
    layers: &Layers,
    reads: &Reads,
    at: ObjectAt,
    now: Timestamp,
) -> Option<ObjectPage> {
    let snapshot = &model.snapshot;
    let served = served(snapshot, at)?;
    let resource = &served.resource;
    if let (Some(namespace), "", "PersistentVolumeClaim") = (
        at.namespace,
        resource.group.as_str(),
        resource.kind.as_str(),
    ) {
        return volume_page(model, layers, VolumeAt::Claim(namespace, at.name), now);
    }
    let object = Object::new(&resource.group, &resource.kind, at.namespace, at.name);
    let (found, note) = match served.read {
        Some(ReadFrom::Get) => {
            let key = Key::of(resource, at.namespace, at.name)?;
            let (found, note) = read_once(reads, &key)?;
            (
                found,
                Some(transect_resources::resources::OffLine::plain(note)),
            )
        }
        _ => (find::found(snapshot, &object)?, None),
    };
    let place = match at.namespace {
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
    let kind_href = match (custom, at.namespace) {
        (true, Some(namespace)) => Some(transect_graph::links::objects(namespace, &served.url())),
        _ => None,
    };
    let dynamic = found.dynamic();
    let level = transect_observe::health::reported(&snapshot.kinds, snapshot, &object, &dynamic);
    let resting = transect_observe::health::resting(&snapshot.kinds, snapshot, &object, &dynamic);
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

/// The message of each condition whose level is not healthy, so the
/// board shows the reason without a hover.
pub fn messages(conditions: &[objects::Condition]) -> Vec<String> {
    let unhealthy = conditions.iter().filter(|condition| {
        condition
            .level
            .is_some_and(|level| level != transect_observe::health::Level::Healthy)
    });
    unhealthy
        .filter_map(|condition| condition.message.clone())
        .collect()
}

/// Every section of `object`'s page: what it is, the printer columns,
/// what its kind adds, and the edges that leave it and reach it.
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

/// The sections that `object`'s kind adds, for the built-in kinds that
/// have any.
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

/// A reference to `end` for a section: its text, and its link when it
/// has a page.
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

/// A reference to `object` for a section.
pub fn refer_object(snapshot: &Snapshot, object: &Object, here: Option<&str>) -> Reference {
    refer(snapshot, &Endpoint::Object(object.clone()), here)
}

#[cfg(test)]
mod tests;
