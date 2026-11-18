//! Decides how to watch each custom kind: whole or trimmed objects, at
//! which API version, and which printer columns and fields a trimmed
//! object keeps.

use std::collections::BTreeMap;
use std::sync::Arc;

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::CustomResourceDefinition;
use kube::core::ApiResource;

use super::GroupKind;
use crate::cluster::plurals::Preferred;
use crate::cluster::reflect::Stored;
use transect_resources::knowledge::Column;

/// How to watch one custom kind. `resource` is `None` when neither the
/// kind's CRD nor API discovery at startup gave a version, and the
/// watch looks one up when it starts. `columns` and `fields` apply only
/// to a trimmed kind: the printer columns to keep, and the fields that
/// kind knowledge reads.
#[derive(Clone, Debug, PartialEq)]
pub(super) struct Want {
    pub(super) stored: Stored,
    pub(super) resource: Option<ApiResource>,
    pub(super) columns: Vec<Column>,
    pub(super) fields: Vec<String>,
}

impl Want {
    /// Whole objects at `preferred`, the version that API discovery
    /// prefers. It is `None` when discovery at startup did not find the
    /// kind.
    pub(super) fn whole(preferred: Option<ApiResource>) -> Self {
        Self {
            stored: Stored::Whole,
            resource: preferred,
            columns: Vec::new(),
            fields: Vec::new(),
        }
    }
}

/// Picks how to watch each kind. Kinds in `whole` are watched whole.
/// Every other kind in `served` (from the CRDs) or in `controlled`
/// (kinds whose status a controller may write) is watched trimmed, and
/// keeps the fields that `kept` lists for it.
///
/// A whole kind, and a trimmed kind that keeps fields, is watched at the
/// version that API discovery prefers, because the field paths in kind
/// knowledge are written against that version. Any other trimmed kind
/// is watched at its CRD's storage version.
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
            stored: Stored::Trimmed,
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

/// Returns each kind that a CRD in `crds` defines, at its storage
/// version, with that version's printer columns. A CRD whose storage
/// version is not served is left out, because no watch can read it.
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
