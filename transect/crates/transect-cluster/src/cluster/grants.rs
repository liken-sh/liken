//! The base's RBAC in `deploy/rbac.yaml`, checked against the rule
//! `transect` follows: its account may get, list, and watch, it may
//! never read a `Secret`, and no rule has a wildcard, because a
//! wildcard on the core group would include Secrets.

use k8s_openapi::api::rbac::v1::{ClusterRole, PolicyRule};
use rstest::rstest;
use serde::Deserialize;

/// Every rule of every `ClusterRole` in the base's RBAC.
fn rules() -> Vec<PolicyRule> {
    let path = concat!(env!("CARGO_MANIFEST_DIR"), "/../../deploy/rbac.yaml");
    let text = std::fs::read_to_string(path).unwrap();
    serde_yaml::Deserializer::from_str(&text)
        .map(serde_yaml::Value::deserialize)
        .map(Result::unwrap)
        .filter(|doc| doc["kind"] == "ClusterRole")
        .map(|doc| serde_yaml::from_value::<ClusterRole>(doc).unwrap())
        .flat_map(|role| role.rules.unwrap_or_default())
        .collect()
}

/// The rules for which `breaks` holds.
fn breaking(breaks: fn(&PolicyRule) -> bool) -> Vec<PolicyRule> {
    rules().into_iter().filter(breaks).collect()
}

fn names(rule: &PolicyRule, name: &str) -> bool {
    rule.resources
        .iter()
        .flatten()
        .any(|resource| resource == name)
}

#[rstest]
#[case::only_reads(|rule: &PolicyRule| {
    rule.verbs.iter().any(|verb| !["get", "list", "watch"].contains(&verb.as_str()))
})]
#[case::no_secrets(|rule: &PolicyRule| names(rule, "secrets"))]
#[case::no_wildcard_resource(|rule: &PolicyRule| names(rule, "*"))]
#[case::no_wildcard_group(|rule: &PolicyRule| {
    rule.api_groups.iter().flatten().any(|group| group == "*")
})]
#[case::no_proxy(|rule: &PolicyRule| names(rule, "services/proxy") || names(rule, "nodes/proxy"))]
fn the_base_role_keeps_the_rule(#[case] breaks: fn(&PolicyRule) -> bool) {
    assert_eq!(breaking(breaks), []);
}
