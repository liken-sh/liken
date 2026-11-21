//! The ends of a reference of a custom resource, for a reading or a
//! health `through` the reference. The snapshot has the relations of
//! each custom resource and the store of each custom kind, so it answers
//! `ReferenceEnds` for every page.

use kube::core::DynamicObject;

use super::Snapshot;
use transect_graph::edges::Object;
use transect_resources::knowledge::{self, Followed, ReferenceEnds};

impl ReferenceEnds for Snapshot {
    fn ends(&self, object: &Object, path: &str) -> Vec<Followed<'_>> {
        let relations = self.relations.get(object).map(Vec::as_slice);
        let stored = |named: &Object| -> Option<Option<&DynamicObject>> {
            let (_, objects) = self.custom_kind(&named.group, &named.kind)?;
            let found = objects.iter().find(|found| {
                let meta = &found.metadata;
                meta.name.as_deref() == Some(&named.name) && meta.namespace == named.namespace
            });
            Some(found.map(AsRef::as_ref))
        };
        knowledge::follow(relations.unwrap_or_default(), path, stored)
    }
}
