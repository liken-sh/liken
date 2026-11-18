//! The page of a custom resource whose kind has a `KindKnowledge`. It
//! reads the resource the way a workload's page does, so the two pages
//! show the same card.

use super::super::model::Model;
use super::WorkloadAt;
use super::object::card;
use super::read_workloads;
use k8s_openapi::jiff::Timestamp;
use transect_graph::edges::{Object, Shown};
use transect_map::map::{
    Board, EdgeRow, KnownEntry, KnownPage, every_chip, has_page, known_chips, known_through,
    workload_card,
};
use transect_map::objects::{self, Center, ObjectCard};
use transect_observe::health::Deciding;
use transect_observe::usage;
use transect_resources::knowledge::plural_label;
use transect_resources::layers::Layers;

/// The page for the custom resource `namespace/name` whose kind is
/// served under the URL segment `resource`, or `None` when no watched
/// resource of a kind with a `KindKnowledge` matches.
pub fn known_page(
    model: &Model,
    layers: &Layers,
    usage: &usage::Reading,
    at: WorkloadAt,
    shown: &Shown,
    now: Timestamp,
) -> Option<KnownPage> {
    let WorkloadAt {
        namespace,
        resource,
        name,
    } = at;
    let snapshot = &model.snapshot;
    let plurals = &snapshot.plurals;
    let (object, found) = snapshot.custom.iter().find_map(|(api, objects)| {
        snapshot.kinds.of(&api.group, &api.kind)?;
        let found = objects.iter().find(|found| {
            let meta = &found.metadata;
            meta.namespace.as_deref() == Some(namespace) && meta.name.as_deref() == Some(name)
        })?;
        let object = Object::new(&api.group, &api.kind, Some(namespace), name);
        let served = transect_graph::links::kinds::workload(&object, plurals);
        (served.as_deref() == Some(resource)).then_some((object, found))
    })?;
    let knowledge = snapshot.kinds.of_object(&object)?;
    let page = read_workloads(
        model,
        layers,
        usage,
        namespace,
        shown,
        |map, contents| {
            let layer = map.layer_of(namespace)?.to_owned();
            let path = transect_graph::links::page(&object, plurals)?;
            let workload = contents.owners.contains(&&object);
            let level = match workload {
                true => contents.by_workload.get(&object).map(|health| health.level),
                false => transect_observe::health::condition(&snapshot.kinds, &object, found)
                    .map(|r| r.level()),
            };
            let siblings = snapshot.custom_objects().into_iter().filter(|other| {
                (&other.group, &other.kind, other.namespace.as_deref())
                    == (&object.group, &object.kind, Some(namespace))
            });
            let mut column: Vec<KnownEntry> = siblings
                .map(|other| KnownEntry {
                    text: transect_graph::edges::kind_and_name(&other.kind, &other.name),
                    href: transect_graph::links::page(&other, plurals),
                    current: other == object,
                })
                .collect();
            column.sort_by(|a, b| a.text.cmp(&b.text));
            let own = plural_label(
                &object.kind,
                plurals
                    .get(&(object.group.clone(), object.kind.clone()))
                    .map(String::as_str),
            );
            let counts = snapshot
                .counts(namespace)
                .into_iter()
                .filter(|c| c.label != own);
            let page_of = |other: &Object| {
                has_page(other, &contents)
                    .then(|| transect_graph::links::page(other, plurals))
                    .flatten()
            };
            let board = Board {
                object: &object,
                knowledge,
                layer,
                path,
                reached: snapshot.relations_reached(&object, contents.graph),
                column,
                counts: counts.collect(),
                chips: known_chips(&found.data, &Deciding::of(&snapshot.kinds, &object)),
                level: level.unwrap_or_default(),
                resting: transect_observe::health::resting(&snapshot.kinds, &object, found),
                plurals,
                page: &page_of,
                ground: &|title| super::ground_link(model, title),
                device: &|device| snapshot.devices.named(device, &snapshot.resources.devices),
            };
            // The card is on the object's own page, so its title does not
            // link.
            let card = workload.then(|| workload_card(&object, &contents).with_href(None));
            let known = known_through(&object, &contents)?;
            let deciding = Deciding::of(&snapshot.kinds, &object);
            let resting = transect_observe::health::resting(&snapshot.kinds, &object, found);
            let reported = transect_observe::health::reported(&snapshot.kinds, &object, found);
            let object_card = ObjectCard {
                known: Some(known),
                ..card::card(
                    snapshot,
                    &object,
                    Some(&found.metadata),
                    contents.graph,
                    reported,
                    resting.is_some(),
                    now,
                )
            };
            let center = Center {
                kind: object.kind.clone(),
                name: object.name.clone(),
                level: reported,
                resting,
                note: None,
                chips: every_chip(&found.data, &deciding),
                messages: super::object::messages(&objects::conditions(&found.data, &deciding)),
            };
            let found = super::object::Found::of(found);
            let sections = super::object::sections(model, &object, &found, contents.graph, now);
            let nesting = super::object::part_of::nesting(snapshot, contents.graph, &object);
            let page = KnownPage::build(board, card, object_card).with_board(center, sections);
            Some(page.with_nesting(nesting))
        },
        now,
    )?;
    let mut page = page;
    if super::has_access(model, namespace, resource, name) {
        page.tabs = page.tabs.map(|tabs| tabs.with_access(&page.path));
    }
    // The edge kinds affect only the diagram, so a page without a
    // diagram has no edge kinds in its masthead.
    if !page.diagram() {
        return Some(page);
    }
    let row = EdgeRow::build(&snapshot.offered(), shown, &page.path);
    Some(page.with_edge_row(row))
}
