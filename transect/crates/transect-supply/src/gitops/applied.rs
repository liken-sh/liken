//! The applier of each workload, read from the marks that a GitOps tool
//! writes on the objects that it applies.  The `applies` field of the
//! tool's kind knowledge names those marks.
//!
//! A tool marks the objects that it applies, and those are often not
//! the objects that a reader counts as the workload.  The tool applies
//! an operator's custom resource, but not the `StatefulSet` that the
//! operator makes from it, and a chart may mark only some of the
//! objects that it renders.  So transect counts each marked object for
//! its top owner, and the top owner's own mark takes precedence over a
//! mark found further down its chain of owners.

use std::collections::HashMap;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use super::template::captures;
use transect_cluster::ownership::Ownership;
use transect_graph::edges::Object;
use transect_resources::knowledge::gitops::{Applies, Mark, MetaKey};

/// The marks of each applier kind, the most specific first, and the
/// appliers of each kind by name.  The names resolve the marks that
/// name no namespace.
#[derive(Debug, Default)]
pub struct Appliers {
    marks: Vec<(String, String, Applies)>,
    by_name: HashMap<(String, String, String), Vec<Object>>,
}

impl Appliers {
    /// The appliers of `kinds`, each with its `applies`, whose objects
    /// are `objects`.
    pub fn new<'a>(
        kinds: impl IntoIterator<Item = (String, String, Applies)>,
        objects: impl IntoIterator<Item = &'a Object>,
    ) -> Self {
        let mut marks: Vec<(String, String, Applies)> = kinds.into_iter().collect();
        marks.sort_by(|a, b| {
            let (a_key, b_key) = ((&a.0, &a.1), (&b.0, &b.1));
            b.2.precedence.cmp(&a.2.precedence).then(a_key.cmp(&b_key))
        });
        let mut by_name: HashMap<(String, String, String), Vec<Object>> = HashMap::new();
        for object in objects {
            let key = (
                object.group.clone(),
                object.kind.clone(),
                object.name.clone(),
            );
            by_name.entry(key).or_default().push(object.clone());
        }
        Self { marks, by_name }
    }

    /// The applier that the marks in `meta` name, when a tool applied
    /// the object.
    pub fn of(&self, meta: &ObjectMeta) -> Option<Object> {
        self.marks.iter().find_map(|(group, kind, applies)| {
            let named = read(meta, &applies.name, "name")?;
            let name = named.get("name")?;
            let namespace = match &applies.namespace {
                Some(mark) => Some(read(meta, mark, "namespace")?.get("namespace")?.clone()),
                None => named.get("namespace").cloned(),
            };
            match namespace {
                Some(namespace) => Some(Object::new(group, kind, Some(&namespace), name)),
                None => {
                    let key = (group.clone(), kind.clone(), name.clone());
                    match self.by_name.get(&key)?.as_slice() {
                        [one] => Some(one.clone()),
                        _ => None,
                    }
                }
            }
        })
    }
}

/// The parts that `mark` reads from `meta`.  When the mark has no
/// formats, the whole value is the one part, named `part`.  Otherwise
/// the parts are what the first matching format captures in the value.
fn read(meta: &ObjectMeta, mark: &Mark, part: &str) -> Option<HashMap<String, String>> {
    let value = match &mark.key {
        MetaKey::Label(key) => meta.labels.as_ref()?.get(key)?,
        MetaKey::Annotation(key) => meta.annotations.as_ref()?.get(key)?,
    };
    if mark.formats.is_empty() {
        return Some(HashMap::from([(part.to_string(), value.clone())]));
    }
    let found = mark
        .formats
        .iter()
        .find_map(|format| captures(format, value))?;
    Some(found.into_iter().map(|(k, v)| (k, v.to_string())).collect())
}

/// Each workload a tool applied, and the applier that applied it.
#[derive(Debug, Default)]
pub struct Applied {
    by: HashMap<Object, Object>,
}

impl Applied {
    /// Reads the marks that `appliers` names on each of `objects`, which
    /// come with their metadata, and counts each marked object for its
    /// top owner in `ownership`.
    pub fn build<'a>(
        objects: impl IntoIterator<Item = (Object, &'a ObjectMeta)>,
        ownership: &Ownership,
        appliers: &Appliers,
    ) -> Self {
        let mut own = HashMap::new();
        let mut below = HashMap::new();
        for (object, meta) in objects {
            let Some(by) = appliers.of(meta) else {
                continue;
            };
            let top = ownership.top_owner(&object);
            if top == object {
                own.insert(top, by);
            } else {
                below.entry(top).or_insert(by);
            }
        }
        below.extend(own);
        Self { by: below }
    }

    pub fn iter(&self) -> impl Iterator<Item = (&Object, &Object)> {
        self.by.iter()
    }
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;

    use rstest::rstest;

    use super::*;
    use transect_cluster::ownership::Entry;
    use transect_cluster::ownership::fixtures::meta;
    use transect_resources::knowledge::fixtures::knowledge;

    const GROUP: &str = "apply.example.test";

    fn applies(spec: &str) -> Applies {
        let found = knowledge("appliers.apply.example.test", spec)
            .gitops
            .unwrap();
        found.applies.unwrap()
    }

    /// A tree applier marked by two labels, a release applier that
    /// takes precedence over it, and an app applier marked by a
    /// tracking annotation.
    fn appliers(objects: &[Object]) -> Appliers {
        Appliers::new(
            [
                (
                    GROUP.to_string(),
                    "Tree".to_string(),
                    applies(
                        "{applies: {name: {label: tree/name}, namespace: {label: tree/namespace}}}",
                    ),
                ),
                (
                    GROUP.to_string(),
                    "Release".to_string(),
                    applies(
                        "{applies: {name: {label: release/name}, namespace: {label: release/namespace}, precedence: 1}}",
                    ),
                ),
                (
                    GROUP.to_string(),
                    "App".to_string(),
                    applies(
                        "{applies: {name: {annotation: app/tracking, formats: ['{namespace}_{name}:*', '{name}:*']}}}",
                    ),
                ),
            ],
            objects,
        )
    }

    fn marked(labels: &[(&str, &str)], annotations: &[(&str, &str)]) -> ObjectMeta {
        let map = |pairs: &[(&str, &str)]| {
            let pairs = pairs.iter().map(|(k, v)| (k.to_string(), v.to_string()));
            Some(pairs.collect::<BTreeMap<_, _>>())
        };
        ObjectMeta {
            labels: map(labels),
            annotations: map(annotations),
            ..ObjectMeta::default()
        }
    }

    fn object(kind: &str, namespace: &str, name: &str) -> Object {
        Object::new(GROUP, kind, Some(namespace), name)
    }

    #[rstest]
    #[case::a_tree(&[("tree/name", "shop"), ("tree/namespace", "ops")], &[], Some(object("Tree", "ops", "shop")))]
    #[case::a_release_over_a_tree(
        &[("tree/name", "shop"), ("tree/namespace", "ops"), ("release/name", "cache"), ("release/namespace", "shop")],
        &[],
        Some(object("Release", "shop", "cache"))
    )]
    #[case::a_name_without_its_namespace(&[("tree/name", "shop")], &[], None)]
    #[case::an_annotation_by_its_format(&[], &[("app/tracking", "shop:apps/Deployment:shop/web")], Some(object("App", "deploy", "shop")))]
    #[case::an_app_in_another_namespace(&[], &[("app/tracking", "team_shop:apps/Deployment:shop/web")], Some(object("App", "team", "shop")))]
    #[case::an_app_no_object_has(&[], &[("app/tracking", "blog:apps/Deployment:blog/web")], None)]
    #[case::none(&[("app", "web")], &[], None)]
    fn reads_what_applied_an_object(
        #[case] labels: &[(&str, &str)],
        #[case] annotations: &[(&str, &str)],
        #[case] expected: Option<Object>,
    ) {
        let appliers = appliers(&[object("App", "deploy", "shop")]);
        assert_eq!(appliers.of(&marked(labels, annotations)), expected);
    }

    /// A `Cache` in `shop` that owns a `StatefulSet`.  The arguments set
    /// the mark on each object, and each mark names a tree applier in
    /// `ops`.
    fn cache(on_cache: Option<&str>, on_stateful_set: Option<&str>) -> Applied {
        let label = |name: Option<&str>| {
            name.map(|name| {
                BTreeMap::from([
                    ("tree/name".into(), name.into()),
                    ("tree/namespace".into(), "ops".into()),
                ])
            })
        };
        let mut cache = meta(Some("shop"), "sessions", &[]);
        cache.labels = label(on_cache);
        let mut set = meta(
            Some("shop"),
            "sessions",
            &[("cache.example.io/v1", "Cache", "sessions")],
        );
        set.labels = label(on_stateful_set);
        let ownership = Ownership::build([
            Entry {
                group: "cache.example.io",
                kind: "Cache",
                meta: &cache,
            },
            Entry {
                group: "apps",
                kind: "StatefulSet",
                meta: &set,
            },
        ]);
        let objects = [
            (
                Object::new("cache.example.io", "Cache", Some("shop"), "sessions"),
                &cache,
            ),
            (
                Object::new("apps", "StatefulSet", Some("shop"), "sessions"),
                &set,
            ),
        ];
        Applied::build(objects, &ownership, &appliers(&[]))
    }

    #[rstest]
    #[case::on_the_top_owner(Some("shop"), None, Some(object("Tree", "ops", "shop")))]
    #[case::below_the_top_owner(None, Some("caches"), Some(object("Tree", "ops", "caches")))]
    #[case::the_top_owners_own_wins(
        Some("shop"),
        Some("caches"),
        Some(object("Tree", "ops", "shop"))
    )]
    #[case::none(None, None, None)]
    fn counts_a_mark_for_its_top_owner(
        #[case] on_cache: Option<&str>,
        #[case] on_stateful_set: Option<&str>,
        #[case] expected: Option<Object>,
    ) {
        let applied = cache(on_cache, on_stateful_set);
        let found: Vec<(&Object, &Object)> = applied.iter().collect();
        let top = Object::new("cache.example.io", "Cache", Some("shop"), "sessions");
        let expected: Vec<(&Object, &Object)> = expected.iter().map(|by| (&top, by)).collect();
        assert_eq!(found, expected);
    }
}
