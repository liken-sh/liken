//! The sections of an object page for a `Role` or `ClusterRole` (a
//! table of its rules) and for a `RoleBinding` or `ClusterRoleBinding`
//! (the role it grants and the subjects it grants it to).

use k8s_openapi::api::rbac::v1::{PolicyRule, RoleRef, Subject};
use kube::ResourceExt;

use super::super::super::snapshot::Snapshot;
use super::refer_object;
use transect_graph::edges::Object;
use transect_graph::links::Reference;
use transect_map::objects::{Section, Side, Table, words};

const GROUP: &str = "rbac.authorization.k8s.io";

pub fn rules(snapshot: &Snapshot, object: &Object) -> Vec<Section> {
    let rbac = &snapshot.paged.rbac;
    let here = object.namespace.as_deref();
    let rules = match object.kind.as_str() {
        "Role" => rbac
            .roles
            .iter()
            .find(|role| role.namespace().as_deref() == here && role.name_any() == object.name)
            .and_then(|role| role.rules.clone()),
        _ => rbac
            .cluster_roles
            .iter()
            .find(|role| role.name_any() == object.name)
            .and_then(|role| role.rules.clone()),
    };
    vec![
        Section::new("Rules", Side::Own)
            .with_table(table(&rules.unwrap_or_default()))
            .or("It grants nothing."),
    ]
}

/// One row per rule: its verbs, API groups, resources, and resource
/// names. The core API group shows as "core". An empty list shows as
/// "none" for groups and resources, and as "any" for names. A rule's
/// non-resource URLs replace its resources.
pub fn table(rules: &[PolicyRule]) -> Table {
    let join = |values: &Option<Vec<String>>, all: &str| match values.as_deref() {
        None | Some([]) => all.to_string(),
        Some(values) => values
            .iter()
            .map(|value| match value.as_str() {
                "" => "core".to_string(),
                value => value.to_string(),
            })
            .collect::<Vec<_>>()
            .join(", "),
    };
    let rows = rules.iter().map(|rule| {
        let resources = match &rule.non_resource_urls {
            Some(urls) if !urls.is_empty() => urls.join(", "),
            _ => join(&rule.resources, "none"),
        };
        vec![
            rule.verbs.join(", "),
            join(&rule.api_groups, "none"),
            resources,
            join(&rule.resource_names, "any"),
        ]
    });
    Table {
        head: ["Verbs", "Groups", "Resources", "Names"]
            .map(Into::into)
            .into(),
        rows: rows.collect(),
    }
}

pub fn binding(snapshot: &Snapshot, object: &Object) -> Vec<Section> {
    let rbac = &snapshot.paged.rbac;
    let here = object.namespace.as_deref();
    let found: Option<(RoleRef, Vec<Subject>)> = match object.kind.as_str() {
        "RoleBinding" => rbac
            .role_bindings
            .iter()
            .find(|b| b.namespace().as_deref() == here && b.name_any() == object.name)
            .map(|b| (b.role_ref.clone(), b.subjects.clone().unwrap_or_default())),
        _ => rbac
            .cluster_role_bindings
            .iter()
            .find(|b| b.name_any() == object.name)
            .map(|b| (b.role_ref.clone(), b.subjects.clone().unwrap_or_default())),
    };
    let Some((role, subjects)) = found else {
        return Vec::new();
    };
    let role_namespace = match role.kind.as_str() {
        "Role" => here,
        _ => None,
    };
    let role = Object::new(GROUP, &role.kind, role_namespace, &role.name);
    let subjects = subjects
        .iter()
        .map(|subject| self::subject(snapshot, subject, here));
    vec![
        Section::new("Binding", Side::Out)
            .fact("grants", vec![refer_object(snapshot, &role, here)])
            .fact("to", subjects.collect()),
    ]
}

/// A subject, linked when it is a `ServiceAccount` with a page.
fn subject(snapshot: &Snapshot, subject: &Subject, here: Option<&str>) -> Reference {
    match subject.kind.as_str() {
        "ServiceAccount" => {
            let namespace = subject.namespace.as_deref().or(here);
            let account = Object::new("", "ServiceAccount", namespace, &subject.name);
            refer_object(snapshot, &account, here)
        }
        kind => words(format!("{kind} {}", subject.name)),
    }
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    #[rstest]
    #[case::resources("{verbs: [get, list], apiGroups: [''], resources: [pods]}", ["get, list", "core", "pods", "any"])]
    #[case::named("{verbs: [update], apiGroups: [apps], resources: [deployments], resourceNames: [web]}", ["update", "apps", "deployments", "web"])]
    #[case::urls("{verbs: [get], nonResourceURLs: [/healthz]}", ["get", "none", "/healthz", "any"])]
    fn reads_each_rule_as_a_row(#[case] rule: &str, #[case] expected: [&str; 4]) {
        let rule: PolicyRule = serde_yaml::from_str(rule).unwrap();
        assert_eq!(
            table(&[rule]).rows,
            vec![expected.map(String::from).to_vec()]
        );
    }
}
