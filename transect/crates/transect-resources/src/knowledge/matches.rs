//! References by value.  A reference with `match` finds its target by
//! comparing a value to a field of the target, instead of by the
//! target's name.  For example, a paired device's status holds a radio's
//! address, and the target is the radio whose address field has that
//! value.  `Matches` indexes every object of the target kind by that
//! field.  `transect-cluster` watches such a kind whole, because a
//! trimmed watch drops the field.

use std::collections::{BTreeSet, HashMap};
use std::sync::Arc;

use kube::core::{ApiResource, DynamicObject};

use super::{KindKnowledge, Target, path};
use transect_graph::edges::Object;

/// The group and kind of the target objects, and the path of the field
/// that a reference matches.
pub type Key = (String, String, String);

/// The target objects of each `Key`, grouped by the value of the field.
#[derive(Debug, Default)]
pub struct Matches {
    found: HashMap<Key, HashMap<String, Vec<Object>>>,
}

impl KindKnowledge {
    /// The `Key` of each of this kind's references that matches by value.
    pub fn matched(&self) -> impl Iterator<Item = Key> + '_ {
        self.references.iter().filter_map(|reference| {
            let field = reference.matched.clone()?;
            let Target::Object { kind, group } = &reference.target else {
                return None;
            };
            Some((group.clone().unwrap_or_default(), kind.clone(), field))
        })
    }
}

impl Matches {
    /// Indexes the objects in `custom` by the field of each `Key` in
    /// `wanted`.  Each list of objects is sorted.
    pub fn build(
        wanted: impl IntoIterator<Item = Key>,
        custom: &[(ApiResource, Vec<Arc<DynamicObject>>)],
    ) -> Self {
        let wanted: BTreeSet<Key> = wanted.into_iter().collect();
        let mut found: HashMap<Key, HashMap<String, Vec<Object>>> = HashMap::new();
        for (resource, objects) in custom {
            let fields = wanted
                .iter()
                .filter(|(group, kind, _)| *group == resource.group && *kind == resource.kind);
            for key in fields {
                let by_value = found.entry(key.clone()).or_default();
                for one in objects {
                    let meta = &one.metadata;
                    let Some(name) = meta.name.as_deref() else {
                        continue;
                    };
                    let namespace = meta.namespace.as_deref();
                    let object = Object::new(&resource.group, &resource.kind, namespace, name);
                    for value in path::read_of(&object, &one.data, &key.2) {
                        by_value.entry(value).or_default().push(object.clone());
                    }
                }
            }
        }
        for objects in found.values_mut().flat_map(HashMap::values_mut) {
            objects.sort();
        }
        Self { found }
    }

    /// The objects of `group` and `kind` whose `field` has `value`.
    pub fn named(&self, group: &str, kind: &str, field: &str, value: &str) -> &[Object] {
        let key = (group.to_string(), kind.to_string(), field.to_string());
        let found = self
            .found
            .get(&key)
            .and_then(|by_value| by_value.get(value));
        found.map_or(&[], Vec::as_slice)
    }
}
