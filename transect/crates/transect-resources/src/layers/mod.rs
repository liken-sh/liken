//! The layers of the map: the horizontal bands that namespaces are
//! drawn in.
//!
//! A `Layers` resource lists the layers from top to bottom. A namespace
//! goes in the layer that its `layer` annotation sets. Without the
//! annotation, it goes in the first layer with a selector that matches
//! the namespace's labels. When neither places it, it goes in the layer
//! that the kind knowledge of its custom resources gives. Kind knowledge
//! comes last because the cluster's own `Layers` is more specific than
//! a default that the project of a kind ships. A namespace that nothing
//! places goes in the unsorted band, so a missing assignment shows on
//! the map.

mod parse;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

pub use parse::{Layer, parse};

use crate::resources::{Condition, DocId, annotation};

/// The key of the `Namespace` annotation that sets its layer, after the
/// `transect.liken.sh/` prefix.
pub const LAYER_FIELD: &str = "layer";

#[derive(Debug, Default)]
pub struct Layers {
    layers: Vec<Layer>,
    /// The document the layers were read from, or `None` when no file
    /// declared any.
    pub doc: Option<DocId>,
}

impl Layers {
    pub fn new(layers: Vec<Layer>, doc: DocId) -> Self {
        Self {
            layers,
            doc: Some(doc),
        }
    }

    #[cfg(any(test, feature = "fixtures"))]
    pub fn from_yaml(text: &str) -> anyhow::Result<Self> {
        let value = serde_yaml::from_str(text)?;
        let layers = parse(value).map_err(anyhow::Error::msg)?;
        Ok(Self::new(layers, DocId::default()))
    }

    /// A condition for each selector that matches none of `namespaces`.
    /// Such a selector usually means that a namespace was renamed or a
    /// label is misspelled.
    pub fn unmatched<'a>(
        &self,
        namespaces: impl IntoIterator<Item = &'a ObjectMeta>,
    ) -> Vec<Condition> {
        let labels: Vec<_> = namespaces
            .into_iter()
            .map(|namespace| namespace.labels.clone().unwrap_or_default())
            .collect();
        let mut conditions = Vec::new();
        for layer in &self.layers {
            for (at, selector) in layer.namespace_selectors.iter().enumerate() {
                if !labels
                    .iter()
                    .any(|labels| transect_graph::selector::matches(selector, labels))
                {
                    conditions.push(Condition::unmatched(format!(
                        "selector {} of layer {:?} matches no namespace",
                        at + 1,
                        layer.name
                    )));
                }
            }
        }
        conditions
    }

    pub fn names(&self) -> impl Iterator<Item = &str> {
        self.layers.iter().map(|layer| layer.name.as_str())
    }

    /// The index of the layer for `namespace`, or `None` for the
    /// unsorted band. `known` is the layer that kind knowledge gives the
    /// namespace, and it applies only when neither the annotation nor a
    /// selector places the namespace. An annotation that sets an
    /// undeclared layer returns `None` with no fallback to the
    /// selectors, so the typo shows on the map.
    pub fn place(&self, namespace: &ObjectMeta, known: Option<&str>) -> Option<usize> {
        if let Some(named) = annotation(namespace.annotations.as_ref(), LAYER_FIELD) {
            return self.position(named);
        }
        self.selected(namespace)
            .or_else(|| known.and_then(|known| self.position(known)))
    }

    /// Whether the annotation or a selector places `namespace`, so that
    /// kind knowledge does not apply to it.
    pub fn decides(&self, namespace: &ObjectMeta) -> bool {
        let annotated = annotation(namespace.annotations.as_ref(), LAYER_FIELD).is_some();
        annotated || self.selected(namespace).is_some()
    }

    pub fn position(&self, name: &str) -> Option<usize> {
        self.layers.iter().position(|layer| layer.name == name)
    }

    fn selected(&self, namespace: &ObjectMeta) -> Option<usize> {
        let labels = namespace.labels.clone().unwrap_or_default();
        self.layers.iter().position(|layer| {
            layer
                .namespace_selectors
                .iter()
                .any(|s| transect_graph::selector::matches(s, &labels))
        })
    }
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use std::collections::BTreeMap;

    use super::*;

    /// The metadata of a `Namespace`, with the
    /// `kubernetes.io/metadata.name` label that the API server adds to
    /// every namespace.
    pub fn namespace(name: &str, labels: &[(&str, &str)], layer: Option<&str>) -> ObjectMeta {
        let mut all_labels: BTreeMap<String, String> = labels
            .iter()
            .map(|(key, value)| (key.to_string(), value.to_string()))
            .collect();
        all_labels.insert("kubernetes.io/metadata.name".into(), name.into());
        ObjectMeta {
            name: Some(name.into()),
            labels: Some(all_labels),
            annotations: layer.map(|layer| {
                let key = format!("{}/{LAYER_FIELD}", crate::resources::GROUP);
                BTreeMap::from([(key, layer.into())])
            }),
            ..Default::default()
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::{fixture, rstest};

    use super::fixtures::namespace;
    use super::*;

    #[fixture]
    fn layers() -> Layers {
        Layers::from_yaml(
            r#"
apiVersion: transect.liken.sh/v1alpha1
kind: Layers
metadata: {name: layers}
spec:
  layers:
    - name: apps
      namespaceSelectors:
        - matchExpressions:
            - {key: kubernetes.io/metadata.name, operator: In, values: [shop, blog]}
    - name: agents
      namespaceSelectors:
        - matchLabels: {example.com/agents: "true"}
        - matchExpressions:
            - {key: kubernetes.io/metadata.name, operator: In, values: [scheduler]}
    - name: platform
      namespaceSelectors:
        - matchExpressions:
            - {key: kubernetes.io/metadata.name, operator: In, values: [kube-system, shop]}
"#,
        )
        .unwrap()
    }

    #[rstest]
    #[case::selected_by_name(namespace("blog", &[], None), Some("apps"))]
    #[case::first_layer_wins(namespace("shop", &[], None), Some("apps"))]
    #[case::later_layer(namespace("kube-system", &[], None), Some("platform"))]
    #[case::first_of_several_selectors(namespace("worker", &[("example.com/agents", "true")], None), Some("agents"))]
    #[case::second_of_several_selectors(namespace("scheduler", &[], None), Some("agents"))]
    #[case::annotation_overrides_selectors(namespace("shop", &[], Some("platform")), Some("platform"))]
    #[case::annotation_places_unmatched(namespace("scratch", &[], Some("agents")), Some("agents"))]
    #[case::unknown_annotation_is_unsorted(namespace("shop", &[], Some("nowhere")), None)]
    #[case::unmatched_is_unsorted(namespace("scratch", &[], None), None)]
    fn placement(layers: Layers, #[case] namespace: ObjectMeta, #[case] expected: Option<&str>) {
        let placed = layers
            .place(&namespace, None)
            .map(|i| layers.layers[i].name.as_str());
        assert_eq!(placed, expected);
    }

    #[rstest]
    #[case::places_an_unmatched_namespace(namespace("scratch", &[], None), "platform", Some("platform"))]
    #[case::a_selector_wins(namespace("blog", &[], None), "platform", Some("apps"))]
    #[case::an_annotation_wins(namespace("scratch", &[], Some("agents")), "platform", Some("agents"))]
    #[case::an_unknown_annotation_stays_unsorted(namespace("scratch", &[], Some("nowhere")), "platform", None)]
    #[case::an_undeclared_layer(namespace("scratch", &[], None), "edge", None)]
    fn kind_knowledge_places_what_nothing_else_does(
        layers: Layers,
        #[case] namespace: ObjectMeta,
        #[case] known: &str,
        #[case] expected: Option<&str>,
    ) {
        let placed = layers
            .place(&namespace, Some(known))
            .map(|i| layers.layers[i].name.as_str());
        assert_eq!(
            (placed, layers.decides(&namespace)),
            (
                expected,
                namespace.annotations.is_some() || expected == Some("apps")
            )
        );
    }

    #[rstest]
    fn no_layers_resource_means_no_layers() {
        assert_eq!(Layers::default().names().count(), 0);
    }

    #[rstest]
    fn names_each_selector_that_matches_no_namespace(layers: Layers) {
        let namespaces = [
            namespace("shop", &[], None),
            namespace("kube-system", &[], None),
        ];
        let messages: Vec<String> = layers
            .unmatched(&namespaces)
            .into_iter()
            .map(|condition| condition.message)
            .collect();
        assert_eq!(
            messages,
            vec![
                "selector 1 of layer \"agents\" matches no namespace",
                "selector 2 of layer \"agents\" matches no namespace",
            ]
        );
    }
}
