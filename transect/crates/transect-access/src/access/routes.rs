//! The rules that let an account act as another account or give itself
//! a role: creating a ServiceAccount's token, impersonating an account,
//! binding a role, rewriting a role, or creating a binding.  The steps
//! in `follows` start from these routes.  The account's page lists the
//! rule with the account's other rules.

use std::collections::{BTreeMap, BTreeSet};

use k8s_openapi::api::rbac::v1::PolicyRule;

use super::capability::{ResourceVerbs, Scope, allows, ns, reaches};

/// A kind of rule that lets an account act as another account or give
/// itself a role.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Route {
    /// `create` on `serviceaccounts/token`: the API server creates a
    /// token for a ServiceAccount in the namespace, and a request with
    /// the token authenticates as that ServiceAccount.
    MintsTokens,
    /// `impersonate` on `serviceaccounts`: a request may act as a
    /// ServiceAccount in the namespace, with that ServiceAccount's
    /// groups.
    ImpersonatesServiceAccounts,
    /// `impersonate` on `users`, which is cluster-scoped.
    ImpersonatesUsers,
    /// `impersonate` on `groups`, which is cluster-scoped.  The API
    /// server accepts a group to impersonate only together with a user
    /// or a ServiceAccount to impersonate.
    ImpersonatesGroups,
    /// `bind` on `roles`: a binding that the account creates may name a
    /// Role in the namespace even when the account lacks some of the
    /// Role's rules.
    BindsRoles,
    /// `bind` on `clusterroles`: a RoleBinding in the namespace may
    /// name the ClusterRole.  When a ClusterRoleBinding grants the
    /// rule, a ClusterRoleBinding may also name the ClusterRole.
    BindsClusterRoles,
    /// `escalate` on `roles`: the account may update a Role to grant
    /// more than the account has.
    EscalatesRoles,
    /// `escalate` on `clusterroles`, which is cluster-scoped.
    EscalatesClusterRoles,
    /// `update` or `patch` on `roles`.
    RewritesRoles,
    /// `update` or `patch` on `clusterroles`, which is cluster-scoped.
    RewritesClusterRoles,
    /// `create` on `rolebindings`.
    CreatesRoleBindings,
    /// `create` on `clusterrolebindings`, which is cluster-scoped.
    CreatesClusterRoleBindings,
    /// `create` on `pods/exec` or `pods/attach`, or `patch` or `update`
    /// on `pods` or `pods/ephemeralcontainers`.  The steps from the
    /// `Exec` capability use only a grant on every pod, so a step reads
    /// this route for a rule that names pods.
    ExecsPods,
    /// `get` or `create` on `nodes/proxy`, which is cluster-scoped: the
    /// account may exec into the pods on the Nodes.  As with
    /// `ExecsPods`, a step reads this route for a rule that names
    /// Nodes.
    ProxiesNodes,
}

const RBAC: &str = "rbac.authorization.k8s.io";

const ROUTES: &[(Route, ResourceVerbs)] = &[
    (
        Route::MintsTokens,
        ns("", "serviceaccounts/token", &["create"]),
    ),
    (
        Route::ImpersonatesServiceAccounts,
        ns("", "serviceaccounts", &["impersonate"]),
    ),
    (
        Route::ImpersonatesUsers,
        ns("", "users", &["impersonate"]).in_cluster(),
    ),
    (
        Route::ImpersonatesGroups,
        ns("", "groups", &["impersonate"]).in_cluster(),
    ),
    (Route::BindsRoles, ns(RBAC, "roles", &["bind"])),
    (
        Route::BindsClusterRoles,
        ns(RBAC, "clusterroles", &["bind"]),
    ),
    (Route::EscalatesRoles, ns(RBAC, "roles", &["escalate"])),
    (
        Route::EscalatesClusterRoles,
        ns(RBAC, "clusterroles", &["escalate"]).in_cluster(),
    ),
    (
        Route::RewritesRoles,
        ns(RBAC, "roles", &["update", "patch"]),
    ),
    (
        Route::RewritesClusterRoles,
        ns(RBAC, "clusterroles", &["update", "patch"]).in_cluster(),
    ),
    (
        Route::CreatesRoleBindings,
        ns(RBAC, "rolebindings", &["create"]),
    ),
    (
        Route::CreatesClusterRoleBindings,
        ns(RBAC, "clusterrolebindings", &["create"]).in_cluster(),
    ),
    (Route::ExecsPods, ns("", "pods/exec", &["create"])),
    (Route::ExecsPods, ns("", "pods/attach", &["create"])),
    (
        Route::ExecsPods,
        ns("", "pods/ephemeralcontainers", &["patch", "update"]),
    ),
    (Route::ExecsPods, ns("", "pods", &["patch", "update"])),
    (
        Route::ProxiesNodes,
        ns("", "nodes/proxy", &["get", "create"]).in_cluster(),
    ),
];

/// The object names that a route is limited to, or an empty list for
/// every object.
pub type Names = Vec<String>;

/// Each route that `rules` grant through a binding of `scope`, with the
/// names that a rule limits it to.
pub fn read(rules: &[PolicyRule], scope: Scope) -> BTreeMap<Route, Names> {
    let mut found: BTreeMap<Route, Limit> = BTreeMap::new();
    for (route, verbs) in ROUTES {
        if reaches(verbs, scope) {
            found.entry(*route).or_default().add(rules, verbs);
        }
    }
    let found = found.into_iter();
    found
        .filter_map(|(route, limit)| Some((route, limit.names()?)))
        .collect()
}

/// The object names that a set of rules limits a request to.
#[derive(Default)]
pub struct Limit {
    every: bool,
    names: BTreeSet<String>,
}

impl Limit {
    /// Adds what `rules` allow for `verbs`.
    pub fn add(&mut self, rules: &[PolicyRule], verbs: &ResourceVerbs) {
        self.add_kind(rules, verbs.group, verbs.resource, verbs.verbs);
    }

    /// Adds what `rules` allow for `verbs` on `resource` in `group`.
    pub fn add_kind(&mut self, rules: &[PolicyRule], group: &str, resource: &str, verbs: &[&str]) {
        // The API server authorizes a `create` with no object name, so
        // a rule with `resourceNames` never allows `create` on the kind
        // itself.  A request on a subresource has its parent's name in
        // the path, as `serviceaccounts/{name}/token` does, so
        // `resourceNames` does limit `create` there.
        let by_name = verbs != ["create"] || resource.contains('/');
        for rule in rules {
            if !verbs.iter().any(|verb| allows(rule, verb, group, resource)) {
                continue;
            }
            match rule.resource_names.as_deref().unwrap_or_default() {
                [] => self.every = true,
                named if by_name => self.names.extend(named.iter().cloned()),
                _ => {}
            }
        }
    }

    /// The names, an empty list for every object, or `None` when no
    /// rule allows the request.
    pub fn names(self) -> Option<Names> {
        match (self.every, self.names.is_empty()) {
            (true, _) => Some(Vec::new()),
            (false, false) => Some(self.names.into_iter().collect()),
            (false, true) => None,
        }
    }
}

/// Whether `names`, a limit from `Limit::names`, allows `name`.
pub fn admits(names: &[String], name: &str) -> bool {
    names.is_empty() || names.iter().any(|named| named == name)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn rules(text: &str) -> Vec<PolicyRule> {
        serde_yaml_ng::from_str(text).unwrap()
    }

    #[rstest]
    #[case::mint_a_token("[{apiGroups: [''], resources: [serviceaccounts/token], verbs: [create]}]", Scope::Namespace, &[(Route::MintsTokens, &[] as &[&str])])]
    #[case::read_a_token_request_mints_none(
        "[{apiGroups: [''], resources: [serviceaccounts/token], verbs: [get]}]",
        Scope::Namespace,
        &[]
    )]
    #[case::mint_a_named_accounts_token("[{apiGroups: [''], resources: [serviceaccounts/token], verbs: [create], resourceNames: [deployer]}]", Scope::Namespace, &[(Route::MintsTokens, &["deployer"] as &[&str])])]
    #[case::impersonate_accounts("[{apiGroups: [''], resources: [serviceaccounts], verbs: [impersonate]}]", Scope::Namespace, &[(Route::ImpersonatesServiceAccounts, &[] as &[&str])])]
    #[case::impersonate_users_through_a_rolebinding(
        "[{apiGroups: [''], resources: [users, groups], verbs: [impersonate]}]",
        Scope::Namespace,
        &[]
    )]
    #[case::impersonate_users_and_groups("[{apiGroups: [''], resources: [users, groups], verbs: [impersonate]}]", Scope::Cluster, &[(Route::ImpersonatesUsers, &[] as &[&str]), (Route::ImpersonatesGroups, &[])])]
    #[case::bind_and_create_bindings("[{apiGroups: [rbac.authorization.k8s.io], resources: [clusterroles], verbs: [bind], resourceNames: [view]}, {apiGroups: [rbac.authorization.k8s.io], resources: [rolebindings], verbs: [create]}]", Scope::Namespace, &[(Route::BindsClusterRoles, &["view"] as &[&str]), (Route::CreatesRoleBindings, &[])])]
    #[case::create_by_name_grants_no_create(
        "[{apiGroups: [rbac.authorization.k8s.io], resources: [rolebindings], verbs: [create], resourceNames: [mine]}]",
        Scope::Namespace,
        &[]
    )]
    #[case::create_clusterrolebindings_through_a_rolebinding(
        "[{apiGroups: [rbac.authorization.k8s.io], resources: [clusterrolebindings], verbs: [create]}]",
        Scope::Namespace,
        &[]
    )]
    #[case::exec_into_named_pods("[{apiGroups: [''], resources: [pods/exec], verbs: [create], resourceNames: [web-1]}, {apiGroups: [''], resources: [pods], verbs: [patch], resourceNames: [web-2]}]", Scope::Namespace, &[(Route::ExecsPods, &["web-1", "web-2"] as &[&str])])]
    #[case::escalate_and_patch_roles("[{apiGroups: [rbac.authorization.k8s.io], resources: [roles], verbs: [escalate, patch]}]", Scope::Namespace, &[(Route::EscalatesRoles, &[] as &[&str]), (Route::RewritesRoles, &[])])]
    #[case::every_verb_everywhere("[{apiGroups: ['*'], resources: ['*'], verbs: ['*']}]", Scope::Cluster, &[(Route::MintsTokens, &[] as &[&str]), (Route::ImpersonatesServiceAccounts, &[]), (Route::ImpersonatesUsers, &[]), (Route::ImpersonatesGroups, &[]), (Route::BindsRoles, &[]), (Route::BindsClusterRoles, &[]), (Route::EscalatesRoles, &[]), (Route::EscalatesClusterRoles, &[]), (Route::RewritesRoles, &[]), (Route::RewritesClusterRoles, &[]), (Route::CreatesRoleBindings, &[]), (Route::CreatesClusterRoleBindings, &[]), (Route::ExecsPods, &[]), (Route::ProxiesNodes, &[])])]
    fn reads_the_routes_a_rule_grants(
        #[case] text: &str,
        #[case] scope: Scope,
        #[case] expected: &[(Route, &[&str])],
    ) {
        let expected: BTreeMap<Route, Names> = expected
            .iter()
            .map(|(route, names)| (*route, names.iter().map(|n| n.to_string()).collect()))
            .collect();
        assert_eq!(read(&rules(text), scope), expected);
    }
}
