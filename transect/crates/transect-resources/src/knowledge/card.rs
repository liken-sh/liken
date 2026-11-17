//! The section that ends the card of a custom resource whose kind has
//! knowledge: which controller's knowledge it is read through, what
//! the kind is, the condition that means it works, and what its fields
//! name.

use super::KindKnowledge;
use super::references::{Names, Relation};
use crate::intent::{Docs, Line};
use transect_graph::edges::{Endpoint, Object, Plurals, Provenance};
use transect_graph::links::Reference;
use transect_graph::untrusted::{capped, link};

/// The longest a docs value runs on a card, in characters.
const DOCS_CHARS: usize = 300;

#[derive(Clone, Debug, PartialEq)]
pub struct KnownThrough {
    /// The controller's workload, linked to its page, or none when
    /// attribution found no single one.
    pub controller: Option<Reference>,
    /// The kind knowledge's name, `<plural>.<group>`.
    pub name: String,
    /// The summary, when the card does not already open with it.
    pub summary: Option<String>,
    pub healthy: Option<String>,
    pub docs: Option<Docs>,
    /// What the object's fields name, in the order the knowledge lists
    /// its references.
    pub related: Vec<Related>,
}

/// One relation as a card lists it: "plays through Receiver amp, from
/// status.receiver.name".
#[derive(Clone, Debug, PartialEq)]
pub struct Related {
    pub label: String,
    pub reference: Reference,
    /// "from status.receiver.name".
    pub source: String,
    pub provenance: Provenance,
}

impl Related {
    /// `relation` of an object in `namespace`, linked when what it names
    /// has a page: a pod, or one of `has_page`'s objects.
    pub fn of(
        relation: &Relation,
        namespace: Option<&str>,
        plurals: &Plurals,
        has_page: impl Fn(&Object) -> bool,
    ) -> Self {
        let reference = match &relation.names {
            Names::Object(object) => {
                let mut reference =
                    Reference::to_end(&Endpoint::Object(object.clone()), namespace, plurals);
                reference.href = page(object, plurals, has_page);
                reference
            }
            Names::Address | Names::Value => Reference {
                text: relation.value.clone(),
                href: None,
            },
            Names::Ambiguous(count) => Reference {
                text: format!("{} (matches {count} objects)", relation.value),
                href: None,
            },
        };
        Self {
            label: relation.label.clone(),
            reference,
            source: relation.source(),
            provenance: relation.provenance.clone(),
        }
    }
}

/// The page of `object`, when it has one.  A pod's page has its own
/// URL, apart from the one other kinds share, and a `Node`'s page is its
/// machine's.
pub fn page(
    object: &Object,
    plurals: &Plurals,
    has_page: impl Fn(&Object) -> bool,
) -> Option<String> {
    if (object.group.as_str(), object.kind.as_str()) == ("", "Node") {
        return Some(format!("/machines/{}", object.name));
    }
    match (object.group.as_str(), object.kind.as_str()) {
        ("", "Pod") => {
            let namespace = object.namespace.as_deref()?;
            Some(format!("/namespaces/{namespace}/pods/{}", object.name))
        }
        _ => has_page(object)
            .then(|| transect_graph::links::page(object, plurals))
            .flatten(),
    }
}

impl KnownThrough {
    /// The section for an object of the kind `knowledge` describes,
    /// whose controller is `controller`.
    pub fn of(knowledge: &KindKnowledge, controller: Option<Reference>) -> Self {
        let docs = knowledge.docs.as_deref().and_then(|docs| {
            Some(Docs {
                href: link(docs),
                text: capped(docs, DOCS_CHARS)?,
                provenance: knowledge.provenance("docs"),
            })
        });
        Self {
            controller,
            name: knowledge.name.clone(),
            summary: knowledge.summary.clone(),
            healthy: knowledge.healthy.clone(),
            docs,
            related: Vec::new(),
        }
    }

    /// The section with what the object's fields name.
    pub fn with_related(self, related: Vec<Related>) -> Self {
        Self { related, ..self }
    }

    /// Where every mark in the section came from.
    pub fn provenances(&self) -> impl Iterator<Item = &Provenance> {
        let docs = self.docs.iter().map(|docs| &docs.provenance);
        docs.chain(self.related.iter().map(|related| &related.provenance))
    }

    /// The section without what the object's fields name, for the
    /// object's own page, whose board draws each of them already.
    pub fn without_related(self) -> Self {
        Self {
            related: Vec::new(),
            ..self
        }
    }

    /// The mark the heading has for every line, which all come
    /// from the one kind knowledge when there are two or more.
    pub fn shared(&self) -> Option<Provenance> {
        let related = self.related.iter().map(|related| Some(&related.provenance));
        let docs = self.docs.iter().map(|docs| Some(&docs.provenance));
        transect_graph::edges::provenance::shared(related.chain(docs))
    }

    /// The summary as a card's opening line.
    pub fn opening(&self) -> Option<Line> {
        Some(Line {
            text: self.summary.clone()?,
            provenance: Provenance::Kinds {
                knowledge: self.name.clone(),
                field: "summary".into(),
            },
        })
    }

    /// The section without its summary, for a card that opens with it.
    pub fn without_summary(self) -> Self {
        Self {
            summary: None,
            ..self
        }
    }

    /// What it is known through: the controller, "media-operator", or
    /// the knowledge's name when no single controller is known, which
    /// has no page.
    pub fn through(&self) -> Reference {
        let named = Reference {
            text: self.name.clone(),
            href: None,
        };
        self.controller.clone().unwrap_or(named)
    }

    /// The summary and the condition that means the object works, as
    /// "A cache that keeps sessions. Healthy when its Ready condition is
    /// True.", or none when the knowledge declares neither.
    pub fn body(&self) -> Option<String> {
        let healthy = self
            .healthy
            .as_ref()
            .map(|condition| format!("Healthy when its {condition} condition is True."));
        let parts: Vec<String> = [self.summary.clone(), healthy]
            .into_iter()
            .flatten()
            .collect();
        (!parts.is_empty()).then(|| parts.join(" "))
    }

    /// Where the section was read, "from the KindKnowledge
    /// caches.cache.example.io".
    pub fn source(&self) -> String {
        format!("from the KindKnowledge {}", self.name)
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::{knowledge, plurals};
    use super::*;

    fn known(spec: &str, controller: Option<&str>) -> KnownThrough {
        let controller = controller.map(|name| Reference {
            text: name.into(),
            href: Some(format!("/namespaces/system/deployments/{name}")),
        });
        KnownThrough::of(&knowledge("widgets.widgets.example.com", spec), controller)
    }

    #[rstest]
    #[case::a_controller(
        Some("widget-operator"),
        ("widget-operator", Some("/namespaces/system/deployments/widget-operator"))
    )]
    #[case::no_single_controller(None, ("widgets.widgets.example.com", None))]
    fn names_what_it_is_known_through(
        #[case] controller: Option<&str>,
        #[case] expected: (&str, Option<&str>),
    ) {
        let through = known("{}", controller).through();
        assert_eq!((through.text.as_str(), through.href.as_deref()), expected);
    }

    #[rstest]
    #[case::both(
        "{summary: Turns things., healthy: {condition: Ready}}",
        Some("Turns things. Healthy when its Ready condition is True.")
    )]
    #[case::a_summary_only("{summary: Turns things.}", Some("Turns things."))]
    #[case::a_condition_only(
        "{healthy: {condition: Spinning}}",
        Some("Healthy when its Spinning condition is True.")
    )]
    #[case::neither("{}", None)]
    fn says_what_the_kind_is_and_when_it_works(#[case] spec: &str, #[case] body: Option<&str>) {
        assert_eq!(known(spec, None).body().as_deref(), body);
    }

    #[rstest]
    fn leaves_out_the_summary_a_card_opens_with() {
        let known = known(
            "{summary: Turns things., healthy: {condition: Ready}}",
            None,
        );
        let opening = known.opening().map(|line| line.text);
        let body = known.without_summary().body();
        assert_eq!(
            (opening.as_deref(), body.as_deref()),
            (
                Some("Turns things."),
                Some("Healthy when its Ready condition is True.")
            )
        );
    }

    #[rstest]
    fn marks_the_heading_once_for_lines_from_one_knowledge() {
        let related = |field: &str| Related {
            label: "plays through".into(),
            reference: Reference {
                text: "amp".into(),
                href: None,
            },
            source: format!("from {field}"),
            provenance: Provenance::Kinds {
                knowledge: "widgets.widgets.example.com".into(),
                field: field.into(),
            },
        };
        let known = known("{docs: 'https://widgets.example.com'}", None)
            .with_related(vec![related("spec.a"), related("status.b")]);
        let titles = (
            known.shared().map(|p| p.title()),
            known.clone().without_related().shared(),
        );
        assert_eq!(
            titles,
            (
                Some("from the KindKnowledge widgets.widgets.example.com".into()),
                None
            )
        );
    }

    #[rstest]
    #[case::a_link(
        "{docs: 'https://widgets.example.com/docs'}",
        Some("https://widgets.example.com/docs")
    )]
    #[case::a_name_that_is_not_a_link("{docs: 'widgets:declare-a-widget'}", None)]
    fn links_docs_only_when_they_are_http(#[case] spec: &str, #[case] href: Option<&str>) {
        let docs = known(spec, None).docs.unwrap();
        assert_eq!(docs.href.as_deref(), href);
    }

    fn relation(names: Names, value: &str) -> Relation {
        Relation {
            label: "plays through".into(),
            path: "status.receiver.name".into(),
            edge: None,
            value: value.into(),
            names,
            provenance: Provenance::default(),
        }
    }

    fn widget(namespace: Option<&str>, name: &str) -> Names {
        Names::Object(Object::new(
            "widgets.example.com",
            "Widget",
            namespace,
            name,
        ))
    }

    fn pod(name: &str) -> Names {
        Names::Object(Object::new("", "Pod", Some("shop"), name))
    }

    #[rstest]
    #[case::an_object_with_a_page(
        widget(Some("shop"), "amp"),
        "amp",
        "Widget amp",
        Some("/namespaces/shop/widgets.widgets.example.com/amp")
    )]
    #[case::an_object_with_none(widget(Some("shop"), "spare"), "spare", "Widget spare", None)]
    #[case::an_object_in_another_namespace(
        widget(Some("blog"), "amp"),
        "amp",
        "Widget blog/amp",
        None
    )]
    #[case::a_pod(pod("db-1"), "db-1", "Pod db-1", Some("/namespaces/shop/pods/db-1"))]
    #[case::a_node(
        Names::Object(Object::new("", "Node", None, "alpha")),
        "alpha",
        "Node alpha",
        Some("/machines/alpha")
    )]
    #[case::an_address(Names::Address, "broker:1883", "broker:1883", None)]
    #[case::a_value(Names::Value, "tv-1", "tv-1", None)]
    fn lists_what_each_field_names(
        #[case] names: Names,
        #[case] value: &str,
        #[case] text: &str,
        #[case] href: Option<&str>,
    ) {
        let has_page =
            |object: &Object| object.name == "amp" && object.namespace.as_deref() == Some("shop");
        let related = Related::of(&relation(names, value), Some("shop"), &plurals(), has_page);
        assert_eq!(
            (
                related.label.as_str(),
                related.reference.text.as_str(),
                related.reference.href.as_deref(),
                related.source.as_str()
            ),
            ("plays through", text, href, "from status.receiver.name")
        );
    }
}
