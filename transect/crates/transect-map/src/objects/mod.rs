//! The pages of arbitrary objects: a namespace's custom resources
//! grouped by kind below its workloads, the list of one kind's objects
//! in one namespace at `/namespaces/{ns}/{resource}`, and the page of
//! any single object. An object's page reads it from the stores, or,
//! for a kind that no store holds, fetches it once when the page loads.

mod board;
mod card;
mod kind;
pub mod kinds;
pub mod list;
mod page;
pub mod reads;
mod section;

use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::sync::Arc;

use kube::core::ApiResource;

pub use board::{Center, Nesting, ObjectBoard, Part};
pub use card::ObjectCard;
pub use kind::{
    ApiGroup, KindCard, KindObject, KindPage, KindRow, KindsPage, Manages, first_sentence,
};
pub use list::ListPage;
pub use page::{Condition, ObjectPage, conditions};
pub use section::{Section, Shape, Side, Table, words};
use transect_cluster::cluster::Custom;
use transect_graph::edges::Object;
use transect_observe::health::{Level, reported, resting};
use transect_resources::knowledge::{Kinds, plural_label};

/// The most objects a kind's group lists on a namespace page. Any
/// further objects appear as a count that links to the kind's list.
const MOST: usize = 12;

/// One object in a list: its name, the link to its page when it has
/// one, and the health level of its own condition when it reports one.
#[derive(Clone, Debug, PartialEq)]
pub struct Item {
    pub name: String,
    pub href: Option<String>,
    pub level: Option<Level>,
    /// Whether the object is at rest: its condition is `False` with a
    /// reason that kind knowledge marks as calm. It is drawn gray.
    pub resting: bool,
}

impl Item {
    /// The dot's CSS class: gray for an object at rest, and `None` for an
    /// object that reports no condition.
    pub fn dot_class(&self) -> Option<&'static str> {
        let level = self.level?;
        Some(match self.resting {
            true => "dot dot-gray",
            false => dot_class(level),
        })
    }
}

/// The dot's CSS class for a condition: the class for `level`, or gray
/// when `level` is `None`. Only the condition that decides the object's
/// health has a level, and it has none when the object is at rest.
pub fn condition_dot(level: Option<Level>) -> &'static str {
    match level {
        Some(level) => dot_class(level),
        None => "dot dot-gray",
    }
}

pub fn dot_class(level: Level) -> &'static str {
    match level {
        Level::Healthy => "dot dot-clear",
        Level::Notice => "dot concern-notice",
        Level::Warning => "dot concern-warning",
        Level::Failing => "dot concern-failing",
    }
}

/// One kind's objects in a namespace: the kind's plural label, the URL
/// of its list page, the first `MOST` objects by name, and how many more
/// there are.
#[derive(Clone, Debug, PartialEq)]
pub struct Group {
    pub label: String,
    pub href: String,
    pub items: Vec<Item>,
    pub more: usize,
}

impl Group {
    /// "+3 more", when the group has more objects than it lists.
    pub fn more_text(&self) -> Option<String> {
        (self.more > 0).then(|| format!("+{} more", self.more))
    }
}

pub struct Sources<'a> {
    pub custom: &'a [(ApiResource, Vec<Arc<Custom>>)],
    pub kinds: &'a Kinds,
    pub href: &'a dyn Fn(&Object) -> Option<String>,
}

impl Sources<'_> {
    /// The custom resources in `namespace`, one group per kind, sorted by
    /// API group in DNS order and then by label. Objects among
    /// `workloads` are left out, because they are drawn as workload
    /// boxes.
    pub fn groups(&self, namespace: &str, workloads: &BTreeSet<&Object>) -> Vec<Group> {
        let labels = labels(self.custom.iter().map(|(resource, _)| resource));
        let mut groups: BTreeMap<(Vec<String>, String), Group> = BTreeMap::new();
        for (resource, objects) in self.custom {
            let items = self.items(resource, objects, namespace, workloads);
            if items.is_empty() {
                continue;
            }
            let label = labels[&(resource.group.clone(), resource.kind.clone())].clone();
            let more = items.len().saturating_sub(MOST);
            let group = Group {
                href: transect_graph::links::objects(
                    namespace,
                    &transect_graph::links::kinds::url_name(&resource.group, &resource.plural),
                ),
                items: items.into_iter().take(MOST).collect(),
                more,
                label: label.clone(),
            };
            groups.insert((transect_graph::dns::key(&resource.group), label), group);
        }
        groups.into_values().collect()
    }

    fn items(
        &self,
        resource: &ApiResource,
        objects: &[Arc<Custom>],
        namespace: &str,
        workloads: &BTreeSet<&Object>,
    ) -> Vec<Item> {
        let mut items: Vec<Item> = objects
            .iter()
            .filter(|found| found.metadata.namespace.as_deref() == Some(namespace))
            .filter_map(|found| {
                let name = found.metadata.name.as_deref()?;
                let object = Object::new(&resource.group, &resource.kind, Some(namespace), name);
                (!workloads.contains(&object)).then(|| self.item(object, found))
            })
            .collect();
        items.sort_by(|a, b| a.name.cmp(&b.name));
        items
    }

    fn item(&self, object: Object, found: &Custom) -> Item {
        Item {
            level: reported(self.kinds, &object, found),
            resting: resting(self.kinds, &object, found).is_some(),
            href: (self.href)(&object),
            name: object.name,
        }
    }
}

/// The plural label of each kind, such as "Certificates", keyed by group
/// and kind. When two kinds would get the same label, each label ends
/// with the API group in parentheses.
pub fn labels<'a>(
    resources: impl IntoIterator<Item = &'a ApiResource>,
) -> HashMap<(String, String), String> {
    let resources: Vec<&ApiResource> = resources.into_iter().collect();
    let plain = |resource: &ApiResource| plural_label(&resource.kind, Some(&resource.plural));
    let mut seen: HashMap<String, usize> = HashMap::new();
    for resource in &resources {
        *seen.entry(plain(resource)).or_default() += 1;
    }
    resources
        .into_iter()
        .map(|resource| {
            let label = plain(resource);
            let label = match seen[&label] {
                1 => label,
                _ => format!("{label} ({})", resource.group),
            };
            ((resource.group.clone(), resource.kind.clone()), label)
        })
        .collect()
}

#[cfg(test)]
mod tests;
