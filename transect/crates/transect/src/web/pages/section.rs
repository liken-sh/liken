//! The section view of one workload: its workloads, Services,
//! Ingresses, storage, and devices drawn in columns, with its card
//! beside them.

use k8s_openapi::jiff::Timestamp;

use super::{WorkloadAt, has_page, read_workloads, workload_card};
use crate::web::model::Model;
use transect_graph::edges::{Object, Shown};
use transect_map::map::{EdgeRow, SectionContents, SectionPage};
use transect_observe::usage;
use transect_resources::layers::Layers;

/// The section view of the workload `namespace/name` of the kind `resource`
/// names, with its card beside it, or `None` when the namespace holds
/// no such workload.
pub fn section_page(
    model: &Model,
    layers: &Layers,
    usage: &usage::Reading,
    at: WorkloadAt,
    shown: &Shown,
    now: Timestamp,
) -> Option<SectionPage> {
    let WorkloadAt {
        namespace,
        resource,
        name,
    } = at;
    let snapshot = &model.snapshot;
    let machines = snapshot.machines(usage);
    let page = read_workloads(
        model,
        layers,
        usage,
        namespace,
        shown,
        |map, contents| {
            let pages = |object: &Object| has_page(object, &contents);
            let drawn = SectionContents {
                workloads: contents.workloads,
                owners: contents.owners.clone(),
                plurals: contents.plurals,
                graph: contents.graph,
                services: contents.services,
                ingresses: contents.ingresses,
                endpoints: contents.endpoints,
                issuers: contents.issuers,
                storage: contents.storage,
                machines: &machines,
                shown,
                intents: contents.intents,
                has_page: &pages,
                traffic: snapshot.traffic(),
                device_title: &|device| snapshot.devices.title(device, &snapshot.resources.devices),
            };
            let card = |object: &Object| workload_card(object, &contents);
            SectionPage::build(map, namespace, resource, name, drawn, card)
        },
        now,
    )?;
    let outsides = &snapshot.outsides;
    let page = page.with_ground_pages(|name| {
        let title = outsides.box_of(name).to_string();
        (title, outsides.page_of(name), outsides.link_of(name))
    });
    let row = EdgeRow::build(&snapshot.offered(), shown, &page.path);
    let access = super::has_access(model, namespace, resource, name);
    Some(page.with_edge_row(row).with_access(access))
}
