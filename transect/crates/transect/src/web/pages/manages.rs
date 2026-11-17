//! A workload's Controls section: the custom resources its controller
//! reconciles, read in reverse from the controlled-by edges, one table
//! per kind, each read from its kind's store.

use k8s_openapi::jiff::Timestamp;
use std::collections::BTreeMap;

use super::super::model::Model;
use super::super::snapshot::Snapshot;
use super::kinds::table;
use transect_graph::edges::{Endpoint, Kind, Object, Provenance, Shown};
use transect_map::objects::{self, Manages};

/// The workload `namespace/name` of the kind `resource` names.
pub fn workload_at(
    snapshot: &Snapshot,
    namespace: &str,
    resource: &str,
    name: &str,
) -> Option<Object> {
    let plurals = &snapshot.plurals;
    let mut workloads = snapshot.ownership.workloads(namespace);
    let found = workloads.find(|workload| {
        workload.name == name
            && transect_graph::links::kinds::workload(workload, plurals).as_deref()
                == Some(resource)
    });
    found.cloned()
}

/// The objects `workload`'s controller reconciles, by kind, each kind
/// with how its first object was found to be controlled by it.
fn controlled(
    model: &Model,
    workload: &Object,
    shown: &Shown,
) -> BTreeMap<(String, String), (Vec<Object>, Provenance)> {
    let graph = model.graph(shown);
    let end = Endpoint::Object(workload.clone());
    let mut by_kind: BTreeMap<(String, String), (Vec<Object>, Provenance)> = BTreeMap::new();
    for edge in graph.edges_reaching(Kind::ControlledBy, &end) {
        if let Endpoint::Object(object) = &edge.from {
            let kind = (object.group.clone(), object.kind.clone());
            let entry = by_kind
                .entry(kind)
                .or_insert_with(|| (Vec::new(), edge.provenance.clone()));
            entry.0.push(object.clone());
        }
    }
    by_kind
}

/// How a Controls table's objects were found to be the workload's: the
/// rule of controller attribution that matched, in words.
pub fn rule(provenance: &Provenance) -> String {
    match provenance {
        Provenance::Kinds { knowledge, .. } => {
            format!(
                "Matched by image: the KindKnowledge {knowledge} names this workload's image as its controller."
            )
        }
        Provenance::Package { crd, labels } => format!(
            "A guess from labels: this workload and the CRD {crd} both have {}.",
            labels.join(", ")
        ),
        _ => "Matched by who RBAC lets write their status, and who wrote it.".into(),
    }
}

/// One table per kind of the custom resources `workload` controls.
pub fn manages(model: &Model, workload: &Object, shown: &Shown, now: Timestamp) -> Vec<Manages> {
    let snapshot = &model.snapshot;
    let labels = objects::labels(snapshot.custom.iter().map(|(api, _)| api));
    let by_kind = controlled(model, workload, shown);
    by_kind
        .into_iter()
        .filter_map(|(kind, (controlled, provenance))| {
            let (api, stored) = snapshot.custom_kind(&kind.0, &kind.1)?;
            let (columns, rows) = table(
                snapshot,
                api,
                stored,
                |object| controlled.contains(object),
                now,
            );
            Some(Manages {
                label: labels[&kind].clone(),
                href: transect_graph::links::kind(&api.group, &api.plural),
                rule: rule(&provenance),
                columns,
                rows,
            })
        })
        .collect()
}
