//! The capabilities that the access lens shows, and whether an RBAC
//! rule grants each one.
//!
//! A rule lists verbs, API groups, and resources, and any of them may
//! be `*`.  A resource may include a subresource, as in `pods/exec`.
//! The API server's authorizer (`ResourceMatches` in
//! pkg/apis/rbac/v1/evaluation_helpers.go) matches a subresource in
//! three forms only: `*` for every resource and subresource, the exact
//! `pods/exec`, and `*/exec` for the `exec` subresource of every
//! resource.  `pods/*` is not a wildcard and grants nothing on
//! `pods/exec`.  A rule limited by `resourceNames` still grants the
//! capability, on the named objects only.

use k8s_openapi::api::rbac::v1::PolicyRule;

/// An action that the lens shows a column for on each account.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Capability {
    ReadSecrets,
    Exec,
    RunPods,
    ChangeWorkloads,
    GrantRoles,
    /// Change an object of a kind whose `KindKnowledge` declares
    /// `applies` or `repository`, which changes what a GitOps tool
    /// applies or the source it fetches from.
    ChangeApplied,
}

pub const ALL: [Capability; 6] = [
    Capability::ReadSecrets,
    Capability::Exec,
    Capability::RunPods,
    Capability::ChangeWorkloads,
    Capability::GrantRoles,
    Capability::ChangeApplied,
];

/// The kind of binding that grants a rule.  A request on a
/// cluster-scoped resource, such as a Node, has no namespace, so only a
/// rule granted by a ClusterRoleBinding authorizes it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Scope {
    /// Through a RoleBinding, in its namespace.
    Namespace,
    /// Through a ClusterRoleBinding.
    Cluster,
}

/// Verbs on one resource of one API group.  `cluster` is true when
/// only a ClusterRoleBinding can grant them.  `note` says what the verbs
/// allow when the resource's name does not make that clear.
#[derive(Clone, Copy, Debug)]
pub struct ResourceVerbs {
    pub group: &'static str,
    pub resource: &'static str,
    pub verbs: &'static [&'static str],
    pub cluster: bool,
    pub note: Option<&'static str>,
}

pub(super) const fn ns(
    group: &'static str,
    resource: &'static str,
    verbs: &'static [&'static str],
) -> ResourceVerbs {
    ResourceVerbs {
        group,
        resource,
        verbs,
        cluster: false,
        note: None,
    }
}

impl ResourceVerbs {
    pub(super) const fn noted(self, note: &'static str) -> Self {
        Self {
            note: Some(note),
            ..self
        }
    }

    pub(super) const fn in_cluster(self) -> Self {
        Self {
            cluster: true,
            ..self
        }
    }
}

const READ_SECRETS: &[ResourceVerbs] = &[ns("", "secrets", &["get", "list", "watch"])];

/// Every way the API lets an account run a command in a running pod,
/// or run code as the pod's ServiceAccount with the pod's volumes.
///
/// The lens counts exec and attach only with `create`.  A client that
/// uses SPDY opens them with a POST, which the API server authorizes as
/// `create`.  A client that uses a websocket opens them with a GET,
/// which the API server authorizes as `get`.  From Kubernetes 1.35 the
/// API server also checks `create` on the websocket upgrade, under the
/// feature gate AuthorizePodWebsocketUpgradeCreatePermission, which is
/// beta and on by default in 1.35.  On an older server, or with the
/// gate off, `get` alone allows a websocket exec.  The lens reads
/// neither the server's version nor its gates, so the card states this
/// limit.
///
/// `nodes/proxy` gives access to the kubelet's API, whose `exec` and
/// `run` endpoints run commands in any pod on that machine.  The kubelet
/// authorizes a POST as `create` and a GET as `get`, and it does not
/// check `create` on a websocket upgrade, so either verb allows exec.
/// The Kubernetes page "RBAC good practices" lists `nodes/proxy` as a
/// way to exec.
const EXEC: &[ResourceVerbs] = &[
    ns("", "pods/exec", &["create"]),
    ns("", "pods/attach", &["create"]).noted("attach reaches a container's main process"),
    ns("", "pods/ephemeralcontainers", &["patch", "update"])
        .noted("adds a container to a running pod, with its volumes and token"),
    ns("", "pods", &["patch", "update"])
        .noted("a new image in a running pod runs as its account, with its volumes"),
    ns("", "nodes/proxy", &["get", "create"])
        .noted("the kubelet's API runs commands in any pod on its machine")
        .in_cluster(),
];
const RUN_PODS: &[ResourceVerbs] = &[
    ns("", "pods", &["create"]),
    ns("batch", "jobs", &["create"]),
    ns("batch", "cronjobs", &["create"]),
];
const CHANGE_WORKLOADS: &[ResourceVerbs] = &[
    ns("apps", "deployments", &["create", "patch", "update"]),
    ns("apps", "statefulsets", &["create", "patch", "update"]),
    ns("apps", "daemonsets", &["create", "patch", "update"]),
    ns("apps", "replicasets", &["create", "patch", "update"]),
    ns("", "replicationcontrollers", &["create", "patch", "update"]),
    ns("batch", "jobs", &["patch", "update"]),
    ns("batch", "cronjobs", &["patch", "update"]),
];
const GRANT_ROLES: &[ResourceVerbs] = &[
    ns("rbac.authorization.k8s.io", "roles", &["bind", "escalate"]),
    ns(
        "rbac.authorization.k8s.io",
        "clusterroles",
        &["bind", "escalate"],
    ),
];

/// The verbs on a GitOps kind that change what its tool applies.
const CHANGES: &[&str] = &["create", "patch", "update"];

/// A kind whose objects name what a GitOps tool applies, as its API
/// group and plural.
pub type GitOpsKind = (String, String);

impl Capability {
    /// The capability as a verb phrase, for its column heading and for
    /// the sentences that start with "May".
    pub fn words(self) -> &'static str {
        match self {
            Self::ReadSecrets => "read Secrets",
            Self::Exec => "exec into pods",
            Self::RunPods => "run pods",
            Self::ChangeWorkloads => "change workloads",
            Self::GrantRoles => "grant roles",
            Self::ChangeApplied => "change what a GitOps tool applies",
        }
    }

    /// The verbs and resources that grant it, for the tooltip of its
    /// column heading.
    pub fn verbs(self, gitops: &[GitOpsKind]) -> String {
        match self {
            Self::ReadSecrets => "get, list, or watch on secrets".into(),
            Self::Exec => "create on pods/exec or pods/attach; patch or update on pods or pods/ephemeralcontainers; get or create on nodes/proxy, through a ClusterRoleBinding".into(),
            Self::RunPods => "create on pods, or on jobs or cronjobs in batch".into(),
            Self::ChangeWorkloads => "create, patch, or update on deployments, statefulsets, daemonsets, or replicasets in apps, or on replicationcontrollers; patch or update on jobs or cronjobs".into(),
            Self::GrantRoles => "bind or escalate on roles or clusterroles".into(),
            Self::ChangeApplied => match gitops {
                [] => "no kind knowledge declares applies or repository".into(),
                kinds => {
                    let names: Vec<String> = kinds
                        .iter()
                        .map(|(group, plural)| format!("{plural}.{group}"))
                        .collect();
                    format!("create, patch, or update on {}", names.join(", "))
                }
            },
        }
    }
}

/// One resource and the verbs on it that grant a capability, as
/// [`granting_verbs`] returns them.
#[derive(Clone, Debug)]
pub struct GrantingVerbs {
    pub group: String,
    pub resource: String,
    pub verbs: Vec<&'static str>,
    pub note: Option<&'static str>,
}

/// The verbs on each resource that grant `capability` through a
/// binding of `scope`.  `gitops` lists the kinds that
/// `Capability::ChangeApplied` covers.
pub fn granting_verbs(
    capability: Capability,
    gitops: &[GitOpsKind],
    scope: Scope,
) -> Vec<GrantingVerbs> {
    let fixed = |resources: &[ResourceVerbs]| {
        let reached = resources.iter().filter(|resource| reaches(resource, scope));
        reached
            .map(|resource| GrantingVerbs {
                group: resource.group.to_string(),
                resource: resource.resource.to_string(),
                verbs: resource.verbs.to_vec(),
                note: resource.note,
            })
            .collect()
    };
    match capability {
        Capability::ReadSecrets => fixed(READ_SECRETS),
        Capability::Exec => fixed(EXEC),
        Capability::RunPods => fixed(RUN_PODS),
        Capability::ChangeWorkloads => fixed(CHANGE_WORKLOADS),
        Capability::GrantRoles => fixed(GRANT_ROLES),
        Capability::ChangeApplied => gitops
            .iter()
            .map(|(group, plural)| GrantingVerbs {
                group: group.clone(),
                resource: plural.clone(),
                verbs: CHANGES.to_vec(),
                note: None,
            })
            .collect(),
    }
}

/// Whether a binding of `scope` can grant `verbs`.
pub(super) fn reaches(verbs: &ResourceVerbs, scope: Scope) -> bool {
    scope == Scope::Cluster || !verbs.cluster
}

/// Whether `rule`, bound with `scope`, grants `capability`.  `gitops`
/// lists the kinds that `Capability::ChangeApplied` covers.
/// `Some(names)` means the rule grants it, on only the named objects
/// when `names` is not empty.  `None` means the rule does not grant it.
pub fn grants(
    rule: &PolicyRule,
    capability: Capability,
    gitops: &[GitOpsKind],
    scope: Scope,
) -> Option<Vec<String>> {
    let allowed = granting_verbs(capability, gitops, scope).iter().any(|ask| {
        let verbs = ask.verbs.iter();
        verbs
            .clone()
            .any(|verb| allows(rule, verb, &ask.group, &ask.resource))
    });
    allowed.then(|| rule.resource_names.clone().unwrap_or_default())
}

/// Whether `rule` allows `verb` on `resource`, which may be
/// `<resource>/<subresource>`, in `group`.
pub fn allows(rule: &PolicyRule, verb: &str, group: &str, resource: &str) -> bool {
    let star_or = |names: &[String], wanted: &str| names.iter().any(|n| n == "*" || n == wanted);
    let groups = rule.api_groups.as_deref().unwrap_or_default();
    let resources = rule.resources.as_deref().unwrap_or_default();
    star_or(&rule.verbs, verb)
        && star_or(groups, group)
        && resources
            .iter()
            .any(|named| resource_matches(named, resource))
}

/// Whether a rule's `named` resource covers `resource`.
fn resource_matches(named: &str, resource: &str) -> bool {
    if named == "*" || named == resource {
        return true;
    }
    match (named.split_once('/'), resource.split_once('/')) {
        (Some(("*", sub)), Some((_, wanted))) => sub == wanted,
        _ => false,
    }
}

/// Whether `rule` allows every verb on every resource of every group.
pub fn everything(rule: &PolicyRule) -> bool {
    let star = |names: Option<&[String]>| names.unwrap_or_default().iter().any(|n| n == "*");
    let named = rule.resource_names.as_ref().is_some_and(|n| !n.is_empty());
    !named
        && star(Some(&rule.verbs))
        && star(rule.api_groups.as_deref())
        && star(rule.resources.as_deref())
}

#[cfg(test)]
mod tests {
    use rstest::rstest;

    use super::*;

    fn rule(text: &str) -> PolicyRule {
        serde_yaml::from_str(text).unwrap()
    }

    fn gitops() -> Vec<GitOpsKind> {
        vec![("apply.example.test".into(), "trees".into())]
    }

    #[rstest]
    #[case::get_secrets("{apiGroups: [''], resources: [secrets], verbs: [get]}", Capability::ReadSecrets, Some(vec![]))]
    #[case::watch_secrets("{apiGroups: [''], resources: [secrets], verbs: [watch]}", Capability::ReadSecrets, Some(vec![]))]
    #[case::secrets_in_another_group(
        "{apiGroups: [apps], resources: [secrets], verbs: [get]}",
        Capability::ReadSecrets,
        None
    )]
    #[case::create_secrets_alone(
        "{apiGroups: [''], resources: [secrets], verbs: [create]}",
        Capability::ReadSecrets,
        None
    )]
    #[case::every_verb("{apiGroups: [''], resources: [secrets], verbs: ['*']}", Capability::ReadSecrets, Some(vec![]))]
    #[case::every_group("{apiGroups: ['*'], resources: [secrets], verbs: [list]}", Capability::ReadSecrets, Some(vec![]))]
    #[case::every_resource("{apiGroups: [''], resources: ['*'], verbs: [list]}", Capability::ReadSecrets, Some(vec![]))]
    #[case::by_name("{apiGroups: [''], resources: [secrets], verbs: [get], resourceNames: [db-password, api-token]}", Capability::ReadSecrets, Some(vec!["db-password".into(), "api-token".into()]))]
    #[case::exec("{apiGroups: [''], resources: [pods/exec], verbs: [create]}", Capability::Exec, Some(vec![]))]
    #[case::attach("{apiGroups: [''], resources: [pods/attach], verbs: [create]}", Capability::Exec, Some(vec![]))]
    #[case::exec_by_get_alone(
        "{apiGroups: [''], resources: [pods/exec], verbs: [get]}",
        Capability::Exec,
        None
    )]
    #[case::attach_by_get_alone(
        "{apiGroups: [''], resources: [pods/attach], verbs: [get]}",
        Capability::Exec,
        None
    )]
    #[case::a_star_after_the_kind_is_no_wildcard(
        "{apiGroups: [''], resources: ['pods/*'], verbs: [create]}",
        Capability::Exec,
        None
    )]
    #[case::exec_of_every_resource("{apiGroups: [''], resources: ['*/exec'], verbs: [create]}", Capability::Exec, Some(vec![]))]
    #[case::every_verb_on_pods_patches_them("{apiGroups: [''], resources: [pods], verbs: ['*']}", Capability::Exec, Some(vec![]))]
    #[case::patch_a_pods_image("{apiGroups: [''], resources: [pods], verbs: [patch]}", Capability::Exec, Some(vec![]))]
    #[case::add_an_ephemeral_container("{apiGroups: [''], resources: [pods/ephemeralcontainers], verbs: [update]}", Capability::Exec, Some(vec![]))]
    #[case::the_kubelets_api_by_get("{apiGroups: [''], resources: [nodes/proxy], verbs: [get]}", Capability::Exec, Some(vec![]))]
    #[case::the_kubelets_api_by_create("{apiGroups: [''], resources: [nodes/proxy], verbs: [create]}", Capability::Exec, Some(vec![]))]
    #[case::the_proxy_of_every_resource("{apiGroups: [''], resources: ['*/proxy'], verbs: [get]}", Capability::Exec, Some(vec![]))]
    #[case::deleting_through_the_kubelets_api(
        "{apiGroups: [''], resources: [nodes/proxy], verbs: [delete]}",
        Capability::Exec,
        None
    )]
    #[case::reading_pods(
        "{apiGroups: [''], resources: [pods], verbs: [get, list, watch]}",
        Capability::Exec,
        None
    )]
    #[case::logs_are_not_exec(
        "{apiGroups: [''], resources: [pods/log], verbs: [get]}",
        Capability::Exec,
        None
    )]
    #[case::create_pods("{apiGroups: [''], resources: [pods], verbs: [create]}", Capability::RunPods, Some(vec![]))]
    #[case::create_jobs("{apiGroups: [batch], resources: [jobs], verbs: [create]}", Capability::RunPods, Some(vec![]))]
    #[case::patch_pods_does_not_run_pods(
        "{apiGroups: [''], resources: [pods], verbs: [patch]}",
        Capability::RunPods,
        None
    )]
    #[case::patch_deployments("{apiGroups: [apps], resources: [deployments], verbs: [patch]}", Capability::ChangeWorkloads, Some(vec![]))]
    #[case::create_replicationcontrollers("{apiGroups: [''], resources: [replicationcontrollers], verbs: [create]}", Capability::ChangeWorkloads, Some(vec![]))]
    #[case::update_cronjobs("{apiGroups: [batch], resources: [cronjobs], verbs: [update]}", Capability::ChangeWorkloads, Some(vec![]))]
    #[case::create_cronjobs_does_not_change_workloads(
        "{apiGroups: [batch], resources: [cronjobs], verbs: [create]}",
        Capability::ChangeWorkloads,
        None
    )]
    #[case::scale_alone(
        "{apiGroups: [apps], resources: [deployments/scale], verbs: [patch]}",
        Capability::ChangeWorkloads,
        None
    )]
    #[case::bind("{apiGroups: [rbac.authorization.k8s.io], resources: [clusterroles], verbs: [bind]}", Capability::GrantRoles, Some(vec![]))]
    #[case::impersonate_does_not_grant_roles(
        "{apiGroups: [''], resources: [serviceaccounts], verbs: [impersonate]}",
        Capability::GrantRoles,
        None
    )]
    #[case::create_roles_alone(
        "{apiGroups: [rbac.authorization.k8s.io], resources: [roles], verbs: [create]}",
        Capability::GrantRoles,
        None
    )]
    #[case::patch_an_applier("{apiGroups: [apply.example.test], resources: [trees], verbs: [patch]}", Capability::ChangeApplied, Some(vec![]))]
    #[case::read_an_applier(
        "{apiGroups: [apply.example.test], resources: [trees], verbs: [get, list]}",
        Capability::ChangeApplied,
        None
    )]
    #[case::the_appliers_status(
        "{apiGroups: [apply.example.test], resources: [trees/status], verbs: [patch]}",
        Capability::ChangeApplied,
        None
    )]
    fn reads_what_a_rule_grants(
        #[case] text: &str,
        #[case] capability: Capability,
        #[case] expected: Option<Vec<String>>,
    ) {
        assert_eq!(
            grants(&rule(text), capability, &gitops(), Scope::Cluster),
            expected
        );
    }

    #[rstest]
    #[case::the_kubelets_api_in_the_cluster(
        "{apiGroups: [''], resources: [nodes/proxy], verbs: [get]}",
        Scope::Cluster,
        true
    )]
    #[case::the_kubelets_api_through_a_rolebinding(
        "{apiGroups: [''], resources: [nodes/proxy], verbs: [get]}",
        Scope::Namespace,
        false
    )]
    #[case::exec_through_a_rolebinding(
        "{apiGroups: [''], resources: [pods/exec], verbs: [create]}",
        Scope::Namespace,
        true
    )]
    fn reads_a_rule_by_the_scope_of_its_binding(
        #[case] text: &str,
        #[case] scope: Scope,
        #[case] expected: bool,
    ) {
        let found = grants(&rule(text), Capability::Exec, &gitops(), scope);
        assert_eq!(found.is_some(), expected);
    }

    #[rstest]
    #[case::every_verb_everywhere("{apiGroups: ['*'], resources: ['*'], verbs: ['*']}", true)]
    #[case::one_group("{apiGroups: [''], resources: ['*'], verbs: ['*']}", false)]
    #[case::by_name(
        "{apiGroups: ['*'], resources: ['*'], verbs: ['*'], resourceNames: [one]}",
        false
    )]
    fn reads_a_rule_that_allows_everything(#[case] text: &str, #[case] expected: bool) {
        assert_eq!(everything(&rule(text)), expected);
    }
}
