//! Lists of intent written where they draw nothing.  A `depends-on` or
//! `authenticates-through` list draws edges only from a workload, or
//! from a `ConfigMap` its workloads read; on a `Service`, a `Namespace`,
//! or any other object, it is read and then ignored.  A list an overlay
//! or an `Intent` resource wrote gets a condition on that resource, so
//! the person who wrote it learns it draws nothing.  A list in the live
//! object's own annotation gets no condition, because `transect` has no
//! resource of its own to put the condition on.

use super::{Intent, Listed, Said};
use crate::resources::{Condition, DocId, Overlay};
use transect_graph::edges::{Object, Provenance, Stated};

/// One list field of what intent states about an object.
type Field = fn(&Said) -> Option<&Listed>;

/// The list fields that draw edges, by their names.
const LISTS: [(&str, Field); 2] = [
    ("depends-on", |said| said.depends_on.as_ref()),
    ("authenticates-through", |said| {
        said.authenticates_through.as_ref()
    }),
];

/// A condition for each list among `said` that is on an object
/// `attaches` reports draws no edges, on the overlay or `Intent` that
/// wrote it, among `overlays` and `intents`.
pub fn unattached<'a>(
    said: impl IntoIterator<Item = (&'a Object, &'a Said)>,
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
            let Some(doc) = written.by(&listed.provenance, object) else {
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

/// The resources of `transect`'s that write intent: the overlays and the
/// Intents.
#[derive(Clone, Copy)]
pub struct Written<'a> {
    pub overlays: &'a [Overlay],
    pub intents: &'a [Intent],
}

impl Written<'_> {
    /// The document of `transect`'s that wrote a list about `object`: the
    /// overlay of that object in the file, or the `Intent` of that name.
    fn by(self, provenance: &Provenance, object: &Object) -> Option<DocId> {
        match provenance {
            Provenance::Intent(Stated::Overlay { file, .. }) => self
                .overlays
                .iter()
                .find(|overlay| &overlay.file == file && overlay.names(object))
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

    fn said(depends_on: Option<Listed>, authenticates_through: Option<Listed>) -> Said {
        Said {
            depends_on,
            authenticates_through,
            ..Said::default()
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
        #[case] said: Said,
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
