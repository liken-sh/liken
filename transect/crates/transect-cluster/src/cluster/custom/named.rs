//! Chooses custom kinds to watch for a reason other than owning pods or
//! workloads: kind knowledge that reads fields a trimmed watch drops, a
//! reference that matches objects of the kind by a field value, and
//! RBAC that lets a controller write the status of a cluster-scoped
//! kind. `kept` lists the fields that a trimmed watch keeps for the
//! GitOps and findings kinds.

use super::GroupKind;
use crate::ownership::{Grants, custom};
use std::collections::{BTreeMap, BTreeSet};
use transect_graph::edges::ClusterScoped;
use transect_graph::edges::{Object, Plurals};
use transect_resources::knowledge::{KindKnowledge, Kinds};

/// The kinds the cluster serves that must be watched whole: each kind
/// whose knowledge reads fields that a trimmed watch drops, each kind
/// that a reference matches by a field value, and each of
/// `device_kinds`. Knowledge that reads only what a trimmed object
/// keeps, such as a summary and the controller's image, leaves its kind
/// trimmed, so a kind with many large objects, such as a scanner's
/// reports, is not stored whole for one line of text.
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

/// The fields, by group and kind, that the GitOps and findings
/// knowledge in `knowledge` reads from each kind the cluster serves. A
/// trimmed watch keeps these fields, so a GitOps kind whose objects list
/// every object they applied, or keep a history of reconciles, is not
/// stored whole only for its source and revision.
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

/// The kind of each reference that matches objects by a field value.
/// The field it matches on is one that a trimmed watch drops.
fn matched(kinds: &Kinds) -> impl Iterator<Item = GroupKind> + '_ {
    let known = kinds
        .kinds()
        .filter_map(|(group, kind)| kinds.of(group, kind));
    known.flat_map(|known| known.matched().map(|(group, kind, _)| (group, kind)))
}

/// The cluster-scoped custom kinds, by group and kind, whose status
/// RBAC lets some `ServiceAccount` write. Such an object usually owns
/// nothing in a namespace, so no owner reference leads to it, but its
/// controller's card lists it. The `k8s.io` groups, such as the one
/// that serves `CustomResourceDefinition`s, are left out because the
/// control plane runs their controllers.
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
    #[case::the_matched_kind_served(&[("Handset", "handsets"), ("Radio", "radios")], &["Handset", "Radio"])]
    #[case::the_matched_kind_not_served(&[("Handset", "handsets")], &["Handset"])]
    fn watches_the_matched_kind_whole(#[case] served: &[(&str, &str)], #[case] whole: &[&str]) {
        let spec = "{references: [{path: status.radio, kind: Radio, group: radios.example.io, match: status.address}]}";
        let knowledge = [knowledge("handsets.radios.example.io", spec)];
        let found = described(&knowledge, &[], &plurals(served));
        let found: Vec<&str> = found.iter().map(|(_, kind)| kind.as_str()).collect();
        assert_eq!(found, whole);
    }
}
