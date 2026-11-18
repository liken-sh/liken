//! A workload's vulnerabilities tab, drawn beside the workload's card.

use k8s_openapi::jiff::Timestamp;

use super::super::model::Model;
use super::{WorkloadAt, read_workloads, workload_card};
use transect_graph::edges::Shown;
use transect_map::map::vulnerabilities::WorkloadVulnerabilitiesPage;
use transect_observe::usage;
use transect_resources::layers::Layers;
use transect_supply::vulnerabilities::Shown as Severities;

/// The vulnerabilities tab of the workload `at`, or `None` when the
/// workload does not exist or no scanner report covers it.
pub fn vulnerabilities_page(
    model: &Model,
    layers: &Layers,
    usage: &usage::Reading,
    at: WorkloadAt,
    severities: &Severities,
    now: Timestamp,
) -> Option<WorkloadVulnerabilitiesPage> {
    let WorkloadAt {
        namespace,
        resource,
        name,
    } = at;
    let shown = Shown::default();
    let build = |map: &transect_map::map::ClusterMap,
                 contents: transect_map::map::WorkloadContents| {
        let layer = map.layer_of(namespace)?.to_owned();
        let object = transect_map::map::find(&contents.owners, contents.plurals, resource, name)?;
        let workload_path = transect_graph::links::page(object, contents.plurals)?;
        let vulnerabilities = &model.vulnerabilities;
        let drawn = transect_map::map::vulnerabilities::drawn(
            vulnerabilities,
            object,
            &workload_path,
            severities,
            now,
        )?;
        Some(WorkloadVulnerabilitiesPage {
            namespace: namespace.into(),
            layer,
            name: name.into(),
            access: model.serves(object),
            choices: drawn.choices,
            scanned: drawn.scanned,
            stale: drawn.stale,
            query: severities.query(),
            card: workload_card(object, &contents),
            workload_path,
        })
    };
    read_workloads(model, layers, usage, namespace, &shown, build, now)
}
