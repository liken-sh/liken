//! `/kinds` lists the custom kinds, and `/kinds/{plural.group}` lists
//! one kind's objects in every namespace. Both pages read the printer
//! columns, which the watch keeps also for a kind whose objects it
//! trims.

use std::collections::BTreeMap;
use std::sync::Arc;

use k8s_openapi::jiff::Timestamp;
use kube::core::{ApiResource, DynamicObject};

use super::super::model::Model;
use super::super::snapshot::Snapshot;
use transect_graph::edges::{Endpoint, Kind, Object, Shown};
use transect_graph::links::Reference;
use transect_map::map::KnownEntry;
use transect_map::objects::{
    self, ApiGroup, Item, KindCard, KindObject, KindPage, KindRow, KindsPage, first_sentence,
};
use transect_observe::health::{Level, reported};
use transect_resources::knowledge::{Column, ColumnRead, columns};

/// Every custom kind that `transect` watches, grouped by API group. The
/// kinds with no objects go in `empty`, apart from the others.
pub fn kinds_page(model: &Model) -> KindsPage {
    let snapshot = &model.snapshot;
    let graph = model.graph(&Shown::default());
    let labels = objects::labels(snapshot.custom.iter().map(|(api, _)| api));
    let mut groups: BTreeMap<String, Vec<KindRow>> = BTreeMap::new();
    let mut empty: BTreeMap<String, Vec<KindRow>> = BTreeMap::new();
    for (resource, found) in &snapshot.custom {
        let kind = Object::new(&resource.group, &resource.kind, None, "");
        let mut controllers: Vec<Reference> = Vec::new();
        let (mut unhealthy, mut worst) = (0, None);
        for (object, found) in found
            .iter()
            .filter_map(|found| Some((as_object(resource, found)?, found)))
        {
            let level = reported(&snapshot.kinds, snapshot, &object, found);
            let resting =
                transect_observe::health::resting(&snapshot.kinds, snapshot, &object, found)
                    .is_some();
            if let Some(level) = level.filter(|level| *level != Level::Healthy && !resting) {
                unhealthy += 1;
                worst = worst.max(Some(level));
            }
            let end = Endpoint::Object(object);
            for controller in graph.leaving(Kind::ControlledBy, &end) {
                let reference = super::object::refer(snapshot, controller, None);
                if !controllers.contains(&reference) {
                    controllers.push(reference);
                }
            }
        }
        let summary = snapshot.definitions.of(&kind);
        let row = KindRow {
            label: labels[&(resource.group.clone(), resource.kind.clone())].clone(),
            href: transect_graph::links::kind(&resource.group, &resource.plural),
            count: found.len(),
            unhealthy,
            worst,
            controllers,
            summary: summary.map(|line| first_sentence(&line.text)),
        };
        let into = match found.is_empty() {
            true => &mut empty,
            false => &mut groups,
        };
        into.entry(resource.group.clone()).or_default().push(row);
    }
    let sorted = |groups: BTreeMap<String, Vec<KindRow>>| {
        let mut groups: Vec<ApiGroup> = groups
            .into_iter()
            .map(|(name, mut kinds)| {
                kinds.sort_by(|a, b| a.label.cmp(&b.label));
                ApiGroup::new(name, kinds)
            })
            .collect();
        groups.sort_by(|a, b| transect_graph::dns::order(&a.name, &b.name));
        groups
    };
    KindsPage {
        groups: sorted(groups),
        empty: sorted(empty),
    }
}

/// `None` for a kind that `transect` does not watch.
pub fn kind_page(model: &Model, resource: &str, now: Timestamp) -> Option<KindPage> {
    let snapshot = &model.snapshot;
    let (api, stored) = snapshot.custom_at(resource)?;
    let (columns, rows) = table(snapshot, api, stored, |_| true, now);
    let labels = objects::labels(snapshot.custom.iter().map(|(api, _)| api));
    let siblings = snapshot.custom.iter().filter(|(other, found)| {
        other.group == api.group && (!found.is_empty() || other.kind == api.kind)
    });
    let mut kind_links: Vec<KnownEntry> = siblings
        .map(|(other, found)| KnownEntry {
            text: format!(
                "{} ×{}",
                labels[&(other.group.clone(), other.kind.clone())],
                found.len()
            ),
            href: Some(transect_graph::links::kind(&other.group, &other.plural)),
            current: other.kind == api.kind,
        })
        .collect();
    kind_links.sort_by(|a, b| a.text.cmp(&b.text));
    let page = KindPage {
        label: labels[&(api.group.clone(), api.kind.clone())].clone(),
        path: format!("/kinds/{resource}"),
        kind_links,
        columns,
        rows,
        scans: model
            .vulnerabilities
            .scans(&api.group, &api.kind, &snapshot.plurals),
        card: kind_card(model, api, stored, |_| true),
    };
    Some(page)
}

/// The card of `api`'s kind, for the objects in `found` that `keep`
/// accepts: how many there are, how many are not healthy, and which
/// workloads control them.
pub fn kind_card(
    model: &Model,
    api: &ApiResource,
    found: &[Arc<DynamicObject>],
    keep: impl Fn(&Object) -> bool,
) -> KindCard {
    let snapshot = &model.snapshot;
    let labels = objects::labels(snapshot.custom.iter().map(|(api, _)| api));
    let kind = Object::new(&api.group, &api.kind, None, "");
    let graph = model.graph(&Shown::default());
    let (mut controllers, mut rule) = (Vec::new(), None);
    let (mut count, mut unhealthy, mut worst) = (0, 0, None);
    let kept = found.iter().filter_map(|found| {
        let object = as_object(api, found).filter(|object| keep(object))?;
        Some((object, found))
    });
    for (object, found) in kept {
        count += 1;
        let level = reported(&snapshot.kinds, snapshot, &object, found);
        let resting =
            transect_observe::health::resting(&snapshot.kinds, snapshot, &object, found).is_some();
        if let Some(level) = level.filter(|level| *level != Level::Healthy && !resting) {
            unhealthy += 1;
            worst = worst.max(Some(level));
        }
        let end = Endpoint::Object(object);
        for edge in graph.edges_leaving(Kind::ControlledBy, &end) {
            let reference = super::object::refer(snapshot, &edge.to, None);
            if !controllers.contains(&reference) {
                controllers.push(reference);
            }
            rule.get_or_insert_with(|| super::manages::rule(&edge.provenance));
        }
    }
    KindCard {
        label: labels[&(api.group.clone(), api.kind.clone())].clone(),
        group: api.group.clone(),
        count,
        unhealthy,
        worst,
        why: snapshot.definitions.of(&kind),
        controllers,
        rule,
    }
}

/// The column names, and a row for each object in `found` that `keep`
/// accepts. A printer column is left out when `transect` cannot read its
/// JSONPath, or when `kubectl get` prints it only with `-o wide`.
pub fn table(
    snapshot: &Snapshot,
    api: &ApiResource,
    found: &[Arc<DynamicObject>],
    keep: impl Fn(&Object) -> bool,
    now: Timestamp,
) -> (Vec<String>, Vec<KindObject>) {
    let declared = snapshot
        .paged
        .columns
        .of(&api.group, &api.kind, &api.version);
    let declared: Vec<Column> = declared
        .iter()
        .filter(|column| column.priority == 0 && column.steps.is_ok())
        .cloned()
        .collect();
    let pages = snapshot.pages();
    let rows = found.iter().filter_map(|found| {
        let object = as_object(api, found).filter(|object| keep(object))?;
        let values = columns::read(&declared, found, now).into_iter();
        let values = values.filter_map(|read| match read {
            ColumnRead::Value { value, .. } => Some(value),
            ColumnRead::Unread { .. } => None,
        });
        let deciding = transect_observe::health::Deciding::of(&snapshot.kinds, &object);
        let condition = transect_map::map::known_chips(&found.data, &deciding);
        Some(KindObject {
            condition: condition.into_iter().next().map(|chip| chip.text),
            item: Item {
                level: reported(&snapshot.kinds, snapshot, &object, found),
                resting: transect_observe::health::resting(
                    &snapshot.kinds,
                    snapshot,
                    &object,
                    found,
                )
                .is_some(),
                href: pages.href(&object),
                name: object.name.clone(),
            },
            namespace: object.namespace.clone(),
            values: values.collect(),
        })
    });
    let mut rows: Vec<KindObject> = rows.collect();
    rows.sort_by(|a, b| (&a.namespace, &a.item.name).cmp(&(&b.namespace, &b.item.name)));
    let names = declared.into_iter().map(|column| column.name);
    (names.collect(), rows)
}

fn as_object(resource: &ApiResource, found: &DynamicObject) -> Option<Object> {
    let meta = &found.metadata;
    let name = meta.name.as_deref()?;
    Some(Object::new(
        &resource.group,
        &resource.kind,
        meta.namespace.as_deref(),
        name,
    ))
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::super::fixture;
    use super::*;

    #[rstest]
    fn folds_the_kinds_with_no_objects_and_counts_the_unhealthy() {
        let mut model = fixture::model();
        let widgets = ApiResource {
            group: "widgets.example.test".into(),
            version: "v1".into(),
            api_version: "widgets.example.test/v1".into(),
            kind: "Widget".into(),
            plural: "widgets".into(),
        };
        model.snapshot.custom.push((widgets, Vec::new()));
        let page = kinds_page(&model);
        let shown: Vec<(&str, usize, usize)> = page
            .groups
            .iter()
            .flat_map(|group| &group.kinds)
            .map(|kind| (kind.label.as_str(), kind.count, kind.unhealthy))
            .collect();
        assert_eq!(
            shown,
            [
                ("Certificates", 1, 0),
                ("ClusterIssuers", 1, 0),
                ("HelmReleases", 1, 0),
                ("Kustomizations", 4, 1),
                ("Buckets", 1, 0),
                ("GitRepositories", 2, 0),
                ("HelmRepositories", 1, 0),
                ("OCIRepositories", 1, 0),
                ("Machines", 1, 0),
                ("Caches", 1, 0),
                ("Gizmos", 2, 1),
                ("Ledgers", 2, 0),
                ("Promotions", 2, 0),
                ("Gates", 3, 0),
                ("Handsets", 1, 0),
                ("Radios", 1, 0),
                ("ImageReports", 4, 0),
                ("Bins", 2, 1),
                ("Rooms", 2, 0),
                ("Shelves", 2, 0),
                ("Thermostats", 1, 0),
                ("Vents", 2, 1),
                ("Tills", 1, 0)
            ]
        );
        assert_eq!(page.empty_count(), 1);
        assert_eq!(page.empty[0].kinds[0].label, "Widgets");
    }
}
