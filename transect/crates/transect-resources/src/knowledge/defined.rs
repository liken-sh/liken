//! What each custom kind's CRD states it is: the description at the top
//! of its schema, which the API server publishes with the kind.  A
//! workload of a custom kind with no `why` and no summary in its kind's
//! knowledge opens its card with it, since an operator that ships no
//! knowledge often still documents its kinds there.
//!
//! Code generators fill the description from the type's doc comment,
//! and an undocumented type gets "Widget is the Schema for the widgets
//! API", which states nothing a reader does not see in the kind's name.
//! That sentence is left out, so the card opens with the image's
//! description instead.

use std::collections::HashMap;
use std::sync::Arc;

use k8s_openapi::apiextensions_apiserver::pkg::apis::apiextensions::v1::{
    CustomResourceDefinition, CustomResourceDefinitionNames, CustomResourceDefinitionVersion,
};

use crate::intent::Line;
use transect_graph::edges::{Object, Provenance};
use transect_graph::untrusted::{capped, markdown};

/// The longest a CRD's description runs on a card, in characters, as
/// long as an image's description runs.
const DESCRIPTION_CHARS: usize = 200;

/// The description of each custom kind whose CRD gives one, and the
/// names of every custom kind, by group and kind.
#[derive(Debug, Default)]
pub struct Definitions {
    described: HashMap<(String, String), Definition>,
    names: HashMap<(String, String), Arc<[String]>>,
}

#[derive(Clone, Debug, PartialEq)]
struct Definition {
    /// The CRD's name, `<plural>.<group>`.
    crd: String,
    text: String,
}

impl Definitions {
    pub fn read<'a>(crds: impl IntoIterator<Item = &'a CustomResourceDefinition>) -> Self {
        let mut read = Self::default();
        for crd in crds {
            let (group, kind) = (&crd.spec.group, &crd.spec.names.kind);
            let key = (group.clone(), kind.clone());
            read.names.insert(key.clone(), names(&crd.spec.names));
            let text = described(&crd.spec.versions, kind);
            if let (Some(text), Some(crd)) = (text, crd.metadata.name.clone()) {
                read.described.insert(key, Definition { crd, text });
            }
        }
        read
    }

    /// Every name `group`'s `kind` goes by, lowercase: its kind, its
    /// singular, its plural, and its short names, as `kubectl` takes
    /// them.
    pub fn names(&self, group: &str, kind: &str) -> Arc<[String]> {
        let key = (group.to_string(), kind.to_string());
        self.names
            .get(&key)
            .cloned()
            .unwrap_or_else(|| Arc::new([]))
    }

    /// What `object`'s CRD states its kind is, as a card's opening line.
    pub fn of(&self, object: &Object) -> Option<Line> {
        let key = (object.group.clone(), object.kind.clone());
        let found = self.described.get(&key)?;
        Some(Line {
            text: found.text.clone(),
            provenance: Provenance::Definition {
                crd: found.crd.clone(),
            },
        })
    }
}

/// The first paragraph of the schema's description, as plain text, from
/// the version the API server stores, or else the first version that
/// has one.
fn described(versions: &[CustomResourceDefinitionVersion], kind: &str) -> Option<String> {
    let stored = versions.iter().filter(|version| version.storage);
    let others = versions.iter().filter(|version| !version.storage);
    let texts = stored.chain(others).filter_map(|version| {
        let schema = version.schema.as_ref()?.open_api_v3_schema.as_ref()?;
        schema.description.as_deref()
    });
    let text = texts.map(first_paragraph).find(|text| !text.is_empty())?;
    if generated(text, kind) {
        return None;
    }
    let plain = capped(&markdown::plain(text), usize::MAX)?;
    Some(sentences_within(&plain, DESCRIPTION_CHARS))
}

/// The whole sentences of `text` that fit in `chars` characters.  When
/// even the first does not fit, the first whole, up to twice `chars`,
/// since a reader would rather read a long sentence than half of one;
/// past that, `text` cut short with "…".
fn sentences_within(text: &str, chars: usize) -> String {
    if text.chars().count() <= chars {
        return text.to_string();
    }
    let ends = |head: &str| {
        let ends = head.match_indices(". ").map(|(at, _)| at + 1);
        ends.chain(head.ends_with('.').then_some(head.len()))
            .collect::<Vec<_>>()
    };
    let head: String = text.chars().take(chars).collect();
    if let Some(end) = ends(&head).last() {
        return head[..*end].to_string();
    }
    let longer: String = text.chars().take(2 * chars).collect();
    match ends(&longer).first() {
        Some(end) => longer[..*end].to_string(),
        None => capped(text, chars).unwrap_or_default(),
    }
}

/// The names in a CRD's `names`, lowercase, each once.
fn names(names: &CustomResourceDefinitionNames) -> Arc<[String]> {
    let kind = std::iter::once(&names.kind).chain(names.singular.as_ref());
    let all = kind
        .chain([&names.plural])
        .chain(names.short_names.iter().flatten());
    let mut found: Vec<String> = Vec::new();
    for name in all.map(|name| name.to_lowercase()) {
        if !name.is_empty() && !found.contains(&name) {
            found.push(name);
        }
    }
    found.into()
}

fn first_paragraph(text: &str) -> &str {
    let text = text.trim();
    text.split("\n\n").next().unwrap_or(text).trim()
}

/// Whether `text` is the sentence a code generator writes for a type
/// with no doc comment: "Widget is the Schema for the widgets API".
fn generated(text: &str, kind: &str) -> bool {
    let text = text.trim_end_matches('.');
    let rest = text.strip_prefix(kind).and_then(|rest| {
        let rest = rest.strip_prefix(" is the Schema for the ")?;
        rest.strip_suffix(" API")
    });
    rest.is_some()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    /// The CRD of `Widget` in `widgets.example.com`, whose versions
    /// `versions` gives in YAML.
    fn crd(versions: &str) -> CustomResourceDefinition {
        serde_yaml::from_str(&format!(
            "metadata: {{name: widgets.widgets.example.com}}\n\
             spec: {{group: widgets.example.com, scope: Namespaced,\
             names: {{kind: Widget, plural: widgets}}, versions: {versions}}}"
        ))
        .unwrap()
    }

    fn version(name: &str, storage: bool, description: &str) -> String {
        format!(
            "{{name: {name}, served: true, storage: {storage}, \
             schema: {{openAPIV3Schema: {{type: object, description: {description:?}}}}}}}"
        )
    }

    fn opening(versions: &str) -> Option<String> {
        let definitions = Definitions::read([&crd(versions)]);
        let widget = Object::new("widgets.example.com", "Widget", Some("shop"), "one");
        definitions.of(&widget).map(|line| line.text)
    }

    #[rstest]
    #[case::the_stored_version(
        format!("[{}, {}]", version("v1beta1", false, "Old words."), version("v1", true, "Turns things.")),
        Some("Turns things.")
    )]
    #[case::another_when_the_stored_one_has_none(
        format!("[{}, {{name: v1, served: true, storage: true}}]", version("v1beta1", false, "Turns things.")),
        Some("Turns things.")
    )]
    #[case::its_first_paragraph(
        format!("[{}]", version("v1", true, "A `Widget` turns\nthings.\n\nIts spec says how.")),
        Some("A Widget turns things.")
    )]
    #[case::not_the_generated_sentence(
        format!("[{}]", version("v1", true, "Widget is the Schema for the widgets API.")),
        None
    )]
    #[case::none_written("[{name: v1, served: true, storage: true}]".into(), None)]
    fn opens_with_what_its_crd_says(#[case] versions: String, #[case] expected: Option<&str>) {
        assert_eq!(opening(&versions).as_deref(), expected);
    }

    #[rstest]
    #[case::short_enough("One. Two.", 20, "One. Two.")]
    #[case::the_sentences_that_fit("One sentence. Another that runs long.", 20, "One sentence.")]
    #[case::a_long_first_sentence_whole("A sentence too long. Two.", 10, "A sentence too long.")]
    #[case::a_first_sentence_far_too_long(
        "A sentence far too long to fit at all.",
        10,
        "A sentenc…"
    )]
    fn cuts_at_the_last_whole_sentence(
        #[case] text: &str,
        #[case] chars: usize,
        #[case] expected: &str,
    ) {
        assert_eq!(sentences_within(text, chars), expected);
    }

    #[rstest]
    #[case::kind_and_plural("{kind: Widget, plural: widgets}", vec!["widget", "widgets"])]
    #[case::singular_once("{kind: Widget, singular: widget, plural: widgets}", vec!["widget", "widgets"])]
    #[case::short_names(
        "{kind: Widget, singular: widget, plural: widgets, shortNames: [wd, wdg]}",
        vec!["widget", "widgets", "wd", "wdg"]
    )]
    fn names_a_kind_as_kubectl_takes_it(#[case] names: &str, #[case] expected: Vec<&str>) {
        let crd: CustomResourceDefinition = serde_yaml::from_str(&format!(
            "metadata: {{name: widgets.widgets.example.com}}\n\
             spec: {{group: widgets.example.com, scope: Namespaced, names: {names}, versions: []}}"
        ))
        .unwrap();
        let names = Definitions::read([&crd]).names("widgets.example.com", "Widget");
        assert_eq!(
            names.iter().map(String::as_str).collect::<Vec<_>>(),
            expected
        );
    }

    #[rstest]
    fn names_the_crd_it_read() {
        let crd = crd(&format!("[{}]", version("v1", true, "Turns things.")));
        let widget = Object::new("widgets.example.com", "Widget", Some("shop"), "one");
        let line = Definitions::read([&crd]).of(&widget).unwrap();
        assert_eq!(
            line.provenance.title(),
            "from its CRD widgets.widgets.example.com"
        );
    }
}
