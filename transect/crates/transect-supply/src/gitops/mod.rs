//! The reconciles-from edge: the GitOps object that applied each
//! object, found through the kind knowledge of the GitOps tool's kinds.
//! An object reconciles from the applier whose marks are on it, and the
//! applier reconciles from its source, the object that the tool fetches
//! through.  The applier's directory and the URL that it fetches from
//! give the directory where the object is declared, and each card links
//! to that directory.
//!
//! A GitOps tool is optional, and the code names no tool.  When no kind
//! knowledge declares `applies` for a kind that the cluster serves, this
//! edge kind is off, and each card shows the reason in place of its
//! code entries.

mod applied;
mod card;
mod code;
mod forge;
mod read;
mod revision;
mod sentence;
mod template;

use std::collections::HashMap;
use std::sync::Arc;

use kube::api::ApiResource;
use kube::core::DynamicObject;

pub use applied::{Applied, Appliers};
pub use card::CodeSection;
pub use code::Code;
pub use read::{Found, GitRef, Ready};
pub use revision::Revision;

use transect_cluster::cluster::Watch;
use transect_graph::edges::{Edge, Endpoint, Kind, Object, Provenance};
use transect_resources::knowledge::{GitOpsFields, Kinds, plural_label};

/// The condition that shows whether an object is ready when its kind's
/// knowledge names no condition.  Every kind's `Ready` dot uses the
/// same default.
const READY: &str = "Ready";

/// Why reconciles-from is off.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Off {
    /// No kind knowledge declares `applies` for a kind the cluster
    /// serves.
    NoKnowledge,
    /// The watches of the kinds that apply are still listing.  The text
    /// names those kinds by their plurals.
    Listing(String),
    /// transect may list none of the kinds that apply.
    Refused(String),
    /// No kind knowledge that loaded declares `applies`, and the kind
    /// knowledge named here did not load.
    Unloaded(String),
}

/// What an object's page shows of it.
#[derive(Clone, Debug, PartialEq)]
pub struct Detail {
    pub object: Object,
    pub ready: Ready,
    /// The revision it last applied or fetched.
    pub revision: Option<String>,
    /// The source it reconciles from.
    pub source: Option<Object>,
    /// Where it fetches from: its own URL, or its source's.
    pub url: Option<String>,
    /// The directory it applies from its source.
    pub path: Option<String>,
    /// The chart it installs.
    pub chart: Option<String>,
    /// Whether it applies objects, rather than only fetching for the
    /// objects that do.
    pub applies: bool,
}

/// The objects of a GitOps tool's kinds as of one render, or why there
/// are none.
#[derive(Debug, Default)]
pub struct GitOps {
    pub off: Option<Off>,
    found: HashMap<Object, Found>,
    /// The fields of each GitOps kind, by group and kind.
    fields: HashMap<(String, String), GitOpsFields>,
    /// The applier of each GitOps object, found from the object's own
    /// marks.  For example, a tree applier applies a release.
    applied: Vec<(Object, Object)>,
}

impl GitOps {
    #[cfg(any(test, debug_assertions))]
    pub fn off(off: Off) -> Self {
        Self {
            off: Some(off),
            ..Self::default()
        }
    }

    /// Reads every object in `custom` of a kind whose knowledge in
    /// `kinds` declares GitOps fields.  `listing` returns the state of
    /// each kind's watch, and that state decides whether the edge is on.
    pub fn build(
        kinds: &Kinds,
        custom: &[(ApiResource, Vec<Arc<DynamicObject>>)],
        listing: impl Fn(&str, &str) -> Watch,
    ) -> Self {
        let mut gitops = Self::default();
        for (group, kind) in kinds.kinds() {
            let Some(known) = kinds.of(group, kind) else {
                continue;
            };
            if let Some(fields) = &known.gitops {
                gitops
                    .fields
                    .insert((group.clone(), kind.clone()), fields.clone());
            }
        }
        gitops.off = gitops.off_reason(kinds, &listing);
        for (resource, objects) in custom {
            let key = (resource.group.clone(), resource.kind.clone());
            let Some(fields) = gitops.fields.get(&key) else {
                continue;
            };
            let known = kinds.of(&resource.group, &resource.kind);
            let healthy = known.and_then(|known| known.healthy.as_deref());
            for found in objects {
                let meta = &found.metadata;
                let Some(name) = meta.name.as_deref() else {
                    continue;
                };
                let object = Object::new(&key.0, &key.1, meta.namespace.as_deref(), name);
                let read = read::read(object.clone(), found, fields, healthy.unwrap_or(READY));
                gitops.found.insert(object, read);
            }
        }
        let appliers = gitops.appliers();
        for (resource, objects) in custom {
            let key = (resource.group.clone(), resource.kind.clone());
            if !gitops.fields.contains_key(&key) {
                continue;
            }
            for found in objects {
                let meta = &found.metadata;
                let (Some(name), Some(by)) = (meta.name.as_deref(), appliers.of(meta)) else {
                    continue;
                };
                let object = Object::new(&key.0, &key.1, meta.namespace.as_deref(), name);
                gitops.applied.push((object, by));
            }
        }
        gitops.applied.sort();
        gitops
    }

    /// The reason that the edge is off when no kind that applies has
    /// finished listing, or `None` when one kind has.
    fn off_reason(&self, kinds: &Kinds, listing: &impl Fn(&str, &str) -> Watch) -> Option<Off> {
        let mut appliers: Vec<(&(String, String), Watch)> = self
            .fields
            .iter()
            .filter(|(_, fields)| fields.applies.is_some())
            .map(|(key, _)| (key, listing(&key.0, &key.1)))
            .collect();
        appliers.sort_by(|a, b| a.0.cmp(b.0));
        if appliers.is_empty() {
            return Some(Off::NoKnowledge);
        }
        if appliers.iter().any(|(_, listed)| *listed == Watch::Listed) {
            return None;
        }
        let names = |only: Watch| {
            let named = appliers.iter().filter(|(_, listed)| *listed == only);
            let named = named.map(|((group, kind), _)| {
                let plural = kinds.of(group, kind).map(|known| known.plural.as_str());
                plural_label(kind, plural)
            });
            named.collect::<Vec<_>>().join(" and ")
        };
        match appliers.iter().all(|(_, listed)| *listed == Watch::Refused) {
            true => Some(Off::Refused(names(Watch::Refused))),
            false => Some(Off::Listing(names(Watch::Listing))),
        }
    }

    /// The tool with an off reason that names `unloaded`, when no kind
    /// knowledge declares `applies`.  `unloaded` is kind knowledge that
    /// did not load, and it may be the knowledge that turns the edge on.
    pub fn with_unloaded(mut self, unloaded: Option<&str>) -> Self {
        if let (Some(Off::NoKnowledge), Some(name)) = (&self.off, unloaded) {
            self.off = Some(Off::Unloaded(name.to_string()));
        }
        self
    }

    /// The marks of every kind that applies.  A mark on an object names
    /// the applier of that object.
    pub fn appliers(&self) -> Appliers {
        let kinds = self.fields.iter().filter_map(|((group, kind), fields)| {
            Some((group.clone(), kind.clone(), fields.applies.clone()?))
        });
        Appliers::new(kinds, self.found.keys())
    }

    /// Every GitOps object transect read.
    pub fn objects(&self) -> impl Iterator<Item = &Object> {
        self.found.keys()
    }

    /// The fields of `object`'s kind, when it is a GitOps kind.
    pub fn fields_of(&self, object: &Object) -> Option<&GitOpsFields> {
        self.fields
            .get(&(object.group.clone(), object.kind.clone()))
    }

    /// `object` as read, when it is a GitOps object.
    pub fn found(&self, object: &Object) -> Option<&Found> {
        self.found.get(object)
    }

    /// The source that `found` fetches through, when `found` names an
    /// object of a GitOps kind that fetches from a URL.  When the
    /// reference gives no group, it matches a kind of that name only
    /// when exactly one such kind exists.
    pub fn source_of(&self, found: &Found) -> Option<Object> {
        let named = found.source.as_ref()?;
        let mut kinds = self.fields.iter().filter(|((group, kind), fields)| {
            !fields.repository.is_empty()
                && *kind == named.kind
                && named.group.as_ref().is_none_or(|named| named == group)
        });
        let ((group, kind), _) = kinds.next()?;
        if kinds.next().is_some() {
            return None;
        }
        Some(Object::new(
            group,
            kind,
            named.namespace.as_deref(),
            &named.name,
        ))
    }

    /// Where `found` fetches from: its own URL, or its source's.
    pub fn url_of(&self, found: &Found) -> Option<String> {
        let source = || self.found.get(&self.source_of(found)?)?.url.clone();
        found.url.clone().or_else(source)
    }

    /// What `object`'s page shows of it, or none for an object that is
    /// not a GitOps object.
    pub fn detail(&self, object: &Object) -> Option<Detail> {
        let found = self.found.get(object)?;
        let applies = self.fields_of(object)?.applies.is_some();
        Some(Detail {
            object: object.clone(),
            ready: found.ready.clone(),
            revision: found.revision.clone(),
            source: self.source_of(found),
            url: self.url_of(found),
            path: found.directory.clone(),
            chart: found.chart.clone(),
            applies,
        })
    }

    /// What was read of each object of `kind` in `group`.
    pub fn found_of<'a>(
        &'a self,
        group: &'a str,
        kind: &'a str,
    ) -> impl Iterator<Item = &'a Found> {
        let found = self.found.values();
        found.filter(move |found| found.object.group == group && found.object.kind == kind)
    }

    /// Each object that fetches from its own URL, with that URL.
    /// Address resolution uses these to find a source that the cluster
    /// serves.
    pub fn sources(&self) -> impl Iterator<Item = (&Object, &str)> {
        let found = self.found.values();
        found.filter_map(|found| Some((&found.object, found.url.as_deref()?)))
    }

    /// A reconciles-from edge from each object in `applied` to what
    /// applied it, from each GitOps object to what its own marks name,
    /// and from each object that fetches through a source to that
    /// source.
    pub fn edges(&self, applied: &Applied) -> Vec<Edge> {
        let edge = |from: &Object, to: &Object| Edge {
            kind: Kind::ReconcilesFrom,
            from: Endpoint::Object(from.clone()),
            to: Endpoint::Object(to.clone()),
            guessed: false,
            provenance: Provenance::Kubernetes,
        };
        let by_marks = applied
            .iter()
            .chain(self.applied.iter().map(|(a, b)| (a, b)));
        let mut edges: Vec<Edge> = by_marks.map(|(from, to)| edge(from, to)).collect();
        for found in self.found.values() {
            if let Some(source) = self.source_of(found) {
                edges.push(edge(&found.object, &source));
            }
        }
        edges
    }
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures;
#[cfg(test)]
mod tests;
