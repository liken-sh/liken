//! Separates custom owner kinds from built-in ones, and lists the
//! custom kinds that need a watch so that an owner chain can be
//! followed through them.

use std::collections::BTreeMap;

use k8s_openapi::apimachinery::pkg::apis::meta::v1::OwnerReference;

use transect_graph::edges::Object;

/// The core group and the groups of the built-in workloads. An owner
/// kind of these groups needs no custom watch, because `transect`
/// already watches it or never follows an owner chain through it.
const BUILT_IN_GROUPS: [&str; 3] = ["", "apps", "batch"];

/// The group of an `apiVersion`: `apps` for `apps/v1`, and the empty
/// core group for `v1`.
pub fn group_of(api_version: &str) -> &str {
    api_version
        .rsplit_once('/')
        .map_or("", |(group, _version)| group)
}

/// Whether `object`'s group is outside `BUILT_IN_GROUPS`. A kind of
/// another built-in group, such as `networking.k8s.io`, counts as
/// custom too.
pub fn custom(object: &Object) -> bool {
    !BUILT_IN_GROUPS.contains(&object.group.as_str())
}

/// The custom kinds that `owners` refer to, by group and kind, each with
/// the first `apiVersion` that an owner reference gave it. The version
/// is only for API discovery to look the kind up. A kind gets one watch
/// even when owner references use several versions, because a watch at
/// one version returns every object of the kind.
pub fn kinds_to_watch<'a>(
    owners: impl IntoIterator<Item = &'a OwnerReference>,
) -> BTreeMap<(String, String), String> {
    let mut kinds = BTreeMap::new();
    for owner in owners {
        let group = group_of(&owner.api_version);
        if BUILT_IN_GROUPS.contains(&group) {
            continue;
        }
        kinds
            .entry((group.to_string(), owner.kind.clone()))
            .or_insert_with(|| owner.api_version.clone());
    }
    kinds
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::a_named_group("apps/v1", "apps")]
    #[case::the_core_group("v1", "")]
    #[case::a_custom_group("cache.example.io/v1beta1", "cache.example.io")]
    fn reads_the_group(#[case] api_version: &str, #[case] group: &str) {
        assert_eq!(group_of(api_version), group);
    }

    #[rstest]
    #[case::a_deployment("apps", false)]
    #[case::a_pod("", false)]
    #[case::a_cron_job("batch", false)]
    #[case::a_cache("cache.example.io", true)]
    fn tells_custom_kinds_apart(#[case] group: &str, #[case] is_custom: bool) {
        let object = Object::new(group, "Thing", Some("shop"), "web");
        assert_eq!(custom(&object), is_custom);
    }

    fn owner(api_version: &str, kind: &str) -> OwnerReference {
        OwnerReference {
            api_version: api_version.into(),
            kind: kind.into(),
            ..Default::default()
        }
    }

    #[rstest]
    fn watches_each_custom_kind_once_and_no_built_in_kind() {
        let owners = [
            owner("apps/v1", "ReplicaSet"),
            owner("batch/v1", "Job"),
            owner("v1", "Node"),
            owner("cache.example.io/v1", "Cache"),
            owner("cache.example.io/v1beta1", "Cache"),
            owner("agents.example.io/v1", "Mandate"),
        ];
        let kinds = kinds_to_watch(&owners);
        assert_eq!(
            kinds,
            BTreeMap::from([
                (
                    ("agents.example.io".into(), "Mandate".into()),
                    "agents.example.io/v1".into()
                ),
                (
                    ("cache.example.io".into(), "Cache".into()),
                    "cache.example.io/v1".into()
                ),
            ])
        );
    }
}
