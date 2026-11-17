//! Which ServiceAccounts RBAC lets write each custom kind's status.
//!
//! A controller reports what it did in its resources' status, and the
//! API server serves status as a subresource with its own permission:
//! `update` or `patch` on `<plural>/status`.  So a role that grants it
//! names the kind's controller, or something that acts as one.
//! Rules that grant every resource or every group (`*`), such as
//! `cluster-admin`'s, are left out: they name every kind, so they
//! name no controller in particular.

use std::collections::{BTreeSet, HashMap};

use k8s_openapi::api::rbac::v1::{
    ClusterRole, ClusterRoleBinding, PolicyRule, Role, RoleBinding, Subject,
};

/// A `ServiceAccount` by namespace and name.
pub type ServiceAccount = (String, String);

/// A custom kind by its API group and plural.
type Resource = (String, String);

/// Each kind's status writers, and the RBAC kinds `transect` could not
/// read, which leave writers out.
#[derive(Debug, Default)]
pub struct Grants {
    /// Each kind's writers, with the one namespace a `RoleBinding` limits
    /// the grant to, or none for a `ClusterRoleBinding`'s grant.
    writers: HashMap<Resource, Vec<(Option<String>, ServiceAccount)>>,
    /// The RBAC kinds `transect` may not list, such as RoleBindings.
    pub missing: Vec<&'static str>,
}

/// The four RBAC kinds as one render reads them.
pub struct Rbac<'a> {
    pub cluster_roles: Vec<&'a ClusterRole>,
    pub roles: Vec<&'a Role>,
    pub cluster_role_bindings: Vec<&'a ClusterRoleBinding>,
    pub role_bindings: Vec<&'a RoleBinding>,
    pub missing: Vec<&'static str>,
}

impl Grants {
    pub fn build(rbac: Rbac) -> Self {
        let cluster_roles: HashMap<&str, BTreeSet<Resource>> = rbac
            .cluster_roles
            .iter()
            .filter_map(|role| Some((role.metadata.name.as_deref()?, statuses(&role.rules))))
            .collect();
        let roles: HashMap<(&str, &str), BTreeSet<Resource>> = rbac
            .roles
            .iter()
            .filter_map(|role| {
                let meta = &role.metadata;
                let key = (meta.namespace.as_deref()?, meta.name.as_deref()?);
                Some((key, statuses(&role.rules)))
            })
            .collect();
        let mut grants = Self {
            missing: rbac.missing,
            ..Self::default()
        };
        for binding in rbac.cluster_role_bindings {
            let role = &binding.role_ref;
            if role.kind != "ClusterRole" {
                continue;
            }
            let accounts = accounts(binding.subjects.as_deref(), None);
            grants.grant(cluster_roles.get(role.name.as_str()), None, &accounts);
        }
        for binding in rbac.role_bindings {
            let Some(namespace) = binding.metadata.namespace.as_deref() else {
                continue;
            };
            let role = &binding.role_ref;
            let resources = match role.kind.as_str() {
                "ClusterRole" => cluster_roles.get(role.name.as_str()),
                "Role" => roles.get(&(namespace, role.name.as_str())),
                _ => None,
            };
            let accounts = accounts(binding.subjects.as_deref(), Some(namespace));
            grants.grant(resources, Some(namespace), &accounts);
        }
        grants
    }

    fn grant(
        &mut self,
        resources: Option<&BTreeSet<Resource>>,
        scope: Option<&str>,
        accounts: &[ServiceAccount],
    ) {
        for resource in resources.into_iter().flatten() {
            let writers = self.writers.entry(resource.clone()).or_default();
            for account in accounts {
                writers.push((scope.map(Into::into), account.clone()));
            }
        }
    }

    /// Every kind some `ServiceAccount` may write the status of, by group
    /// and plural: the kinds that have a controller to find.
    pub fn kinds(&self) -> impl Iterator<Item = &(String, String)> {
        self.writers.keys()
    }

    /// The ServiceAccounts that may write the status of a `plural` in
    /// `group` in `namespace`, or of a cluster-scoped one when
    /// `namespace` is none, which only a `ClusterRoleBinding` grants.
    pub fn writers(
        &self,
        group: &str,
        plural: &str,
        namespace: Option<&str>,
    ) -> BTreeSet<&ServiceAccount> {
        let key = (group.to_string(), plural.to_string());
        let writers = self.writers.get(&key).into_iter().flatten();
        writers
            .filter(|(scope, _)| scope.is_none() || scope.as_deref() == namespace)
            .map(|(_, account)| account)
            .collect()
    }

    /// The ServiceAccounts that may write the status of a `plural` in
    /// `group` in any namespace.
    pub fn all_writers(&self, group: &str, plural: &str) -> BTreeSet<&ServiceAccount> {
        let key = (group.to_string(), plural.to_string());
        let writers = self.writers.get(&key).into_iter().flatten();
        writers.map(|(_, account)| account).collect()
    }
}

/// The ServiceAccounts among `subjects`.  A `RoleBinding`'s subject
/// with no namespace is in the binding's own.  Users and groups run no
/// pods, so they lead to no controller.
fn accounts(subjects: Option<&[Subject]>, namespace: Option<&str>) -> Vec<ServiceAccount> {
    subjects
        .into_iter()
        .flatten()
        .filter(|subject| subject.kind == "ServiceAccount")
        .filter_map(|subject| {
            let namespace = subject.namespace.as_deref().or(namespace)?;
            Some((namespace.to_string(), subject.name.clone()))
        })
        .collect()
}

/// The kinds whose status `rules` let a subject write, by group and
/// plural.  `<plural>/*` grants every subresource of one kind, status
/// among them, so it counts.  A rule limited to named objects is left
/// out, because a controller reconciles every object of its kind.
fn statuses(rules: &Option<Vec<PolicyRule>>) -> BTreeSet<Resource> {
    let mut found = BTreeSet::new();
    for rule in rules.iter().flatten() {
        let writes = rule
            .verbs
            .iter()
            .any(|verb| matches!(verb.as_str(), "update" | "patch" | "*"));
        let named = rule.resource_names.as_ref().is_some_and(|n| !n.is_empty());
        let groups = rule.api_groups.as_deref().unwrap_or_default();
        let resources = rule.resources.as_deref().unwrap_or_default();
        let every = |names: &[String]| names.iter().any(|name| name == "*");
        if !writes || named || every(groups) || every(resources) {
            continue;
        }
        for group in groups {
            for resource in resources {
                let plural = resource
                    .strip_suffix("/status")
                    .or_else(|| resource.strip_suffix("/*"));
                if let Some(plural) = plural {
                    found.insert((group.clone(), plural.to_string()));
                }
            }
        }
    }
    found
}

#[cfg(test)]
mod tests {
    use rstest::rstest;
    use serde::de::DeserializeOwned;

    use super::*;

    fn yaml<K: DeserializeOwned>(text: &str) -> K {
        serde_yaml::from_str(text).unwrap()
    }

    fn cluster_role(name: &str, rule: &str) -> ClusterRole {
        yaml(&format!("{{metadata: {{name: {name}}}, rules: [{rule}]}}"))
    }

    fn role(namespace: &str, name: &str, rule: &str) -> Role {
        yaml(&format!(
            "{{metadata: {{namespace: {namespace}, name: {name}}}, rules: [{rule}]}}"
        ))
    }

    fn cluster_binding(role: &str, account: &str) -> ClusterRoleBinding {
        yaml(&format!(
            "{{metadata: {{name: {role}}}, roleRef: {{apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: {role}}}, subjects: [{account}]}}"
        ))
    }

    fn binding(namespace: &str, kind: &str, role: &str, account: &str) -> RoleBinding {
        yaml(&format!(
            "{{metadata: {{namespace: {namespace}, name: {role}}}, roleRef: {{apiGroup: rbac.authorization.k8s.io, kind: {kind}, name: {role}}}, subjects: [{account}]}}"
        ))
    }

    const OPERATOR: &str = "{kind: ServiceAccount, namespace: cache-system, name: operator}";
    const STATUS: &str =
        "{apiGroups: [cache.example.io], resources: [caches/status], verbs: [get, update]}";

    fn writers(rbac: Rbac, namespace: Option<&str>) -> Vec<ServiceAccount> {
        let grants = Grants::build(rbac);
        let found = grants.writers("cache.example.io", "caches", namespace);
        found.into_iter().cloned().collect()
    }

    fn cluster_wide(rule: &str, account: &str) -> Vec<ServiceAccount> {
        let role = cluster_role("cache-operator", rule);
        let binding = cluster_binding("cache-operator", account);
        let rbac = Rbac {
            cluster_roles: vec![&role],
            roles: vec![],
            cluster_role_bindings: vec![&binding],
            role_bindings: vec![],
            missing: vec![],
        };
        writers(rbac, Some("shop"))
    }

    fn operator() -> Vec<ServiceAccount> {
        vec![("cache-system".into(), "operator".into())]
    }

    #[rstest]
    #[case::status(STATUS, operator())]
    #[case::patch(
        "{apiGroups: [cache.example.io], resources: [caches/status], verbs: [patch]}",
        operator()
    )]
    #[case::every_verb(
        "{apiGroups: [cache.example.io], resources: [caches/status], verbs: ['*']}",
        operator()
    )]
    #[case::every_subresource(
        "{apiGroups: [cache.example.io], resources: ['caches/*'], verbs: [update]}",
        operator()
    )]
    #[case::read_only(
        "{apiGroups: [cache.example.io], resources: [caches/status], verbs: [get, list]}",
        vec![]
    )]
    #[case::the_kind_not_its_status(
        "{apiGroups: [cache.example.io], resources: [caches], verbs: [update]}",
        vec![]
    )]
    #[case::every_resource(
        "{apiGroups: [cache.example.io], resources: ['*'], verbs: ['*']}",
        vec![]
    )]
    #[case::every_group("{apiGroups: ['*'], resources: [caches/status], verbs: ['*']}", vec![])]
    #[case::named_objects(
        "{apiGroups: [cache.example.io], resources: [caches/status], resourceNames: [one], verbs: [update]}",
        vec![]
    )]
    #[case::another_group(
        "{apiGroups: [other.example.io], resources: [caches/status], verbs: [update]}",
        vec![]
    )]
    fn reads_who_may_write_status(#[case] rule: &str, #[case] expected: Vec<ServiceAccount>) {
        assert_eq!(cluster_wide(rule, OPERATOR), expected);
    }

    #[rstest]
    #[case::a_user("{kind: User, name: someone}", vec![])]
    #[case::a_group("{kind: Group, name: admins}", vec![])]
    fn only_service_accounts_count(#[case] subject: &str, #[case] expected: Vec<ServiceAccount>) {
        assert_eq!(cluster_wide(STATUS, subject), expected);
    }

    #[rstest]
    #[case::its_namespace(Some("shop"), operator())]
    #[case::another_namespace(Some("blog"), vec![])]
    #[case::cluster_scoped(None, vec![])]
    fn a_role_binding_grants_one_namespace(
        #[case] namespace: Option<&str>,
        #[case] expected: Vec<ServiceAccount>,
    ) {
        let role = role("shop", "cache-operator", STATUS);
        let to_role = binding("shop", "Role", "cache-operator", OPERATOR);
        let rbac = Rbac {
            cluster_roles: vec![],
            roles: vec![&role],
            cluster_role_bindings: vec![],
            role_bindings: vec![&to_role],
            missing: vec![],
        };
        assert_eq!(writers(rbac, namespace), expected);
    }

    #[rstest]
    fn a_role_binding_to_a_cluster_role_grants_its_namespace() {
        let role = cluster_role("cache-operator", STATUS);
        let to_role = binding("shop", "ClusterRole", "cache-operator", OPERATOR);
        let rbac = Rbac {
            cluster_roles: vec![&role],
            roles: vec![],
            cluster_role_bindings: vec![],
            role_bindings: vec![&to_role],
            missing: vec![],
        };
        assert_eq!(writers(rbac, Some("shop")), operator());
    }

    #[rstest]
    fn a_role_binding_subject_defaults_to_its_namespace() {
        let role = role("shop", "cache-operator", STATUS);
        let account = "{kind: ServiceAccount, name: operator}";
        let to_role = binding("shop", "Role", "cache-operator", account);
        let rbac = Rbac {
            cluster_roles: vec![],
            roles: vec![&role],
            cluster_role_bindings: vec![],
            role_bindings: vec![&to_role],
            missing: vec![],
        };
        assert_eq!(
            writers(rbac, Some("shop")),
            vec![("shop".into(), "operator".into())]
        );
    }
}
