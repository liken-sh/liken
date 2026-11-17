//! Reading one `Outside` resource out of its YAML document.

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

/// The Outside `value` declares, or why it does not validate.
pub fn parse(doc: DocId, value: serde_yaml::Value) -> Result<Outside, String> {
    let document: Document = serde_yaml::from_value(value).map_err(|error| error.to_string())?;
    if document.metadata.namespace.is_some() {
        return Err("an Outside names a thing outside the cluster, so it has no namespace".into());
    }
    let spec = document.spec;
    let title = spec.title.trim().to_string();
    if title.is_empty() {
        return Err("title is empty".into());
    }
    if spec.addresses.is_empty() && spec.drivers.is_empty() {
        return Err("lists no addresses and no drivers, so nothing reaches it".into());
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
