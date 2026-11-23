//! Intent is what the people and agents who run a cluster declare about
//! its parts.  `transect` reads it from annotations on the objects,
//! from overlays that supply those annotations, and from `Intent`
//! resources.

mod object_intent;
mod resource;
mod secrets;
mod unattached;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::ObjectMeta;

pub use object_intent::{Docs, Intents, Line, Listed, ObjectIntent};
pub use resource::Intent;
#[cfg(any(test, feature = "fixtures"))]
pub use resource::fixtures;
pub use secrets::SecretIntent;
pub use unattached::{Written, unattached};

use crate::resources::{Condition, DocId};
use transect_graph::edges::{Object, Plurals};

/// A condition for each term of an `Intent`'s `selects` that matches no
/// object of `objects` in the `Intent`'s namespace.
pub fn unmatched(
    intents: &[Intent],
    objects: &[(Object, &ObjectMeta)],
    plurals: &Plurals,
) -> Vec<(DocId, Condition)> {
    let mut conditions = Vec::new();
    for intent in intents {
        for (at, select) in intent.selects.iter().enumerate() {
            let found = objects.iter().any(|(object, meta)| {
                intent.holds(object) && select.names(object, meta.labels.as_ref(), plurals)
            });
            if !found {
                let message = format!(
                    "selects[{at}], {}, matches nothing in {}",
                    select.resource, intent.namespace
                );
                conditions.push((intent.doc, Condition::unmatched(message)));
            }
        }
    }
    conditions
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::fixtures::{intent, plurals};
    use super::*;

    fn meta(labels: &str) -> ObjectMeta {
        serde_yaml_ng::from_str(&format!("labels: {{{labels}}}")).unwrap()
    }

    #[rstest]
    #[case::every_term_matches("{selects: [{resource: deployments, name: web}]}", vec![])]
    #[case::a_name_that_matches_nothing(
        "{selects: [{resource: deployments, name: web}, {resource: deployments, name: gone}]}",
        vec!["selects[1], deployments, matches nothing in shop"]
    )]
    #[case::labels_that_match_nothing(
        "{selects: [{resource: deployments, matchLabels: {tier: back}}]}",
        vec!["selects[0], deployments, matches nothing in shop"]
    )]
    #[case::a_resource_the_cluster_does_not_serve(
        "{selects: [{resource: widgets, name: web}]}",
        vec!["selects[0], widgets, matches nothing in shop"]
    )]
    fn names_each_term_that_matches_nothing(#[case] spec: &str, #[case] messages: Vec<&str>) {
        let web = meta("tier: front");
        let blog = meta("");
        let objects = [
            (Object::new("apps", "Deployment", Some("shop"), "web"), &web),
            (
                Object::new("apps", "Deployment", Some("blog"), "gone"),
                &blog,
            ),
        ];
        let found = unmatched(&[intent("db", spec)], &objects, &plurals());
        let found: Vec<String> = found.into_iter().map(|(_, c)| c.message).collect();
        assert_eq!(found, messages);
    }
}
