//! Bindings whose subject or role the API server treats as a special
//! case, each in a cluster of a few objects.

use rstest::rstest;

use super::*;

/// A ServiceAccount, a ClusterRole that may read Secrets, and a Role
/// that may exec into pods, in namespace `shop`.
const ROLES: &str = r#"
kind: ServiceAccount
metadata: {namespace: shop, name: web}
---
kind: ClusterRole
metadata: {name: secret-reader}
rules: [{apiGroups: [''], resources: [secrets], verbs: [get]}]
---
kind: Role
metadata: {namespace: shop, name: exec}
rules: [{apiGroups: [''], resources: [pods/exec], verbs: [create]}]
"#;

#[rstest]
#[case::a_binding_to_a_role_that_does_not_exist(
    "kind: RoleBinding
metadata: {namespace: shop, name: ghost}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: missing}
subjects: [{kind: ServiceAccount, name: web}]",
    "shop/web",
    Capability::Exec,
    &[]
)]
#[case::a_clusterrolebinding_naming_a_role(
    "kind: ClusterRoleBinding
metadata: {name: role-in-the-cluster}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: exec}
subjects: [{kind: ServiceAccount, namespace: shop, name: web}]",
    "shop/web",
    Capability::Exec,
    &[]
)]
#[case::system_authenticated_reaches_a_user(
    "kind: ClusterRoleBinding
metadata: {name: everyone}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: secret-reader}
subjects: [{kind: Group, name: 'system:authenticated'}]
---
kind: RoleBinding
metadata: {namespace: shop, name: alice}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: exec}
subjects: [{kind: User, name: alice}]",
    "User alice",
    Capability::ReadSecrets,
    &["granted * as Group system:authenticated"]
)]
#[case::a_rule_on_urls_alone(
    "kind: ClusterRole
metadata: {name: every-url}
rules: [{nonResourceURLs: ['*'], verbs: ['*']}]
---
kind: ClusterRoleBinding
metadata: {name: urls}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: every-url}
subjects: [{kind: ServiceAccount, namespace: shop, name: web}]",
    "shop/web",
    Capability::ReadSecrets,
    &[]
)]
fn reads_each_binding_as_the_api_server_does(
    #[case] bindings: &str,
    #[case] account: &str,
    #[case] capability: Capability,
    #[case] expected: &[&str],
) {
    let cluster = format!("{ROLES}---\n{bindings}");
    let access = reduce(&cluster, Vec::new(), Vec::new());
    assert_eq!(said(&access, account, capability), expected);
}

#[test]
fn a_service_account_with_no_namespace_in_a_clusterrolebinding_names_no_account() {
    let cluster = format!(
        "{ROLES}---
kind: ClusterRoleBinding
metadata: {{name: nowhere}}
roleRef: {{apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: secret-reader}}
subjects: [{{kind: ServiceAccount, name: nobody}}]"
    );
    let access = reduce(&cluster, Vec::new(), Vec::new());
    let names: Vec<String> = access.accounts.keys().map(Account::short).collect();
    assert_eq!(names, ["shop/web"]);
}
