//! References by value.  A reference with `match` names its far object
//! not by name but by a value one of the far object's fields holds,
//! such as a radio's address that a paired device's status writes.
//! Reading one needs every object of the far kind, so the index here is
//! built once per reading of the cluster from the stores.  The watch of
//! such a kind keeps its objects whole instead of trimming them.

use std::collections::{BTreeSet, HashMap};
use std::sync::Arc;

use kube::core::{ApiResource, DynamicObject};

use super::{KindKnowledge, Target, path};
use transect_graph::edges::Object;

/// A far kind, by group and kind, and the field path whose value a
/// reference matches.
pub type Key = (String, String, String);

/// The objects of each matched kind, by the value of each matched field.
#[derive(Debug, Default)]
pub struct Matches {
    found: HashMap<Key, HashMap<String, Vec<Object>>>,
}

impl KindKnowledge {
    /// The far kind and field of each reference by value.
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
    /// The index of every field in `wanted` over the objects of
    /// `custom`, each list of objects by name.
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

    /// The objects of `group` and `kind` whose `field` holds `value`.
    pub fn named(&self, group: &str, kind: &str, field: &str, value: &str) -> &[Object] {
        let key = (group.to_string(), kind.to_string(), field.to_string());
        let found = self
            .found
            .get(&key)
            .and_then(|by_value| by_value.get(value));
        found.map_or(&[], Vec::as_slice)
    }
}
