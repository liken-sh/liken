//! Conditions on kind knowledge that matches nothing in this snapshot:
//! a field path that no object of the kind has, a `node` field whose
//! values are not the name of any `Node`, an applier mark (a label or
//! annotation key) that no object has, and an `ignoreChanges` entry for
//! a kind the cluster does not serve. Knowledge with a typo in a path
//! still loads, and the section it fills is empty with no explanation,
//! so these conditions give the explanation.
//!
//! A kind that has no objects gets no condition, because its knowledge
//! can be correct for objects that do not exist yet.

use std::collections::HashSet;
use std::sync::Arc;

use super::super::Snapshot;
use transect_cluster::cluster::Custom;
use transect_graph::edges::Object;
use transect_resources::knowledge::gitops::MetaKey;
use transect_resources::knowledge::{KindKnowledge, path};
use transect_resources::resources::{Condition, DocId};

/// The label and annotation keys that appear on at least one object.
#[derive(Default)]
struct MetadataKeys {
    labels: HashSet<String>,
    annotations: HashSet<String>,
}

impl MetadataKeys {
    fn contain(&self, key: &MetaKey) -> bool {
        match key {
            MetaKey::Label(key) => self.labels.contains(key),
            MetaKey::Annotation(key) => self.annotations.contains(key),
        }
    }
}

impl Snapshot {
    /// A condition for each path, `node` field, or applier mark of kind
    /// knowledge that matches nothing in this snapshot, and for each
    /// `ignoreChanges` kind that the cluster does not serve.
    pub(super) fn unmatched_kinds(&self) -> Vec<(DocId, Condition)> {
        let keys = self.metadata_keys();
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
            let of = KindObjects {
                group,
                kind,
                objects,
            };
            let messages = [
                self.fields(known, &of, whole),
                of.node(known, &nodes).into_iter().collect(),
                self.gitops_fields(known, &of),
                of.marks(known, &keys),
            ];
            let messages = messages.into_iter().flatten();
            found.extend(messages.map(|message| (known.doc, Condition::unmatched(message))));
        }
        found.extend(self.unserved_ignores());
        found
    }

    /// The label and annotation keys on every object that intent can
    /// describe, which covers every object an applier's mark can be on.
    fn metadata_keys(&self) -> MetadataKeys {
        let mut keys = MetadataKeys::default();
        for (_, meta) in self.described() {
            let labels = meta.labels.iter().flatten();
            keys.labels.extend(labels.map(|(key, _)| key.clone()));
            let annotations = meta.annotations.iter().flatten();
            keys.annotations
                .extend(annotations.map(|(key, _)| key.clone()));
        }
        keys
    }

    /// A message for each of `known`'s `references` and `writesSecret`
    /// paths that no object of the kind has. A kind that is not watched
    /// whole has only metadata and conditions, so its paths cannot be
    /// checked and it gets no message. An entry by a name template reads
    /// no field, so it is not checked.
    fn fields(&self, known: &KindKnowledge, of: &KindObjects, whole: bool) -> Vec<String> {
        if !whole {
            return Vec::new();
        }
        let references = known
            .references
            .iter()
            .enumerate()
            .filter(|(_, reference)| !reference.by_name)
            .map(|(at, reference)| (format!("references[{at}]"), &reference.path));
        let named = [("writesSecret".to_string(), known.writes_secret.as_ref())];
        let named = named
            .into_iter()
            .filter_map(|(field, path)| Some((field, path?)));
        references
            .chain(named)
            .filter(|(_, path)| !of.any_has(path))
            .map(|(field, path)| format!("{field} reads {path}, which no {} has", of.kind))
            .collect()
    }

    /// A message for each GitOps field of `known` that reads nothing on
    /// any object of the kind.
    fn gitops_fields(&self, known: &KindKnowledge, of: &KindObjects) -> Vec<String> {
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

    /// A condition for each kind in an `ignoreChanges` entry that the
    /// cluster does not serve.
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

/// The objects of one custom kind.
struct KindObjects<'a> {
    group: &'a str,
    kind: &'a str,
    objects: &'a [Arc<Custom>],
}

impl KindObjects<'_> {
    fn object(&self, found: &Custom) -> Option<Object> {
        let name = found.metadata.name.as_deref()?;
        let namespace = found.metadata.namespace.as_deref();
        Some(Object::new(self.group, self.kind, namespace, name))
    }

    /// Whether any object has a value at the path `field`.
    fn any_has(&self, field: &str) -> bool {
        self.objects.iter().any(|found| match field {
            "metadata.name" | "metadata.namespace" => self
                .object(found)
                .is_some_and(|object| !path::read_of(&object, &found.data, field).is_empty()),
            _ => path::holds(&found.data, field),
        })
    }

    /// A message when no value that `known`'s `node` field reads is the
    /// name of a `Node`.
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

    /// A message for each applier mark of `known` whose label or
    /// annotation key is on no object, so no object can name its applier.
    fn marks(&self, known: &KindKnowledge, keys: &MetadataKeys) -> Vec<String> {
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
            .filter(|(_, mark)| !keys.contain(&mark.key))
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
