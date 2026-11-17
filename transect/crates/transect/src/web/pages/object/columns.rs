//! The printer columns of a custom resource, as `kubectl get` prints
//! them, for its page.

use k8s_openapi::jiff::Timestamp;

use super::super::super::snapshot::Snapshot;
use super::Found;
use transect_graph::edges::Object;
use transect_map::objects::{Section, Side};
use transect_resources::knowledge::{ColumnRead, columns};

/// The columns its CRD declares for the version `found` was read at,
/// with a line naming each column whose path `transect` does not read,
/// or none for a kind that declares none.
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
