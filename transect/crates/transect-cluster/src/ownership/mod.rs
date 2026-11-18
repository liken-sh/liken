//! Ownership: follows each pod's and workload's owner references up to
//! its top owner, which is the object a reader thinks of as the
//! workload.
//!
//! A `Deployment` owns ReplicaSets that own pods, a `CronJob` owns Jobs
//! that own pods, and an operator's custom resource, such as a
//! database, owns the `StatefulSet` that owns its pods. A reader counts
//! each of those chains as one workload, named by the object at the
//! top. So a database that an operator runs counts once, as its custom
//! kind.
//!
//! The walk reads only `ownerReferences`, so the index needs only
//! metadata for each kind on a chain, built-in or custom.

mod card;
mod controller;
mod departed;
mod generated;
mod kinds;

use std::collections::{BTreeSet, HashMap, HashSet};

use k8s_openapi::apimachinery::pkg::apis::meta::v1::{ObjectMeta, OwnerReference};

pub use card::Managed;
pub use controller::{
    Grants, KindSources, Package, Packages, Rbac, StatusManagers, controlled_by,
    controlled_by_kind, package, package_key, runs_image,
};
pub use departed::{Before, Departed};
pub use kinds::{custom, group_of, kinds_to_watch};
use transect_graph::edges::{Edge, Endpoint, Kind, Object, Provenance};

/// The most owners the walk follows above an object.
const MAX_DEPTH: usize = 16;

/// The kinds whose top owners count as workloads: pods and the built-in
/// kinds that run pods. `ReplicaSet` is left out, because a
/// `ReplicaSet` that runs pods is counted through its pods, and an old
/// one with no pods is not a workload.
const WORKLOAD_KINDS: [(&str, &str); 6] = [
    ("", "Pod"),
    ("apps", "Deployment"),
    ("apps", "StatefulSet"),
    ("apps", "DaemonSet"),
    ("batch", "CronJob"),
    ("batch", "Job"),
];

/// One object's metadata, with the group and kind of the store it came
/// from.
#[derive(Clone, Copy)]
pub struct Entry<'a> {
    pub group: &'a str,
    pub kind: &'a str,
    pub meta: &'a ObjectMeta,
}

#[derive(Debug, Default)]
pub struct Ownership {
    /// The top owner of each indexed object that has an owner, including
    /// objects that run no pods, such as a `Service` that an operator
    /// created.
    top: HashMap<Object, Object>,
    /// Each namespace's workloads: the top owners of its pods and
    /// built-in workloads.
    workloads: HashMap<String, BTreeSet<Object>>,
    /// Each indexed object's controlling owner, which `is_owned_by`
    /// follows to find owners below the top.
    index: Index,
}

impl Ownership {
    /// Finds the top owner of every pod and workload in `entries`. Each
    /// owner on a chain is looked up in `entries`. An owner that is not
    /// there, such as a custom kind that `transect` cannot list, ends the
    /// walk and is the top owner.
    pub fn build<'a>(entries: impl IntoIterator<Item = Entry<'a>>) -> Self {
        let mut objects = Vec::new();
        let mut parents: HashMap<Object, Option<Object>> = HashMap::new();
        for entry in entries {
            let Some(object) = entry.object() else {
                continue;
            };
            let member = WORKLOAD_KINDS.contains(&(entry.group, entry.kind));
            objects.push((object.clone(), member));
            parents.insert(object, controller(entry.meta));
        }
        let mut ownership = Self {
            index: Index { parents },
            ..Self::default()
        };
        for (object, member) in objects {
            let top = ownership.index.top(&object);
            if let (true, Some(namespace)) = (member, object.namespace.clone()) {
                let workloads = ownership.workloads.entry(namespace).or_default();
                workloads.insert(top.clone());
            }
            if top != object {
                ownership.top.insert(object, top);
            }
        }
        ownership
    }

    /// The top owner of `object`, or `object` itself when it has no owner.
    pub fn top_owner(&self, object: &Object) -> Object {
        self.top.get(object).unwrap_or(object).clone()
    }

    /// Whether `owner` is anywhere on `object`'s chain of owner
    /// references, at the top or below it. An object is not its own
    /// owner.
    pub fn is_owned_by(&self, object: &Object, owner: &Object) -> bool {
        self.index.chain(object).skip(1).any(|link| &link == owner)
    }

    /// `namespace`'s workloads, sorted by group, kind, and name. A
    /// cluster-scoped top owner of a pod is a workload of the namespace
    /// that the pod runs in.
    pub fn workloads(&self, namespace: &str) -> impl Iterator<Item = &Object> {
        self.workloads.get(namespace).into_iter().flatten()
    }

    /// Every namespace's workloads.
    pub fn all_workloads(&self) -> impl Iterator<Item = &Object> {
        self.workloads.values().flatten()
    }

    /// The number of workloads in each namespace that has any.
    pub fn workloads_per_namespace(&self) -> HashMap<String, usize> {
        self.workloads
            .iter()
            .map(|(namespace, workloads)| (namespace.clone(), workloads.len()))
            .collect()
    }

    /// A managed-by edge from each owned object to its top owner.
    pub fn edges(&self) -> Vec<Edge> {
        self.top
            .iter()
            .map(|(object, top)| Edge {
                kind: Kind::ManagedBy,
                from: Endpoint::Object(object.clone()),
                to: Endpoint::Object(top.clone()),
                guessed: false,
                provenance: Provenance::Kubernetes,
            })
            .collect()
    }
}

/// Each indexed object and its controlling owner, or none for an
/// object at the top of a chain.
#[derive(Debug, Default)]
struct Index {
    parents: HashMap<Object, Option<Object>>,
}

impl Index {
    fn top(&self, object: &Object) -> Object {
        self.chain(object).last().unwrap_or_else(|| object.clone())
    }

    /// `object`, then each owner above it up to the top. The API server
    /// does not check owner references for cycles, so a chain that loops
    /// ends when it reaches an owner a second time.
    fn chain(&self, object: &Object) -> impl Iterator<Item = Object> {
        let mut seen = HashSet::new();
        let mut next = Some(object.clone());
        std::iter::from_fn(move || {
            let current = next.take()?;
            if seen.insert(current.clone()) {
                next = self.parent(&current);
            }
            Some(current)
        })
        .take(MAX_DEPTH + 1)
    }

    /// The owner of `object`. An owner reference does not say whether
    /// the owner is namespaced, so the owner is looked up in the
    /// dependent's namespace first, then among cluster-scoped objects.
    /// An owner in neither is not in the index, and the walk ends there.
    fn parent(&self, object: &Object) -> Option<Object> {
        let owner = self.parents.get(object)?.clone()?;
        if self.parents.contains_key(&owner) {
            return Some(owner);
        }
        let cluster_scoped = Object {
            namespace: None,
            ..owner.clone()
        };
        if self.parents.contains_key(&cluster_scoped) {
            Some(cluster_scoped)
        } else {
            Some(owner)
        }
    }
}

impl Entry<'_> {
    /// The object this entry describes, or none when the metadata has no
    /// name.
    pub fn object(&self) -> Option<Object> {
        Some(Object::new(
            self.group,
            self.kind,
            self.meta.namespace.as_deref(),
            self.meta.name.as_deref()?,
        ))
    }
}

/// The object that controls `meta`, taken from its owner references and
/// placed in `meta`'s namespace. The API server allows one controller
/// per object. When no owner sets `controller`, the first owner is used.
fn controller(meta: &ObjectMeta) -> Option<Object> {
    let owners = meta.owner_references.as_deref().unwrap_or_default();
    let owner: &OwnerReference = owners
        .iter()
        .find(|owner| owner.controller == Some(true))
        .or_else(|| owners.first())?;
    Some(Object::new(
        group_of(&owner.api_version),
        &owner.kind,
        meta.namespace.as_deref(),
        &owner.name,
    ))
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    /// Metadata for `namespace/name`, owned by each of `owners`, given as
    /// `apiVersion`, kind, and name. The first owner is the controller.
    pub fn meta(namespace: Option<&str>, name: &str, owners: &[(&str, &str, &str)]) -> ObjectMeta {
        let owner_references = owners
            .iter()
            .enumerate()
            .map(|(index, &(api_version, kind, name))| OwnerReference {
                api_version: api_version.into(),
                kind: kind.into(),
                name: name.into(),
                controller: Some(index == 0),
                ..Default::default()
            })
            .collect();
        ObjectMeta {
            namespace: namespace.map(Into::into),
            name: Some(name.into()),
            owner_references: Some(owner_references),
            ..Default::default()
        }
    }

    /// One object to index: group, kind, namespace, name, and owners.
    pub type Row = (
        &'static str,
        &'static str,
        Option<&'static str>,
        &'static str,
        Vec<(&'static str, &'static str, &'static str)>,
    );

    /// The ownership index built from `rows`.
    pub fn build(rows: &[Row]) -> Ownership {
        let metas: Vec<ObjectMeta> = rows
            .iter()
            .map(|(_, _, namespace, name, owners)| meta(*namespace, name, owners))
            .collect();
        Ownership::build(rows.iter().zip(&metas).map(|(row, meta)| Entry {
            group: row.0,
            kind: row.1,
            meta,
        }))
    }
}

#[cfg(test)]
mod tests;
