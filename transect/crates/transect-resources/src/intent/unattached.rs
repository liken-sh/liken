//! Reports a `depends-on` or `authenticates-through` list that
//! `transect` ignores. Such a list produces edges only on a workload, or
//! on a `ConfigMap` that a workload reads. When an overlay or an
//! `Intent` wrote the list on any other object, such as a `Service`,
//! `transect` sets a condition on that overlay or `Intent`, so its
//! author learns that the list has no effect. A list in the object's
//! own annotation gets no condition, because there is no `transect`
//! resource to set it on.

use super::{Intent, Listed, ObjectIntent};
use crate::resources::{Condition, DocId, Overlay};
use transect_graph::edges::{Object, Provenance, Stated};

type Field = fn(&ObjectIntent) -> Option<&Listed>;

/// The list fields, by their annotation names.
const LISTS: [(&str, Field); 2] = [
    ("depends-on", |said| said.depends_on.as_ref()),
    ("authenticates-through", |said| {
        said.authenticates_through.as_ref()
    }),
];

/// A condition for each list in `said` on an object for which
/// `attaches` returns false, set on the overlay or `Intent` in `written`
/// that wrote the list.
pub fn unattached<'a>(
    said: impl IntoIterator<Item = (&'a Object, &'a ObjectIntent)>,
    written: Written,
    attaches: impl Fn(&Object) -> bool,
) -> Vec<(DocId, Condition)> {
    let mut conditions = Vec::new();
    for (object, said) in said {
        if attaches(object) {
            continue;
        }
        for (field, list) in LISTS {
            let Some(listed) = list(said) else {
                continue;
            };
            let Some(doc) = written.writer_of(&listed.provenance, object) else {
                continue;
            };
            let message = format!(
                "{field} on {} draws nothing: only a workload's list, or a list on a ConfigMap a workload reads, draws edges",
                transect_graph::links::kind_and_name_in(object, None)
            );
            conditions.push((doc, Condition::unattached(message)));
        }
    }
    conditions
}

/// The `transect` resources that can write intent: the overlays and the
/// `Intent` resources.
#[derive(Clone, Copy)]
pub struct Written<'a> {
    pub overlays: &'a [Overlay],
    pub intents: &'a [Intent],
}

impl Written<'_> {
    /// The document that wrote a list on `object`: the overlay for
    /// `object` in the file that `provenance` records, or the `Intent`
    /// that it gives by namespace and name. `None` for any other
    /// provenance, such as the object's own annotation.
    fn writer_of(self, provenance: &Provenance, object: &Object) -> Option<DocId> {
        match provenance {
            Provenance::Intent(Stated::Overlay { file, .. }) => self
                .overlays
                .iter()
                .find(|overlay| &overlay.file == file && overlay.is_for(object))
                .map(|overlay| overlay.doc),
            Provenance::Intent(Stated::Resource { name, .. }) => self
                .intents
                .iter()
                .find(|intent| format!("{}/{}", intent.namespace, intent.name) == *name)
                .map(|intent| intent.doc),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::super::fixtures::intent;
    use super::*;

    fn doc(document: usize) -> DocId {
        DocId { file: 0, document }
    }

    fn service() -> Object {
        Object::new("", "Service", Some("shop"), "web")
    }

    fn overlay() -> Overlay {
        Overlay {
            doc: doc(1),
            file: "overlays.yaml".into(),
            group: String::new(),
            kind: "Service".into(),
            namespace: Some("shop".into()),
            name: "web".into(),
            labels: Default::default(),
            annotations: Default::default(),
        }
    }

    fn resources() -> (Vec<Overlay>, Vec<Intent>) {
        let mut resource = intent("the-web", "{selects: [{resource: services, name: web}]}");
        resource.doc = doc(2);
        (vec![overlay()], vec![resource])
    }

    fn written(stated: Stated) -> Listed {
        Listed {
            entries: vec!["api.example.com".into()],
            provenance: Provenance::Intent(stated),
        }
    }

    fn by_overlay(field: &str) -> Listed {
        written(Stated::Overlay {
            field: field.into(),
            file: "overlays.yaml".into(),
        })
    }

    fn by_intent(field: &str) -> Listed {
        written(Stated::Resource {
            field: field.into(),
            name: "shop/the-web".into(),
        })
    }

    fn by_annotation(field: &str) -> Listed {
        written(Stated::Annotation {
            field: field.into(),
            on: "Service web".into(),
        })
    }

    fn said(depends_on: Option<Listed>, authenticates_through: Option<Listed>) -> ObjectIntent {
        ObjectIntent {
            depends_on,
            authenticates_through,
            ..ObjectIntent::default()
        }
    }

    const DEPENDS: &str = "depends-on on Service shop/web draws nothing: only a workload's list, or a list on a ConfigMap a workload reads, draws edges";
    const SIGNS_IN: &str = "authenticates-through on Service shop/web draws nothing: only a workload's list, or a list on a ConfigMap a workload reads, draws edges";

    #[rstest]
    #[case::from_an_overlay(said(Some(by_overlay("depends-on")), None), false, vec![(doc(1), DEPENDS)])]
    #[case::from_an_intent(said(None, Some(by_intent("authenticates-through"))), false, vec![(doc(2), SIGNS_IN)])]
    #[case::both_lists(
        said(Some(by_overlay("depends-on")), Some(by_intent("authenticates-through"))),
        false,
        vec![(doc(1), DEPENDS), (doc(2), SIGNS_IN)]
    )]
    #[case::from_the_live_annotation(said(Some(by_annotation("depends-on")), None), false, vec![])]
    #[case::on_an_object_that_attaches(said(Some(by_overlay("depends-on")), None), true, vec![])]
    #[case::no_list(said(None, None), false, vec![])]
    fn says_where_a_list_draws_nothing(
        #[case] said: ObjectIntent,
        #[case] attaches: bool,
        #[case] expected: Vec<(DocId, &str)>,
    ) {
        let object = service();
        let (overlays, intents) = resources();
        let written = Written {
            overlays: &overlays,
            intents: &intents,
        };
        let found = unattached([(&object, &said)], written, |_| attaches);
        let found: Vec<(DocId, &str)> = found
            .iter()
            .map(|(doc, condition)| (*doc, condition.message.as_str()))
            .collect();
        assert_eq!(found, expected);
    }
}
