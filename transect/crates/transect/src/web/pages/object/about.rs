//! The "Belongs to" section of an object's page: its controller owner
//! reference, and the controllers that reconcile it.

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use super::super::super::snapshot::Snapshot;
use super::{refer, refer_object};
use transect_graph::edges::{Endpoint, Graph, Kind, Object};
use transect_map::objects::{Section, Side};

pub fn about(snapshot: &Snapshot, object: &Object, meta: &ObjectMeta, graph: &Graph) -> Section {
    let here = object.namespace.as_deref();
    let owners = meta.owner_references.iter().flatten();
    let owner = owners
        .filter(|owner| owner.controller == Some(true))
        .map(|owner| {
            let group = owner
                .api_version
                .rsplit_once('/')
                .map_or("", |(group, _)| group);
            let namespaced = Object::new(group, &owner.kind, here, &owner.name);
            let scoped = Object::new(group, &owner.kind, None, &owner.name);
            let found = match snapshot.pages().has(&namespaced) {
                true => namespaced,
                false if snapshot.pages().has(&scoped) => scoped,
                false => namespaced,
            };
            refer_object(snapshot, &found, here)
        });
    let end = Endpoint::Object(object.clone());
    let controllers = graph.leaving(Kind::ControlledBy, &end);
    let controllers = controllers
        .into_iter()
        .map(|end| refer(snapshot, end, here));
    // The namespace is left out because the breadcrumb and the
    // namespace switcher already show it.
    Section::new("Belongs to", Side::BelongsTo)
        .fact("owned by", owner.collect())
        .fact("controlled by", controllers.collect())
}
