//! Device knowledge describes the devices of one DRA driver.  It is one
//! resource named for the driver, as kind knowledge is named for its
//! CRD.  It declares which attribute holds a device's title, which
//! attributes to show, and which attributes refer to something else.
//! A reference finds its target by value: either another device whose
//! attribute has the same value, or an object whose field has the same
//! value.

use serde::Deserialize;

use super::Devices;
use transect_graph::edges::{Device, Kind};
use transect_resources::resources::DocId;

/// The title of `device`: the value of the attribute that its driver's
/// knowledge marks as the title, or else the device's name.
pub fn title(
    knowledge: &[DeviceKnowledge],
    attributes: &[(String, String)],
    device: &Device,
) -> String {
    known_title(knowledge, attributes, device).unwrap_or_else(|| device.name.clone())
}

/// The value of `device`'s title attribute, or `None` when its driver's
/// knowledge sets no title or the device lacks that attribute.
pub fn known_title(
    knowledge: &[DeviceKnowledge],
    attributes: &[(String, String)],
    device: &Device,
) -> Option<String> {
    let known = knowledge.iter().find(|k| k.driver == device.driver)?;
    let attribute = known.title.as_deref()?;
    let found = attributes.iter().find(|(n, _)| n == attribute)?;
    Some(found.1.clone())
}

impl Devices {
    pub fn title(&self, device: &Device, knowledge: &[DeviceKnowledge]) -> String {
        title(knowledge, self.attributes(device), device)
    }

    /// The name of `device` for a line that lists it among other
    /// objects: the first label of its driver's name, then its title, as
    /// in "radio desk-remote".
    pub fn named(&self, device: &Device, knowledge: &[DeviceKnowledge]) -> String {
        let driver = device.driver.split('.').next().unwrap_or(&device.driver);
        format!("{driver} {}", self.title(device, knowledge))
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct DeviceKnowledge {
    pub doc: DocId,
    /// The driver's name, as a `ResourceSlice`'s `spec.driver` gives it.
    pub driver: String,
    /// The attribute whose value is a device's title, shown in place of
    /// its name.
    pub title: Option<String>,
    /// The attributes to show, in order, each with its label.
    pub facts: Vec<Fact>,
    pub references: Vec<DeviceReference>,
}

#[derive(Clone, Debug, PartialEq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Fact {
    pub attribute: String,
    pub label: String,
    /// The text for a `false` value, such as "not connected".  When it is
    /// `None`, the line leaves out a `false` value.  A `true` value shows
    /// the label alone.
    #[serde(default, rename = "whenFalse")]
    pub when_false: Option<String>,
}

impl Fact {
    /// The fact's text for the attribute's `value`: the label for
    /// `true`, `when_false` for `false`, and the label and the value for
    /// any other value, as in "battery 80".
    pub fn text_for(&self, value: &str) -> Option<String> {
        match value {
            "true" => Some(self.label.clone()),
            "false" => self.when_false.clone(),
            value => Some(format!("{} {value}", self.label)),
        }
    }
}

/// An attribute whose value refers to a device or an object.
#[derive(Clone, Debug, PartialEq)]
pub struct DeviceReference {
    pub attribute: String,
    pub label: String,
    pub edge: Kind,
    pub target: ReferenceTarget,
}

/// What a device reference points at, and the attribute or field on the
/// target that has the same value as the reference's attribute.
#[derive(Clone, Debug, PartialEq)]
pub enum ReferenceTarget {
    /// A device of `driver` whose attribute `matched` has the value.
    Device { driver: String, matched: String },
    /// An object of `group` and `kind` whose field `matched` has the
    /// value.
    Object {
        group: String,
        kind: String,
        matched: String,
    },
}

#[derive(Deserialize)]
struct Document {
    metadata: Metadata,
    #[serde(default)]
    spec: Spec,
}

#[derive(Deserialize)]
struct Metadata {
    name: String,
}

#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct Spec {
    title: Option<String>,
    #[serde(default)]
    facts: Vec<Fact>,
    #[serde(default)]
    references: Vec<ReferenceSpec>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ReferenceSpec {
    attribute: String,
    label: String,
    edge: Option<String>,
    driver: Option<String>,
    kind: Option<String>,
    group: Option<String>,
    #[serde(rename = "match")]
    matched: String,
}

/// The device knowledge in `value`, or the reason that it is invalid.
pub fn parse(doc: DocId, value: serde_yaml_ng::Value) -> Result<DeviceKnowledge, String> {
    let document: Document = serde_yaml_ng::from_value(value).map_err(|error| error.to_string())?;
    let references = document.spec.references.into_iter().enumerate();
    let references = references
        .map(|(at, spec)| reference(spec).map_err(|error| format!("references[{at}]: {error}")))
        .collect::<Result<_, _>>()?;
    Ok(DeviceKnowledge {
        doc,
        driver: document.metadata.name,
        title: document.spec.title,
        facts: document.spec.facts,
        references,
    })
}

fn reference(spec: ReferenceSpec) -> Result<DeviceReference, String> {
    let target = match (spec.driver, spec.kind) {
        (Some(driver), None) => ReferenceTarget::Device {
            driver,
            matched: spec.matched,
        },
        (None, Some(kind)) => ReferenceTarget::Object {
            group: spec.group.unwrap_or_default(),
            kind,
            matched: spec.matched,
        },
        (Some(_), Some(_)) => {
            return Err("sets both driver and kind, and a reference needs one of them".into());
        }
        (None, None) => {
            return Err("sets neither driver nor kind, and a reference needs one of them".into());
        }
    };
    let edge = match spec.edge.as_deref() {
        None => Kind::DependsOn,
        Some(edge) => transect_resources::knowledge::edge_kind(edge).ok_or_else(|| {
            format!(
                "edge {edge:?} is not one of: runs on, stores on, holds, depends on, \
                 managed by, authenticates through, part of, reconciles from"
            )
        })?,
    };
    Ok(DeviceReference {
        attribute: spec.attribute,
        label: spec.label,
        edge,
        target,
    })
}

#[cfg(any(test, feature = "fixtures"))]
pub mod fixtures {
    use super::*;

    /// The device knowledge for `driver` with `spec`.
    pub fn knowledge(driver: &str, spec: &str) -> DeviceKnowledge {
        let text = format!("metadata: {{name: {driver}}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap()).unwrap()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn parsed(spec: &str) -> Result<DeviceKnowledge, String> {
        let text = format!("metadata: {{name: radio.example.com}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml_ng::from_str(&text).unwrap())
    }

    #[rstest]
    #[case::by_the_attribute_its_knowledge_names(Some("{title: alias}"), "desk-remote")]
    #[case::by_its_name_with_no_knowledge(None, "aa-bb")]
    #[case::by_its_name_when_the_attribute_is_missing(Some("{title: product}"), "aa-bb")]
    fn titles_a_device(#[case] spec: Option<&str>, #[case] expected: &str) {
        let known: Vec<DeviceKnowledge> =
            spec.map(|spec| parsed(spec).unwrap()).into_iter().collect();
        let device = Device {
            driver: "radio.example.com".into(),
            pool: "alpha".into(),
            name: "aa-bb".into(),
            machine: Some("alpha".into()),
        };
        let attributes = [("alias".to_string(), "desk-remote".to_string())];
        assert_eq!(title(&known, &attributes, &device), expected);
    }

    #[rstest]
    #[case::true_shows_the_label("true", Some("connected"))]
    #[case::false_shows_when_false("false", Some("not connected"))]
    #[case::a_value_follows_the_label("80", Some("connected 80"))]
    fn words_a_fact_by_its_value(#[case] value: &str, #[case] text: Option<&str>) {
        let fact = Fact {
            attribute: "connected".into(),
            label: "connected".into(),
            when_false: Some("not connected".into()),
        };
        assert_eq!(fact.text_for(value).as_deref(), text);
    }

    #[rstest]
    fn leaves_out_a_false_fact_with_no_words_for_it() {
        let fact = Fact {
            attribute: "battery".into(),
            label: "reports its battery".into(),
            when_false: None,
        };
        assert_eq!(fact.text_for("false"), None);
    }

    #[rstest]
    fn reads_the_title_facts_and_references() {
        let found = parsed(
            "{title: product, facts: [{attribute: battery, label: battery}], references: [{attribute: monitor, driver: screen.example.com, match: monitor, label: speakers of}, {attribute: address, kind: Pairing, group: radio.example.com, match: status.address, label: pairs as}]}",
        )
        .unwrap();
        assert_eq!(
            (found.title.as_deref(), found.facts.len(), found.references),
            (
                Some("product"),
                1,
                vec![
                    DeviceReference {
                        attribute: "monitor".into(),
                        label: "speakers of".into(),
                        edge: Kind::DependsOn,
                        target: ReferenceTarget::Device {
                            driver: "screen.example.com".into(),
                            matched: "monitor".into()
                        },
                    },
                    DeviceReference {
                        attribute: "address".into(),
                        label: "pairs as".into(),
                        edge: Kind::DependsOn,
                        target: ReferenceTarget::Object {
                            group: "radio.example.com".into(),
                            kind: "Pairing".into(),
                            matched: "status.address".into()
                        },
                    },
                ]
            )
        );
    }

    #[rstest]
    #[case::both(
        "{references: [{attribute: a, driver: d, kind: K, match: m, label: l}]}",
        "both"
    )]
    #[case::neither("{references: [{attribute: a, match: m, label: l}]}", "neither")]
    #[case::no_match("{references: [{attribute: a, driver: d, label: l}]}", "match")]
    #[case::an_unknown_field("{colour: blue}", "unknown field")]
    fn reports_why_knowledge_is_invalid(#[case] spec: &str, #[case] message: &str) {
        let error = parsed(spec).unwrap_err();
        assert!(
            error.contains(message),
            "the error is {error:?}, want it to contain {message:?}"
        );
    }
}
