//! Device knowledge describes a DRA driver's devices, in one resource
//! named for the driver, the same way that kind knowledge is named for
//! its CRD.  The resource names the attribute whose value is a device's
//! title, the attributes to show, and the attributes whose value names
//! something else.  Such a value names another device, by that device's
//! attribute with the same value, or an object, by the object's field
//! with the same value.

use serde::Deserialize;

use super::Devices;
use transect_graph::edges::{Device, Kind};
use transect_resources::resources::DocId;

/// The title of `device`: the value of the attribute that its driver's
/// knowledge names, or the device's own name.
pub fn title(
    knowledge: &[DeviceKnowledge],
    attributes: &[(String, String)],
    device: &Device,
) -> String {
    known_title(knowledge, attributes, device).unwrap_or_else(|| device.name.clone())
}

/// The value of the attribute that the driver's knowledge names as the
/// title of `device`, or `None` when there is no knowledge or the device
/// does not have the attribute.
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
    /// `device`'s title, from the attributes that its slice lists and
    /// the attribute that its driver's knowledge names.
    pub fn title(&self, device: &Device, knowledge: &[DeviceKnowledge]) -> String {
        title(knowledge, self.attributes(device), device)
    }

    /// The name of `device` for a line that names it beside other
    /// things: the first label of its driver's name and its title, such
    /// as "radio desk-remote".
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
    /// The attribute whose value is a device's title, in place of its
    /// name.
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
    /// The fact as a device's line shows it: "connected" for `true`,
    /// `when_false` or nothing for `false`, and "battery 80" for any
    /// other value.
    pub fn said(&self, value: &str) -> Option<String> {
        match value {
            "true" => Some(self.label.clone()),
            "false" => self.when_false.clone(),
            value => Some(format!("{} {value}", self.label)),
        }
    }
}

/// An attribute whose value names something else.
#[derive(Clone, Debug, PartialEq)]
pub struct DeviceReference {
    pub attribute: String,
    pub label: String,
    pub edge: Kind,
    pub target: Far,
}

/// What a device reference names, and the far attribute or field that
/// holds the same value.
#[derive(Clone, Debug, PartialEq)]
pub enum Far {
    /// A device of `driver` whose attribute `matched` holds the value.
    Device { driver: String, matched: String },
    /// An object of `group` and `kind` whose field `matched` holds it.
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
    references: Vec<Written>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Written {
    attribute: String,
    label: String,
    edge: Option<String>,
    driver: Option<String>,
    kind: Option<String>,
    group: Option<String>,
    #[serde(rename = "match")]
    matched: String,
}

/// The device knowledge `value` declares, or why it does not validate.
pub fn parse(doc: DocId, value: serde_yaml::Value) -> Result<DeviceKnowledge, String> {
    let document: Document = serde_yaml::from_value(value).map_err(|error| error.to_string())?;
    let references = document.spec.references.into_iter().enumerate();
    let references = references
        .map(|(at, written)| {
            reference(written).map_err(|error| format!("references[{at}]: {error}"))
        })
        .collect::<Result<_, _>>()?;
    Ok(DeviceKnowledge {
        doc,
        driver: document.metadata.name,
        title: document.spec.title,
        facts: document.spec.facts,
        references,
    })
}

fn reference(written: Written) -> Result<DeviceReference, String> {
    let target = match (written.driver, written.kind) {
        (Some(driver), None) => Far::Device {
            driver,
            matched: written.matched,
        },
        (None, Some(kind)) => Far::Object {
            group: written.group.unwrap_or_default(),
            kind,
            matched: written.matched,
        },
        (Some(_), Some(_)) => return Err("names both a driver and a kind".into()),
        (None, None) => return Err("names neither a driver nor a kind".into()),
    };
    let edge = match written.edge.as_deref() {
        None => Kind::DependsOn,
        Some(edge) => transect_resources::knowledge::edge_kind(edge).ok_or_else(|| {
            format!("edge {edge:?} is not one of the edge kinds kind knowledge names")
        })?,
    };
    Ok(DeviceReference {
        attribute: written.attribute,
        label: written.label,
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
        parse(DocId::default(), serde_yaml::from_str(&text).unwrap()).unwrap()
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn parsed(spec: &str) -> Result<DeviceKnowledge, String> {
        let text = format!("metadata: {{name: radio.example.com}}\nspec: {spec}\n");
        parse(DocId::default(), serde_yaml::from_str(&text).unwrap())
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
    #[case::true_says_the_label("true", Some("connected"))]
    #[case::false_says_when_false("false", Some("not connected"))]
    #[case::a_value_follows_the_label("80", Some("connected 80"))]
    fn says_a_fact_in_words(#[case] value: &str, #[case] said: Option<&str>) {
        let fact = Fact {
            attribute: "connected".into(),
            label: "connected".into(),
            when_false: Some("not connected".into()),
        };
        assert_eq!(fact.said(value).as_deref(), said);
    }

    #[rstest]
    fn leaves_out_a_false_fact_with_no_words_for_it() {
        let fact = Fact {
            attribute: "battery".into(),
            label: "reports its battery".into(),
            when_false: None,
        };
        assert_eq!(fact.said("false"), None);
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
                        target: Far::Device {
                            driver: "screen.example.com".into(),
                            matched: "monitor".into()
                        },
                    },
                    DeviceReference {
                        attribute: "address".into(),
                        label: "pairs as".into(),
                        edge: Kind::DependsOn,
                        target: Far::Object {
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
        "names both"
    )]
    #[case::neither("{references: [{attribute: a, match: m, label: l}]}", "names neither")]
    #[case::no_match("{references: [{attribute: a, driver: d, label: l}]}", "match")]
    #[case::an_unknown_field("{colour: blue}", "unknown field")]
    fn says_why_knowledge_does_not_validate(#[case] spec: &str, #[case] says: &str) {
        let error = parsed(spec).unwrap_err();
        assert!(error.contains(says), "{error:?} does not say {says:?}");
    }
}
