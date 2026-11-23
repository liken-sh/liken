//! The `IngressController` resource declares an ingress controller:
//! the value that an `IngressClass` sets in `spec.controller` for it,
//! the image its workload runs, the Prometheus queries for the traffic
//! it serves, and the ports it serves an `Ingress` on.
//!
//! An `IngressClass` gives only the controller's identifier, not its
//! workload, and a controller with no CRD of its own has no kind
//! knowledge.  So each controller is declared once as an
//! `IngressController`, and transect finds the workload whose pods run
//! its image, as it does for an operator from kind knowledge's
//! `controller.image`.

use serde::Deserialize;

use super::entry_points::{self, EntryPoints};
use super::queries::{self, EntryQueries};
use transect_resources::resources::DocId;

/// One `IngressController` resource, validated.
#[derive(Clone, Debug, PartialEq)]
pub struct IngressController {
    pub doc: DocId,
    pub name: String,
    /// The value of `spec.controller` in its `IngressClass` objects.
    pub controller: String,
    /// The image repository its workload runs, without a registry host,
    /// so a mirror does not hide it.
    pub image: String,
    pub entry: Option<EntryQueries>,
    pub entry_points: Option<EntryPoints>,
}

#[derive(Deserialize)]
struct Document {
    metadata: Metadata,
    spec: Spec,
}

#[derive(Deserialize)]
struct Metadata {
    name: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Spec {
    controller: String,
    image: String,
    entry: Option<queries::Written>,
    entry_points: Option<entry_points::Written>,
}

/// Returns the `IngressController` in `value`, or an error message
/// that says which field is invalid.
pub fn parse(doc: DocId, value: serde_yaml_ng::Value) -> Result<IngressController, String> {
    let document: Document = serde_yaml_ng::from_value(value).map_err(|error| error.to_string())?;
    let spec = document.spec;
    if spec.controller.trim().is_empty() {
        return Err("spec.controller is empty, and must name an IngressClass controller".into());
    }
    if spec.image.trim().is_empty() {
        return Err("spec.image is empty, and must name a container image".into());
    }
    Ok(IngressController {
        doc,
        name: document.metadata.name,
        controller: spec.controller,
        image: spec.image,
        entry: spec.entry.map(queries::read).transpose()?,
        entry_points: spec.entry_points.map(entry_points::read).transpose()?,
    })
}

#[cfg(test)]
pub mod fixtures {
    use super::*;

    /// An `IngressController` named `proxy` with `spec`.
    pub fn controller(spec: &str) -> IngressController {
        let text = format!("metadata: {{name: proxy}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap()).unwrap()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn read(spec: &str) -> Result<IngressController, String> {
        let text = format!("metadata: {{name: proxy}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap())
    }

    #[rstest]
    fn reads_the_controller_and_its_image() {
        let found = read("{controller: example.io/proxy, image: example/proxy}").unwrap();
        assert_eq!(
            (found.controller.as_str(), found.image.as_str(), found.entry),
            ("example.io/proxy", "example/proxy", None)
        );
    }

    #[rstest]
    #[case::no_image("{controller: example.io/proxy}", "image")]
    #[case::an_empty_controller("{controller: '', image: example/proxy}", "spec.controller")]
    #[case::an_unknown_field("{controller: a, image: b, extra: {}}", "unknown field")]
    fn rejects_an_invalid_spec_with_the_reason(#[case] spec: &str, #[case] message: &str) {
        let error = read(spec).unwrap_err();
        assert!(error.contains(message), "{error:?} lacks {message:?}");
    }
}
