//! The head of the page of an object whose kind has knowledge: the
//! strip of the other objects of its kind, and the reason that its
//! health condition gives when it is not healthy.

use super::super::super::snapshot::Snapshot;
use transect_graph::edges::{Object, kind_and_name};
use transect_graph::links::kinds::url_name;
use transect_map::map::KnownEntry;
use transect_resources::knowledge::plural_label;

/// The reason and the message of `object`'s health condition when it is
/// not `True`, for the head of the page.  A failed `Session` says there
/// that its last turns failed and that it takes no more events, and a
/// reader should not have to point at the state to learn it.
pub fn fault(snapshot: &Snapshot, object: &Object, data: &serde_json::Value) -> Option<String> {
    let condition = snapshot.kinds.of_object(object)?.condition()?;
    let conditions = data.pointer("/status/conditions")?.as_array()?;
    let found = conditions
        .iter()
        .find(|c| c.get("type").and_then(serde_json::Value::as_str) == Some(condition))?;
    let text = |key: &str| found.get(key).and_then(serde_json::Value::as_str);
    if text("status") == Some("True") {
        return None;
    }
    match (text("reason"), text("message")) {
        (Some(reason), Some(message)) => Some(format!("{reason}: {message}")),
        (reason, message) => message.or(reason).map(String::from),
    }
}

/// The most objects of the page's own kind that the strip above the
/// page names.  A busy namespace has hundreds of `Session`s, and a strip
/// that names each one with no state says little and pushes the page
/// down.
const MANY: usize = 12;

/// The strip of `object`'s kind in `namespace`: each object of the kind,
/// or, past `MANY`, the page's own object, the objects that are not
/// healthy, and a link to the list of the kind with the count of all.
pub fn siblings(snapshot: &Snapshot, object: &Object, namespace: &str) -> Vec<KnownEntry> {
    let plurals = &snapshot.plurals;
    let Some((api, found)) = snapshot.custom_kind(&object.group, &object.kind) else {
        return Vec::new();
    };
    let here = found.iter().filter_map(|found| {
        let meta = &found.metadata;
        (meta.namespace.as_deref() == Some(namespace)).then_some(())?;
        let other = Object::new(
            &api.group,
            &api.kind,
            Some(namespace),
            meta.name.as_deref()?,
        );
        Some((other, found))
    });
    let here: Vec<_> = here.collect();
    let entry = |other: &Object, said: Option<&str>| KnownEntry {
        text: match said {
            Some(said) => format!("{} · {said}", kind_and_name(&other.kind, &other.name)),
            None => kind_and_name(&other.kind, &other.name),
        },
        href: transect_graph::links::page(other, plurals),
        current: other == object,
    };
    if here.len() <= MANY {
        let mut column: Vec<KnownEntry> =
            here.iter().map(|(other, _)| entry(other, None)).collect();
        column.sort_by(|a, b| a.text.cmp(&b.text));
        return column;
    }
    let mut column = Vec::new();
    for (other, found) in &here {
        let reason = transect_observe::health::condition(&snapshot.kinds, snapshot, other, found);
        let level = reason.as_ref().map(|r| r.level());
        let unwell = level.is_some_and(|l| l >= transect_observe::health::Level::Warning);
        if other == object || unwell {
            let said = unwell.then_some("not healthy");
            column.push(entry(other, said));
        }
    }
    column.sort_by(|a, b| a.text.cmp(&b.text));
    let label = plural_label(&api.kind, Some(&api.plural));
    let list = transect_graph::links::objects(namespace, &url_name(&api.group, &api.plural));
    column.push(KnownEntry {
        text: format!("all {label} ×{}", here.len()),
        href: Some(list),
        current: false,
    });
    column
}
