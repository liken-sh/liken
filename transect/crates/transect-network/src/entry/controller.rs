//! The `IngressController` resource.  It declares an ingress
//! controller by the identifier that its IngressClasses name
//! in `spec.controller`, the image its workload runs, the queries that
//! read the traffic it serves, and the ports it serves an `Ingress`
//! on.
//!
//! An `IngressClass` names its controller by an identifier, not by a
//! workload, and a controller that ships no CRD of its own has no kind
//! knowledge to describe it further.  So the controller is declared
//! once, and the entry finds the workload that runs its image, as kind
//! knowledge's `controller.image` finds an operator.

use serde::Deserialize;

use super::entry_points::{self, EntryPoints};
use super::queries::{self, EntryQueries};
use transect_resources::resources::DocId;

/// One `IngressController` resource, validated.
#[derive(Clone, Debug, PartialEq)]
pub struct IngressController {
    pub doc: DocId,
    pub name: String,
    /// The identifier an `IngressClass`'s `spec.controller` gives it.
    pub controller: String,
    /// The image repository its workload runs, without a registry host,
    /// so a mirror does not hide it.
    pub image: String,
    /// How to read the traffic of the Ingresses it serves.
    pub entry: Option<EntryQueries>,
    /// The ports it serves an `Ingress` on, when it declares them.
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

/// The `IngressController` `value` declares, or why it does not
/// validate.
pub fn parse(doc: DocId, value: serde_yaml::Value) -> Result<IngressController, String> {
    let document: Document = serde_yaml::from_value(value).map_err(|error| error.to_string())?;
    let spec = document.spec;
    if spec.controller.trim().is_empty() {
        return Err("controller names no IngressClass controller".into());
    }
    if spec.image.trim().is_empty() {
        return Err("image names no image".into());
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
        parse(DocId::default(), serde_yaml::from_str(&text).unwrap()).unwrap()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn read(spec: &str) -> Result<IngressController, String> {
        let text = format!("metadata: {{name: proxy}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml::from_str(&text).unwrap())
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
    #[case::an_empty_controller("{controller: '', image: example/proxy}", "controller names no")]
    #[case::an_unknown_field("{controller: a, image: b, frontDoor: {}}", "unknown field")]
    fn says_why_it_does_not_validate(#[case] spec: &str, #[case] says: &str) {
        let error = read(spec).unwrap_err();
        assert!(error.contains(says), "{error:?} does not say {says:?}");
    }
}
