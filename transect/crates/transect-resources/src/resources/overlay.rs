//! An overlay is a YAML document with only the metadata of an object
//! that `transect` watches. `transect` adds the overlay's labels and
//! annotations to the live object with the same group, kind,
//! namespace, and name. You write the same lines that you would add to
//! the object's manifest, so you can try annotations without changing
//! the cluster, and later copy them into the manifest.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;
use kube::Resource;
use serde::Deserialize;

use super::{Condition, DocId};
use transect_graph::edges::Object;

#[derive(Clone, Debug, PartialEq)]
pub struct Overlay {
    pub doc: DocId,
    /// The file the overlay was read from. The provenance of each
    /// annotation that the overlay added records this file.
    pub file: String,
    pub group: String,
    pub kind: String,
    pub namespace: Option<String>,
    pub name: String,
    pub labels: BTreeMap<String, String>,
    pub annotations: BTreeMap<String, String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Document {
    api_version: String,
    kind: String,
    metadata: Metadata,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Metadata {
    name: String,
    namespace: Option<String>,
    #[serde(default)]
    labels: BTreeMap<String, String>,
    #[serde(default)]
    annotations: BTreeMap<String, String>,
}

impl Overlay {
    /// Any field other than `apiVersion`, `kind`, and the four
    /// `metadata` fields is an error, because someone who writes a
    /// `spec` in an overlay expects `transect` to use it.
    pub fn parse(doc: DocId, file: &str, value: serde_yaml::Value) -> Result<Self, String> {
        let document: Document = serde_yaml::from_value(value).map_err(|error| {
            format!(
                "an overlay may set only apiVersion, kind, and metadata's name, namespace, labels, and annotations: {error}"
            )
        })?;
        let group = match document.api_version.rsplit_once('/') {
            Some((group, _version)) => group.to_string(),
            None => String::new(),
        };
        Ok(Self {
            doc,
            file: file.into(),
            group,
            kind: document.kind,
            namespace: document.metadata.namespace,
            name: document.metadata.name,
            labels: document.metadata.labels,
            annotations: document.metadata.annotations,
        })
    }

    pub fn is_for(&self, object: &Object) -> bool {
        &self.object() == object
    }

    fn object(&self) -> Object {
        Object::new(
            &self.group,
            &self.kind,
            self.namespace.as_deref(),
            &self.name,
        )
    }
}

/// Applies the overlays to one snapshot of the cluster, and records
/// which overlays matched a live object and which annotations each
/// overlay added.
#[derive(Debug, Default)]
pub struct Overlaid {
    overlays: Arc<Vec<Overlay>>,
    by_object: HashMap<Object, Vec<usize>>,
    /// `apply_as` returns early for a kind that no overlay is for.
    kinds: HashSet<(String, String)>,
    matched: HashSet<usize>,
    /// For each object, the index of the overlay that added each
    /// annotation key.
    added: HashMap<Object, BTreeMap<String, usize>>,
}

impl Overlaid {
    pub fn new(overlays: Arc<Vec<Overlay>>) -> Self {
        let mut by_object: HashMap<Object, Vec<usize>> = HashMap::new();
        let mut kinds = HashSet::new();
        for (at, overlay) in overlays.iter().enumerate() {
            by_object.entry(overlay.object()).or_default().push(at);
            kinds.insert((overlay.group.clone(), overlay.kind.clone()));
        }
        Self {
            overlays,
            by_object,
            kinds,
            ..Self::default()
        }
    }

    /// `apply_as` for a built-in kind, whose group and kind come from
    /// the type.
    pub fn apply<K>(&mut self, objects: Vec<Arc<K>>) -> Vec<Arc<K>>
    where
        K: Resource<DynamicType = ()> + Clone,
    {
        self.apply_as(&K::group(&()), &K::kind(&()), objects)
    }

    /// Merges the overlays into `objects` of `group` and `kind`. Only an
    /// object that has an overlay is cloned.
    pub fn apply_as<K: Resource + Clone>(
        &mut self,
        group: &str,
        kind: &str,
        mut objects: Vec<Arc<K>>,
    ) -> Vec<Arc<K>> {
        if !self.kinds.contains(&(group.to_string(), kind.to_string())) {
            return objects;
        }
        for object in &mut objects {
            let meta = object.meta();
            let Some(name) = meta.name.as_deref() else {
                continue;
            };
            let key = Object::new(group, kind, meta.namespace.as_deref(), name);
            let Some(overlays) = self.by_object.get(&key).cloned() else {
                continue;
            };
            let meta = Arc::make_mut(object).meta_mut();
            for at in overlays {
                self.matched.insert(at);
                let added = merge(meta, &self.overlays[at]);
                let into = self.added.entry(key.clone()).or_default();
                for annotation in added {
                    into.entry(annotation).or_insert(at);
                }
            }
        }
        objects
    }

    /// The file of the overlay that added the annotation `key` to
    /// `object`, or `None` when the live object already had it or no
    /// overlay set it.
    pub fn file_of(&self, object: &Object, key: &str) -> Option<&str> {
        let at = *self.added.get(object)?.get(key)?;
        Some(self.overlays[at].file.as_str())
    }

    /// A condition for each overlay that matched no live object.
    pub fn unmatched(&self) -> Vec<(DocId, Condition)> {
        let overlays = self.overlays.iter().enumerate();
        overlays
            .filter(|(at, _)| !self.matched.contains(at))
            .map(|(_, overlay)| {
                let object = Object::new(
                    &overlay.group,
                    &overlay.kind,
                    overlay.namespace.as_deref(),
                    &overlay.name,
                );
                let message = format!(
                    "no {} among the objects transect watches",
                    transect_graph::links::kind_and_name_in(&object, None)
                );
                (overlay.doc, Condition::unmatched(message))
            })
            .collect()
    }
}

/// Adds `overlay`'s labels and annotations to `meta`, and returns the
/// annotation keys it added. A key the live object already has keeps
/// its live value.
fn merge(meta: &mut ObjectMeta, overlay: &Overlay) -> Vec<String> {
    let labels = meta.labels.get_or_insert_default();
    for (key, value) in &overlay.labels {
        labels.entry(key.clone()).or_insert_with(|| value.clone());
    }
    let annotations = meta.annotations.get_or_insert_default();
    let mut added = Vec::new();
    for (key, value) in &overlay.annotations {
        if !annotations.contains_key(key) {
            annotations.insert(key.clone(), value.clone());
            added.push(key.clone());
        }
    }
    added
}

#[cfg(test)]
mod tests {
    use k8s_openapi::api::apps::v1::Deployment;
    use rstest::rstest;

    use super::super::{Directory, Read, sort};
    use super::*;

    const OVERLAY: &str = "
apiVersion: apps/v1
kind: Deployment
metadata:
  namespace: shop
  name: web
  labels: {tier: front}
  annotations:
    transect.liken.sh/why: Sells things.
    transect.liken.sh/gotcha: Slow to start.
";

    fn overlaid(text: &str) -> Overlaid {
        let loaded: Directory = sort(vec![Read {
            path: "overlays/web.yaml".into(),
            text: Ok(text.into()),
        }]);
        Overlaid::new(loaded.overlays)
    }

    fn deployment(namespace: &str, name: &str, annotations: &str) -> Arc<Deployment> {
        let text = format!(
            "metadata: {{namespace: {namespace}, name: {name}, annotations: {{{annotations}}}}}"
        );
        Arc::new(serde_yaml::from_str(&text).unwrap())
    }

    fn web() -> Object {
        Object::new("apps", "Deployment", Some("shop"), "web")
    }

    #[rstest]
    fn merges_onto_the_object_it_names() {
        let mut overlaid = overlaid(OVERLAY);
        let merged = overlaid.apply(vec![deployment("shop", "web", "")]);
        let meta = &merged[0].metadata;
        let why = meta.annotations.as_ref().unwrap()["transect.liken.sh/why"].as_str();
        assert_eq!(why, "Sells things.");
        assert_eq!(meta.labels.as_ref().unwrap()["tier"], "front");
        assert_eq!(
            overlaid.file_of(&web(), "transect.liken.sh/why"),
            Some("overlays/web.yaml")
        );
        assert!(overlaid.unmatched().is_empty());
    }

    #[rstest]
    fn the_live_annotation_wins() {
        let mut overlaid = overlaid(OVERLAY);
        let live = "transect.liken.sh/why: Sells everything.";
        let merged = overlaid.apply(vec![deployment("shop", "web", live)]);
        let annotations = merged[0].metadata.annotations.as_ref().unwrap();
        assert_eq!(annotations["transect.liken.sh/why"], "Sells everything.");
        assert_eq!(overlaid.file_of(&web(), "transect.liken.sh/why"), None);
        assert_eq!(
            overlaid.file_of(&web(), "transect.liken.sh/gotcha"),
            Some("overlays/web.yaml")
        );
    }

    #[rstest]
    #[case::another_name(deployment("shop", "api", ""))]
    #[case::another_namespace(deployment("blog", "web", ""))]
    fn an_overlay_naming_no_live_object_has_a_condition(#[case] live: Arc<Deployment>) {
        let mut overlaid = overlaid(OVERLAY);
        let merged = overlaid.apply(vec![live.clone()]);
        assert_eq!(merged, vec![live]);
        let messages: Vec<String> = overlaid
            .unmatched()
            .into_iter()
            .map(|(_, condition)| condition.message)
            .collect();
        assert_eq!(
            messages,
            vec!["no Deployment shop/web among the objects transect watches"]
        );
    }

    #[rstest]
    #[case::the_core_group("v1", "")]
    #[case::a_named_group("apps/v1", "apps")]
    #[case::a_custom_group("cache.example.io/v1beta1", "cache.example.io")]
    fn reads_the_group_from_the_api_version(#[case] api_version: &str, #[case] group: &str) {
        let text = format!("{{apiVersion: {api_version}, kind: Thing, metadata: {{name: one}}}}");
        let value = serde_yaml::from_str(&text).unwrap();
        let overlay = Overlay::parse(DocId::default(), "a.yaml", value).unwrap();
        assert_eq!(overlay.group, group);
    }
}
