//! The Controls section of a workload's page: one table for each kind of
//! custom resource that the workload reconciles, found from the
//! `ControlledBy` edges that point at the workload.

use k8s_openapi::jiff::Timestamp;
use std::collections::BTreeMap;

use super::super::model::Model;
use super::super::snapshot::Snapshot;
use super::kinds::table;
use transect_graph::edges::{Endpoint, Kind, Object, Provenance};
use transect_map::objects::{self, Manages};

/// The workload `name` in `namespace` whose resource plural is
/// `resource`.
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

/// The objects controlled by `workload`, grouped by group and kind, with
/// the provenance of the first edge found for each kind.
fn controlled(
    model: &Model,
    workload: &Object,
) -> BTreeMap<(String, String), (Vec<Object>, Provenance)> {
    let graph = model.graph();
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

/// The sentence under a Controls table that says how transect matched
/// its objects to the workload.
pub fn rule(provenance: &Provenance) -> String {
    match provenance {
        Provenance::Kinds { knowledge, .. } => {
            format!(
                "Matched by image: the KindKnowledge {knowledge} lists this workload's image as its controller."
            )
        }
        Provenance::Package { crd, labels } => format!(
            "A guess from labels: this workload and the CRD {crd} both have {}.",
            labels.join(", ")
        ),
        _ => "Matched by the accounts that RBAC allows to write their status, and by the account that wrote it.".into(),
    }
}

/// One table for each custom kind that `workload` controls. A kind that
/// the snapshot has no store for is left out.
pub fn manages(model: &Model, workload: &Object, now: Timestamp) -> Vec<Manages> {
    let snapshot = &model.snapshot;
    let labels = objects::labels(snapshot.custom.iter().map(|(api, _)| api));
    let by_kind = controlled(model, workload);
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
