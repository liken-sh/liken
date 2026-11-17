//! Overlays: plain objects of a kind `transect` watches that have only
//! metadata, merged onto the live object of the same kind, namespace,
//! and name as if the cluster's manifest had their annotations and
//! labels.  An overlay is the patch a person would make to the real
//! manifest, so intent can be tried against a cluster without editing
//! it, and adopted by moving the same lines into its repository.

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
    /// The file it came from, which every fact read from it names.
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
    /// The overlay `value` declares, or why it is not one.  Anything
    /// beyond metadata is refused rather than ignored, because a reader
    /// who wrote a spec would expect `transect` to use it.
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

    /// Whether this overlay adds to `object`'s metadata.
    pub fn names(&self, object: &Object) -> bool {
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

/// The overlays merged onto one reading of the cluster: which matched a
/// live object, and which annotations each one filled in.
#[derive(Debug, Default)]
pub struct Overlaid {
    overlays: Arc<Vec<Overlay>>,
    /// The overlays naming each object, by their place in `overlays`.
    by_object: HashMap<Object, Vec<usize>>,
    /// The group and kind of every overlay, so a kind that no overlay
    /// names is skipped without a lookup per object.
    kinds: HashSet<(String, String)>,
    matched: HashSet<usize>,
    /// The annotations an overlay set on each object, by key, with the
    /// overlay that set it.
    filled: HashMap<Object, BTreeMap<String, usize>>,
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

    /// `objects` of a built-in kind with the overlays naming them merged
    /// on.
    pub fn apply<K>(&mut self, objects: Vec<Arc<K>>) -> Vec<Arc<K>>
    where
        K: Resource<DynamicType = ()> + Clone,
    {
        self.apply_as(&K::group(&()), &K::kind(&()), objects)
    }

    /// `objects`, of the kind `group` and `kind` name, with the overlays
    /// naming them merged on.  Only an object an overlay names is copied.
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
                let filled = merge(meta, &self.overlays[at]);
                let into = self.filled.entry(key.clone()).or_default();
                for annotation in filled {
                    into.entry(annotation).or_insert(at);
                }
            }
        }
        objects
    }

    /// The file of the overlay that set `object`'s annotation `key`,
    /// when an overlay set it and the live object did not.
    pub fn file_of(&self, object: &Object, key: &str) -> Option<&str> {
        let at = *self.filled.get(object)?.get(key)?;
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

/// Merges `overlay`'s labels and annotations onto `meta`, leaving every
/// key the live object already has, and returns the annotations it set.
fn merge(meta: &mut ObjectMeta, overlay: &Overlay) -> Vec<String> {
    let labels = meta.labels.get_or_insert_default();
    for (key, value) in &overlay.labels {
        labels.entry(key.clone()).or_insert_with(|| value.clone());
    }
    let annotations = meta.annotations.get_or_insert_default();
    let mut filled = Vec::new();
    for (key, value) in &overlay.annotations {
        if !annotations.contains_key(key) {
            annotations.insert(key.clone(), value.clone());
            filled.push(key.clone());
        }
    }
    filled
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
