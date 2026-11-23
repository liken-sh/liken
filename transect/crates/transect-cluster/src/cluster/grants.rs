//! Checks the base RBAC in `deploy/rbac.yaml`. The account of
//! `transect` may only get, list, and watch, and no rule grants
//! `secrets`, `services/proxy`, `nodes/proxy`, or a wildcard. A wildcard
//! on the core group would include `Secret`s.

use k8s_openapi::api::rbac::v1::{ClusterRole, PolicyRule};
use rstest::rstest;
use serde::Deserialize;

/// Every rule of every `ClusterRole` in the base's RBAC.
fn rules() -> Vec<PolicyRule> {
    let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../../deploy/rbac.yaml");
    let text = std::fs::read_to_string(path).unwrap();
    serde_yaml_ng::Deserializer::from_str(&text)
        .map(serde_yaml_ng::Value::deserialize)
        .map(Result::unwrap)
        .filter(|doc| doc["kind"] == "ClusterRole")
        .map(|doc| serde_yaml_ng::from_value::<ClusterRole>(doc).unwrap())
        .flat_map(|role| role.rules.unwrap_or_default())
        .collect()
}

/// The rules for which `breaks` returns true.
fn breaking(breaks: fn(&PolicyRule) -> bool) -> Vec<PolicyRule> {
    rules().into_iter().filter(breaks).collect()
}

fn has_resource(rule: &PolicyRule, name: &str) -> bool {
    rule.resources
        .iter()
        .flatten()
        .any(|resource| resource == name)
}

#[rstest]
#[case::only_reads(|rule: &PolicyRule| {
    rule.verbs.iter().any(|verb| !["get", "list", "watch"].contains(&verb.as_str()))
})]
#[case::no_secrets(|rule: &PolicyRule| has_resource(rule, "secrets"))]
#[case::no_wildcard_resource(|rule: &PolicyRule| has_resource(rule, "*"))]
#[case::no_wildcard_group(|rule: &PolicyRule| {
    rule.api_groups.iter().flatten().any(|group| group == "*")
})]
#[case::no_proxy(|rule: &PolicyRule| has_resource(rule, "services/proxy") || has_resource(rule, "nodes/proxy"))]
fn the_base_role_follows_the_rule(#[case] breaks: fn(&PolicyRule) -> bool) {
    assert_eq!(breaking(breaks), []);
}
