use serde::Deserialize;

use super::Outside;
use transect_resources::resources::DocId;

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

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Spec {
    title: String,
    #[serde(default)]
    addresses: Vec<String>,
    #[serde(default)]
    drivers: Vec<String>,
    why: Option<String>,
    gotcha: Option<String>,
    url: Option<String>,
}

/// Returns the `Outside` that `value` declares, or the reason it is invalid.
pub fn parse(doc: DocId, value: serde_yaml_ng::Value) -> Result<Outside, String> {
    let document: Document = serde_yaml_ng::from_value(value).map_err(|error| error.to_string())?;
    if document.metadata.namespace.is_some() {
        return Err("metadata.namespace is set, but an Outside is not in the cluster, so it has no namespace".into());
    }
    let spec = document.spec;
    let title = spec.title.trim().to_string();
    if title.is_empty() {
        return Err("spec.title is empty".into());
    }
    if spec.addresses.is_empty() && spec.drivers.is_empty() {
        return Err("spec.addresses and spec.drivers are empty, so nothing connects to it".into());
    }
    Ok(Outside {
        doc,
        name: document.metadata.name,
        title,
        addresses: spec.addresses,
        drivers: spec.drivers,
        why: spec.why,
        gotcha: spec.gotcha,
        url: spec
            .url
            .map(|url| url.trim().to_string())
            .filter(|url| !url.is_empty()),
    })
}
