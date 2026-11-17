//! The custom kinds other parts of the cluster name for a watch: those
//! kind knowledge reads the fields of, the far kinds of its references
//! by value, and those RBAC lets a controller write the status of, and
//! the fields a trimmed watch keeps for a GitOps tool's kinds.

use super::GroupKind;
use crate::ownership::{Grants, custom};
use std::collections::{BTreeMap, BTreeSet};
use transect_graph::edges::ClusterScoped;
use transect_graph::edges::{Object, Plurals};
use transect_resources::knowledge::{KindKnowledge, Kinds};

/// The kinds that `knowledge` describes, that the cluster
/// serves, and whose knowledge reads fields a trimmed watch drops, by
/// group and kind.  Knowledge that reads only what a trimmed object
/// keeps, such as a summary and the image of its controller, leaves
/// its kind trimmed, so a kind of many large objects, such as a
/// scanner's reports, is not held whole for a line of text.
pub(super) fn described(
    knowledge: &[KindKnowledge],
    device_kinds: &[GroupKind],
    plurals: &Plurals,
) -> Vec<GroupKind> {
    let kinds = Kinds::resolve(knowledge, plurals, &[]);
    let reading = kinds
        .kinds()
        .filter(|(group, kind)| kinds.of(group, kind).is_some_and(|k| k.reads_fields()));
    let matched = matched(&kinds)
        .chain(device_kinds.iter().cloned())
        .filter(|group_kind| plurals.contains_key(group_kind));
    let mut found: Vec<GroupKind> = reading.cloned().chain(matched).collect();
    found.sort();
    found.dedup();
    found
}

/// The fields that the GitOps and findings knowledge in `knowledge`
/// reads of each kind the cluster serves, which a trimmed watch keeps,
/// by group and kind.  A
/// tool's kinds are watched trimmed with these, so an applier that
/// lists every object it applied, or grows a history on each
/// reconcile, is not held whole for its source and its revision.
pub(super) fn kept(
    knowledge: &[KindKnowledge],
    plurals: &Plurals,
) -> BTreeMap<GroupKind, Vec<String>> {
    let kinds = Kinds::resolve(knowledge, plurals, &[]);
    let fields = kinds.kinds().filter_map(|(group, kind)| {
        let paths = kinds.of(group, kind)?.kept_paths();
        (!paths.is_empty()).then(|| ((group.clone(), kind.clone()), paths))
    });
    fields.collect()
}

/// The far kind of each reference by value, which is read whole,
/// because the field a match reads is one a trimmed watch drops.
fn matched(kinds: &Kinds) -> impl Iterator<Item = GroupKind> + '_ {
    let known = kinds
        .kinds()
        .filter_map(|(group, kind)| kinds.of(group, kind));
    known.flat_map(|known| known.matched().map(|(group, kind, _)| (group, kind)))
}

/// The custom kinds outside any namespace whose status RBAC lets some
/// `ServiceAccount` write, by group and kind.  Such an object runs no
/// pods and is no namespace's workload, so nothing else leads to a
/// watch on it, yet its controller's card should list it.  Kubernetes's
/// own groups, such as the one that serves CustomResourceDefinitions,
/// are left out, because their controllers are part of the control
/// plane.
pub(super) fn controlled(
    grants: &Grants,
    plurals: &Plurals,
    scoped: &ClusterScoped,
) -> Vec<GroupKind> {
    let granted: BTreeSet<&(String, String)> = grants.kinds().collect();
    let kinds = plurals
        .iter()
        .filter(|((group, _), plural)| granted.contains(&(group.clone(), (*plural).clone())));
    kinds
        .map(|(group_kind, _)| group_kind.clone())
        .filter(|group_kind| scoped.contains(group_kind))
        .filter(|(group, kind)| custom(&Object::new(group, kind, None, "")))
        .filter(|(group, _)| group != "k8s.io" && !group.ends_with(".k8s.io"))
        .collect()
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;
    use transect_resources::knowledge::fixtures::knowledge;

    fn plurals(kinds: &[(&str, &str)]) -> Plurals {
        let kinds = kinds.iter().map(|(kind, plural)| {
            let key = ("radios.example.io".to_string(), kind.to_string());
            (key, plural.to_string())
        });
        kinds.collect()
    }

    #[rstest]
    #[case::the_far_kind_served(&[("Handset", "handsets"), ("Radio", "radios")], &["Handset", "Radio"])]
    #[case::the_far_kind_not_served(&[("Handset", "handsets")], &["Handset"])]
    fn reads_the_far_kind_of_a_match_whole(
        #[case] served: &[(&str, &str)],
        #[case] whole: &[&str],
    ) {
        let spec = "{references: [{path: status.radio, kind: Radio, group: radios.example.io, match: status.address}]}";
        let knowledge = [knowledge("handsets.radios.example.io", spec)];
        let found = described(&knowledge, &[], &plurals(served));
        let found: Vec<&str> = found.iter().map(|(_, kind)| kind.as_str()).collect();
        assert_eq!(found, whole);
    }
}
