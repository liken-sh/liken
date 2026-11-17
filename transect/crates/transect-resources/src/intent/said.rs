//! What intent states about each object: its `why`, `gotcha`,
//! `provides`, `depends-on`, `authenticates-through`, `docs`, `data-on`,
//! `console`, `signs-in`, and `host-access`, read from its annotations,
//! from overlays that take the place of them, and from the `Intent`
//! resources that select it, each with where it was written.

use std::collections::HashMap;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

use super::resource::{Fields, Intent};
use crate::resources::{Condition, DocId, GROUP, Overlaid, annotation, field};
use transect_graph::edges::{Object, Plurals, Provenance, Stated};
use transect_graph::untrusted::{capped, link};

/// The longest a line of intent runs on a card, in characters.
const LINE_CHARS: usize = 300;

/// One line of stored text, and where it was written.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Line {
    pub text: String,
    pub provenance: Provenance,
}

/// Where to read more: a link when the value is plain `http` or
/// `https`, and the value as text otherwise.
#[derive(Clone, Debug, PartialEq)]
pub struct Docs {
    pub href: Option<String>,
    pub text: String,
    pub provenance: Provenance,
}

/// The entries of a list field such as `depends-on`, each as written,
/// and where the list was written.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Listed {
    pub entries: Vec<String>,
    pub provenance: Provenance,
}

/// What intent states about one object.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Said {
    pub why: Option<Line>,
    pub gotcha: Option<Line>,
    /// What the rest of the cluster loses when it goes, in a few words
    /// such as "order storage", which the cascade shows in place of its
    /// name.
    pub provides: Option<Line>,
    /// What it depends on that its configuration does not name.
    pub depends_on: Option<Listed>,
    /// The identity providers it signs its users in through.
    pub authenticates_through: Option<Listed>,
    pub docs: Option<Docs>,
    /// Where a CSI driver keeps its volumes' data, such as `machines`.
    pub data_on: Option<Line>,
    /// A CSI driver's console, as a URL template filled from its
    /// volumes' attributes and its StorageClasses' parameters.
    pub console: Option<Line>,
    /// How a workload signs its callers in itself, such as "OAuth, in
    /// the app", on the workload or on one route to it.
    pub signs_in: Option<Line>,
    /// Why a workload uses its machine's network, a host port, a host
    /// path, or a privileged container.
    pub host_access: Option<Line>,
}

/// What intent states about every object that has any, and the
/// conflicts among Intents.
#[derive(Debug, Default)]
pub struct Intents {
    said: HashMap<Object, Said>,
    pub conditions: Vec<(DocId, Condition)>,
}

impl Intents {
    /// The intent on each of `objects`, from its annotations, which
    /// `overlaid` reports an overlay may have set, and from `intents`.
    /// An annotation wins over an `Intent` resource, and of two `Intent`
    /// resources that set one field, the first by name is shown and
    /// both get a condition.
    pub fn read(
        objects: &[(Object, &ObjectMeta)],
        intents: &[Intent],
        overlaid: &Overlaid,
        plurals: &Plurals,
    ) -> Self {
        let mut sorted: Vec<&Intent> = intents.iter().collect();
        sorted.sort_by(|a, b| (&a.namespace, &a.name).cmp(&(&b.namespace, &b.name)));
        let mut found = Self::default();
        for (object, meta) in objects {
            let selecting: Vec<&Intent> = sorted
                .iter()
                .filter(|intent| intent.selects(object, meta.labels.as_ref(), plurals))
                .copied()
                .collect();
            let annotated = meta
                .annotations
                .iter()
                .flatten()
                .any(|(k, _)| field(k).is_some());
            if selecting.is_empty() && !annotated {
                continue;
            }
            let reading = Reading {
                object,
                meta,
                selecting: &selecting,
                overlaid,
            };
            let mut conditions = Vec::new();
            let mut read = |name, get: fn(&Fields) -> Option<String>| {
                reading.field(name, get, &mut conditions)
            };
            let why = read("why", |f| f.why.clone());
            let gotcha = read("gotcha", |f| f.gotcha.clone());
            let provides = read("provides", |f| f.provides.clone());
            let depends_on = read("depends-on", |f| joined(&f.depends_on));
            let authenticates_through = read("authenticates-through", |f| {
                joined(&f.authenticates_through)
            });
            let docs = read("docs", |f| f.docs.clone());
            // An `Intent` is namespaced, and a CSI driver is not, so only
            // an annotation or an overlay states where a driver's data is.
            let data_on = read("data-on", |_| None);
            let console = read("console", |_| None);
            let signs_in = read("signs-in", |f| f.signs_in.clone());
            let host_access = read("host-access", |f| f.host_access.clone());
            let line = |(text, provenance): (String, Provenance)| {
                Some(Line {
                    text: capped(&text, LINE_CHARS)?,
                    provenance,
                })
            };
            let said = Said {
                why: why.and_then(line),
                gotcha: gotcha.and_then(line),
                provides: provides.and_then(line),
                depends_on: depends_on.and_then(listed),
                authenticates_through: authenticates_through.and_then(listed),
                docs: docs.and_then(|(text, provenance)| {
                    Some(Docs {
                        href: link(&text),
                        text: capped(&text, LINE_CHARS)?,
                        provenance,
                    })
                }),
                data_on: data_on.and_then(line),
                console: console.and_then(line),
                signs_in: signs_in.and_then(line),
                host_access: host_access.and_then(line),
            };
            found.conditions.extend(conditions);
            if said != Said::default() {
                found.said.insert(object.clone(), said);
            }
        }
        found
    }

    pub fn of(&self, object: &Object) -> Option<&Said> {
        self.said.get(object)
    }

    /// Every object intent states something about, with what it states.
    pub fn iter(&self) -> impl Iterator<Item = (&Object, &Said)> {
        self.said.iter()
    }
}

/// One object being read.
struct Reading<'a> {
    object: &'a Object,
    meta: &'a ObjectMeta,
    /// The Intents that select it, by name.
    selecting: &'a [&'a Intent],
    overlaid: &'a Overlaid,
}

impl Reading<'_> {
    /// The value of the field `name`, from the annotation or else from
    /// the first `Intent` that sets it, with where it was written.
    fn field(
        &self,
        name: &str,
        get: fn(&Fields) -> Option<String>,
        conditions: &mut Vec<(DocId, Condition)>,
    ) -> Option<(String, Provenance)> {
        let (object, field) = (self.object, name.to_string());
        if let Some(value) = annotation(self.meta.annotations.as_ref(), name) {
            let key = format!("{GROUP}/{name}");
            let stated = match self.overlaid.file_of(object, &key) {
                Some(file) => Stated::Overlay {
                    field,
                    file: file.into(),
                },
                None => Stated::Annotation {
                    field,
                    on: transect_graph::edges::kind_and_name(&object.kind, &object.name),
                },
            };
            return Some((value.into(), Provenance::Intent(stated)));
        }
        let setting: Vec<&Intent> = self
            .selecting
            .iter()
            .filter(|intent| get(&intent.fields).is_some())
            .copied()
            .collect();
        let first = *setting.first()?;
        if setting.len() > 1 {
            let names: Vec<&str> = setting.iter().map(|intent| intent.name.as_str()).collect();
            let message = format!(
                "{name} on {} {} is set by {}; the first by name, {}, is shown",
                object.kind,
                object.name,
                names.join(" and "),
                first.name
            );
            for intent in &setting {
                conditions.push((intent.doc, Condition::conflict(message.clone())));
            }
        }
        let stated = Stated::Resource {
            field,
            name: format!("{}/{}", first.namespace, first.name),
        };
        Some((get(&first.fields)?, Provenance::Intent(stated)))
    }
}

/// An `Intent`'s list as one value, as an annotation writes it, or none
/// for an empty list.
fn joined(entries: &[String]) -> Option<String> {
    (!entries.is_empty()).then(|| entries.join("\n"))
}

/// The entries of a list's value, split on commas and newlines, each on
/// one line and cut to a card's length, or none when it names nothing.
fn listed((value, provenance): (String, Provenance)) -> Option<Listed> {
    let entries: Vec<String> = value
        .split([',', '\n'])
        .filter_map(|entry| capped(entry, LINE_CHARS))
        .collect();
    (!entries.is_empty()).then_some(Listed {
        entries,
        provenance,
    })
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use k8s_openapi::api::apps::v1::Deployment;
    use rstest::rstest;

    use super::super::fixtures::{intent, plurals};
    use super::*;
    use crate::resources::Overlay;

    fn web() -> Object {
        Object::new("apps", "Deployment", Some("shop"), "web")
    }

    fn meta(annotations: &str) -> ObjectMeta {
        let text = format!("{{name: web, namespace: shop, annotations: {{{annotations}}}}}");
        serde_yaml::from_str(&text).unwrap()
    }

    /// What intent states about `web`, annotated with `annotations`, given
    /// `intents`.
    fn read(annotations: &str, intents: &[Intent]) -> (Option<Said>, Vec<String>) {
        let meta = meta(annotations);
        let objects = [(web(), &meta)];
        let found = Intents::read(&objects, intents, &Overlaid::default(), &plurals());
        let messages = found.conditions.iter().map(|(_, c)| c.message.clone());
        (found.of(&web()).cloned(), messages.collect())
    }

    fn annotated(field: &str, text: &str) -> Line {
        Line {
            text: text.into(),
            provenance: Provenance::Intent(Stated::Annotation {
                field: field.into(),
                on: "Deployment web".into(),
            }),
        }
    }

    fn resourced(field: &str, name: &str, text: &str) -> Line {
        Line {
            text: text.into(),
            provenance: Provenance::Intent(Stated::Resource {
                field: field.into(),
                name: format!("shop/{name}"),
            }),
        }
    }

    const SELECTS_WEB: &str = "selects: [{resource: deployments, name: web}]";

    #[rstest]
    #[case::nothing("", vec![], None)]
    #[case::an_annotation(
        "transect.liken.sh/why: Sells things., transect.liken.sh/gotcha: Slow to start.",
        vec![],
        Some(Said { why: Some(annotated("why", "Sells things.")), gotcha: Some(annotated("gotcha", "Slow to start.")), ..Said::default() })
    )]
    #[case::another_prefix("example.com/why: Not ours.", vec![], None)]
    #[case::an_intent(
        "",
        vec![intent("store", &format!("{{{SELECTS_WEB}, why: Sells things.}}"))],
        Some(Said { why: Some(resourced("why", "store", "Sells things.")), ..Said::default() })
    )]
    #[case::an_intent_selecting_something_else(
        "",
        vec![intent("store", "{selects: [{resource: deployments, name: api}], why: Serves.}")],
        None
    )]
    #[case::the_annotation_wins(
        "transect.liken.sh/why: From the manifest.",
        vec![intent("store", &format!("{{{SELECTS_WEB}, why: From the Intent., gotcha: Slow.}}"))],
        Some(Said { why: Some(annotated("why", "From the manifest.")), gotcha: Some(resourced("gotcha", "store", "Slow.")), ..Said::default() })
    )]
    #[case::provides(
        "transect.liken.sh/provides: order storage",
        vec![],
        Some(Said { provides: Some(annotated("provides", "order storage")), ..Said::default() })
    )]
    #[case::provides_from_an_intent(
        "",
        vec![intent("store", &format!("{{{SELECTS_WEB}, provides: order storage}}"))],
        Some(Said { provides: Some(resourced("provides", "store", "order storage")), ..Said::default() })
    )]
    #[case::signs_in(
        "transect.liken.sh/signs-in: 'OAuth, in the app'",
        vec![],
        Some(Said { signs_in: Some(annotated("signs-in", "OAuth, in the app")), ..Said::default() })
    )]
    #[case::host_access(
        "transect.liken.sh/host-access: Finds devices by multicast.",
        vec![],
        Some(Said { host_access: Some(annotated("host-access", "Finds devices by multicast.")), ..Said::default() })
    )]
    #[case::signs_in_from_an_intent(
        "",
        vec![intent("store", &format!("{{{SELECTS_WEB}, signsIn: its own login page}}"))],
        Some(Said { signs_in: Some(resourced("signs-in", "store", "its own login page")), ..Said::default() })
    )]
    #[case::data_on(
        "transect.liken.sh/data-on: machines",
        vec![],
        Some(Said { data_on: Some(annotated("data-on", "machines")), ..Said::default() })
    )]
    #[case::console(
        "transect.liken.sh/console: 'https://{dsm}:5001'",
        vec![],
        Some(Said { console: Some(annotated("console", "https://{dsm}:5001")), ..Said::default() })
    )]
    #[case::text_on_one_line(
        "transect.liken.sh/why: \"Sells\\nthings.\\u0007\"",
        vec![],
        Some(Said { why: Some(annotated("why", "Sells things.")), ..Said::default() })
    )]
    fn reads_why_gotcha_and_provides(
        #[case] annotations: &str,
        #[case] intents: Vec<Intent>,
        #[case] expected: Option<Said>,
    ) {
        assert_eq!(read(annotations, &intents), (expected, vec![]));
    }

    #[rstest]
    fn of_two_intents_the_first_by_name_is_shown_and_both_are_told() {
        let intents = [
            intent("zeta", &format!("{{{SELECTS_WEB}, why: From zeta.}}")),
            intent("alpha", &format!("{{{SELECTS_WEB}, why: From alpha.}}")),
        ];
        let message =
            "why on Deployment web is set by alpha and zeta; the first by name, alpha, is shown";
        let (said, messages) = read("", &intents);
        assert_eq!(
            (said.unwrap().why, messages),
            (
                Some(resourced("why", "alpha", "From alpha.")),
                vec![message.to_string(), message.to_string()]
            )
        );
    }

    #[rstest]
    #[case::commas_and_newlines(
        "transect.liken.sh/depends-on: \"db.shop, https://api.example.com/v1\\n deployments/cache,\"",
        vec![],
        vec!["db.shop", "https://api.example.com/v1", "deployments/cache"]
    )]
    #[case::from_an_intent(
        "",
        vec![intent("store", &format!("{{{SELECTS_WEB}, dependsOn: [db.shop, \"queue:9092\"]}}"))],
        vec!["db.shop", "queue:9092"]
    )]
    #[case::nothing_named("transect.liken.sh/depends-on: \" , \"", vec![], vec![])]
    fn reads_depends_on_as_a_list(
        #[case] annotations: &str,
        #[case] intents: Vec<Intent>,
        #[case] entries: Vec<&str>,
    ) {
        let (said, _) = read(annotations, &intents);
        let found = said
            .and_then(|said| said.depends_on)
            .map(|listed| listed.entries);
        assert_eq!(found.unwrap_or_default(), entries);
    }

    #[rstest]
    #[case::an_annotation(
        "transect.liken.sh/authenticates-through: https://id.example.com/",
        vec![],
        vec!["https://id.example.com/"]
    )]
    #[case::from_an_intent(
        "",
        vec![intent("store", &format!("{{{SELECTS_WEB}, authenticatesThrough: [services/login]}}"))],
        vec!["services/login"]
    )]
    fn reads_authenticates_through_as_a_list(
        #[case] annotations: &str,
        #[case] intents: Vec<Intent>,
        #[case] entries: Vec<&str>,
    ) {
        let (said, _) = read(annotations, &intents);
        let found = said.and_then(|said| said.authenticates_through);
        assert_eq!(
            found.map(|listed| listed.entries).unwrap_or_default(),
            entries
        );
    }

    #[rstest]
    #[case::a_link("https://docs.example.com/web", Some("https://docs.example.com/web"))]
    #[case::not_a_link("javascript:alert(1)", None)]
    #[case::a_name("the runbook", None)]
    fn links_docs_only_when_plain_http(#[case] value: &str, #[case] href: Option<&str>) {
        let (said, _) = read(&format!("transect.liken.sh/docs: \"{value}\""), &[]);
        let docs = said.unwrap().docs.unwrap();
        assert_eq!((docs.href.as_deref(), docs.text.as_str()), (href, value));
    }

    #[rstest]
    fn an_overlay_names_its_file() {
        let overlay = Overlay {
            doc: DocId::default(),
            file: "overlays/web.yaml".into(),
            group: "apps".into(),
            kind: "Deployment".into(),
            namespace: Some("shop".into()),
            name: "web".into(),
            labels: Default::default(),
            annotations: [("transect.liken.sh/why".into(), "Sells things.".into())].into(),
        };
        let mut overlaid = Overlaid::new(Arc::new(vec![overlay]));
        let live: Arc<Deployment> =
            Arc::new(serde_yaml::from_str("metadata: {name: web, namespace: shop}").unwrap());
        let merged = overlaid.apply(vec![live]);
        let objects = [(web(), &merged[0].metadata)];
        let found = Intents::read(&objects, &[], &overlaid, &plurals());
        let why = found.of(&web()).and_then(|said| said.why.clone());
        let provenance = Provenance::Intent(Stated::Overlay {
            field: "why".into(),
            file: "overlays/web.yaml".into(),
        });
        assert_eq!(
            why,
            Some(Line {
                text: "Sells things.".into(),
                provenance
            })
        );
    }
}
