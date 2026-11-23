//! Reading one `Layers` resource out of its YAML document.

use k8s_openapi::apimachinery::pkg::apis::meta::v1::LabelSelector;
use serde::Deserialize;

use transect_graph::selector;

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Layer {
    pub name: String,
    #[serde(default)]
    pub namespace_selectors: Vec<LabelSelector>,
}

#[derive(Deserialize)]
struct LayersDocument {
    spec: LayersSpec,
}

#[derive(Deserialize)]
struct LayersSpec {
    layers: Vec<Layer>,
}

/// The layers listed in a `Layers` document, in document order from top
/// to bottom, or an error message if the document is invalid.  A layer
/// name used twice and an invalid namespace selector are invalid.
pub fn parse(value: serde_yaml_ng::Value) -> Result<Vec<Layer>, String> {
    let layers = serde_yaml_ng::from_value::<LayersDocument>(value)
        .map_err(|error| error.to_string())?
        .spec
        .layers;
    for layer in &layers {
        for selector in &layer.namespace_selectors {
            selector::validate(selector)
                .map_err(|error| format!("layer {:?}: {error}", layer.name))?;
        }
    }
    match first_duplicate(&layers) {
        Some(name) => Err(format!("layer {name:?} is declared twice")),
        None => Ok(layers),
    }
}

fn first_duplicate(layers: &[Layer]) -> Option<&str> {
    layers.iter().enumerate().find_map(|(i, layer)| {
        layers[..i]
            .iter()
            .any(|earlier| earlier.name == layer.name)
            .then_some(layer.name.as_str())
    })
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn value(text: &str) -> serde_yaml_ng::Value {
        serde_yaml_ng::from_str(text).unwrap()
    }

    #[rstest]
    fn reads_the_layers_in_order() {
        let layers = parse(value(
            "spec: {layers: [{name: apps, namespaceSelectors: [{matchLabels: {tier: apps}}]}, {name: platform}]}",
        ))
        .unwrap();
        let names: Vec<&str> = layers.iter().map(|l| l.name.as_str()).collect();
        assert_eq!(names, vec!["apps", "platform"]);
    }

    #[rstest]
    #[case::an_invalid_selector(
        "spec: {layers: [{name: apps, namespaceSelectors: [{matchExpressions: [{key: tier, operator: Equals, values: [apps]}]}]}]}"
    )]
    #[case::a_layer_declared_twice("spec: {layers: [{name: apps}, {name: apps}]}")]
    #[case::no_spec("metadata: {name: layers}")]
    fn rejects(#[case] text: &str) {
        assert!(parse(value(text)).is_err());
    }
}
