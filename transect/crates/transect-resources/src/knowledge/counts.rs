//! Counts the objects of each kind with kind knowledge that are not
//! workloads, such as remotes or keymaps. The map draws no box for such
//! an object, so the namespace shows one count for each kind, such as
//! "Remotes ×3", that links to the list of those objects in the
//! namespace.

use std::collections::{BTreeMap, BTreeSet};

use super::{Kinds, plural_label};
use transect_graph::edges::{Object, Plurals};

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Count {
    /// The kind's plural, as the count shows it, such as "Remotes".
    pub label: String,
    pub count: usize,
    /// The summary from the kind knowledge, shown as the chip's title.
    pub summary: Option<String>,
    /// The link to the list of the kind's objects in the namespace.
    pub href: Option<String>,
}

/// One count for each known kind among the `objects` in `namespace`,
/// sorted by API group in DNS order and then by label. Objects in
/// `workloads` are not counted, because the map draws them as boxes.
pub fn counts<'a>(
    kinds: &Kinds,
    objects: impl IntoIterator<Item = &'a Object>,
    workloads: &BTreeSet<&Object>,
    namespace: &str,
    plurals: &Plurals,
) -> Vec<Count> {
    let mut found: BTreeMap<(Vec<String>, String), Count> = BTreeMap::new();
    let here = objects
        .into_iter()
        .filter(|object| object.namespace.as_deref() == Some(namespace))
        .filter(|object| !workloads.contains(object));
    for object in here {
        let Some(knowledge) = kinds.of_object(object) else {
            continue;
        };
        let plural = plurals.get(&(object.group.clone(), object.kind.clone()));
        let label = plural_label(&object.kind, plural.map(String::as_str));
        let resource = transect_graph::links::kinds::workload(object, plurals);
        let key = (transect_graph::dns::key(&object.group), label.clone());
        let count = found.entry(key).or_insert_with(|| Count {
            label,
            count: 0,
            summary: knowledge.summary.clone(),
            href: resource.map(|resource| transect_graph::links::objects(namespace, &resource)),
        });
        count.count += 1;
    }
    found.into_values().collect()
}

impl Count {
    /// "Remotes ×3".
    pub fn text(&self) -> String {
        format!("{} ×{}", self.label, self.count)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::{knowledge, plurals};
    use super::*;

    fn widget(namespace: &str, name: &str) -> Object {
        Object::new("widgets.example.com", "Widget", Some(namespace), name)
    }

    #[rstest]
    fn counts_the_objects_of_a_known_kind_that_run_nothing() {
        let kinds = Kinds::resolve(
            &[knowledge(
                "widgets.widgets.example.com",
                "{summary: Turns.}",
            )],
            &plurals(),
            &[],
        );
        let objects = [
            widget("shop", "b"),
            widget("shop", "a"),
            widget("shop", "runs"),
            widget("blog", "c"),
            Object::new("gadgets.example.com", "Gadget", Some("shop"), "d"),
        ];
        let runs = widget("shop", "runs");
        let workloads = BTreeSet::from([&runs]);
        let found = counts(&kinds, &objects, &workloads, "shop", &plurals());
        let texts: Vec<(String, Option<String>, Option<String>)> = found
            .iter()
            .map(|count| (count.text(), count.summary.clone(), count.href.clone()))
            .collect();
        assert_eq!(
            texts,
            vec![(
                "Widgets ×2".into(),
                Some("Turns.".into()),
                Some("/namespaces/shop/widgets.widgets.example.com".into())
            )]
        );
    }
}
