//! The rules that grant none of the six capabilities but let an account
//! act as another account or grant itself a role.  These rules mint a
//! ServiceAccount's token, impersonate an account, bind a role, rewrite
//! a role, or create a binding.  A step in follows.rs uses each route,
//! and the account's drawing shows the rule among its other rules.

use std::collections::{BTreeMap, BTreeSet};

use k8s_openapi::api::rbac::v1::PolicyRule;

use super::capability::{Asked, Scope, allows, ns, reaches};

/// A rule that lets an account act as another.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Route {
    /// `create` on `serviceaccounts/token`: the API server mints a
    /// token for a ServiceAccount in the namespace, and the token signs
    /// in as that ServiceAccount.
    MintsTokens,
    /// `impersonate` on `serviceaccounts`: a request may act as a
    /// ServiceAccount in the namespace, with its groups.
    ImpersonatesServiceAccounts,
    /// `impersonate` on `users`, which is cluster-scoped.
    ImpersonatesUsers,
    /// `impersonate` on `groups`, which is cluster-scoped.  The API
    /// server takes a group to impersonate only beside a user or a
    /// ServiceAccount to impersonate.
    ImpersonatesGroups,
    /// `bind` on `roles`: a binding that the account creates may name a
    /// Role in the namespace even when the account does not have every
    /// rule of that Role.
    BindsRoles,
    /// `bind` on `clusterroles`: a RoleBinding in the namespace may
    /// name the ClusterRole.  When a ClusterRoleBinding grants the
    /// rule, a ClusterRoleBinding may also name the ClusterRole.
    BindsClusterRoles,
    /// `escalate` on `roles`: an update may give a Role more than the
    /// account has.
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
    /// Exec into, attach to, or patch pods by their names.  The Exec
    /// capability counts a rule on every pod, and this route reads the
    /// rules that name pods.
    ExecsPods,
    /// `get` or `create` on `nodes/proxy` by the names of Nodes: exec
    /// into the pods on those machines.
    ProxiesNodes,
}

const RBAC: &str = "rbac.authorization.k8s.io";

const ROUTES: &[(Route, Asked)] = &[
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

/// The objects a route is limited to, by name, or none for every
/// object.
pub type Names = Vec<String>;

/// Each route `rules` grant through a binding of `scope`, with the
/// names a rule limits it to.
pub fn read(rules: &[PolicyRule], scope: Scope) -> BTreeMap<Route, Names> {
    let mut found: BTreeMap<Route, Limit> = BTreeMap::new();
    for (route, asked) in ROUTES {
        if reaches(asked, scope) {
            found.entry(*route).or_default().add(rules, asked);
        }
    }
    let found = found.into_iter();
    found
        .filter_map(|(route, limit)| Some((route, limit.names()?)))
        .collect()
}

/// The names that the rules allowing one route are limited to, which
/// `add` collects as it reads each rule.
#[derive(Default)]
pub struct Limit {
    every: bool,
    names: BTreeSet<String>,
}

impl Limit {
    /// Adds what `rules` allow of `asked`.
    pub fn add(&mut self, rules: &[PolicyRule], asked: &Asked) {
        self.add_kind(rules, asked.group, asked.resource, asked.verbs);
    }

    /// Adds what `rules` allow of `verbs` on `resource` in `group`.
    pub fn add_kind(&mut self, rules: &[PolicyRule], group: &str, resource: &str, verbs: &[&str]) {
        // A request to create an object gives the authorizer no name,
        // so a rule that names objects grants no `create` of the kind
        // itself.  A request on a subresource names its object, as
        // `serviceaccounts/{name}/token` does, so a rule that names
        // objects still grants `create` on a subresource.
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

    /// The names the route is limited to, an empty list for every
    /// object, or `None` when no rule allows the route.
    pub fn names(self) -> Option<Names> {
        match (self.every, self.names.is_empty()) {
            (true, _) => Some(Vec::new()),
            (false, false) => Some(self.names.into_iter().collect()),
            (false, true) => None,
        }
    }
}

/// Whether `names`, a route's limit, lets the route act on `name`.
pub fn admits(names: &[String], name: &str) -> bool {
    names.is_empty() || names.iter().any(|named| named == name)
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn rules(text: &str) -> Vec<PolicyRule> {
        serde_yaml::from_str(text).unwrap()
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
