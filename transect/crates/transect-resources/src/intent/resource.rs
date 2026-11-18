//! An `Intent` resource sets the same fields as the intent annotations,
//! on the objects that it selects in its own namespace. Use it for text
//! that is too long for an annotation, or to try the fields on a
//! cluster before the manifests have the annotations.

use std::collections::BTreeMap;

use serde::Deserialize;

use crate::resources::DocId;
use transect_graph::edges::{Object, Plurals};

#[derive(Clone, Debug, PartialEq)]
pub struct Intent {
    pub doc: DocId,
    pub namespace: String,
    pub name: String,
    pub selects: Vec<Select>,
    pub fields: Fields,
}

/// One entry of `selects`. `resource` is a plural or `<plural>.<group>`,
/// as `kubectl get` takes it, and the entry needs a name, labels, or
/// both.
#[derive(Clone, Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct Select {
    pub resource: String,
    pub name: Option<String>,
    pub match_labels: Option<BTreeMap<String, String>>,
}

/// The fields that an `Intent` resource and the annotations have in
/// common, as written.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Fields {
    pub why: Option<String>,
    pub gotcha: Option<String>,
    pub provides: Option<String>,
    pub depends_on: Vec<String>,
    pub authenticates_through: Vec<String>,
    pub docs: Option<String>,
    /// How the workload authenticates its callers itself, which its API
    /// objects do not show.
    pub signs_in: Option<String>,
    /// Why the workload uses the host network, a host port, a host
    /// path, or a privileged container.
    pub host_access: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Spec {
    selects: Vec<Select>,
    why: Option<String>,
    gotcha: Option<String>,
    provides: Option<String>,
    #[serde(default)]
    depends_on: Vec<String>,
    #[serde(default)]
    authenticates_through: Vec<String>,
    docs: Option<String>,
    signs_in: Option<String>,
    host_access: Option<String>,
}

#[derive(Deserialize)]
struct Document {
    metadata: Metadata,
    spec: Spec,
}

#[derive(Deserialize)]
struct Metadata {
    name: String,
    namespace: Option<String>,
}

impl Intent {
    pub fn parse(doc: DocId, value: serde_yaml::Value) -> Result<Self, String> {
        let document: Document =
            serde_yaml::from_value(value).map_err(|error| error.to_string())?;
        let namespace = document
            .metadata
            .namespace
            .ok_or("an Intent is namespaced and selects objects in its namespace")?;
        let Spec {
            selects,
            why,
            gotcha,
            provides,
            depends_on,
            authenticates_through,
            docs,
            signs_in,
            host_access,
        } = document.spec;
        if selects.is_empty() {
            return Err("selects is empty, and an intent must select at least one object".into());
        }
        if let Some(at) = selects
            .iter()
            .position(|s| s.name.is_none() && s.match_labels.is_none())
        {
            return Err(format!(
                "selects[{at}] sets neither name nor matchLabels, and needs at least one"
            ));
        }
        Ok(Self {
            doc,
            namespace,
            name: document.metadata.name,
            selects,
            fields: Fields {
                why,
                gotcha,
                provides,
                depends_on,
                authenticates_through,
                docs,
                signs_in,
                host_access,
            },
        })
    }

    /// Whether `object`, which has `labels`, is in this `Intent`'s
    /// namespace and matches any entry of `selects`.
    pub fn selects(
        &self,
        object: &Object,
        labels: Option<&BTreeMap<String, String>>,
        plurals: &Plurals,
    ) -> bool {
        self.holds(object)
            && self
                .selects
                .iter()
                .any(|select| select.names(object, labels, plurals))
    }
}

impl Intent {
    /// Whether `object` is in this `Intent`'s namespace.
    pub fn holds(&self, object: &Object) -> bool {
        object.namespace.as_deref() == Some(self.namespace.as_str())
    }
}

impl Select {
    /// Whether this entry matches `object`, ignoring the `Intent`'s
    /// namespace.
    pub fn names(
        &self,
        object: &Object,
        labels: Option<&BTreeMap<String, String>>,
        plurals: &Plurals,
    ) -> bool {
        let plural = plurals.get(&(object.group.clone(), object.kind.clone()));
        let resource = plural.is_some_and(|plural| {
            self.resource == *plural || self.resource == format!("{plural}.{}", object.group)
        });
        let name = self.name.as_ref().is_none_or(|name| *name == object.name);
        let matched = self
            .match_labels
            .iter()
            .flatten()
            .all(|(key, value)| labels.and_then(|labels| labels.get(key)) == Some(value));
        resource && name && matched
    }
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use std::collections::HashMap;

    use super::*;

    pub fn plurals() -> Plurals {
        HashMap::from([
            (("apps".into(), "Deployment".into()), "deployments".into()),
            (("apps".into(), "StatefulSet".into()), "statefulsets".into()),
            (("cache.example.io".into(), "Cache".into()), "caches".into()),
        ])
    }

    /// An `Intent` called `name` in the namespace `shop`, with `spec`.
    pub fn intent(name: &str, spec: &str) -> Intent {
        let text = format!("metadata: {{namespace: shop, name: {name}}}\nspec: {spec}");
        Intent::parse(DocId::default(), serde_yaml::from_str(&text).unwrap()).unwrap()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::*;
    use super::*;

    fn object(group: &str, kind: &str, namespace: &str, name: &str) -> Object {
        Object::new(group, kind, Some(namespace), name)
    }

    #[rstest]
    #[case::by_name(
        "{resource: deployments, name: web}",
        object("apps", "Deployment", "shop", "web"),
        true
    )]
    #[case::another_name(
        "{resource: deployments, name: web}",
        object("apps", "Deployment", "shop", "api"),
        false
    )]
    #[case::another_kind(
        "{resource: deployments, name: web}",
        object("apps", "StatefulSet", "shop", "web"),
        false
    )]
    #[case::another_namespace(
        "{resource: deployments, name: web}",
        object("apps", "Deployment", "blog", "web"),
        false
    )]
    #[case::by_labels(
        "{resource: deployments, matchLabels: {tier: front}}",
        object("apps", "Deployment", "shop", "api"),
        true
    )]
    #[case::labels_that_differ(
        "{resource: deployments, matchLabels: {tier: back}}",
        object("apps", "Deployment", "shop", "api"),
        false
    )]
    #[case::a_custom_kind_by_plural(
        "{resource: caches, name: sessions}",
        object("cache.example.io", "Cache", "shop", "sessions"),
        true
    )]
    #[case::a_custom_kind_by_plural_and_group(
        "{resource: caches.cache.example.io, name: sessions}",
        object("cache.example.io", "Cache", "shop", "sessions"),
        true
    )]
    fn selects_objects_in_its_namespace(
        #[case] select: &str,
        #[case] object: Object,
        #[case] selected: bool,
    ) {
        let intent = intent("one", &format!("{{selects: [{select}], why: Here.}}"));
        let labels = BTreeMap::from([("tier".to_string(), "front".to_string())]);
        assert_eq!(intent.selects(&object, Some(&labels), &plurals()), selected);
    }

    #[rstest]
    #[case::cluster_scoped(
        "metadata: {name: db}\nspec: {selects: [{resource: deployments, name: db}]}"
    )]
    #[case::no_selects("metadata: {namespace: shop, name: db}\nspec: {selects: []}")]
    #[case::a_term_with_only_a_resource(
        "metadata: {namespace: shop, name: db}\nspec: {selects: [{resource: deployments}]}"
    )]
    #[case::an_unknown_field(
        "metadata: {namespace: shop, name: db}\nspec: {selects: [{resource: deployments, name: db}], wyh: typo}"
    )]
    fn refuses(#[case] text: &str) {
        let value = serde_yaml::from_str(text).unwrap();
        assert!(Intent::parse(DocId::default(), value).is_err());
    }
}
