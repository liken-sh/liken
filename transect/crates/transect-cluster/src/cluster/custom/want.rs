//! What to watch of each custom kind: whole or trimmed, at which
//! version, and with which printer columns and knowledge fields kept.

use std::collections::BTreeMap;
use std::sync::Arc;

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
use kube::core::ApiResource;

use super::GroupKind;
use crate::cluster::plurals::Preferred;
use crate::cluster::reflect::Holding;
use transect_resources::knowledge::Column;

/// What to watch of one kind: whole or trimmed, the resource to watch
/// it at, and the printer columns and the fields of its kind knowledge
/// that a trimmed kind keeps.  The resource is set when the kind's CRD
/// or the discovery at the start gives one, and otherwise discovery
/// resolves it.
#[derive(Clone, Debug, PartialEq)]
pub(super) struct Want {
    pub(super) holding: Holding,
    pub(super) resource: Option<ApiResource>,
    pub(super) columns: Vec<Column>,
    pub(super) fields: Vec<String>,
}

impl Want {
    /// Whole objects, at `preferred`, the version discovery prefers,
    /// when the discovery at the start found the kind.
    pub(super) fn whole(preferred: Option<ApiResource>) -> Self {
        Self {
            holding: Holding::Whole,
            resource: preferred,
            columns: Vec::new(),
            fields: Vec::new(),
        }
    }
}

/// What to watch of each kind: whole objects for the kinds in `whole`,
/// and trimmed objects for every other kind in `served`, the CRDs, and
/// in `controlled`, each keeping the fields `kept` names for it.  A kind
/// read whole is watched at the version discovery prefers, and so is a
/// trimmed kind that keeps fields, because its knowledge's paths are
/// written against that version.  `preferred` gives that version for
/// each kind the discovery at the start found, and the rest are
/// resolved one by one.
/// Any other trimmed kind is watched at its CRD's storage version.
pub(super) fn wants(
    whole: impl IntoIterator<Item = GroupKind>,
    served: &BTreeMap<GroupKind, (ApiResource, Vec<Column>)>,
    controlled: impl IntoIterator<Item = GroupKind>,
    kept: &BTreeMap<GroupKind, Vec<String>>,
    preferred: &Preferred,
) -> BTreeMap<GroupKind, Want> {
    let trimmed = |group_kind: &GroupKind| {
        let served = served.get(group_kind);
        let fields = kept.get(group_kind).cloned().unwrap_or_default();
        let resource = match fields.is_empty() {
            true => served.map(|(resource, _)| resource.clone()),
            false => preferred.get(group_kind).cloned(),
        };
        Want {
            holding: Holding::Trimmed,
            resource,
            columns: served
                .map(|(_, columns)| columns.clone())
                .unwrap_or_default(),
            fields,
        }
    };
    let mut found: BTreeMap<GroupKind, Want> = controlled
        .into_iter()
        .map(|group_kind| (group_kind.clone(), trimmed(&group_kind)))
        .collect();
    found.extend(
        served
            .keys()
            .map(|group_kind| (group_kind.clone(), trimmed(group_kind))),
    );
    found.extend(whole.into_iter().map(|group_kind| {
        let resource = preferred.get(&group_kind).cloned();
        (group_kind, Want::whole(resource))
    }));
    found
}

/// Each kind a CRD in `crds` defines, at the version etcd stores it in,
/// with the printer columns of that version.
/// A CRD with no storage version, which the API server refuses, or
/// whose storage version is not served, which no watch can read, is
/// left out.
pub(super) fn served(
    crds: &[Arc<CustomResourceDefinition>],
) -> BTreeMap<GroupKind, (ApiResource, Vec<Column>)> {
    crds.iter()
        .filter_map(|crd| {
            let spec = &crd.spec;
            let version = spec.versions.iter().find(|v| v.storage && v.served)?;
            let resource = ApiResource {
                group: spec.group.clone(),
                version: version.name.clone(),
                api_version: format!("{}/{}", spec.group, version.name),
                kind: spec.names.kind.clone(),
                plural: spec.names.plural.clone(),
            };
            let columns = Column::declared(version);
            let group_kind = (spec.group.clone(), spec.names.kind.clone());
            Some((group_kind, (resource, columns)))
        })
        .collect()
}
