//! The "Status" section of a custom resource's page, built from the
//! printer columns that `kubectl get` prints for it.

use k8s_openapi::jiff::Timestamp;

use super::super::super::snapshot::Snapshot;
use super::Found;
use transect_graph::edges::Object;
use transect_map::objects::{Section, Side};
use transect_resources::knowledge::{ColumnRead, columns};

/// The printer columns that the CRD declares for the version `found`
/// was read at. A "left out" line lists each column whose path uses a
/// JSONPath feature `transect` does not support. Returns none when the
/// kind declares no columns.
pub fn columns(
    snapshot: &Snapshot,
    object: &Object,
    found: &Found,
    now: Timestamp,
) -> Option<Section> {
    let version = found.api_version.as_deref()?.rsplit('/').next()?;
    let declared = snapshot
        .paged
        .columns
        .of(&object.group, &object.kind, version);
    if declared.is_empty() {
        return None;
    }
    let mut section = Section::new("Status", Side::Own);
    let mut unread = Vec::new();
    for read in columns::read(declared, &found.dynamic(), now) {
        match read {
            ColumnRead::Value { name, value } if value.is_empty() => {
                section = section.unset(&name);
            }
            ColumnRead::Value { name, value } => section = section.literals(&name, vec![value]),
            ColumnRead::Unread { name, part } => {
                unread.push(format!("{name}, whose path uses {part}"));
            }
        }
    }
    Some(section.text("left out", (!unread.is_empty()).then(|| unread.join("; "))))
}
