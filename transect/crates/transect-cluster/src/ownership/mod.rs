//! Ownership: each pod and workload walked up its owner references to
//! its top owner, the object a reader thinks of as the workload.
//!
//! A `Deployment` owns ReplicaSets that own pods, a `CronJob` owns
//! Jobs that own pods, and an operator's custom resource, such as a
//! database, owns the `StatefulSet` that owns its pods.  A reader
//! counts each of those chains as one thing, named by the object at its
//! top.  So a
//! namespace's workloads are the top owners its pods and built-in
//! workloads resolve to, and a database an operator runs counts once, as
//! its custom kind.
//!
//! The walk reads only `ownerReferences`, so every kind it passes
//! through, built-in or custom, needs only its metadata in the index.

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

/// The longest owner chain the walk follows.  Real chains are three or
/// four links long; the limit only ends a chain that loops, which the
/// API server allows because it does not check owner references.
const MAX_DEPTH: usize = 16;

/// The kinds whose top owners are workloads: pods, and the built-in
/// kinds that run pods.  A `ReplicaSet` is left out because a
/// `Deployment` owns it, and an old one with no pods would otherwise
/// count as a workload of its own whenever its `Deployment`'s metadata
/// is missing.
const WORKLOAD_KINDS: [(&str, &str); 6] = [
    ("", "Pod"),
    ("apps", "Deployment"),
    ("apps", "StatefulSet"),
    ("apps", "DaemonSet"),
    ("batch", "CronJob"),
    ("batch", "Job"),
];

/// One object's metadata, as the index reads it, with the group and
/// kind its store holds.
#[derive(Clone, Copy)]
pub struct Entry<'a> {
    pub group: &'a str,
    pub kind: &'a str,
    pub meta: &'a ObjectMeta,
}

#[derive(Debug, Default)]
pub struct Ownership {
    /// Each indexed object that has an owner, and its top owner.  Kinds
    /// that run no pods, such as a `Service` an operator made, are here
    /// too, so a workload's own `Service` has that workload as its top
    /// owner.
    top: HashMap<Object, Object>,
    /// Each namespace's workloads: the top owners its pods and built-in
    /// workloads resolve to, each once, in order.
    workloads: HashMap<String, BTreeSet<Object>>,
    /// Each indexed object's controlling owner, which `is_owned_by`
    /// walks for an owner below the top, such as a `StatefulSet` that a
    /// database's custom resource owns.
    index: Index,
}

impl Ownership {
    /// Walks every pod and workload in `entries` to its top owner.  The
    /// owners it passes through are looked up in `entries` too; an owner
    /// missing from them, such as a custom kind `transect` cannot list,
    /// ends the walk and is the top owner itself.
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

    /// Whether `owner` is on `object`'s chain of owner references, at
    /// the top or anywhere below it.  An object is not its own owner.
    pub fn is_owned_by(&self, object: &Object, owner: &Object) -> bool {
        self.index.chain(object).skip(1).any(|link| &link == owner)
    }

    /// `namespace`'s workloads, sorted by group, kind, and name.  A
    /// cluster-scoped top owner, such as a catalog that owns a pod in one
    /// namespace, is a workload of the namespace its pod runs in.
    pub fn workloads(&self, namespace: &str) -> impl Iterator<Item = &Object> {
        self.workloads.get(namespace).into_iter().flatten()
    }

    /// Every namespace's workloads.
    pub fn all_workloads(&self) -> impl Iterator<Item = &Object> {
        self.workloads.values().flatten()
    }

    /// How many workloads each namespace that has any holds.
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

/// Each object the walk can pass through, and the owner that controls
/// it, or none at the top of a chain.
#[derive(Debug, Default)]
struct Index {
    parents: HashMap<Object, Option<Object>>,
}

impl Index {
    fn top(&self, object: &Object) -> Object {
        self.chain(object).last().unwrap_or_else(|| object.clone())
    }

    /// `object`, then each owner above it up to the top.  A chain that
    /// loops ends at the first owner it reaches again.
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

    /// The owner of `object`.  An owner reference does not state whether
    /// its owner is namespaced, so the owner is looked up in the
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
    /// The object this entry is, or none for metadata without a name.
    pub fn object(&self) -> Option<Object> {
        Some(Object::new(
            self.group,
            self.kind,
            self.meta.namespace.as_deref(),
            self.meta.name.as_deref()?,
        ))
    }
}

/// The owner reference that controls `meta`, in its own namespace.  The
/// API server allows one controller per object; an object whose owners
/// all leave `controller` unset follows its first owner, as a dependent
/// with only one owner usually does.
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
    /// `apiVersion`, kind, and name, the first of them as its controller.
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

    /// One object to index: its group, kind, namespace, name, and owners.
    pub type Row = (
        &'static str,
        &'static str,
        Option<&'static str>,
        &'static str,
        Vec<(&'static str, &'static str, &'static str)>,
    );

    /// The ownership index of `rows`.
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
