//! The conditions kind knowledge gets when what it names matches
//! nothing in this reading of the cluster: a field that no object of
//! its kind holds, a `node` that names no `Node`, an applier's mark
//! that no object has, and an `ignoreChanges` kind the cluster does not
//! serve.  Knowledge with a typo in a path otherwise loads, and the
//! section it would fill is empty with no reason given.
//!
//! A kind with no objects yet holds no field, so it gets none of these:
//! the knowledge may be right for the objects to come.

use std::collections::HashSet;
use std::sync::Arc;

use super::super::Snapshot;
use transect_cluster::cluster::Custom;
use transect_graph::edges::Object;
use transect_resources::knowledge::gitops::MetaKey;
use transect_resources::knowledge::{KindKnowledge, path};
use transect_resources::resources::{Condition, DocId};

/// Every label and annotation key some object has.
#[derive(Default)]
struct Carried {
    labels: HashSet<String>,
    annotations: HashSet<String>,
}

impl Carried {
    fn holds(&self, key: &MetaKey) -> bool {
        match key {
            MetaKey::Label(key) => self.labels.contains(key),
            MetaKey::Annotation(key) => self.annotations.contains(key),
        }
    }
}

impl Snapshot {
    /// The conditions of kind knowledge whose paths, node, or marks
    /// match nothing here.
    pub(super) fn unmatched_kinds(&self) -> Vec<(DocId, Condition)> {
        let carried = self.carried();
        let nodes: HashSet<&str> = self
            .nodes
            .iter()
            .filter_map(|node| node.metadata.name.as_deref())
            .collect();
        let mut found = Vec::new();
        for (resource, objects) in &self.custom {
            let (group, kind) = (&resource.group, &resource.kind);
            let Some(known) = self.kinds.of(group, kind) else {
                continue;
            };
            if objects.is_empty() {
                continue;
            }
            let whole = self.paged.whole.contains(&(group.clone(), kind.clone()));
            let of = Of {
                group,
                kind,
                objects,
            };
            let messages = [
                self.fields(known, &of, whole),
                of.node(known, &nodes).into_iter().collect(),
                self.gitops_fields(known, &of),
                of.marks(known, &carried),
            ];
            let messages = messages.into_iter().flatten();
            found.extend(messages.map(|message| (known.doc, Condition::unmatched(message))));
        }
        found.extend(self.unserved_ignores());
        found
    }

    /// Every label and annotation key on an object intent can be
    /// written on, which is every object an applier's mark may be on.
    fn carried(&self) -> Carried {
        let mut carried = Carried::default();
        for (_, meta) in self.described() {
            let labels = meta.labels.iter().flatten();
            carried.labels.extend(labels.map(|(key, _)| key.clone()));
            let annotations = meta.annotations.iter().flatten();
            carried
                .annotations
                .extend(annotations.map(|(key, _)| key.clone()));
        }
        carried
    }

    /// The fields of `known` no object of its kind holds: each
    /// reference, the `Secret` it writes, and its primary.  A kind not
    /// watched whole yet holds none of them, so it is not judged.
    fn fields(&self, known: &KindKnowledge, of: &Of, whole: bool) -> Vec<String> {
        if !whole {
            return Vec::new();
        }
        let references = known
            .references
            .iter()
            .enumerate()
            .map(|(at, reference)| (format!("references[{at}]"), &reference.path));
        let named = [
            ("writesSecret".to_string(), known.writes_secret.as_ref()),
            ("primary".to_string(), known.primary.as_ref()),
        ];
        let named = named
            .into_iter()
            .filter_map(|(field, path)| Some((field, path?)));
        references
            .chain(named)
            .filter(|(_, path)| !of.any_holds(path))
            .map(|(field, path)| format!("{field} reads {path}, which no {} has", of.kind))
            .collect()
    }

    /// The GitOps fields of `known` that read nothing on any object of
    /// its kind.
    fn gitops_fields(&self, known: &KindKnowledge, of: &Of) -> Vec<String> {
        let Some(fields) = &known.gitops else {
            return Vec::new();
        };
        let found: Vec<_> = self.gitops.found_of(of.group, of.kind).collect();
        let none =
            |read: &dyn Fn(&transect_supply::gitops::Found) -> bool| !found.iter().any(|f| read(f));
        let kind = of.kind;
        let mut messages = Vec::new();
        if !fields.source.is_empty() && none(&|f| f.source.is_some()) {
            let paths = fields.source.join(", ");
            messages.push(format!("source reads none of {paths} on any {kind}"));
        }
        if let Some(directory) = &fields.directory
            && none(&|f| f.directory.is_some())
        {
            messages.push(format!("directory reads {directory}, which no {kind} has"));
        }
        if let Some(chart) = &fields.chart
            && none(&|f| f.chart.is_some())
        {
            messages.push(format!("chart reads {chart}, which no {kind} has"));
        }
        if let Some(revision) = &fields.revision
            && none(&|f| f.revision.is_some())
        {
            let paths = revision.paths.join(", ");
            messages.push(format!("revision reads none of {paths} on any {kind}"));
        }
        if !fields.repository.is_empty() && none(&|f| f.url.is_some()) {
            messages.push(format!("repository fills no URL from any {kind}"));
        }
        let declared = fields.reference != Default::default();
        if declared && none(&|f| f.reference != Default::default()) {
            messages.push(format!("ref reads no branch, tag, or commit on any {kind}"));
        }
        messages
    }

    /// A condition for each kind an `ignoreChanges` entry names that
    /// the cluster does not serve.
    fn unserved_ignores(&self) -> Vec<(DocId, Condition)> {
        let mut found = Vec::new();
        for (group, kind) in self.kinds.kinds() {
            let Some(known) = self.kinds.of(group, kind) else {
                continue;
            };
            for (at, ignore) in known.ignore_changes.iter().enumerate() {
                for named in &ignore.kinds {
                    let key = (named.group.clone(), named.kind.clone());
                    if self.plurals.contains_key(&key) {
                        continue;
                    }
                    let message = match named.group.as_str() {
                        "" => format!("ignoreChanges[{at}] names {}", named.kind),
                        group => format!("ignoreChanges[{at}] names {} in {group}", named.kind),
                    };
                    found.push((known.doc, Condition::not_served(message)));
                }
            }
        }
        found
    }
}

/// The objects of one kind.
struct Of<'a> {
    group: &'a str,
    kind: &'a str,
    objects: &'a [Arc<Custom>],
}

impl Of<'_> {
    fn object(&self, found: &Custom) -> Option<Object> {
        let name = found.metadata.name.as_deref()?;
        let namespace = found.metadata.namespace.as_deref();
        Some(Object::new(self.group, self.kind, namespace, name))
    }

    /// Whether any object holds `field`.
    fn any_holds(&self, field: &str) -> bool {
        self.objects.iter().any(|found| match field {
            "metadata.name" | "metadata.namespace" => self
                .object(found)
                .is_some_and(|object| !path::read_of(&object, &found.data, field).is_empty()),
            _ => path::holds(&found.data, field),
        })
    }

    /// Why `known`'s `node` names no `Node`, when it names none.
    fn node(&self, known: &KindKnowledge, nodes: &HashSet<&str>) -> Option<String> {
        let field = known.node.as_deref()?;
        let names = self.objects.iter().flat_map(|found| {
            let object = self.object(found);
            object.map(|object| path::read_of(&object, &found.data, field))
        });
        let named = names.flatten().any(|name| nodes.contains(name.as_str()));
        (!named).then(|| {
            format!(
                "node reads {field}, which names no Node on any {}",
                self.kind
            )
        })
    }

    /// Why `known`'s applier marks name nothing, when no object has the
    /// key that names an applier.
    fn marks(&self, known: &KindKnowledge, carried: &Carried) -> Vec<String> {
        let gitops = known.gitops.as_ref();
        let Some(applies) = gitops.and_then(|fields| fields.applies.as_ref()) else {
            return Vec::new();
        };
        let marks = [("applies.name", Some(&applies.name))];
        let marks = marks
            .into_iter()
            .chain([("applies.namespace", applies.namespace.as_ref())]);
        marks
            .filter_map(|(field, mark)| Some((field, mark?)))
            .filter(|(_, mark)| !carried.holds(&mark.key))
            .map(|(field, mark)| {
                let (what, key) = match &mark.key {
                    MetaKey::Label(key) => ("label", key),
                    MetaKey::Annotation(key) => ("annotation", key),
                };
                format!(
                    "{field}: no object has the {what} {key}, so nothing names a {} that applied it",
                    self.kind
                )
            })
            .collect()
    }
}
