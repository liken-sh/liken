//! The list of one kind's objects in one namespace.

use k8s_openapi::jiff::Timestamp;
use std::collections::BTreeSet;

use super::super::model::Model;
use super::kinds::{kind_card, table};
use transect_graph::links::kinds::url_name;
use transect_map::map::KnownEntry;
use transect_map::objects::{self, ListPage};
use transect_resources::layers::Layers;

/// The list of the custom kind `resource` names in `namespace`, or
/// none when the map holds no such namespace or `transect` watches no
/// such kind.  A kind with no objects in the namespace lists none.
pub fn objects_page(
    model: &Model,
    layers: &Layers,
    namespace: &str,
    resource: &str,
    now: Timestamp,
) -> Option<ListPage> {
    let snapshot = &model.snapshot;
    let layer = snapshot.layer_of(namespace, layers)?;
    let (api, found) = snapshot.custom_at(resource)?;
    let labels = objects::labels(snapshot.custom.iter().map(|(api, _)| api));
    let label = labels[&(api.group.clone(), api.kind.clone())].clone();
    let here =
        |object: &transect_graph::edges::Object| object.namespace.as_deref() == Some(namespace);
    let (columns, rows) = table(snapshot, api, found, here, now);
    let pages = snapshot.pages();
    let href = |object: &transect_graph::edges::Object| pages.href(object);
    let sources = objects::Sources {
        custom: &snapshot.custom,
        kinds: &snapshot.kinds,
        href: &href,
    };
    let path = transect_graph::links::objects(namespace, &url_name(&api.group, &api.plural));
    let groups = sources.groups(namespace, &BTreeSet::new());
    let strip = groups.into_iter().map(|group| KnownEntry {
        text: format!("{} ×{}", group.label, group.items.len() + group.more),
        current: group.href == path,
        href: Some(group.href),
    });
    Some(ListPage {
        namespace: namespace.into(),
        layer,
        label,
        everywhere: transect_graph::links::kind(&api.group, &api.plural),
        strip: strip.collect(),
        columns,
        rows,
        card: kind_card(model, api, found, here),
        path,
    })
}
