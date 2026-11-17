//! Every object of every kind: a namespace's custom resources grouped
//! by kind below its workloads, one kind's objects in one namespace at
//! `/namespaces/{ns}/{resource}`, and the page of any one object, read
//! from the stores, or for a kind no store holds, read once when its
//! page loads.  Each name is an `object_link`, and each object that
//! reports its condition has a dot.

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

/// The most names a kind's group shows on a namespace page.  The rest
/// show as a count that opens the kind's list.
const MOST: usize = 12;

/// One object in a list: its name, its page when it has one, and the
/// level its own condition gives it, when it reports one.
#[derive(Clone, Debug, PartialEq)]
pub struct Item {
    pub name: String,
    pub href: Option<String>,
    pub level: Option<Level>,
    /// Whether its condition reports it at rest.  An object at rest
    /// draws gray.
    pub resting: bool,
}

impl Item {
    /// The dot's class, as a search row's dot is, gray for an object at
    /// rest, or none for an object that reports no condition.
    pub fn dot_class(&self) -> Option<&'static str> {
        let level = self.level?;
        Some(match self.resting {
            true => "dot dot-gray",
            false => dot_class(level),
        })
    }
}

/// A condition's dot: the dot of `level`, or gray when it is none.  It
/// is none for every condition except the one that decides health, and
/// for that one when it is at rest.
pub fn condition_dot(level: Option<Level>) -> &'static str {
    match level {
        Some(level) => dot_class(level),
        None => "dot dot-gray",
    }
}

/// A dot's class for `level`: clear when healthy, warm otherwise.
pub fn dot_class(level: Level) -> &'static str {
    match level {
        Level::Healthy => "dot dot-clear",
        Level::Notice => "dot concern-notice",
        Level::Warning => "dot concern-warning",
        Level::Failing => "dot concern-failing",
    }
}

/// One kind's objects in a namespace: its plural, its list's URL, the
/// first of its objects by name, and how many more there are.
#[derive(Clone, Debug, PartialEq)]
pub struct Group {
    pub label: String,
    pub href: String,
    pub items: Vec<Item>,
    pub more: usize,
}

impl Group {
    /// "+3 more", when the group has more objects than it shows.
    pub fn more_text(&self) -> Option<String> {
        (self.more > 0).then(|| format!("+{} more", self.more))
    }
}

/// What the lists read: the custom kinds' objects, their knowledge,
/// and the page of any object that has one.
pub struct Sources<'a> {
    pub custom: &'a [(ApiResource, Vec<Arc<Custom>>)],
    pub kinds: &'a Kinds,
    pub href: &'a dyn Fn(&Object) -> Option<String>,
}

impl Sources<'_> {
    /// The custom resources in `namespace` that are not among
    /// `workloads`, which draw boxes of their own, one group per kind,
    /// sorted by API group in DNS order and then by label.
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

    /// The objects of `resource`'s kind in `namespace`, by name, except
    /// those among `workloads`.
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

/// Each kind's label: its plural as a reader says it, "Certificates",
/// with its group after it when two kinds would get the same label.
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
